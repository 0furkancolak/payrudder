package engine

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/0furkancolak/payrudder/pkg/money"
	"github.com/0furkancolak/payrudder/pkg/period"
)

var (
	reconMeter       = otel.Meter("github.com/0furkancolak/payrudder/services/limits-svc/reconcile")
	corrections, _   = reconMeter.Int64Counter("limits_reconcile_corrections", metric.WithDescription("Counters corrected to the aggregator's total"))
	reconSkipped, _  = reconMeter.Int64Counter("limits_reconcile_skipped", metric.WithDescription("Reconciliations skipped, by reason"))
	reconChecked, _  = reconMeter.Int64Counter("limits_reconcile_keys_checked", metric.WithDescription("Counters compared with the aggregator"))
	reconDriftAbs, _ = reconMeter.Int64Histogram("limits_reconcile_drift_units", metric.WithDescription("Absolute drift found, in scaled units"))
)

// CheckKey is one counter a checkpoint compares, with the aggregator's total for it.
type CheckKey struct {
	Key  string
	Want int64
	Max  bool
}

// Result is the outcome of a checkpoint.
type Result struct {
	Checked     int64
	Corrections int64
	Skipped     int64
	SkipReason  string
}

// Checkpoint asks a partition's apply loop to compare counters with the aggregator at an exact offset: the aggregator's
// totals contain exactly the events of the partition before At, so once the loop has applied exactly those records
// the counters describe the same prefix and any difference is drift.
type Checkpoint struct {
	At   int64
	Keys []CheckKey

	once sync.Once
	done chan Result
}

// NewCheckpoint returns a checkpoint at the aggregator offset at.
func NewCheckpoint(at int64, keys []CheckKey) *Checkpoint {
	return &Checkpoint{At: at, Keys: keys, done: make(chan Result, 1)}
}

// Done delivers the result once.
func (c *Checkpoint) Done() <-chan Result { return c.done }

func (c *Checkpoint) finish(r Result) {
	c.once.Do(func() {
		if r.Skipped > 0 {
			reconSkipped.Add(context.Background(), 1, metric.WithAttributes(attribute.String("reason", r.SkipReason)))
		}
		c.done <- r
	})
}

type checkpoints struct {
	mu     sync.Mutex
	byPart map[int32][]*Checkpoint
}

func newCheckpoints() *checkpoints { return &checkpoints{byPart: map[int32][]*Checkpoint{}} }

func (c *checkpoints) add(p int32, cp *Checkpoint) {
	c.mu.Lock()
	defer c.mu.Unlock()
	list := append(c.byPart[p], cp)
	sort.SliceStable(list, func(i, j int) bool { return list[i].At < list[j].At })
	c.byPart[p] = list
}

func (c *checkpoints) first(p int32) *Checkpoint {
	c.mu.Lock()
	defer c.mu.Unlock()
	if l := c.byPart[p]; len(l) > 0 {
		return l[0]
	}
	return nil
}

func (c *checkpoints) remove(p int32, cp *Checkpoint) {
	c.mu.Lock()
	defer c.mu.Unlock()
	l := c.byPart[p]
	for i := range l {
		if l[i] == cp {
			c.byPart[p] = append(l[:i:i], l[i+1:]...)
			return
		}
	}
}

// Register queues a checkpoint on a partition and serves it at once when the partition is exactly at its offset. The
// apply loop serves it otherwise.
func (a *Applier) Register(ctx context.Context, p int32, cp *Checkpoint) error {
	a.cps.add(p, cp)
	ps := a.part(p)
	ps.mu.Lock()
	defer ps.mu.Unlock()
	applied, err := a.cfg.Redis.Get(ctx, OffsetKey(p)).Int64()
	if err != nil && !errors.Is(err, redis.Nil) {
		return err
	}
	switch {
	case applied == cp.At:
		a.cps.remove(p, cp)
		a.resolve(ctx, cp)
	case applied > cp.At:
		a.cps.remove(p, cp)
		cp.finish(Result{Skipped: 1, SkipReason: "ahead"})
	}
	return nil
}

// Abandon drops a checkpoint that was not reached in time.
func (a *Applier) Abandon(p int32, cp *Checkpoint) {
	a.cps.remove(p, cp)
	cp.finish(Result{Skipped: 1, SkipReason: "timeout"})
}

// resolve compares the checkpoint's counters with the aggregator's totals and corrects the drift. The caller holds the
// partition lock, so no batch is applied while the counters are read and corrected.
func (a *Applier) resolve(ctx context.Context, cp *Checkpoint) {
	res := Result{}
	if len(cp.Keys) == 0 {
		cp.finish(res)
		return
	}
	keys := make([]string, len(cp.Keys))
	for i, k := range cp.Keys {
		keys[i] = k.Key
	}
	vals, err := a.cfg.Redis.MGet(ctx, keys...).Result()
	if err != nil {
		a.log.WarnContext(ctx, "reconcile read failed", "error", err)
		cp.finish(Result{Skipped: 1, SkipReason: "redis_error"})
		return
	}
	var fixed []string
	for i, k := range cp.Keys {
		have := int64(0)
		if s, ok := vals[i].(string); ok {
			var perr error
			if have, perr = strconv.ParseInt(s, 10, 64); perr != nil {
				a.log.WarnContext(ctx, "counter holds a non-number, treated as 0", "key", k.Key, "value", s)
				have = 0
			}
		}
		res.Checked++
		reconChecked.Add(ctx, 1)
		if have == k.Want {
			continue
		}
		drift := have - k.Want
		var err error
		if k.Max {
			err = a.cfg.Redis.Set(ctx, k.Key, k.Want, redis.KeepTTL).Err()
		} else {
			err = a.cfg.Redis.IncrBy(ctx, k.Key, -drift).Err()
		}
		if err != nil {
			a.log.WarnContext(ctx, "reconcile correction failed", "key", k.Key, "error", err)
			continue
		}
		fixed = append(fixed, k.Key)
		res.Corrections++
		corrections.Add(ctx, 1, metric.WithAttributes(attribute.String("reason", "drift")))
		reconDriftAbs.Record(ctx, money.Abs(drift))
		a.log.WarnContext(ctx, "counter drift corrected", "key", k.Key, "redis", have, "aggregator", k.Want, "drift", drift)
	}
	for _, k := range fixed {
		at, expires := a.correctedExpiry(ctx, k)
		if !expires {
			continue
		}
		if err := a.cfg.Redis.ExpireNX(ctx, k, at.Sub(a.cfg.Now())).Err(); err != nil {
			a.log.WarnContext(ctx, "reconcile could not set the expiry of a corrected counter", "key", k, "error", err)
		}
	}
	cp.finish(res)
}

// correctedExpiry is when a counter that reconciliation had to create (it had no expiry) should expire: like the apply script's, firedTTL
// from now for a billing period, and the end of its window plus windowTTL for a window. The lifetime counter never expires.
func (a *Applier) correctedExpiry(ctx context.Context, key string) (time.Time, bool) {
	now := a.cfg.Now()
	ref, ok := ParseKey(key)
	if !ok || !ref.Windowed {
		return now.Add(firedTTL), true
	}
	if ref.Period == period.LifetimeID {
		return time.Time{}, false
	}
	tn, err := a.cfg.Tenants.Tenant(ctx, ref.Tenant)
	if err != nil {
		return now.Add(firedTTL), true
	}
	_, _, end, err := period.WindowBounds(ref.Period, tn.Location)
	if err != nil {
		return now.Add(firedTTL), true
	}
	return maxTime(end, now).Add(windowTTL), true
}
