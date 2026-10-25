package engine

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/twmb/franz-go/pkg/kgo"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	eventsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/events/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/meters"
	"github.com/0furkancolak/payrudder/pkg/otelx"
	"github.com/0furkancolak/payrudder/pkg/period"
	"github.com/0furkancolak/payrudder/pkg/tenantinfo"
	"github.com/0furkancolak/payrudder/pkg/usage"
	"github.com/0furkancolak/payrudder/services/limits-svc/internal/domain"
	"github.com/0furkancolak/payrudder/services/limits-svc/internal/entitlements"
)

//go:embed apply_batch.lua
var applyBatchLua string

// KeyTTL is how long a counter outlives the end of its period.
const KeyTTL = 40 * 24 * time.Hour

// firedTTL outlives any counter key, so a threshold is never fired twice for the same period.
const firedTTL = 80 * 24 * time.Hour

// ErrMeterNotFound is the answer for a meter plans-svc does not know.
var ErrMeterNotFound = errkey.New(codes.NotFound, "limits.meter.not_found", "meter not found")

// ErrRedisReset means the offset key vanished while this pod had applied records: Redis lost its data. The partition
// is replayed from the start of the log.
var ErrRedisReset = errors.New("redis lost its offsets: replaying the partition")

// Config configures an Applier.
type Config struct {
	Redis      *redis.Client
	Meters     meters.Source
	Tenants    tenantinfo.Source
	Ents       *entitlements.Store
	Producer   *kgo.Client // LimitExceeded producer
	LimitTopic string
	Log        *slog.Logger
	Now        func() time.Time
	// AfterLua runs after the script committed a batch and before thresholds are evaluated. An error aborts the
	// batch the way a crash at that point would. Chaos tests only.
	AfterLua func() error
}

// Applier applies usage.events.v1 batches to the Redis counters. It implements kafkax.PartitionHandler.
type Applier struct {
	cfg    Config
	log    *slog.Logger
	script *redis.Script
	cps    *checkpoints

	mu    sync.Mutex
	parts map[int32]*partState

	applied    metric.Int64Counter
	skipped    metric.Int64Counter
	overflow   metric.Int64Counter
	fired      metric.Int64Counter
	fireErrors metric.Int64Counter
}

type partState struct {
	mu     sync.Mutex
	expect int64 // next offset this pod believes Redis holds; 0 = none
}

