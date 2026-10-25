// Package apply implements the aggregator's batch transaction: dedup, classify, pre-sum, upsert and advance the
// offset, all in one Postgres transaction (docs/03-services/aggregator.md, docs/07-correctness.md §2).
package apply

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/twmb/franz-go/pkg/kgo"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"google.golang.org/grpc/codes"

	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/kafkax"
	"github.com/0furkancolak/payrudder/pkg/period"
	"github.com/0furkancolak/payrudder/pkg/usage"
	"github.com/0furkancolak/payrudder/services/aggregator/internal/domain"
	"github.com/0furkancolak/payrudder/services/aggregator/internal/settings"
	"github.com/0furkancolak/payrudder/services/aggregator/internal/store/sqlc"
)

// Topic and group the live consumer uses.
const (
	Topic = "usage.events.v1"
	Group = "aggregator"
)

// ErrMeterNotFound is the answer for a meter plans-svc does not know.
var ErrMeterNotFound = errkey.New(codes.NotFound, "aggregator.meter.not_found", "meter not found")

// MeterSource resolves meters.
type MeterSource interface {
	Get(ctx context.Context, tenantID uuid.UUID, code string) (usage.Meter, error)
}

// Settings snapshots period settings.
type Settings interface {
	Ensure(ctx context.Context, q *sqlc.Queries, keys []settings.Key) error
}

// DeadLetter publishes poison records.
type DeadLetter interface {
	Send(ctx context.Context, orig *kgo.Record, reason string, err error) error
}

// Replay says what a replaying handler rebuilds.
type Replay struct {
	Tenant     uuid.UUID
	Generation int32
	// FromPeriod and ToPeriod bound the periods rebuilt (inclusive, "2026-10" style); events of other periods are skipped.
	FromPeriod, ToPeriod string
}

func (r *Replay) wants(ev usage.Event) bool {
	return ev.TenantID == r.Tenant && ev.Period >= r.FromPeriod && ev.Period <= r.ToPeriod
}

// Handler applies batches. It implements kafkax.PartitionHandler.
type Handler struct {
	Pool     *pgxpool.Pool // aggregator_system
	Meters   MeterSource
	Settings Settings
	DLQ      DeadLetter
	Log      *slog.Logger
	// ConsumerGroup is the key in consumer_offsets (default "aggregator").
	ConsumerGroup string
	// Topic is the topic being consumed (default usage.events.v1); it's part of the consumer_offsets key.
	Topic string
	// MeterRetryDelays are the waits between lookups of a meter plans-svc reports as unknown, before the record goes
	// to the DLQ (default 100 ms, 500 ms, 2 s).
	MeterRetryDelays []time.Duration
	// Replay, when set, turns the handler into a replay of one tenant's events into one aggregate generation: the same
	// dedup, classification and upserts, with its own offsets (ConsumerGroup), and without DLQ, generation locking or the
	// frozen-period rule (docs/07-correctness.md#8-replay--re-rating).
	Replay *Replay
	// AfterCommit runs after the batch transaction committed and before Kafka is told. A non-nil error simulates a
	// crash at that point (fault hook AGG_CRASH_AFTER_COMMIT).
	AfterCommit func() error

	once    sync.Once
	ensured sync.Map // periods whose inbox partition exists
	m       metrics
}

type metrics struct {
	applied, duplicates, late, filtered, frozen, dlq, fenced metric.Int64Counter
	batchSecs                                                metric.Float64Histogram
}

func (h *Handler) init() {
	h.once.Do(func() {
		if h.ConsumerGroup == "" {
			h.ConsumerGroup = Group
		}
		if h.Topic == "" {
			h.Topic = Topic
		}
		if h.MeterRetryDelays == nil {
			h.MeterRetryDelays = []time.Duration{100 * time.Millisecond, 500 * time.Millisecond, 2 * time.Second}
		}
		if h.Log == nil {
			h.Log = slog.Default()
		}
		mt := otel.Meter("github.com/0furkancolak/payrudder/services/aggregator")
		ctr := func(name, desc string) metric.Int64Counter {
			c, err := mt.Int64Counter(name, metric.WithDescription(desc))
			if err != nil {
				panic(err) // only on duplicate instrument names with different kinds: a programming error
			}
			return c
		}
		prefix := "aggregator_"
		if h.Replay != nil {
			prefix = "aggregator_replay_" // a replay is not live traffic: its counters must not move the live ones
		}
		h.m.applied = ctr(prefix+"events_applied", "Events applied to an aggregate")
		h.m.duplicates = ctr(prefix+"duplicates_skipped", "Events skipped because the inbox already had them")
		h.m.late = ctr(prefix+"late_events", "Events routed to late usage")
		h.m.filtered = ctr(prefix+"filtered_events", "Events ignored by the meter filter")
		h.m.frozen = ctr(prefix+"frozen_period_violations", "On-time events that arrived for a frozen period")
		h.m.dlq = ctr(prefix+"dlq_records", "Records sent to the dead-letter topic")
		h.m.fenced = ctr(prefix+"fenced_batches", "Batches whose offset write was fenced (a zombie behind the leader)")
		var err error
		if h.m.batchSecs, err = mt.Float64Histogram(prefix+"batch_seconds", metric.WithUnit("s")); err != nil {
			panic(err)
		}
	})
}

