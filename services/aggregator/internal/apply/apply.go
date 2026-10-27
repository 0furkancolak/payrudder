// Package apply implements the aggregator's batch transaction: dedup, classify, pre-sum, upsert and advance the
// offset, all in one Postgres transaction (docs/03-services/aggregator.md, docs/07-correctness.md §2).
//
// In cells mode (ADR 0018) one batch is split by placement: the tenants in the pooled database are written, with the offset, in the one transaction above, and each
// tenant placed in a cell is written in a transaction of its own in its schema, before it. The offset moves only after every split is durable; a batch that fails
// half way is delivered again and the inbox (processed_events, in each tenant's own place) makes the splits that were durable no-ops.
package apply

import (
	"bytes"
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
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"google.golang.org/grpc/codes"

	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/kafkax"
	"github.com/0furkancolak/payrudder/pkg/otelx"
	"github.com/0furkancolak/payrudder/pkg/period"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
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

// batchTxTimeout bounds one batch transaction (a Postgres interval).
const batchTxTimeout = "30s"

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
	// Pool is aggregator_system on the pooled database: the pooled tenants' rows and, always, the consumer's offsets.
	Pool *pgxpool.Pool
	// Cells, when set, is the system-role router of cells mode (ADR 0018): a tenant placed in a cell is written in its own schema, in a transaction of its own.
	Cells    *pgxx.Router
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
	// AfterSplits runs, in cells mode, after the transactions of the tenants placed in cells are durable and before the transaction that moves the offset. A non-nil
	// error simulates a crash at that point (tests).
	AfterSplits func() error

	once    sync.Once
	ensured sync.Map // place|period of the inbox partitions known to exist
	m       metrics
}

type metrics struct {
	applied, duplicates, late, filtered, frozen, dlq, fenced metric.Int64Counter
	missingValue, lateLast                                   metric.Int64Counter
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
		b := otelx.NewInstruments("github.com/0furkancolak/payrudder/services/aggregator")
		prefix := "aggregator_"
		if h.Replay != nil {
			prefix = "aggregator_replay_" // a replay is not live traffic: its counters must not move the live ones
		}
		h.m.applied = b.Counter(prefix+"events_applied", "Events applied to an aggregate")
		h.m.duplicates = b.Counter(prefix+"duplicates_skipped", "Events skipped because the inbox already had them")
		h.m.late = b.Counter(prefix+"late_events", "Events routed to late usage")
		h.m.filtered = b.Counter(prefix+"filtered_events", "Events ignored by the meter filter")
		h.m.frozen = b.Counter(prefix+"frozen_period_violations", "On-time events that arrived for a frozen period")
		h.m.dlq = b.Counter(prefix+"dlq_records", "Records sent to the dead-letter topic")
		h.m.fenced = b.Counter(prefix+"fenced_batches", "Batches whose offset write was fenced (a zombie behind the leader)")
		h.m.missingValue = b.Counter(prefix+"missing_value_events", "Events of a UNIQUE meter that don't carry its value property, which count nothing")
		h.m.lateLast = b.Counter(prefix+"late_last_events", "Late events of a LAST meter, which never change the closed period and are not stored")
		h.m.batchSecs = b.Histogram(prefix+"batch_seconds", "", metric.WithUnit("s"))
		if err := b.Err(); err != nil {
			panic(err) // only on duplicate instrument names with different kinds: a programming error
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
	items, bad, err := h.decodeBatch(ctx, recs)
	if err != nil {
		return err
	}
	if err := h.sendDeadLetters(ctx, partition, bad); err != nil {
		return err
	}

	next := recs[len(recs)-1].Offset + 1
	fenced, err := h.applyBatch(ctx, partition, items, next)
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

// decodeBatch decodes the records of a batch and resolves their meters. A record that can never be applied (undecodable, unknown meter)
// is returned as poison, except in a replay, where the live consumer has already dead-lettered it.
func (h *Handler) decodeBatch(ctx context.Context, recs []*kgo.Record) (items []item, bad []poison, err error) {
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
			return nil, nil, err
		}
		if h.Replay != nil && !h.Replay.wants(ev) {
			continue
		}
		mr, err := h.meter(ctx, meterCache, ev)
		if err != nil {
			return nil, nil, err // infrastructure: pause and retry, never DLQ
		}
		if mr.missing {
			if h.Replay == nil {
				bad = append(bad, poison{rec, "UNKNOWN_METER", fmt.Errorf("meter %q of tenant %s does not exist", ev.Meter, ev.TenantID)})
			}
			continue
		}
		items = append(items, item{ev: ev, meter: mr.meter})
	}
	return items, bad, nil
}

