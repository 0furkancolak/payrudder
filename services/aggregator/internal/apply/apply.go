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

	"github.com/0furkancolak/quotify/pkg/kafkax"
	"github.com/0furkancolak/quotify/pkg/period"
	"github.com/0furkancolak/quotify/services/aggregator/internal/domain"
	"github.com/0furkancolak/quotify/services/aggregator/internal/meters"
	"github.com/0furkancolak/quotify/services/aggregator/internal/settings"
	"github.com/0furkancolak/quotify/services/aggregator/internal/store/sqlc"
)

// Topic and group the live consumer uses.
const (
	Topic = "usage.events.v1"
	Group = "aggregator"
)

// MeterSource resolves meters.
type MeterSource interface {
	Get(ctx context.Context, tenantID uuid.UUID, code string) (domain.Meter, error)
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

func (r *Replay) wants(ev domain.Event) bool {
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
		mt := otel.Meter("github.com/0furkancolak/quotify/services/aggregator")
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
	ps := make([]int32, len(partitions))
	copy(ps, partitions)
	rows, err := sqlc.New(h.Pool).LoadOffsets(ctx, sqlc.LoadOffsetsParams{ConsumerGroup: h.ConsumerGroup, Topic: topic, Partitions: ps})
	if err != nil {
		return nil, err
	}
	out := make(map[int32]int64, len(rows))
	for _, r := range rows {
		out[r.Partition] = r.NextOffset
	}
	return out, nil
}

type poison struct {
	rec    *kgo.Record
	reason string
	err    error
}

type item struct {
	ev    domain.Event
	meter domain.Meter
}

// Apply implements kafkax.PartitionHandler.
func (h *Handler) Apply(ctx context.Context, partition int32, recs []*kgo.Record) error {
	h.init()
	start := time.Now()
	var items []item
	var bad []poison
	meterCache := map[string]meterResult{}

	for _, rec := range recs {
		ev, err := domain.Decode(rec)
		if err != nil {
			var de *domain.DecodeError
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
	meter   domain.Meter
	missing bool
}

// meter resolves the meter of an event, retrying a "not found" a few times before giving up on it.
func (h *Handler) meter(ctx context.Context, cache map[string]meterResult, ev domain.Event) (meterResult, error) {
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
		case errors.Is(err, meters.ErrNotFound):
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
	delta := domain.NewDelta()

	if len(items) > 0 {
		if _, err := tx.Exec(ctx, `SELECT set_config('transaction_timeout', '30s', true)`); err != nil {
			return false, err
		}
		tenants, periods := distinct(items)
		cfgRows, err := q.LockPeriodSettings(ctx, sqlc.LockPeriodSettingsParams{Tenants: tenants, Periods: periods})
		if err != nil {
			return false, fmt.Errorf("lock period settings: %w", err)
		}
		cfg := make(map[settings.Key]sqlc.LockPeriodSettingsRow, len(cfgRows))
		for _, r := range cfgRows {
			cfg[settings.Key{Tenant: r.TenantID, Period: r.PeriodID}] = r
		}
		var gens generationSet
		if h.Replay != nil {
			gens = generationSet{h.Replay.Tenant: {h.Replay.Generation}}
		} else if gens, err = h.generations(ctx, q, tenants); err != nil {
			return false, err
		}

		// 1. dedup against the inbox: one row per (event, target generation).
		type dkey struct {
			tenant uuid.UUID
			event  string
			gen    int32
		}
		var (
			dT []uuid.UUID
			dE []string
			dG []int32
			dP []string
		)
		first := map[dkey]int{}
		for i, it := range items {
			for _, g := range gens.targets(it.ev.TenantID) {
				k := dkey{it.ev.TenantID, it.ev.EventID, g}
				if _, dup := first[k]; dup {
					h.m.duplicates.Add(ctx, 1, tenantAttr(it.ev.TenantID))
					continue
				}
				first[k] = i
				dT, dE, dG, dP = append(dT, it.ev.TenantID), append(dE, it.ev.EventID), append(dG, g), append(dP, it.ev.Period)
			}
		}
		rows, err := tx.Query(ctx, `INSERT INTO processed_events (tenant_id, event_id, generation, period_id)
			SELECT * FROM unnest($1::uuid[], $2::text[], $3::int[], $4::text[])
			ON CONFLICT DO NOTHING RETURNING tenant_id, event_id, generation`, dT, dE, dG, dP)
		if err != nil {
			return false, fmt.Errorf("inbox insert: %w", err)
		}
		type newRow struct {
			idx int
			gen int32
		}
		var fresh []newRow
		for rows.Next() {
			var k dkey
			if err := rows.Scan(&k.tenant, &k.event, &k.gen); err != nil {
				rows.Close()
				return false, err
			}
			fresh = append(fresh, newRow{first[k], k.gen})
		}
		if err := rows.Err(); err != nil {
			return false, err
		}
		if len(dT) > len(fresh) {
			seen := make(map[dkey]struct{}, len(fresh))
			for _, f := range fresh {
				seen[dkey{items[f.idx].ev.TenantID, items[f.idx].ev.EventID, f.gen}] = struct{}{}
			}
			perTenant := map[uuid.UUID]int64{}
			for i := range dT {
				if _, ok := seen[dkey{dT[i], dE[i], dG[i]}]; !ok {
					perTenant[dT[i]]++
				}
			}
			for t, n := range perTenant {
				h.m.duplicates.Add(ctx, n, tenantAttr(t))
			}
		}
		sort.Slice(fresh, func(i, j int) bool {
			if fresh[i].idx != fresh[j].idx {
				return fresh[i].idx < fresh[j].idx
			}
			return fresh[i].gen < fresh[j].gen
		})

		// 2. classify and pre-sum.
		for _, f := range fresh {
			it := items[f.idx]
			if !it.meter.Matches(it.ev.Props) {
				h.m.filtered.Add(ctx, 1, tenantAttr(it.ev.TenantID))
				continue
			}
			c, ok := cfg[settings.Key{Tenant: it.ev.TenantID, Period: it.ev.Period}]
			if !ok {
				return false, fmt.Errorf("no period settings for %s %s", it.ev.TenantID, it.ev.Period)
			}
			class := period.Classify(it.ev.IngestedAt, c.CutoffAt)
			if c.FrozenAt != nil && h.Replay == nil { // a replay classifies by ingested_at alone, as the live path did at the time
				if class == period.OnTime {
					h.m.frozen.Add(ctx, 1, tenantAttr(it.ev.TenantID))
					h.Log.ErrorContext(ctx, "event for a frozen period arrived on time; routing it to late usage",
						"tenant", it.ev.TenantID, "period", it.ev.Period, "event_id", it.ev.EventID)
				}
				class = period.Late
			}
			if class == period.OnTime {
				delta.AddOnTime(domain.OnTimeKey{Tenant: it.ev.TenantID, Customer: it.ev.CustomerID, Meter: it.ev.Meter,
					Period: it.ev.Period, Generation: f.gen}, it.meter, it.ev)
				h.m.applied.Add(ctx, 1, tenantAttr(it.ev.TenantID), classAttr("on_time"))
				continue
			}
			loc, err := time.LoadLocation(c.Timezone)
			if err != nil {
				return false, fmt.Errorf("period settings of %s have unusable time zone %q", it.ev.TenantID, c.Timezone)
			}
			delta.AddLate(domain.LateKey{Tenant: it.ev.TenantID, Customer: it.ev.CustomerID, Meter: it.ev.Meter,
				Billed: period.PeriodOf(it.ev.IngestedAt, loc), Origin: it.ev.Period, Generation: f.gen}, it.meter, it.ev)
			h.m.late.Add(ctx, 1, tenantAttr(it.ev.TenantID))
			h.m.applied.Add(ctx, 1, tenantAttr(it.ev.TenantID), classAttr("late"))
		}

		// 3. upserts.
		if err := upsertOnTime(ctx, tx, delta); err != nil {
			return false, err
		}
		if err := upsertLate(ctx, tx, delta); err != nil {
			return false, err
		}
	}

	// 4. advance the offset, fenced: it never moves backwards.
	tag, err := tx.Exec(ctx, `INSERT INTO consumer_offsets (consumer_group, topic, partition, next_offset)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (consumer_group, topic, partition) DO UPDATE
		SET next_offset = EXCLUDED.next_offset, updated_at = now()
		WHERE consumer_offsets.next_offset <= EXCLUDED.next_offset`, h.ConsumerGroup, h.Topic, partition, next)
	if err != nil {
		return false, fmt.Errorf("advance offset: %w", err)
	}
	return tag.RowsAffected() == 0, nil
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
// During a replay's catch-up a tenant's events go to both (dual write); the replay itself arrives in Phase 11.
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

func upsertOnTime(ctx context.Context, tx pgx.Tx, d *domain.Delta) error {
	for _, agg := range []domain.Aggregation{domain.Sum, domain.Max} {
		var (
			t []uuid.UUID
			p []string
			c []uuid.UUID
			m []string
			g []int32
			q []string
			s []int16
			n []int64
		)
		for _, k := range d.SortedOnTime() {
			cell := d.OnTime[k]
			if (cell.Aggregation == domain.Max) != (agg == domain.Max) {
				continue
			}
			t, p, c, m, g = append(t, k.Tenant), append(p, k.Period), append(c, k.Customer), append(m, k.Meter), append(g, k.Generation)
			q, s, n = append(q, cell.Value.String()), append(s, int16(cell.Scale)), append(n, cell.Events) //nolint:gosec // 0..6
		}
		if len(t) == 0 {
			continue
		}
		merge := "usage_aggregates.quantity + EXCLUDED.quantity"
		if agg == domain.Max {
			merge = "GREATEST(usage_aggregates.quantity, EXCLUDED.quantity)"
		}
		_, err := tx.Exec(ctx, `INSERT INTO usage_aggregates (tenant_id, period_id, customer_id, meter_code, generation, quantity, quantity_scale, event_count, updated_at)
			SELECT u.t, u.p, u.c, u.m, u.g, u.q::numeric, u.s, u.n, clock_timestamp()
			FROM unnest($1::uuid[], $2::text[], $3::uuid[], $4::text[], $5::int[], $6::text[], $7::smallint[], $8::bigint[]) AS u(t, p, c, m, g, q, s, n)
			ON CONFLICT (tenant_id, period_id, customer_id, meter_code, generation) DO UPDATE
			SET quantity = `+merge+`, event_count = usage_aggregates.event_count + EXCLUDED.event_count, updated_at = EXCLUDED.updated_at`,
			t, p, c, m, g, q, s, n)
		if err != nil {
			return fmt.Errorf("upsert usage_aggregates: %w", err)
		}
	}
	return nil
}

func upsertLate(ctx context.Context, tx pgx.Tx, d *domain.Delta) error {
	for _, agg := range []domain.Aggregation{domain.Sum, domain.Max} {
		var (
			t  []uuid.UUID
			b  []string
			c  []uuid.UUID
			m  []string
			o  []string
			g  []int32
			q  []string
			s  []int16
			ec []int64
		)
		for _, k := range d.SortedLate() {
			cell := d.Late[k]
			if (cell.Aggregation == domain.Max) != (agg == domain.Max) {
				continue
			}
			t, b, c, m, o, g = append(t, k.Tenant), append(b, k.Billed), append(c, k.Customer), append(m, k.Meter), append(o, k.Origin), append(g, k.Generation)
			q, s, ec = append(q, cell.Value.String()), append(s, int16(cell.Scale)), append(ec, cell.Events) //nolint:gosec // 0..6
		}
		if len(t) == 0 {
			continue
		}
		merge := "late_usage.quantity + EXCLUDED.quantity"
		if agg == domain.Max {
			merge = "GREATEST(late_usage.quantity, EXCLUDED.quantity)"
		}
		_, err := tx.Exec(ctx, `INSERT INTO late_usage (tenant_id, billed_period_id, customer_id, meter_code, origin_period_id, generation, quantity, quantity_scale, event_count, updated_at)
			SELECT u.t, u.b, u.c, u.m, u.o, u.g, u.q::numeric, u.s, u.n, clock_timestamp()
			FROM unnest($1::uuid[], $2::text[], $3::uuid[], $4::text[], $5::text[], $6::int[], $7::text[], $8::smallint[], $9::bigint[]) AS u(t, b, c, m, o, g, q, s, n)
			ON CONFLICT (tenant_id, billed_period_id, customer_id, meter_code, origin_period_id, generation) DO UPDATE
			SET quantity = `+merge+`, event_count = late_usage.event_count + EXCLUDED.event_count, updated_at = EXCLUDED.updated_at`,
			t, b, c, m, o, g, q, s, ec)
		if err != nil {
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
