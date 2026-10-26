//go:build integration

package itest

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/protobuf/types/known/timestamppb"

	eventsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/events/v1"
	limitsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/limits/v1"
	"github.com/0furkancolak/payrudder/pkg/period"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/pkg/testkit"
	"github.com/0furkancolak/payrudder/services/limits-svc/internal/engine"
	"github.com/0furkancolak/payrudder/services/limits-svc/internal/entitlements"
	"github.com/0furkancolak/payrudder/services/limits-svc/internal/grpcapi"
)

// The quota tests need Redis only: the entitlement map and the apply loop are fed directly, the way their consumers would.
var (
	quotaOnce sync.Once
	quotaRdb  *redis.Client
)

func TestQuotas(t *testing.T) {
	top = t
	for name, fn := range map[string]func(*testing.T){
		"DailyWindowCountsAndFiresInItsDay": testDailyWindow,
		"ModesDecideAllowed":                testModes,
		"WindowCountersAreNotReconciled":    testWindowNotReconciled,
	} {
		t.Run(name, fn)
	}
}

// quotaPod is one replica's entitlement map and apply loop over a flushed Redis, without Kafka (no LimitExceeded is produced; the fired markers
// say what would have been).
type quotaPod struct {
	t       *testing.T
	rdb     *redis.Client
	ents    *entitlements.Store
	applier *engine.Applier
	ev      *env
	entOff  int64
	usedOff int64
}

func newQuotaPod(t *testing.T) *quotaPod {
	t.Helper()
	quotaOnce.Do(func() {
		opts, err := redis.ParseURL(testkit.StartRedis(top))
		if err != nil {
			t.Fatal(err)
		}
		quotaRdb = redis.NewClient(opts)
	})
	if err := quotaRdb.FlushAll(context.Background()).Err(); err != nil {
		t.Fatal(err)
	}
	q := &quotaPod{t: t, rdb: quotaRdb, ents: entitlements.New(quotaRdb, testkit.Logger()), ev: &env{t: t}}
	var err error
	q.applier, err = engine.NewApplier(engine.Config{Redis: quotaRdb, Meters: defaultMeters, Tenants: fixedTenants(), Ents: q.ents, LimitTopic: "unused", Log: testkit.Logger()})
	if err != nil {
		t.Fatal(err)
	}
	if err := q.applier.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	return q
}

// setQuota feeds an entitlement with a window and a mode to the map, as plans-svc publishes it for a plan version or an override.
func (q *quotaPod) setQuota(tenantID, customer uuid.UUID, meter string, limit int64, window, mode string, override bool, thresholds ...int32) {
	q.t.Helper()
	st := &eventsv1.EntitlementState{
		TenantId: tenantID.String(), CustomerId: customer.String(), MeterCode: meter, PlanVersionId: uuid.NewString(),
		LimitUnits: limit, AlertThresholdsPct: thresholds, ValidFrom: timestamppb.New(time.Now().Add(-time.Hour)), Window: window, Mode: mode, Override: override,
	}
	rec := &kgo.Record{Key: []byte(fmt.Sprintf("%s/%s/%s", tenantID, customer, meter)), Value: mustMarshal(q.t, st), Offset: q.entOff}
	q.entOff++
	if err := q.ents.Apply(context.Background(), 0, []*kgo.Record{rec}); err != nil {
		q.t.Fatal(err)
	}
}

// apply runs a batch of usage events through partition 0.
func (q *quotaPod) apply(events ...*eventsv1.UsageEvent) {
	q.t.Helper()
	raws := make([][]byte, len(events))
	for i, ev := range events {
		raws[i] = mustMarshal(q.t, ev)
	}
	if err := q.applier.Apply(context.Background(), 0, records(0, q.usedOff, raws...)); err != nil {
		q.t.Fatal(err)
	}
	q.usedOff += int64(len(events))
}

func (q *quotaPod) get(key string) int64 {
	v, err := q.rdb.Get(context.Background(), key).Int64()
	if err != nil {
		return 0
	}
	return v
}

func (q *quotaPod) status(tn, cust uuid.UUID, meter string) *limitsv1.LimitStatus {
	q.t.Helper()
	ctx := tenant.NewContext(context.Background(), tenant.Info{ID: tn, Timezone: "Europe/Istanbul"})
	r, err := grpcapi.New(q.rdb, q.ents, defaultMeters, nil).CheckEntitlement(ctx, &limitsv1.CheckEntitlementRequest{CustomerId: cust.String(), MeterCode: meter})
	if err != nil {
		q.t.Fatal(err)
	}
	return r.GetStatus()
}