// sendDeadLetters publishes the poison records of a batch to the dead-letter topic.
func (h *Handler) sendDeadLetters(ctx context.Context, partition int32, bad []poison) error {
	// One line per reason and batch, not per record: a replay of events for a meter that is gone would write thousands.
	type tally struct {
		n     int
		first int64
		err   error
	}
	byReason := map[string]*tally{}
	for _, p := range bad {
		if err := h.DLQ.Send(ctx, p.rec, p.reason, p.err); err != nil {
			return err
		}
		h.m.dlq.Add(ctx, 1, metric.WithAttributes(attribute.String("reason", p.reason)))
		if t := byReason[p.reason]; t != nil {
			t.n++
		} else {
			byReason[p.reason] = &tally{n: 1, first: p.rec.Offset, err: p.err}
		}
	}
	for reason, t := range byReason {
		h.Log.WarnContext(ctx, "records sent to DLQ", "partition", partition, "reason", reason, "records", t.n, "first_offset", t.first, "error", t.err)
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
			return meterResult{}, fmt.Errorf("meter lookup: %w", err)
		}
	}
}

// cellBatch is the part of a batch that belongs to one tenant placed in a cell.
type cellBatch struct {
	tenant uuid.UUID
	target pgxx.Target
	items  []item
}

// place splits the items of a batch by where their tenants live: those in the pooled database, in item order, and one cellBatch per tenant placed in a cell, ordered
// by tenant. Every tenant is located before anything is written, so a tenant that is moving, or whose place cannot be had, stops the batch untouched: the partition
// waits for the move (a retry is safe), no tenant is skipped. Without cells everything is pooled.
func (h *Handler) place(ctx context.Context, items []item) (pooled []item, cells []cellBatch, err error) {
	if h.Cells == nil {
		return items, nil, nil
	}
	at := map[uuid.UUID]int{} // index into cells; -1 for a pooled tenant
	for _, it := range items {
		id := it.ev.TenantID
		i, known := at[id]
		if !known {
			t, err := h.Cells.Locate(ctx, id)
			if err != nil {
				return nil, nil, fmt.Errorf("tenant %s: %w", id, err)
			}
			i = -1
			if t.Schema != "" {
				i = len(cells)
				cells = append(cells, cellBatch{tenant: id, target: t})
			}
			at[id] = i
		}
		if i < 0 {
			pooled = append(pooled, it)
		} else {
			cells[i].items = append(cells[i].items, it)
		}
	}
	sort.Slice(cells, func(i, j int) bool { return bytes.Compare(cells[i].tenant[:], cells[j].tenant[:]) < 0 })
	return pooled, cells, nil
}

// applyBatch writes a batch and moves the offset. The tenants placed in cells come first, each in its own transaction in its own schema (their failures are joined:
// one tenant's does not keep the others from becoming durable, which a retry finds already applied); the offset moves in the pooled transaction, after all of them,
// and not at all if any failed. It returns fenced=true when the offset write was a no-op because another consumer is already further along.
func (h *Handler) applyBatch(ctx context.Context, partition int32, items []item, next int64) (bool, error) {
	pooled, cells, err := h.place(ctx, items)
	if err != nil {
		return false, err
	}
	var errs []error
	for _, c := range cells {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if err := h.applyCell(ctx, c); err != nil {
			errs = append(errs, fmt.Errorf("tenant %s: %w", c.tenant, err))
		}
	}
	if err := errors.Join(errs...); err != nil {
		return false, err
	}
	if len(cells) > 0 && h.AfterSplits != nil {
		if err := h.AfterSplits(); err != nil {
			return false, err
		}
	}

	// Period snapshots and inbox partitions are prepared outside the batch transaction: they are idempotent and
	// must exist before it takes its locks.
	made, err := h.prepare(ctx, sqlc.New(h.Pool), "", pooled)
	if err != nil {
		return false, err
	}
	h.remember("", made)
	var fenced bool
	err = pgx.BeginFunc(ctx, h.Pool, func(tx pgx.Tx) error {
		var err error
		fenced, err = h.applyTx(ctx, tx, partition, pooled, next)
		return err
	})
	return fenced, err
}

// applyCell writes the events of one tenant placed in a cell: the snapshots and partitions in one transaction of its schema, then the batch itself in another, the
// same two steps as the pooled path. The inbox rows commit with the aggregates, so a redelivered event finds itself applied.
func (h *Handler) applyCell(ctx context.Context, c cellBatch) error {
	place := c.target.Cell + "/" + c.target.Schema
	var made []string
	err := h.inCell(ctx, c, func(tx pgx.Tx) (err error) {
		made, err = h.prepare(ctx, sqlc.New(tx), place, c.items)
		return err
	})
	if err != nil {
		return err
	}
	h.remember(place, made) // only now: the transaction that made the partitions could have failed and taken them with it
	return h.inCell(ctx, c, func(tx pgx.Tx) error { return h.applyItems(ctx, sqlc.New(tx), c.items) })
}

