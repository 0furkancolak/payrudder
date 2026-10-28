package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	aggregatorv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/aggregator/v1"
	"github.com/0furkancolak/payrudder/pkg/meters"
	"github.com/0furkancolak/payrudder/pkg/period"
	"github.com/0furkancolak/payrudder/pkg/quantity"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/pkg/usage"
)

const (
	// batchKeys is the aggregator's GetUsageBatch limit.
	batchKeys = 1000
	// maxKeysPerCycle bounds the work one partition takes per cycle; the rest is picked up by the next cycles, the most
	// recently updated keys first.
	maxKeysPerCycle = 10000
	// activeRetention is how long a counter key stays in lim:active:{p} after its last update.
	activeRetention = 45 * 24 * time.Hour
	// windowSweepRetention is how long a short-window counter stays in lim:wactive:{p} after its last update: the threshold sweep retries a
	// LimitExceeded that could not be produced for that long, and reconciliation compares the counter with the aggregator.
	windowSweepRetention = time.Hour
	// windowReconcileGrace is how long after its end a short window is still reconciled: late-arriving events of the window settle in that time,
	// and the aggregator keeps hour totals for far longer. A window that ended earlier is final and nobody reads its counter.
	windowReconcileGrace = 2 * time.Hour
)

// ReconcilerConfig configures a Reconciler.
type ReconcilerConfig struct {
	Applier    *Applier
	Redis      *redis.Client
	Aggregator aggregatorv1.UsageQueryServiceClient
	Meters     meters.Source
	// Owned lists the partitions of the usage topic this pod consumes.
	Owned func() []int32
	// Interval between cycles of Run (default 5 minutes).
	Interval time.Duration
	// Replay is asked to feed a partition again from the start of the log after Redis lost its offsets with nothing arriving to show it.
	Replay func(ctx context.Context, partition int32)
	// CheckpointTimeout is how long a checkpoint may wait for its offset (default 30 s).
	CheckpointTimeout time.Duration
	Log               *slog.Logger
}

// Reconciler makes the Redis counters converge to the aggregator's totals (docs/03-services/limits-svc.md#reconciliation).
type Reconciler struct {
	cfg ReconcilerConfig
	log *slog.Logger
	run sync.Mutex // one cycle at a time per pod
}

// NewReconciler applies defaults.
func NewReconciler(cfg ReconcilerConfig) *Reconciler {
	if cfg.Interval <= 0 {
		cfg.Interval = 5 * time.Minute
	}
	if cfg.CheckpointTimeout <= 0 {
		cfg.CheckpointTimeout = 30 * time.Second
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	return &Reconciler{cfg: cfg, log: cfg.Log}
}

// Run reconciles every Interval until ctx is done.
func (r *Reconciler) Run(ctx context.Context) {
	t := time.NewTicker(r.cfg.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if res, err := r.RunOnce(ctx); err != nil {
				r.log.WarnContext(ctx, "reconcile cycle failed", "error", err)
			} else if res.Corrections > 0 {
				r.log.InfoContext(ctx, "reconcile cycle done", "checked", res.Checked, "corrections", res.Corrections)
			}
		}
	}
}

type pending struct {
	p  int32
	cp *Checkpoint
}

