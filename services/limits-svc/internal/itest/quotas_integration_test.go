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
		"ShortWindowsAreReconciled":         testShortWindowReconciled,
		"YearAndLifetimeWindows":            testYearAndLifetime,
		"WindowsOfAMaxMeter":                testMaxMeterWindows,
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

// reconcile runs one reconciliation cycle of partition 0 against an aggregator that holds totals.
func (q *quotaPod) reconcile(totals map[string]int64) engine.Result {
	q.t.Helper()
	agg := &fakeAggregator{totals: totals, scale: map[string]int{}, offsets: map[int32]int64{0: q.usedOff}}
	rec := engine.NewReconciler(engine.ReconcilerConfig{Applier: q.applier, Redis: q.rdb, Aggregator: agg, Meters: defaultMeters,
		Owned: func() []int32 { return []int32{0} }, CheckpointTimeout: 5 * time.Second, Log: testkit.Logger()})
	res, err := rec.RunOnce(context.Background())
	if err != nil {
		q.t.Fatal(err)
	}
	return res
}

// A short-window counter is compared with the aggregator like the monthly one: an outbox duplicate that was counted twice in the hour is taken out again, and a
// counter that restarted from the first event after the entitlement appeared is brought to what the hour really held. A window that ended long ago is left alone.
func testShortWindowReconciled(t *testing.T) {
	q := newQuotaPod(t)
	tn, cust := uuid.New(), uuid.New()
	q.setQuota(tn, cust, "api", 100, "hour", "hard", false, 100)
	q.apply(q.ev.event(tn, cust, "api", 40, 0, nil))
	now := time.Now()
	hourID, _, _ := period.WindowOf(period.Hour, now, istanbul)
	hourKey := engine.WindowKey(tn, cust, "api", hourID)
	monthKey := engine.CounterKey(tn, cust, "api", period.PeriodOf(now, istanbul))
	if q.get(hourKey) != 40 {
		t.Fatalf("hour counter = %d", q.get(hourKey))
	}
	if n := q.rdb.ZCard(context.Background(), engine.ActiveKey(0)).Val(); n != 1 {
		t.Fatalf("lim:active holds %d keys, want only the month counter", n)
	}
	if q.rdb.ZScore(context.Background(), engine.WindowActiveKey(0), hourKey).Err() != nil {
		t.Fatal("the hour counter is not in lim:wactive")
	}
	// An old hour counter is in lim:wactive too (an event of days ago was applied); its window is final, so it is not compared.
	oldEv := q.ev.event(tn, cust, "api", 5, 0, nil)
	oldEv.EventTime = timestamppb.New(now.Add(-72 * time.Hour))
	q.apply(oldEv)
	oldID, _, _ := period.WindowOf(period.Hour, oldEv.GetEventTime().AsTime(), istanbul)
	oldKey := engine.WindowKey(tn, cust, "api", oldID)

	// The hour counter drifts by a duplicate; the aggregator knows the true totals.
	if err := q.rdb.IncrBy(context.Background(), hourKey, 25).Err(); err != nil {
		t.Fatal(err)
	}
	res := q.reconcile(map[string]int64{monthKey: 45, hourKey: 40, oldKey: 999})
	if res.Corrections != 1 || res.Checked != 2 || q.get(hourKey) != 40 || q.get(oldKey) != 5 {
		t.Fatalf("reconcile = %+v, hour counter %d, old hour %d; want the month and hour counters checked, the hour corrected to 40 and the old hour left", res, q.get(hourKey), q.get(oldKey))
	}
	// A counter that started late: the aggregator has the hour's earlier usage too.
	q.reconcile(map[string]int64{monthKey: 45, hourKey: 90})
	if q.get(hourKey) != 90 {
		t.Fatalf("hour counter = %d, want 90 after the aggregator's total", q.get(hourKey))
	}
	if st := q.status(tn, cust, "api"); st.GetUsedUnits() != 90 || st.GetWindow() != "hour" {
		t.Fatalf("status = %v", st)
	}
}