// LoadOffsets implements kafkax.OffsetLoader.
func (h *Handler) LoadOffsets(ctx context.Context, topic string, partitions []int32) (map[int32]int64, error) {
	h.init()
	return kafkax.PGOffsets{DB: h.Pool, Group: h.ConsumerGroup, Table: "consumer_offsets"}.Load(ctx, topic, partitions)
}

type poison struct {
	rec    *kgo.Record
	reason string
	err    error
}

type item struct {
	ev    usage.Event
	meter usage.Meter
}

// Apply implements kafkax.PartitionHandler.
func (h *Handler) Apply(ctx context.Context, partition int32, recs []*kgo.Record) error {
	h.init()
	start := time.Now()
	var items []item
	var bad []poison
	meterCache := map[string]meterResult{}

	for _, rec := range recs {
		ev, err := usage.Decode(rec)
		if err != nil {
			var de *usage.DecodeError
			if errors.As(err, &de) {
				if h.Replay == nil { // the live consumer already dead-lettered it
					bad = append(bad, poison{rec, de.Reason, de.Err})
				}
				continue
			}
			return err
		}
		if h.Replay != nil && !h.Replay.wants(ev) {
			continue
		}
		mr, err := h.meter(ctx, meterCache, ev)
		if err != nil {
			return err // infrastructure: pause and retry, never DLQ
		}
		if mr.missing {
			if h.Replay == nil {
				bad = append(bad, poison{rec, "UNKNOWN_METER", fmt.Errorf("meter %q of tenant %s does not exist", ev.Meter, ev.TenantID)})
			}
			continue
		}
		items = append(items, item{ev: ev, meter: mr.meter})
	}

	for _, p := range bad {
		if err := h.DLQ.Send(ctx, p.rec, p.reason, p.err); err != nil {
			return err
		}
		h.m.dlq.Add(ctx, 1, metric.WithAttributes(attribute.String("reason", p.reason)))
		h.Log.WarnContext(ctx, "record sent to DLQ", "partition", partition, "offset", p.rec.Offset, "reason", p.reason, "error", p.err)
	}

	// Period snapshots and inbox partitions are prepared outside the batch transaction: they are idempotent and
	// must exist before it takes its locks.
	if err := h.prepare(ctx, items); err != nil {
		return err
	}

	next := recs[len(recs)-1].Offset + 1
	var fenced bool
	err := pgx.BeginFunc(ctx, h.Pool, func(tx pgx.Tx) error {
		var err error
		fenced, err = h.applyTx(ctx, tx, partition, items, next)
		return err
	})
	if err != nil {
		return err
	}
	if fenced {
		h.m.fenced.Add(ctx, 1)
	}
	h.m.batchSecs.Record(ctx, time.Since(start).Seconds())
	if h.AfterCommit != nil {
		if err := h.AfterCommit(); err != nil {
			return err
		}
	}
	return nil
}

type meterResult struct {
	meter   usage.Meter
	missing bool
}

// meter resolves the meter of an event, retrying a "not found" a few times before giving up on it.
func (h *Handler) meter(ctx context.Context, cache map[string]meterResult, ev usage.Event) (meterResult, error) {
	key := ev.TenantID.String() + "/" + ev.Meter
	if r, ok := cache[key]; ok {
		return r, nil
	}
	var lastErr error
	for attempt := 0; ; attempt++ {
		m, err := h.Meters.Get(ctx, ev.TenantID, ev.Meter)
		switch {
		case err == nil:
			cache[key] = meterResult{meter: m}
			return cache[key], nil
		case errors.Is(err, ErrMeterNotFound):
			if attempt >= len(h.MeterRetryDelays) {
				cache[key] = meterResult{missing: true}
				return cache[key], nil
			}
			select {
			case <-ctx.Done():
				return meterResult{}, ctx.Err()
			case <-time.After(h.MeterRetryDelays[attempt]):
			}
		default:
			lastErr = err
			return meterResult{}, fmt.Errorf("meter lookup: %w", lastErr)
		}
	}
}