// RunOnce runs one cycle over the partitions this pod owns and waits for its checkpoints.
func (r *Reconciler) RunOnce(ctx context.Context) (Result, error) {
	r.run.Lock()
	defer r.run.Unlock()

	var total Result
	var waiting []pending
	if r.cfg.Replay != nil {
		for _, p := range r.cfg.Applier.LostOffsets(ctx, r.cfg.Owned()) {
			r.log.WarnContext(ctx, "redis lost its offsets, replaying partition", "partition", p)
			r.cfg.Replay(ctx, p)
		}
	}
	for _, p := range r.cfg.Owned() {
		cps, skipped, err := r.partition(ctx, p)
		if err != nil {
			return total, fmt.Errorf("partition %d: %w", p, err)
		}
		total.Skipped += skipped
		waiting = append(waiting, cps...)
	}
	deadline := time.NewTimer(r.cfg.CheckpointTimeout)
	defer deadline.Stop()
	var swept []string
	for _, w := range waiting {
		select {
		case res := <-w.cp.Done():
			total.Checked += res.Checked
			total.Corrections += res.Corrections
			total.Skipped += res.Skipped
			for _, k := range w.cp.Keys {
				swept = append(swept, k.Key)
			}
		case <-deadline.C:
			r.cfg.Applier.Abandon(w.p, w.cp)
			total.Skipped++
			deadline.Reset(time.Millisecond)
		case <-ctx.Done():
			r.cfg.Applier.Abandon(w.p, w.cp)
			return total, ctx.Err()
		}
	}
	// The short-window counters were compared with the aggregator above; their thresholds are swept the same way as the others'.
	for _, p := range r.cfg.Owned() {
		keys, err := r.windowKeys(ctx, p)
		if err != nil {
			r.log.WarnContext(ctx, "window counters not swept", "partition", p, "error", err)
			continue
		}
		swept = append(swept, keys...)
	}
	// Thresholds the apply loop could not fire (crash after the script, failed produce) are fired now.
	for len(swept) > 0 {
		n := min(len(swept), 500)
		if err := r.cfg.Applier.Sweep(ctx, swept[:n]); err != nil {
			r.log.WarnContext(ctx, "threshold sweep failed", "error", err)
			break
		}
		swept = swept[n:]
	}
	return total, nil
}

// windowKeys returns the window counters of partition p updated within windowSweepRetention, dropping the older ones from the set.
func (r *Reconciler) windowKeys(ctx context.Context, p int32) ([]string, error) {
	cutoff := float64(time.Now().Add(-windowSweepRetention).UnixMilli())
	if err := r.cfg.Redis.ZRemRangeByScore(ctx, WindowActiveKey(p), "-inf", strconv.FormatFloat(cutoff, 'f', 0, 64)).Err(); err != nil {
		return nil, err
	}
	return r.cfg.Redis.ZRangeArgs(ctx, redis.ZRangeArgs{Key: WindowActiveKey(p), Start: 0, Stop: maxKeysPerCycle - 1, Rev: true}).Result()
}

// partition builds and registers the checkpoints of one partition: one per (tenant, chunk of keys), since the
// aggregator's read API is per tenant.
func (r *Reconciler) partition(ctx context.Context, p int32) ([]pending, int64, error) {
	rdb := r.cfg.Redis
	cutoff := float64(time.Now().Add(-activeRetention).UnixMilli())
	if err := rdb.ZRemRangeByScore(ctx, ActiveKey(p), "-inf", strconv.FormatFloat(cutoff, 'f', 0, 64)).Err(); err != nil {
		return nil, 0, err
	}
	members, err := rdb.ZRangeArgs(ctx, redis.ZRangeArgs{Key: ActiveKey(p), Start: 0, Stop: maxKeysPerCycle - 1, Rev: true}).Result()
	if err != nil {
		return nil, 0, err
	}
	// The billing-period and long-window counters of lim:active, and the recently updated short windows that are still current.
	recent, err := r.windowKeys(ctx, p)
	if err != nil {
		r.log.WarnContext(ctx, "window counters not reconciled", "partition", p, "error", err)
	}
	byTenant := map[uuid.UUID][]Ref{}
	for _, m := range append(members, recent...) {
		ref, ok := ParseKey(m)
		if !ok || (ref.Windowed && !LongWindow(ref.Period) && !r.current(ctx, ref)) {
			continue
		}
		byTenant[ref.Tenant] = append(byTenant[ref.Tenant], ref)
	}
	tenantIDs := make([]uuid.UUID, 0, len(byTenant))
	for t := range byTenant {
		tenantIDs = append(tenantIDs, t)
	}
	sort.Slice(tenantIDs, func(i, j int) bool { return tenantIDs[i].String() < tenantIDs[j].String() })

	var out []pending
	var skipped int64
	for _, tid := range tenantIDs {
		refs := byTenant[tid]
		for len(refs) > 0 {
			n := min(len(refs), batchKeys)
			chunk := refs[:n]
			refs = refs[n:]
			cp, reason, err := r.checkpointFor(ctx, p, tid, chunk)
			if err != nil {
				return out, skipped, err
			}
			if cp == nil {
				skipped++
				reconSkipped.Add(ctx, 1, metric.WithAttributes(attribute.String("reason", reason)))
				continue
			}
			if err := r.cfg.Applier.Register(ctx, p, cp); err != nil {
				return out, skipped, err
			}
			out = append(out, pending{p: p, cp: cp})
		}
	}
	return out, skipped, nil
}