// inCell runs fn in a transaction of the tenant's schema on its cell's system pool. A schema that is not where the router looked (a move, an answer older than it)
// makes the router ask the directory again next time.
func (h *Handler) inCell(ctx context.Context, c cellBatch, fn func(pgx.Tx) error) error {
	err := pgxx.InTenantSchemaTx(ctx, c.target.Pool, c.tenant, fn)
	if code := pgxx.ErrCode(err); code == "42P01" || code == "3F000" {
		h.Cells.Invalidate(c.tenant)
	}
	return err
}

// prepare snapshots the period settings of the batch's tenants and makes the inbox partitions of its periods exist, through q (the pool for the pooled tenants, a
// transaction of the tenant's schema in a cell). place names the schema the partitions are in. It returns the periods whose partition it had to ensure, for remember.
func (h *Handler) prepare(ctx context.Context, q *sqlc.Queries, place string, items []item) (made []string, err error) {
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
	for p := range periods {
		if _, ok := h.ensured.Load(place + "|" + p); ok {
			continue
		}
		if err := q.EnsureProcessedPartition(ctx, p); err != nil {
			return nil, fmt.Errorf("ensure inbox partition %s: %w", p, err)
		}
		made = append(made, p)
	}
	if err := h.Settings.Ensure(ctx, q, keys); err != nil {
		return nil, fmt.Errorf("period settings: %w", err)
	}
	return made, nil
}