func (h *Handler) prepare(ctx context.Context, items []item) error {
	seen := map[settings.Key]bool{}
	var keys []settings.Key
	periods := map[string]bool{}
	for _, it := range items {
		k := settings.Key{Tenant: it.ev.TenantID, Period: it.ev.Period}
		if !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
		periods[it.ev.Period] = true
	}
	q := sqlc.New(h.Pool)
	for p := range periods {
		if _, ok := h.ensured.Load(p); ok {
			continue
		}
		if err := q.EnsureProcessedPartition(ctx, p); err != nil {
			return fmt.Errorf("ensure inbox partition %s: %w", p, err)
		}
		h.ensured.Store(p, true)
	}
	if err := h.Settings.Ensure(ctx, q, keys); err != nil {
		return fmt.Errorf("period settings: %w", err)
	}
	return nil
}

// applyTx is the batch transaction. It returns fenced=true when the offset write was a no-op because another
// consumer is already further along.
func (h *Handler) applyTx(ctx context.Context, tx pgx.Tx, partition int32, items []item, next int64) (bool, error) {
	q := sqlc.New(tx)
	if len(items) > 0 {
		if err := h.applyItems(ctx, q, items); err != nil {
			return false, err
		}
	}
	// The offset never moves backwards.
	n, err := q.AdvanceOffset(ctx, sqlc.AdvanceOffsetParams{ConsumerGroup: h.ConsumerGroup, Topic: h.Topic, Partition: partition, NextOffset: next})
	if err != nil {
		return false, fmt.Errorf("advance offset: %w", err)
	}
	return n == 0, nil
}

// applyItems locks the batch's period settings and generations, then dedups, classifies, pre-sums and upserts.
func (h *Handler) applyItems(ctx context.Context, q *sqlc.Queries, items []item) error {
	if err := q.SetTransactionTimeout(ctx, "30s"); err != nil {
		return err
	}
	tenants, periods := distinct(items)
	cfgRows, err := q.LockPeriodSettings(ctx, sqlc.LockPeriodSettingsParams{Tenants: tenants, Periods: periods})
	if err != nil {
		return fmt.Errorf("lock period settings: %w", err)
	}
	cfg := make(map[settings.Key]sqlc.LockPeriodSettingsRow, len(cfgRows))
	for _, r := range cfgRows {
		cfg[settings.Key{Tenant: r.TenantID, Period: r.PeriodID}] = r
	}
	gens, err := h.targetGenerations(ctx, q, tenants)
	if err != nil {
		return err
	}
	fresh, err := h.dedup(ctx, q, items, gens)
	if err != nil {
		return err
	}
	delta, err := h.classify(ctx, items, fresh, cfg)
	if err != nil {
		return err
	}
	if err := upsertOnTime(ctx, q, delta); err != nil {
		return err
	}
	return upsertLate(ctx, q, delta)
}

func (h *Handler) targetGenerations(ctx context.Context, q *sqlc.Queries, tenants []uuid.UUID) (generationSet, error) {
	if h.Replay != nil {
		return generationSet{h.Replay.Tenant: {h.Replay.Generation}}, nil
	}
	return h.generations(ctx, q, tenants)
}

// inboxKey is an (event, generation) pair of the inbox; freshRow is a new pair and the first item it came from.
type inboxKey struct {
	tenant uuid.UUID
	event  string
	gen    int32
}

type freshRow struct {
	idx int
	gen int32
}

// dedup claims the batch's (event, generation) pairs in the inbox and returns the new ones in item order.
func (h *Handler) dedup(ctx context.Context, q *sqlc.Queries, items []item, gens generationSet) ([]freshRow, error) {
	var p sqlc.InsertProcessedEventsParams
	first := map[inboxKey]int{}
	for i, it := range items {
		for _, g := range gens.targets(it.ev.TenantID) {
			k := inboxKey{it.ev.TenantID, it.ev.EventID, g}
			if _, dup := first[k]; dup {
				h.m.duplicates.Add(ctx, 1, tenantAttr(it.ev.TenantID))
				continue
			}
			first[k] = i
			p.Tenants, p.EventIds, p.Generations, p.Periods = append(p.Tenants, it.ev.TenantID), append(p.EventIds, it.ev.EventID), append(p.Generations, g), append(p.Periods, it.ev.Period)
		}
	}
	rows, err := q.InsertProcessedEvents(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("inbox insert: %w", err)
	}
	fresh := make([]freshRow, len(rows))
	for i, r := range rows {
		fresh[i] = freshRow{first[inboxKey{r.TenantID, r.EventID, r.Generation}], r.Generation}
	}
	if len(p.Tenants) > len(fresh) {
		h.countInboxDuplicates(ctx, p, rows)
	}
	sort.Slice(fresh, func(i, j int) bool {
		if fresh[i].idx != fresh[j].idx {
			return fresh[i].idx < fresh[j].idx
		}
		return fresh[i].gen < fresh[j].gen
	})
	return fresh, nil
}