// A daily quota counts the day's usage on its own counter (the month counter still counts everything), reports the day and when it resets,
// and fires its thresholds once, for the day.
func testDailyWindow(t *testing.T) {
	q := newQuotaPod(t)
	tn, cust := uuid.New(), uuid.New()
	q.setQuota(tn, cust, "api", 100, "day", "hard", false, 50, 100)
	now := time.Now()
	dayID, _, dayEnd := period.WindowOf(period.Day, now, istanbul)
	monthKey := engine.CounterKey(tn, cust, "api", period.PeriodOf(now, istanbul))
	dayKey := engine.WindowKey(tn, cust, "api", dayID)

	// An event of two days ago counts in its own day, not in today's (it stays in the month it was stamped with).
	old := q.ev.event(tn, cust, "api", 30, 0, nil)
	old.EventTime = timestamppb.New(now.Add(-48 * time.Hour))
	oldID, _, _ := period.WindowOf(period.Day, old.GetEventTime().AsTime(), istanbul)
	q.apply(old)
	for range 6 {
		q.apply(q.ev.event(tn, cust, "api", 10, 0, nil))
	}
	if m, d, o := q.get(monthKey), q.get(dayKey), q.get(engine.WindowKey(tn, cust, "api", oldID)); m != 90 || d != 60 || o != 30 {
		t.Fatalf("month %d, today %d, two days ago %d; want 90, 60, 30", m, d, o)
	}
	st := q.status(tn, cust, "api")
	if st.GetWindow() != "day" || st.GetWindowId() != dayID || !st.GetResetsAt().AsTime().Equal(dayEnd) || st.GetUsedUnits() != 60 || st.GetMode() != "hard" || !st.GetAllowed() || st.GetOverLimit() {
		t.Fatalf("status = %v, want day %s resetting at %s with 60 used and allowed", st, dayID, dayEnd)
	}
	fired := func(pct int32) bool {
		return q.rdb.Exists(context.Background(), engine.FiredKey(tn, cust, "api", dayID, pct)).Val() == 1
	}
	if !fired(50) || fired(100) {
		t.Fatal("at 60 of 100 the 50 % threshold of the day fires, not the 100 % one")
	}
	q.apply(q.ev.event(tn, cust, "api", 40, 0, nil))
	if !fired(100) {
		t.Fatal("100 % of the day did not fire")
	}
	// The month counter never fires a daily limit's thresholds.
	if q.rdb.Exists(context.Background(), engine.FiredKey(tn, cust, "api", period.PeriodOf(now, istanbul), 100)).Val() != 0 {
		t.Fatal("a daily limit fired on the month counter")
	}
	st = q.status(tn, cust, "api")
	if st.GetAllowed() || !st.GetOverLimit() || st.GetUsedUnits() != 100 {
		t.Fatalf("a hard daily limit reached: %v", st)
	}
	if ttl := q.rdb.TTL(context.Background(), dayKey).Val(); ttl <= 0 || ttl > time.Until(dayEnd)+3*24*time.Hour {
		t.Fatalf("day counter TTL %s", ttl)
	}
}

// Soft and notify limits keep allowing over the limit and say they are over it; an override says so.
func testModes(t *testing.T) {
	q := newQuotaPod(t)
	tn, soft, notify, hard := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	q.setQuota(tn, soft, "api", 10, "month", "soft", true, 100)
	q.setQuota(tn, notify, "api", 10, "", "notify", false, 100)
	q.setQuota(tn, hard, "api", 10, "", "", false, 100) // what plans-svc published before modes existed
	for _, c := range []uuid.UUID{soft, notify, hard} {
		q.apply(q.ev.event(tn, c, "api", 15, 0, nil))
	}
	for c, want := range map[uuid.UUID]struct {
		mode               string
		allowed, overridde bool
	}{soft: {"soft", true, true}, notify: {"notify", true, false}, hard: {"hard", false, false}} {
		st := q.status(tn, c, "api")
		if st.GetAllowed() != want.allowed || !st.GetOverLimit() || st.GetMode() != want.mode || st.GetWindow() != "month" || st.GetOverridden() != want.overridde {
			t.Fatalf("%s: %v", want.mode, st)
		}
		if q.rdb.Exists(context.Background(), engine.FiredKey(tn, c, "api", period.PeriodOf(time.Now(), istanbul), 100)).Val() != 1 {
			t.Fatalf("%s: the 100 %% alert did not fire", want.mode)
		}
	}
}

// Reconciliation only reads the billing-period counters: a window counter is never sent to the aggregator, nor corrected.
func testWindowNotReconciled(t *testing.T) {
	q := newQuotaPod(t)
	tn, cust := uuid.New(), uuid.New()
	q.setQuota(tn, cust, "api", 100, "hour", "hard", false, 100)
	q.apply(q.ev.event(tn, cust, "api", 40, 0, nil))
	hourID, _, _ := period.WindowOf(period.Hour, time.Now(), istanbul)
	hourKey := engine.WindowKey(tn, cust, "api", hourID)
	if q.get(hourKey) != 40 {
		t.Fatalf("hour counter = %d", q.get(hourKey))
	}
	if n := q.rdb.ZCard(context.Background(), engine.ActiveKey(0)).Val(); n != 1 {
		t.Fatalf("lim:active holds %d keys, want only the month counter", n)
	}
	if q.rdb.ZScore(context.Background(), engine.WindowActiveKey(0), hourKey).Err() != nil {
		t.Fatal("the hour counter is not in lim:wactive")
	}
	monthKey := engine.CounterKey(tn, cust, "api", period.PeriodOf(time.Now(), istanbul))
	agg := &fakeAggregator{totals: map[string]int64{monthKey: 40}, scale: map[string]int{}, offsets: map[int32]int64{0: q.usedOff}}
	rec := engine.NewReconciler(engine.ReconcilerConfig{Applier: q.applier, Redis: q.rdb, Aggregator: agg, Meters: defaultMeters,
		Owned: func() []int32 { return []int32{0} }, CheckpointTimeout: 5 * time.Second, Log: testkit.Logger()})
	res, err := rec.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Corrections != 0 || res.Checked != 1 || q.get(hourKey) != 40 {
		t.Fatalf("reconcile = %+v, hour counter %d; want the month counter checked and nothing corrected", res, q.get(hourKey))
	}
}