// remember records that the inbox partitions of periods exist in place.
func (h *Handler) remember(place string, periods []string) {
	for _, p := range periods {
		h.ensured.Store(place+"|"+p, true)
	}
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
	if err := q.SetTransactionTimeout(ctx, batchTxTimeout); err != nil {
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
	if err := applyDistinct(ctx, q, delta); err != nil {
		return err
	}
	if err := upsertOnTime(ctx, q, delta); err != nil {
		return err
	}
	if err := upsertHours(ctx, q, delta); err != nil {
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

// classify routes every new pair to on-time or late usage and pre-sums them into a delta. The events of a UNIQUE meter add their value to the
// delta's sets instead (the counts are settled against the stored sets by applyDistinct), and a late event of a LAST meter is dropped: the last value of
// a closed period never changes.
func (h *Handler) classify(ctx context.Context, items []item, fresh []freshRow, cfg map[settings.Key]sqlc.LockPeriodSettingsRow) (*domain.Delta, error) {
	delta := domain.NewDelta()
	locs := map[string]*time.Location{}
	for _, f := range fresh {
		it := items[f.idx]
		if !it.meter.Matches(it.ev.Props) {
			h.m.filtered.Add(ctx, 1, tenantAttr(it.ev.TenantID))
			continue
		}
		var digest domain.Digest
		if it.meter.Aggregation == usage.Unique {
			v, ok := it.meter.DistinctValue(it.ev)
			if !ok {
				h.m.missingValue.Add(ctx, 1, tenantAttr(it.ev.TenantID))
				continue
			}
			digest = usage.Digest(v)
		}
		c, ok := cfg[settings.Key{Tenant: it.ev.TenantID, Period: it.ev.Period}]
		if !ok {
			return nil, fmt.Errorf("no period settings for %s %s", it.ev.TenantID, it.ev.Period)
		}
		if h.classOf(ctx, it.ev, c) == period.OnTime {
			delta.AddOnTime(domain.OnTimeKey{Tenant: it.ev.TenantID, Customer: it.ev.CustomerID, Meter: it.ev.Meter,
				Period: it.ev.Period, Generation: f.gen}, it.meter, it.ev)
			if it.meter.Aggregation == usage.Unique {
				delta.AddSeen(seenKey(it, f.gen, digest), "")
			}
			if err := addHour(delta, locs, c.Timezone, it, f.gen, digest); err != nil {
				return nil, err
			}
			h.m.applied.Add(ctx, 1, tenantAttr(it.ev.TenantID), classAttr("on_time"))
			continue
		}
		if it.meter.Aggregation == usage.Last {
			h.m.lateLast.Add(ctx, 1, tenantAttr(it.ev.TenantID))
			continue
		}
		loc, err := tzLocation(locs, c.Timezone, it.ev.TenantID)
		if err != nil {
			return nil, err
		}
		billed := period.PeriodOf(it.ev.IngestedAt, loc)
		delta.AddLate(domain.LateKey{Tenant: it.ev.TenantID, Customer: it.ev.CustomerID, Meter: it.ev.Meter,
			Billed: billed, Origin: it.ev.Period, Generation: f.gen}, it.meter, it.ev)
		if it.meter.Aggregation == usage.Unique {
			delta.AddSeen(seenKey(it, f.gen, digest), billed)
		}
		h.m.late.Add(ctx, 1, tenantAttr(it.ev.TenantID))
		h.m.applied.Add(ctx, 1, tenantAttr(it.ev.TenantID), classAttr("late"))
	}
	return delta, nil
}

func seenKey(it item, gen int32, digest domain.Digest) domain.SeenKey {
	return domain.SeenKey{Tenant: it.ev.TenantID, Customer: it.ev.CustomerID, Meter: it.ev.Meter, Period: it.ev.Period, Generation: gen, Value: digest}
}

// tzLocation loads the tenant's time zone from its period settings, once per batch.
func tzLocation(cache map[string]*time.Location, name string, tenant uuid.UUID) (*time.Location, error) {
	if loc, ok := cache[name]; ok {
		return loc, nil
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("period settings of %s have unusable time zone %q", tenant, name)
	}
	cache[name] = loc
	return loc, nil
}

// addHour adds an on-time event to the hour total it falls in (to the hour's value set for a UNIQUE meter), unless that hour is older than the totals are
// kept for (maintenance would delete the row at once).
func addHour(d *domain.Delta, locs map[string]*time.Location, tz string, it item, gen int32, digest domain.Digest) error {
	loc, err := tzLocation(locs, tz, it.ev.TenantID)
	if err != nil {
		return err
	}
	id, _, end := period.WindowOf(period.Hour, it.ev.EventTime, loc)
	if end.Before(time.Now().Add(-domain.WindowRetention)) {
		return nil
	}
	if it.meter.Aggregation == usage.Unique {
		d.AddHourSeen(domain.HourSeenKey{Tenant: it.ev.TenantID, Customer: it.ev.CustomerID, Meter: it.ev.Meter, Hour: id, Generation: gen, Value: digest})
		return nil
	}
	d.AddHour(domain.HourKey{Tenant: it.ev.TenantID, Customer: it.ev.CustomerID, Meter: it.ev.Meter, Hour: id, Generation: gen}, it.meter, it.ev)
	return nil
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

// modes are the merges of an upsert, in the order a batch writes them.
var modes = []string{"sum", "max", "last"}

// lastArgs renders the order key of a cell for the upsert: "" for every aggregation but LAST. The time is in microseconds, which is what timestamptz keeps.
func lastArgs(c *domain.Cell) (at, id string) {
	if c.Aggregation != usage.Last {
		return "", ""
	}
	return time.UnixMicro(c.Order.Micros).UTC().Format("2006-01-02T15:04:05.000000Z07:00"), c.Order.ID
}

// upsertOnTime writes the delta's on-time cells in sorted key order, one statement per merge mode: the adding meters first, then the max ones, then the last ones.
func upsertOnTime(ctx context.Context, q *sqlc.Queries, d *domain.Delta) error {
	for _, mode := range modes {
		p := sqlc.UpsertUsageAggregatesParams{Mode: mode}
		for _, k := range d.SortedOnTime() {
			cell := d.OnTime[k]
			if domain.Mode(cell.Aggregation) != mode {
				continue
			}
			p.Tenants, p.Periods, p.Customers, p.MeterCodes, p.Generations = append(p.Tenants, k.Tenant), append(p.Periods, k.Period), append(p.Customers, k.Customer), append(p.MeterCodes, k.Meter), append(p.Generations, k.Generation)
			p.Quantities, p.Scales, p.EventCounts = append(p.Quantities, cell.Value.String()), append(p.Scales, int16(cell.Scale)), append(p.EventCounts, cell.Events) //nolint:gosec // 0..6
			at, id := lastArgs(cell)
			p.LastAts, p.LastIds = append(p.LastAts, at), append(p.LastIds, id)
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

// upsertHours is upsertOnTime for the hour totals.
func upsertHours(ctx context.Context, q *sqlc.Queries, d *domain.Delta) error {
	for _, mode := range modes {
		p := sqlc.UpsertUsageWindowAggregatesParams{Mode: mode}
		for _, k := range d.SortedHours() {
			cell := d.Hours[k]
			if domain.Mode(cell.Aggregation) != mode {
				continue
			}
			p.Tenants, p.Customers, p.MeterCodes, p.Generations, p.HourIds = append(p.Tenants, k.Tenant), append(p.Customers, k.Customer), append(p.MeterCodes, k.Meter), append(p.Generations, k.Generation), append(p.HourIds, k.Hour)
			p.Quantities, p.Scales, p.EventCounts = append(p.Quantities, cell.Value.String()), append(p.Scales, int16(cell.Scale)), append(p.EventCounts, cell.Events) //nolint:gosec // 0..6
			at, id := lastArgs(cell)
			p.LastAts, p.LastIds = append(p.LastAts, at), append(p.LastIds, id)
		}
		if len(p.Tenants) == 0 {
			continue
		}
		if err := q.UpsertUsageWindowAggregates(ctx, p); err != nil {
			return fmt.Errorf("upsert usage_window_aggregates: %w", err)
		}
	}
	return nil
}

// upsertLate is upsertOnTime for late usage. LAST has no late usage (a closed period's last value is final), so the modes are sum and max.
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

// applyDistinct settles the batch's UNIQUE values against the stored sets and moves the counts of the delta accordingly. A value that is new in its period adds
// one to the row of its state (usage_aggregates when an on-time event carried it, late_usage billed in the period of its first late event otherwise); a value that
// exists moves to the minimum of its stored and its new state (domain.MergeSeen) and takes its unit of count along. So the stored count of every row equals the number
// of values in that state, whatever order the events of a value arrive in. Hour sets are written for on-time events only and counted when read.
func applyDistinct(ctx context.Context, q *sqlc.Queries, d *domain.Delta) error {
	if len(d.Seen) > 0 {
		var p sqlc.InsertDistinctValuesParams
		for _, k := range domain.SortedSeen(d.Seen) {
			h := k.Value
			p.Tenants, p.Customers, p.MeterCodes, p.Generations = append(p.Tenants, k.Tenant), append(p.Customers, k.Customer), append(p.MeterCodes, k.Meter), append(p.Generations, k.Generation)
			p.Periods, p.Hashes, p.Billed = append(p.Periods, k.Period), append(p.Hashes, h[:]), append(p.Billed, d.Seen[k])
		}
		inserted, err := q.InsertDistinctValues(ctx, p)
		if err != nil {
			return fmt.Errorf("insert usage_distinct_values: %w", err)
		}
		for _, r := range inserted {
			contribute(d, r.TenantID, r.CustomerID, r.MeterCode, r.Generation, r.PeriodID, r.BilledPeriodID, 1)
		}
		if len(inserted) < len(p.Tenants) {
			moved, err := q.MergeDistinctValues(ctx, sqlc.MergeDistinctValuesParams(p))
			if err != nil {
				return fmt.Errorf("merge usage_distinct_values: %w", err)
			}
			for _, r := range moved {
				contribute(d, r.TenantID, r.CustomerID, r.MeterCode, r.Generation, r.PeriodID, r.OldBilled, -1)
				contribute(d, r.TenantID, r.CustomerID, r.MeterCode, r.Generation, r.PeriodID, r.NewBilled, 1)
			}
		}
	}
	if len(d.HourSeen) > 0 {
		var p sqlc.InsertDistinctHoursParams
		for _, k := range domain.SortedHourSeen(d.HourSeen) {
			h := k.Value
			p.Tenants, p.Customers, p.MeterCodes, p.Generations = append(p.Tenants, k.Tenant), append(p.Customers, k.Customer), append(p.MeterCodes, k.Meter), append(p.Generations, k.Generation)
			p.HourIds, p.Hashes = append(p.HourIds, k.Hour), append(p.Hashes, h[:])
		}
		if err := q.InsertDistinctHours(ctx, p); err != nil {
			return fmt.Errorf("insert usage_distinct_hours: %w", err)
		}
	}
	return nil
}

// contribute adds n units of distinct count to the row a value in the state billed belongs to.
func contribute(d *domain.Delta, tenant, customer uuid.UUID, meter string, gen int32, period, billed string, n int64) {
	if billed == "" {
		d.AddDistinctOnTime(domain.OnTimeKey{Tenant: tenant, Customer: customer, Meter: meter, Period: period, Generation: gen}, n)
		return
	}
	d.AddDistinctLate(domain.LateKey{Tenant: tenant, Customer: customer, Meter: meter, Billed: billed, Origin: period, Generation: gen}, n)
}

var _ kafkax.PartitionHandler = (*Handler)(nil)

func tenantAttr(id uuid.UUID) metric.AddOption {
	return metric.WithAttributes(attribute.String("tenant", id.String()))
}

func classAttr(c string) metric.AddOption {
	return metric.WithAttributes(attribute.String("classification", c))
}