// countInboxDuplicates counts, per tenant, the pairs of the batch that the inbox already had.
func (h *Handler) countInboxDuplicates(ctx context.Context, p sqlc.InsertProcessedEventsParams, fresh []sqlc.InsertProcessedEventsRow) {
	seen := make(map[inboxKey]struct{}, len(fresh))
	for _, f := range fresh {
		seen[inboxKey{f.TenantID, f.EventID, f.Generation}] = struct{}{}
	}
	perTenant := map[uuid.UUID]int64{}
	for i := range p.Tenants {
		if _, ok := seen[inboxKey{p.Tenants[i], p.EventIds[i], p.Generations[i]}]; !ok {
			perTenant[p.Tenants[i]]++
		}
	}
	for t, n := range perTenant {
		h.m.duplicates.Add(ctx, n, tenantAttr(t))
	}
}

// classify routes every new pair to on-time or late usage and pre-sums them into a delta.
func (h *Handler) classify(ctx context.Context, items []item, fresh []freshRow, cfg map[settings.Key]sqlc.LockPeriodSettingsRow) (*domain.Delta, error) {
	delta := domain.NewDelta()
	for _, f := range fresh {
		it := items[f.idx]
		if !it.meter.Matches(it.ev.Props) {
			h.m.filtered.Add(ctx, 1, tenantAttr(it.ev.TenantID))
			continue
		}
		c, ok := cfg[settings.Key{Tenant: it.ev.TenantID, Period: it.ev.Period}]
		if !ok {
			return nil, fmt.Errorf("no period settings for %s %s", it.ev.TenantID, it.ev.Period)
		}
		if h.classOf(ctx, it.ev, c) == period.OnTime {
			delta.AddOnTime(domain.OnTimeKey{Tenant: it.ev.TenantID, Customer: it.ev.CustomerID, Meter: it.ev.Meter,
				Period: it.ev.Period, Generation: f.gen}, it.meter, it.ev)
			h.m.applied.Add(ctx, 1, tenantAttr(it.ev.TenantID), classAttr("on_time"))
			continue
		}
		loc, err := time.LoadLocation(c.Timezone)
		if err != nil {
			return nil, fmt.Errorf("period settings of %s have unusable time zone %q", it.ev.TenantID, c.Timezone)
		}
		delta.AddLate(domain.LateKey{Tenant: it.ev.TenantID, Customer: it.ev.CustomerID, Meter: it.ev.Meter,
			Billed: period.PeriodOf(it.ev.IngestedAt, loc), Origin: it.ev.Period, Generation: f.gen}, it.meter, it.ev)
		h.m.late.Add(ctx, 1, tenantAttr(it.ev.TenantID))
		h.m.applied.Add(ctx, 1, tenantAttr(it.ev.TenantID), classAttr("late"))
	}
	return delta, nil
}

// classOf is the class of an event by its ingestion time. A frozen period takes everything as late usage; a replay
// classifies by ingested_at alone, as the live path did at the time.
func (h *Handler) classOf(ctx context.Context, ev usage.Event, c sqlc.LockPeriodSettingsRow) period.Class {
	class := period.Classify(ev.IngestedAt, c.CutoffAt)
	if c.FrozenAt == nil || h.Replay != nil {
		return class
	}
	if class == period.OnTime {
		h.m.frozen.Add(ctx, 1, tenantAttr(ev.TenantID))
		h.Log.ErrorContext(ctx, "event for a frozen period arrived on time; routing it to late usage",
			"tenant", ev.TenantID, "period", ev.Period, "event_id", ev.EventID)
	}
	return period.Late
}

func distinct(items []item) ([]uuid.UUID, []string) {
	ts, ps := map[uuid.UUID]bool{}, map[string]bool{}
	var tenants []uuid.UUID
	var periods []string
	for _, it := range items {
		if !ts[it.ev.TenantID] {
			ts[it.ev.TenantID] = true
			tenants = append(tenants, it.ev.TenantID)
		}
		if !ps[it.ev.Period] {
			ps[it.ev.Period] = true
			periods = append(periods, it.ev.Period)
		}
	}
	return tenants, periods
}