// current reports whether the short window of ref has not ended more than windowReconcileGrace ago.
func (r *Reconciler) current(ctx context.Context, ref Ref) bool {
	tn, err := r.cfg.Applier.cfg.Tenants.Tenant(ctx, ref.Tenant)
	if err != nil {
		return false
	}
	_, _, end, err := period.WindowBounds(ref.Period, tn.Location)
	return err == nil && time.Now().Before(end.Add(windowReconcileGrace))
}

func (r *Reconciler) checkpointFor(ctx context.Context, p int32, tid uuid.UUID, refs []Ref) (*Checkpoint, string, error) {
	// A counter of a meter that no longer exists has nothing to compare with.
	refs, ms, err := r.withMeters(ctx, tid, refs)
	if err != nil {
		return nil, "", err
	}
	if len(refs) == 0 {
		return nil, "unknown_meter", nil
	}
	req := &aggregatorv1.GetUsageBatchRequest{}
	for i, ref := range refs {
		req.Keys = append(req.Keys, &aggregatorv1.UsageKey{CustomerId: ref.Customer.String(), MeterCode: ref.Meter, PeriodId: ref.Period, Aggregation: ms[i].Aggregation.Proto(), Max: ms[i].Aggregation == usage.Max})
	}
	actx := tenant.OutgoingContext(tenant.NewContext(ctx, tenant.Info{ID: tid}))
	resp, err := r.cfg.Aggregator.GetUsageBatch(actx, req)
	if err != nil {
		return nil, "", fmt.Errorf("aggregator GetUsageBatch: %w", err)
	}
	at, ok := resp.GetConsumerOffsets()[p]
	if !ok {
		return nil, "no_offset", nil
	}
	have, err := r.cfg.Redis.Get(ctx, OffsetKey(p)).Int64()
	if err != nil && !errors.Is(err, redis.Nil) {
		return nil, "", err
	}
	if have > at {
		return nil, "ahead", nil
	}
	if len(resp.GetUsage()) != len(refs) {
		return nil, "", fmt.Errorf("aggregator returned %d entries for %d keys", len(resp.GetUsage()), len(refs))
	}
	keys := make([]CheckKey, len(refs))
	for i, ref := range refs {
		u := resp.GetUsage()[i]
		want, err := quantity.ParseUnits(u.GetQuantity(), int(u.GetQuantityScale()))
		if err != nil {
			return nil, "", fmt.Errorf("aggregator quantity %q: %w", u.GetQuantity(), err)
		}
		keys[i] = CheckKey{Key: ref.Key(), Want: want, Max: ms[i].Aggregation == usage.Max}
		if ms[i].Aggregation == usage.Last {
			// The aggregator names the event its value is of, so that the counter is repaired to that event and not only to its value.
			keys[i].Last = true
			if id := u.GetLastEventId(); id != "" {
				keys[i].Order = &usage.Order{Micros: u.GetLastEventTime().AsTime().UnixMicro(), ID: id}
			}
		}
	}
	return NewCheckpoint(at, keys), "", nil
}

// withMeters returns the refs whose meter still exists, with those meters.
func (r *Reconciler) withMeters(ctx context.Context, tid uuid.UUID, refs []Ref) ([]Ref, []usage.Meter, error) {
	kept := make([]Ref, 0, len(refs))
	ms := make([]usage.Meter, 0, len(refs))
	for _, ref := range refs {
		m, err := r.cfg.Meters.Get(ctx, tid, ref.Meter)
		if errors.Is(err, ErrMeterNotFound) {
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		kept, ms = append(kept, ref), append(ms, m)
	}
	return kept, ms, nil
}