// A yearly counter is the calendar year of the tenant, a lifetime one never starts over and never expires; both are reconciled with the aggregator's sum of the
// months, keep their thresholds' markers as long as they live, and the lifetime status has no reset time.
func testYearAndLifetime(t *testing.T) {
	q := newQuotaPod(t)
	tn, yearly, lifetime := uuid.New(), uuid.New(), uuid.New()
	q.setQuota(tn, yearly, "api", 100, "year", "hard", false, 50, 100)
	q.setQuota(tn, lifetime, "api", 1000, "lifetime", "notify", false, 100)
	now := time.Now()
	yearID, _, yearEnd := period.WindowOf(period.Year, now, istanbul)
	monthID := period.PeriodOf(now, istanbul)
	yearKey := engine.WindowKey(tn, yearly, "api", yearID)
	lifeKey := engine.WindowKey(tn, lifetime, "api", period.LifetimeID)
	if yearKey != fmt.Sprintf("lim:y:%s:%s:api:%s", tn, yearly, yearID) || lifeKey != fmt.Sprintf("lim:y:%s:%s:api:all", tn, lifetime) {
		t.Fatalf("keys %s %s", yearKey, lifeKey)
	}
	q.apply(q.ev.event(tn, yearly, "api", 30, 0, nil), q.ev.event(tn, lifetime, "api", 30, 0, nil))
	if q.get(yearKey) != 30 || q.get(lifeKey) != 30 || q.get(engine.CounterKey(tn, yearly, "api", monthID)) != 30 {
		t.Fatalf("year %d, lifetime %d", q.get(yearKey), q.get(lifeKey))
	}
	ctx := context.Background()
	if ttl := q.rdb.TTL(ctx, lifeKey).Val(); ttl != -1 {
		t.Fatalf("the lifetime counter expires (TTL %s)", ttl)
	}
	if ttl := q.rdb.TTL(ctx, yearKey).Val(); ttl <= 0 || ttl > time.Until(yearEnd)+3*24*time.Hour {
		t.Fatalf("year counter TTL %s", ttl)
	}
	// They live in lim:active (reconciled while they exist), not in lim:wactive.
	if n := q.rdb.ZCard(ctx, engine.ActiveKey(0)).Val(); n != 4 {
		t.Fatalf("lim:active holds %d keys, want 2 month + year + lifetime", n)
	}
	if n := q.rdb.ZCard(ctx, engine.WindowActiveKey(0)).Val(); n != 0 {
		t.Fatalf("lim:wactive holds %d keys, want none", n)
	}

	// The aggregator's year holds the earlier months too; the lifetime even a previous year's.
	res := q.reconcile(map[string]int64{
		engine.CounterKey(tn, yearly, "api", monthID): 30, engine.CounterKey(tn, lifetime, "api", monthID): 30,
		yearKey: 120, lifeKey: 5000,
	})
	if res.Corrections != 2 || q.get(yearKey) != 120 || q.get(lifeKey) != 5000 {
		t.Fatalf("reconcile = %+v, year %d, lifetime %d", res, q.get(yearKey), q.get(lifeKey))
	}
	if ttl := q.rdb.TTL(ctx, lifeKey).Val(); ttl != -1 {
		t.Fatalf("reconciliation gave the lifetime counter a TTL (%s)", ttl)
	}
	// Both are over their limits now: the thresholds fired once, for the year and for the lifetime, and their markers outlive an ordinary period's.
	for _, c := range []struct {
		cust uuid.UUID
		id   string
		pct  int32
	}{{yearly, yearID, 50}, {yearly, yearID, 100}, {lifetime, period.LifetimeID, 100}} {
		fk := engine.FiredKey(tn, c.cust, "api", c.id, c.pct)
		ttl := q.rdb.TTL(ctx, fk).Val()
		if c.id == period.LifetimeID && ttl != -1 || c.id != period.LifetimeID && ttl < 80*24*time.Hour {
			t.Fatalf("marker %s TTL %s", fk, ttl)
		}
	}
	st := q.status(tn, yearly, "api")
	if st.GetWindow() != "year" || st.GetWindowId() != yearID || !st.GetResetsAt().AsTime().Equal(yearEnd) || st.GetUsedUnits() != 120 || st.GetAllowed() || !st.GetOverLimit() {
		t.Fatalf("yearly status = %v, want year %s resetting at %s, over its hard limit", st, yearID, yearEnd)
	}
	st = q.status(tn, lifetime, "api")
	if st.GetWindow() != "lifetime" || st.GetWindowId() != "all" || st.GetResetsAt() != nil || st.GetUsedUnits() != 5000 || !st.GetAllowed() || !st.GetOverLimit() {
		t.Fatalf("lifetime status = %v, want no reset time, over a notify limit and still allowed", st)
	}

	// An event of last year counts in last year's counter, not in this year's.
	old := q.ev.event(tn, yearly, "api", 7, 0, nil)
	old.EventTime = timestamppb.New(yearEnd.AddDate(-1, 0, -10))
	q.apply(old)
	if q.get(yearKey) != 120 || q.get(engine.WindowKey(tn, yearly, "api", fmt.Sprint(yearEnd.Year()-2))) != 7 {
		t.Fatalf("this year %d", q.get(yearKey))
	}
}

// A max meter's window counter keeps the largest value; reconciliation sets it to the aggregator's maximum rather than adding a difference.
func testMaxMeterWindows(t *testing.T) {
	q := newQuotaPod(t)
	tn, cust := uuid.New(), uuid.New()
	q.setQuota(tn, cust, "peak", 100, "day", "hard", false, 100)
	q.apply(q.ev.event(tn, cust, "peak", 30, 0, nil), q.ev.event(tn, cust, "peak", 20, 0, nil))
	now := time.Now()
	dayID, _, _ := period.WindowOf(period.Day, now, istanbul)
	dayKey := engine.WindowKey(tn, cust, "peak", dayID)
	if q.get(dayKey) != 30 {
		t.Fatalf("day counter = %d, want the maximum 30", q.get(dayKey))
	}
	monthKey := engine.CounterKey(tn, cust, "peak", period.PeriodOf(now, istanbul))
	res := q.reconcile(map[string]int64{monthKey: 30, dayKey: 10})
	if res.Corrections != 1 || q.get(dayKey) != 10 {
		t.Fatalf("reconcile = %+v, day counter %d, want it set to the aggregator's 10", res, q.get(dayKey))
	}
}