// generationSet knows the generations a tenant's events are written to. A tenant without rows is on generation 1.
type generationSet map[uuid.UUID][]int32

func (g generationSet) targets(t uuid.UUID) []int32 {
	if gs, ok := g[t]; ok && len(gs) > 0 {
		return gs
	}
	return []int32{1}
}

// generations reads each tenant's active and catching-up generation FOR SHARE, so a promotion can't race the batch.
// During a replay's catch-up a tenant's events go to both (dual write).
func (h *Handler) generations(ctx context.Context, q *sqlc.Queries, tenants []uuid.UUID) (generationSet, error) {
	// Twice, on purpose. The first statement takes the locks: it waits for a promotion or a replay's flip that holds the tenant's
	// active row FOR UPDATE. But a row that became 'catching_up' while it waited is not in that statement's result (READ
	// COMMITTED rechecks the rows it had selected, it does not find new ones), so the batch would write only the old generation
	// and the replay would miss it. With the locks held nothing can flip any more, so a second statement, with a fresh
	// snapshot, reads the settled state.
	if _, err := q.LockGenerations(ctx, tenants); err != nil {
		return nil, fmt.Errorf("lock generations: %w", err)
	}
	rows, err := q.LockGenerations(ctx, tenants)
	if err != nil {
		return nil, fmt.Errorf("lock generations: %w", err)
	}
	out := generationSet{}
	for _, r := range rows {
		out[r.TenantID] = append(out[r.TenantID], r.Generation)
	}
	return out, nil
}

// upsertOnTime writes the delta's on-time cells in sorted key order: the sum meters first, then the max meters.
func upsertOnTime(ctx context.Context, q *sqlc.Queries, d *domain.Delta) error {
	for _, isMax := range []bool{false, true} {
		p := sqlc.UpsertUsageAggregatesParams{IsMax: isMax}
		for _, k := range d.SortedOnTime() {
			cell := d.OnTime[k]
			if (cell.Aggregation == usage.Max) != isMax {
				continue
			}
			p.Tenants, p.Periods, p.Customers, p.MeterCodes, p.Generations = append(p.Tenants, k.Tenant), append(p.Periods, k.Period), append(p.Customers, k.Customer), append(p.MeterCodes, k.Meter), append(p.Generations, k.Generation)
			p.Quantities, p.Scales, p.EventCounts = append(p.Quantities, cell.Value.String()), append(p.Scales, int16(cell.Scale)), append(p.EventCounts, cell.Events) //nolint:gosec // 0..6
		}
		if len(p.Tenants) == 0 {
			continue
		}
		if err := q.UpsertUsageAggregates(ctx, p); err != nil {
			return fmt.Errorf("upsert usage_aggregates: %w", err)
		}
	}
	return nil
}

// upsertLate is upsertOnTime for late usage.
func upsertLate(ctx context.Context, q *sqlc.Queries, d *domain.Delta) error {
	for _, isMax := range []bool{false, true} {
		p := sqlc.UpsertLateUsageParams{IsMax: isMax}
		for _, k := range d.SortedLate() {
			cell := d.Late[k]
			if (cell.Aggregation == usage.Max) != isMax {
				continue
			}
			p.Tenants, p.BilledPeriods, p.Customers, p.MeterCodes, p.OriginPeriods, p.Generations = append(p.Tenants, k.Tenant), append(p.BilledPeriods, k.Billed), append(p.Customers, k.Customer), append(p.MeterCodes, k.Meter), append(p.OriginPeriods, k.Origin), append(p.Generations, k.Generation)
			p.Quantities, p.Scales, p.EventCounts = append(p.Quantities, cell.Value.String()), append(p.Scales, int16(cell.Scale)), append(p.EventCounts, cell.Events) //nolint:gosec // 0..6
		}
		if len(p.Tenants) == 0 {
			continue
		}
		if err := q.UpsertLateUsage(ctx, p); err != nil {
			return fmt.Errorf("upsert late_usage: %w", err)
		}
	}
	return nil
}

var _ kafkax.PartitionHandler = (*Handler)(nil)

func tenantAttr(id uuid.UUID) metric.AddOption {
	return metric.WithAttributes(attribute.String("tenant", id.String()))
}

func classAttr(c string) metric.AddOption {
	return metric.WithAttributes(attribute.String("classification", c))
}