// NewApplier validates cfg and registers metrics.
func NewApplier(cfg Config) (*Applier, error) {
	if cfg.Redis == nil || cfg.Meters == nil || cfg.Tenants == nil || cfg.Ents == nil {
		return nil, errors.New("engine: Redis, Meters, Tenants and Ents are required")
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	b := otelx.NewInstruments("github.com/0furkancolak/payrudder/services/limits-svc")
	a := &Applier{cfg: cfg, log: cfg.Log, script: redis.NewScript(applyBatchLua), cps: newCheckpoints(), parts: map[int32]*partState{}}
	a.applied = b.Counter("limits_events_applied", "Usage events added to a counter")
	a.skipped = b.Counter("limits_events_skipped", "Usage events not counted, by reason")
	a.overflow = b.Counter("limits_counter_overflow", "Counter updates refused because they would pass 2^53")
	a.fired = b.Counter("limits_thresholds_fired", "LimitExceeded events produced")
	a.fireErrors = b.Counter("limits_threshold_publish_failures", "LimitExceeded events that could not be produced")
	if err := b.Err(); err != nil {
		return nil, err
	}
	return a, nil
}

// Load registers the script with Redis (SCRIPT LOAD); calls then use EVALSHA.
func (a *Applier) Load(ctx context.Context) error {
	return a.script.Load(ctx, a.cfg.Redis).Err()
}

func (a *Applier) part(p int32) *partState {
	a.mu.Lock()
	defer a.mu.Unlock()
	ps := a.parts[p]
	if ps == nil {
		ps = &partState{}
		a.parts[p] = ps
	}
	return ps
}

// LoadOffsets is the kafkax.OffsetLoader of the usage consumer: the offsets live in Redis, next to the counters they
// describe. A partition without a key starts at the beginning of the log.
func (a *Applier) LoadOffsets(ctx context.Context, _ string, partitions []int32) (map[int32]int64, error) {
	out := make(map[int32]int64, len(partitions))
	for _, p := range partitions {
		v, err := a.cfg.Redis.Get(ctx, OffsetKey(p)).Result()
		switch {
		case errors.Is(err, redis.Nil):
			out[p] = 0
		case err != nil:
			return nil, err
		default:
			n, perr := strconv.ParseInt(v, 10, 64)
			if perr != nil {
				return nil, fmt.Errorf("%s holds %q: %w", OffsetKey(p), v, perr)
			}
			out[p] = n
		}
	}
	return out, nil
}

// Apply applies the records of one partition, in order, serving any reconciliation checkpoint that falls inside them.
func (a *Applier) Apply(ctx context.Context, p int32, recs []*kgo.Record) error {
	if len(recs) == 0 {
		return nil
	}
	ps := a.part(p)
	ps.mu.Lock()
	defer ps.mu.Unlock()

	last := recs[len(recs)-1].Offset
	for {
		cp := a.cps.first(p)
		if cp == nil || cp.At > last+1 {
			break
		}
		a.cps.remove(p, cp)
		cur := recs[0].Offset
		if cp.At < cur {
			cp.finish(Result{Skipped: 1, SkipReason: "ahead"})
			continue
		}
		n := 0
		for n < len(recs) && recs[n].Offset < cp.At {
			n++
		}
		if n > 0 {
			if err := a.applyRecords(ctx, p, recs[:n]); err != nil {
				a.cps.add(p, cp) // not reached yet
				return err
			}
		}
		a.resolve(ctx, cp)
		recs = recs[n:]
		if len(recs) == 0 {
			return nil
		}
	}
	return a.applyRecords(ctx, p, recs)
}

type cell struct {
	ref      Ref
	isMax    bool
	delta    int64
	over     bool
	expireAt int64
}

func (a *Applier) skip(ctx context.Context, reason string) {
	a.skipped.Add(ctx, 1, metric.WithAttributes(attribute.String("reason", reason)))
}

// batch is what a run of records adds up to before it is sent to Redis in one script call.
type batch struct {
	cells   map[string]*cell
	stamps  map[uuid.UUID]time.Time
	counted int64
}

func (a *Applier) applyRecords(ctx context.Context, p int32, recs []*kgo.Record) error {
	if len(recs) == 0 {
		return nil
	}
	b := &batch{cells: map[string]*cell{}, stamps: map[uuid.UUID]time.Time{}}
	for _, rec := range recs {
		if err := a.accumulate(ctx, b, rec); err != nil {
			return err
		}
	}
	keys, tenantsSorted := a.batchKeys(ctx, b)

	ps := a.part(p)
	first, next := recs[0].Offset, recs[len(recs)-1].Offset+1
	args := make([]any, 0, 6+4*len(keys)+2*len(tenantsSorted))
	args = append(args, first, next, a.cfg.Now().UnixMilli(), ps.expect, len(keys), len(tenantsSorted))
	for _, k := range keys {
		c := b.cells[k]
		op := "I"
		if c.isMax {
			op = "M"
		}
		args = append(args, k, op, c.delta, c.expireAt)
	}
	for _, t := range tenantsSorted {
		args = append(args, TSKey(t), b.stamps[t].UnixMilli())
	}

	res, err := a.script.Run(ctx, a.cfg.Redis, []string{OffsetKey(p), ActiveKey(p)}, args...).Slice()
	if err != nil {
		return fmt.Errorf("apply_batch: %w", err)
	}
	if done, err := a.handleReply(ctx, p, ps, recs, res); done || err != nil {
		return err
	}
	ps.expect = next
	a.applied.Add(ctx, b.counted)

	if a.cfg.AfterLua != nil {
		if err := a.cfg.AfterLua(); err != nil {
			return err
		}
	}
	a.fireCrossed(ctx, res[1:])
	return nil
}

// accumulate folds one record into the batch, or counts it as skipped.
func (a *Applier) accumulate(ctx context.Context, b *batch, rec *kgo.Record) error {
	ev, err := usage.Decode(rec)
	if err != nil {
		a.log.DebugContext(ctx, "usage event skipped as invalid", "partition", rec.Partition, "offset", rec.Offset, "error", err)
		a.skip(ctx, "invalid")
		return nil
	}
	m, err := a.cfg.Meters.Get(ctx, ev.TenantID, ev.Meter)
	if errors.Is(err, ErrMeterNotFound) {
		a.skip(ctx, "unknown_meter")
		return nil
	}
	if err != nil {
		return fmt.Errorf("meter lookup: %w", err)
	}
	if !m.Matches(ev.Props) {
		a.skip(ctx, "filtered")
		return nil
	}
	tn, err := a.cfg.Tenants.Tenant(ctx, ev.TenantID)
	if err != nil {
		return fmt.Errorf("tenant lookup: %w", err)
	}
	cutoff, err := period.Cutoff(ev.Period, tn.Location, tn.Grace)
	if err != nil {
		a.skip(ctx, "invalid")
		return nil
	}
	// Late events are billed in the period of their ingestion, not counted against this one's limit.
	if period.Classify(ev.IngestedAt, cutoff) == period.Late {
		a.skip(ctx, "late")
		return nil
	}
	_, end, _ := period.Bounds(ev.Period, tn.Location)
	ref := Ref{Tenant: ev.TenantID, Customer: ev.CustomerID, Meter: ev.Meter, Period: ev.Period}
	k := ref.Key()
	c := b.cells[k]
	if c == nil {
		c = &cell{ref: ref, isMax: m.Aggregation == usage.Max, expireAt: end.Add(KeyTTL).Unix()}
		b.cells[k] = c
	}
	v := m.Value(ev)
	switch {
	case c.isMax:
		c.delta = max(c.delta, v)
	case c.delta > domain.MaxCounter-v:
		c.over = true
	default:
		c.delta += v
	}
	if ev.IngestedAt.After(b.stamps[ev.TenantID]) {
		b.stamps[ev.TenantID] = ev.IngestedAt
	}
	b.counted++
	return nil
}

// batchKeys returns the counter keys to send and the tenants to stamp, both sorted. Counters whose delta overflowed are
// left out; reconciliation repairs them.
func (a *Applier) batchKeys(ctx context.Context, b *batch) ([]string, []uuid.UUID) {
	keys := make([]string, 0, len(b.cells))
	for k, c := range b.cells {
		if c.over {
			a.overflow.Add(ctx, 1)
			a.log.ErrorContext(ctx, "counter delta too large, leaving the key to reconciliation", "key", k)
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	tenantsSorted := make([]uuid.UUID, 0, len(b.stamps))
	for t := range b.stamps {
		tenantsSorted = append(tenantsSorted, t)
	}
	sort.Slice(tenantsSorted, func(i, j int) bool { return tenantsSorted[i].String() < tenantsSorted[j].String() })
	return keys, tenantsSorted
}

// handleReply interprets the status of apply_batch. done is true when the batch is settled and applyRecords returns err
// as is; false with a nil err means the script applied the batch ("OK").
func (a *Applier) handleReply(ctx context.Context, p int32, ps *partState, recs []*kgo.Record, res []any) (done bool, err error) {
	first, next := recs[0].Offset, recs[len(recs)-1].Offset+1
	status, _ := res[0].(string)
	switch status {
	case "SKIP":
		a.skip(ctx, "redelivered")
		ps.expect = max(ps.expect, next)
		return true, nil
	case "PARTIAL":
		applied, _ := strconv.ParseInt(fmt.Sprint(res[1]), 10, 64)
		rest := recs
		for len(rest) > 0 && rest[0].Offset < applied {
			rest = rest[1:]
		}
		return true, a.applyRecords(ctx, p, rest)
	case "GAP":
		return true, fmt.Errorf("offset gap on partition %d: redis is at %v, batch starts at %d", p, res[1], first)
	case "RESET":
		a.log.WarnContext(ctx, "redis lost its offset key, replaying partition", "partition", p)
		ps.expect = 0
		return true, ErrRedisReset
	case "OK":
		return false, nil
	default:
		return true, fmt.Errorf("apply_batch: unexpected reply %v", res)
	}
}

// fireCrossed checks the (key, before, after) rows of an applied batch against the thresholds.
func (a *Applier) fireCrossed(ctx context.Context, rows []any) {
	for _, item := range rows {
		row, ok := item.([]any)
		if !ok || len(row) != 3 {
			a.log.WarnContext(ctx, "apply_batch returned a malformed threshold row", "row", item)
			continue
		}
		key, _ := row[0].(string)
		if row[1] == "OVERFLOW" {
			a.overflow.Add(ctx, 1)
			a.log.ErrorContext(ctx, "counter would pass 2^53, leaving the key to reconciliation", "key", key)
			continue
		}
		before, err1 := strconv.ParseInt(fmt.Sprint(row[1]), 10, 64)
		after, err2 := strconv.ParseInt(fmt.Sprint(row[2]), 10, 64)
		ref, okRef := ParseCounterKey(key)
		if err1 != nil || err2 != nil || !okRef {
			a.log.WarnContext(ctx, "apply_batch returned an unreadable threshold row", "key", key, "before", row[1], "after", row[2])
			continue
		}
		a.crossed(ctx, ref, before, after)
	}
}

// crossed fires the LimitExceeded events for the thresholds a counter crossed from before to after.
func (a *Applier) crossed(ctx context.Context, ref Ref, before, after int64) {
	a.fireWhere(ctx, ref, after, func(ent entitlements.State, pct int32) bool {
		return domain.Crossed(before, after, ent.Limit, pct)
	})
}

// reached fires the LimitExceeded events for every threshold the counter has already reached: the repair path of reconciliation.
func (a *Applier) reached(ctx context.Context, ref Ref, used int64) {
	a.fireWhere(ctx, ref, used, func(ent entitlements.State, pct int32) bool {
		return domain.Reached(used, ent.Limit, pct)
	})
}

// fireWhere fires the thresholds of the counter's entitlement that hit selects, if the entitlement limits the period.
func (a *Applier) fireWhere(ctx context.Context, ref Ref, used int64, hit func(ent entitlements.State, pct int32) bool) {
	now := a.cfg.Now()
	ent, ok := a.cfg.Ents.Get(ref.Tenant, ref.Customer, ref.Meter, now)
	if !ok || ent.Limit <= 0 || !a.overlaps(ctx, ent, ref.Period) {
		return
	}
	for _, pct := range ent.Thresholds {
		if hit(ent, pct) {
			a.fire(ctx, ent, ref, pct, used, now)
		}
	}
}

// overlaps reports whether the entitlement existed during the period.
func (a *Applier) overlaps(ctx context.Context, ent entitlements.State, periodID string) bool {
	tn, err := a.cfg.Tenants.Tenant(ctx, ent.Tenant)
	if err != nil {
		a.log.WarnContext(ctx, "tenant lookup failed, threshold not evaluated", "tenant", ent.Tenant, "error", err)
		return false
	}
	start, end, err := period.Bounds(periodID, tn.Location)
	if err != nil {
		a.log.WarnContext(ctx, "period bounds failed, threshold not evaluated", "tenant", ent.Tenant, "period", periodID, "error", err)
		return false
	}
	return ent.From.Before(end) && (ent.Until.IsZero() || ent.Until.After(start))
}

// fire produces one LimitExceeded event unless this threshold of this period already fired. A lost marker (Redis
// failover) re-fires, and consumers dedup on the deterministic event id.
func (a *Applier) fire(ctx context.Context, ent entitlements.State, ref Ref, pct int32, used int64, now time.Time) {
	fk := FiredKey(ref.Tenant, ref.Customer, ref.Meter, ref.Period, pct)
	won, err := a.cfg.Redis.SetNX(ctx, fk, "1", firedTTL).Result()
	if err != nil {
		a.fireErrors.Add(ctx, 1)
		a.log.WarnContext(ctx, "threshold marker failed, the next reconciliation retries", "key", fk, "error", err)
		return
	}
	if !won {
		return
	}
	if a.cfg.Producer == nil {
		return
	}
	pb := &eventsv1.LimitExceeded{
		EventId: domain.LimitEventID(ref.Tenant, ref.Customer, ref.Meter, ref.Period, pct), TenantId: ref.Tenant.String(),
		CustomerId: ref.Customer.String(), MeterCode: ref.Meter, PeriodId: ref.Period, ThresholdPct: pct,
		LimitUnits: ent.Limit, UsedUnits: used, DetectedAt: timestamppb.New(now), QuantityScale: uint32(ent.Scale), //nolint:gosec // 0..6
	}
	payload, err := proto.Marshal(pb)
	if err == nil {
		err = a.cfg.Producer.ProduceSync(ctx, &kgo.Record{Topic: a.cfg.LimitTopic, Key: []byte(ref.Tenant.String()), Value: payload}).FirstErr()
	}
	if err != nil {
		// Un-mark so that the reconciliation sweep produces it.
		if derr := a.cfg.Redis.Del(context.WithoutCancel(ctx), fk).Err(); derr != nil {
			a.log.ErrorContext(ctx, "threshold marker not removed, the threshold will not fire again for this period", "key", fk, "error", derr)
		}
		a.fireErrors.Add(ctx, 1)
		a.log.WarnContext(ctx, "LimitExceeded not produced, the next reconciliation retries", "event_id", pb.GetEventId(), "error", err)
		return
	}
	a.fired.Add(ctx, 1, metric.WithAttributes(attribute.Int("pct", int(pct))))
}

// Sweep fires every threshold the given counters have reached and not yet fired. It repairs a crash between the
// script and the produce, and a failed produce.
func (a *Applier) Sweep(ctx context.Context, keys []string) error {
	if len(keys) == 0 {
		return nil
	}
	vals, err := a.cfg.Redis.MGet(ctx, keys...).Result()
	if err != nil {
		return err
	}
	for i, k := range keys {
		ref, ok := ParseCounterKey(k)
		s, isStr := vals[i].(string)
		if !ok || !isStr {
			continue
		}
		used, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			continue
		}
		a.reached(ctx, ref, used)
	}
	return nil
}
