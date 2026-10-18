//go:build integration

package itest

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/protobuf/proto"

	limitsv1 "github.com/0furkancolak/quotify/gen/go/quotify/limits/v1"
	"github.com/0furkancolak/quotify/pkg/period"
	"github.com/0furkancolak/quotify/pkg/tenant"
	"github.com/0furkancolak/quotify/services/limits-svc/internal/domain"
	"github.com/0furkancolak/quotify/services/limits-svc/internal/engine"
	"github.com/0furkancolak/quotify/services/limits-svc/internal/query"
)

func TestLimits(t *testing.T) {
	top = t
	for name, fn := range map[string]func(*testing.T){
		"RedeliveredBatchIsSkipped":        testRedeliveredBatch,
		"ThresholdsOnceAcrossRebalance":    testThresholdsAcrossRebalance,
		"CrashAfterLuaIsSweptByReconcile":  testCrashAfterLuaSweep,
		"ReconcileCorrectsDrift":           testReconcileCorrectsDrift,
		"ReconcileSkipsWhenLimitsIsAhead":  testReconcileAhead,
		"RedisFlushRebuildsAndReconciles":  testRedisFlush,
		"LateEventNotCounted":              testLateEvent,
		"EntitlementChangeVisible":         testEntitlementVisible,
		"FractionalLimitFiresAtExactUnits": testFractionalLimit,
		"MaxMeterAndFilterAndCount":        testMeterSemantics,
		"CounterOverflowGuard":             testOverflow,
	} {
		t.Run(name, fn)
	}
}

func records(p int32, first int64, events ...[]byte) []*kgo.Record {
	out := make([]*kgo.Record, len(events))
	for i, b := range events {
		out[i] = &kgo.Record{Topic: "t", Partition: p, Offset: first + int64(i), Value: b}
	}
	return out
}

func mustMarshal(t *testing.T, m proto.Message) []byte {
	t.Helper()
	b, err := proto.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// A batch applied twice (redelivery after a crash between the script and the Kafka commit) changes nothing.
func testRedeliveredBatch(t *testing.T) {
	e := newEnv(t)
	boom := errors.New("crash after lua")
	calls := 0
	p := e.startPodNoConsume(podOpts{afterLua: func() error {
		calls++
		if calls == 1 {
			return boom
		}
		return nil
	}})
	tn, cust := uuid.New(), uuid.New()
	var raws [][]byte
	for i := 0; i < 5; i++ {
		raws = append(raws, mustMarshal(t, e.event(tn, cust, "api", 10, 0, nil)))
	}
	recs := records(0, 0, raws...)
	if err := p.applier.Apply(context.Background(), 0, recs); !errors.Is(err, boom) {
		t.Fatalf("first apply error = %v, want the crash", err)
	}
	if got := counter(t, tn, cust, "api"); got != 50 {
		t.Fatalf("counter after the crashed batch = %d, want 50 (the script had committed)", got)
	}
	// The consumer would redeliver the same records.
	if err := p.applier.Apply(context.Background(), 0, recs); err != nil {
		t.Fatalf("redelivered apply: %v", err)
	}
	if got := counter(t, tn, cust, "api"); got != 50 {
		t.Fatalf("counter after redelivery = %d, want 50 (skipped)", got)
	}
	// A batch overlapping the applied prefix only adds its new records.
	more := append(recs[3:], records(0, 5, mustMarshal(t, e.event(tn, cust, "api", 7, 0, nil)))...)
	if err := p.applier.Apply(context.Background(), 0, more); err != nil {
		t.Fatal(err)
	}
	if got := counter(t, tn, cust, "api"); got != 57 {
		t.Fatalf("counter after a partially overlapping batch = %d, want 57", got)
	}
}

// The 80 % and 100 % thresholds fire exactly once each, although the owner of the partition changes in between.
func testThresholdsAcrossRebalance(t *testing.T) {
	e := newEnv(t)
	tn, cust := uuid.New(), uuid.New()
	e.setEntitlement(tn, cust, "api", 100, 0, 80, 100)
	a := e.startPod(podOpts{})
	b := e.startPod(podOpts{})
	eventually(t, 20*time.Second, "both pods to hold the entitlement", func() bool { return a.ents.Len() == 1 && b.ents.Len() == 1 })

	send := func(n int) {
		for i := 0; i < n; i++ {
			e.produce(e.event(tn, cust, "api", 10, 0, nil))
		}
	}
	waitCounter := func(want int64) {
		eventually(t, 20*time.Second, fmt.Sprintf("counter = %d", want), func() bool { return counter(t, tn, cust, "api") == want })
	}
	send(7)
	waitCounter(70)
	a.stop() // forces a rebalance: b takes over every partition
	send(2)  // 90: crosses 80 on whoever owns the partition now
	waitCounter(90)
	send(1) // 100
	waitCounter(100)
	send(5) // 150: nothing new may fire
	waitCounter(150)

	got := e.readLimitEvents(2, 3*time.Second)
	if len(got) != 2 {
		t.Fatalf("got %d LimitExceeded events, want exactly 2: %v", len(got), got)
	}
	seen := map[int32]bool{}
	for _, le := range got {
		seen[le.GetThresholdPct()] = true
		if le.GetLimitUnits() != 100 || le.GetTenantId() != tn.String() || le.GetCustomerId() != cust.String() {
			t.Errorf("unexpected event %v", le)
		}
		wantID := domain.LimitEventID(tn, cust, "api", period.PeriodOf(time.Now(), istanbul), le.GetThresholdPct())
		if le.GetEventId() != wantID {
			t.Errorf("event id %s, want deterministic %s", le.GetEventId(), wantID)
		}
	}
	if !seen[80] || !seen[100] {
		t.Fatalf("thresholds seen: %v, want 80 and 100", seen)
	}
}

// A crash between the script and the threshold evaluation loses no threshold: the next reconciliation fires it.
func testCrashAfterLuaSweep(t *testing.T) {
	e := newEnv(t)
	tn, cust := uuid.New(), uuid.New()
	e.setEntitlement(tn, cust, "api", 100, 0, 100)
	crash := true
	p := e.startPodNoConsume(podOpts{afterLua: func() error {
		if crash {
			crash = false
			return errors.New("crash after lua")
		}
		return nil
	}})
	eventuallyEnts(t, e, p, 1)
	recs := records(0, 0, mustMarshal(t, e.event(tn, cust, "api", 100, 0, nil)))
	if err := p.applier.Apply(context.Background(), 0, recs); err == nil {
		t.Fatal("expected the crash")
	}
	if got := e.readLimitEvents(0, time.Second); len(got) != 0 {
		t.Fatalf("threshold fired despite the crash: %v", got)
	}
	agg := &fakeAggregator{totals: map[string]int64{engine.CounterKey(tn, cust, "api", period.PeriodOf(time.Now(), istanbul)): 100},
		scale: map[string]int{}, offsets: map[int32]int64{0: 1}}
	rec := e.reconciler(p, agg, 0)
	if _, err := rec.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := e.readLimitEvents(1, 2*time.Second)
	if len(got) != 1 || got[0].GetThresholdPct() != 100 || got[0].GetUsedUnits() != 100 {
		t.Fatalf("after reconcile got %v, want one 100%% event", got)
	}
	// Running it again must not fire a second time.
	if _, err := rec.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := e.readLimitEvents(1, 2*time.Second); len(got) != 1 {
		t.Fatalf("second reconcile re-fired: %v", got)
	}
}

// Counters corrupted by +500 are corrected to the aggregator's value within one cycle.
func testReconcileCorrectsDrift(t *testing.T) {
	e := newEnv(t)
	tn, cust := uuid.New(), uuid.New()
	p := e.startPod(podOpts{})
	for i := 0; i < 20; i++ {
		e.produce(e.event(tn, cust, "api", 5, 0, nil))
	}
	eventually(t, 20*time.Second, "counter = 100", func() bool { return counter(t, tn, cust, "api") == 100 })
	key := engine.CounterKey(tn, cust, "api", period.PeriodOf(time.Now(), istanbul))
	if err := shared.rdb.IncrBy(context.Background(), key, 500).Err(); err != nil {
		t.Fatal(err)
	}
	next := nextOffset(t, 0, 1, 2, 3)
	agg := &fakeAggregator{totals: map[string]int64{key: 100}, scale: map[string]int{}, offsets: next}
	rec := e.reconciler(p, agg, 0, 1, 2, 3)
	res, err := rec.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Corrections != 1 || res.Checked != 1 {
		t.Fatalf("result = %+v, want 1 checked and 1 correction", res)
	}
	if got := counter(t, tn, cust, "api"); got != 100 {
		t.Fatalf("counter = %d after reconcile, want 100", got)
	}
	// Converged: the next cycle finds nothing.
	if res, err = rec.RunOnce(context.Background()); err != nil || res.Corrections != 0 {
		t.Fatalf("second cycle: %+v, %v", res, err)
	}
}

func nextOffset(t *testing.T, parts ...int32) map[int32]int64 {
	t.Helper()
	out := map[int32]int64{}
	for _, p := range parts {
		v, err := shared.rdb.Get(context.Background(), engine.OffsetKey(p)).Int64()
		if err == nil {
			out[p] = v
		}
	}
	return out
}

// When the aggregator's snapshot is behind limits (O_l > O_a) the partition is skipped; when it is ahead, the loop
// applies the records up to the snapshot offset and compares exactly there.
func testReconcileAhead(t *testing.T) {
	e := newEnv(t)
	tn, cust := uuid.New(), uuid.New()
	p := e.startPodNoConsume(podOpts{})
	key := engine.CounterKey(tn, cust, "api", period.PeriodOf(time.Now(), istanbul))
	var raws [][]byte
	for i := 0; i < 10; i++ {
		raws = append(raws, mustMarshal(t, e.event(tn, cust, "api", 1, 0, nil)))
	}
	if err := p.applier.Apply(context.Background(), 0, records(0, 0, raws...)); err != nil {
		t.Fatal(err)
	}

	// Aggregator at offset 4 (it has seen 4 events), limits at 10: skip, counter untouched even though 4 != 10.
	agg := &fakeAggregator{totals: map[string]int64{key: 4}, scale: map[string]int{}, offsets: map[int32]int64{0: 4}}
	rec := e.reconciler(p, agg, 0)
	res, err := rec.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Corrections != 0 || res.Skipped != 1 {
		t.Fatalf("limits ahead: %+v, want one skip and no correction", res)
	}
	if got := counter(t, tn, cust, "api"); got != 10 {
		t.Fatalf("counter = %d, want 10", got)
	}

	// Aggregator at offset 14 with total 14 (4 events limits has not applied yet): the checkpoint waits for the loop.
	agg.mu.Lock()
	agg.totals[key], agg.offsets[0] = 14, 14
	agg.mu.Unlock()
	done := make(chan engine.Result, 1)
	go func() {
		r, _ := rec.RunOnce(context.Background())
		done <- r
	}()
	time.Sleep(200 * time.Millisecond) // the checkpoint is registered and pending
	var more [][]byte
	for i := 0; i < 8; i++ {
		more = append(more, mustMarshal(t, e.event(tn, cust, "api", 1, 0, nil)))
	}
	// Records 10..17: the loop applies 10..13, compares (10+4 = 14 = aggregator), then applies 14..17.
	if err := p.applier.Apply(context.Background(), 0, records(0, 10, more...)); err != nil {
		t.Fatal(err)
	}
	res = <-done
	if res.Checked != 1 || res.Corrections != 0 {
		t.Fatalf("checkpoint at the aggregator's offset: %+v, want 1 checked, 0 corrections", res)
	}
	if got := counter(t, tn, cust, "api"); got != 18 {
		t.Fatalf("counter = %d, want 18", got)
	}
}

// Redis loses everything: the partition is replayed from the log and the counters equal the aggregator's totals.
func testRedisFlush(t *testing.T) {
	e := newEnv(t)
	tn, cust := uuid.New(), uuid.New()
	p := e.startPod(podOpts{})
	for i := 0; i < 30; i++ {
		e.produce(e.event(tn, cust, "api", 2, 0, nil))
	}
	eventually(t, 20*time.Second, "counter = 60", func() bool { return counter(t, tn, cust, "api") == 60 })
	if err := shared.rdb.FlushAll(context.Background()).Err(); err != nil {
		t.Fatal(err)
	}
	e.produce(e.event(tn, cust, "api", 2, 0, nil)) // the next batch finds the offset key gone
	eventually(t, 30*time.Second, "counter rebuilt to 62", func() bool { return counter(t, tn, cust, "api") == 62 })

	key := engine.CounterKey(tn, cust, "api", period.PeriodOf(time.Now(), istanbul))
	eventually(t, 10*time.Second, "offsets in redis", func() bool { return len(nextOffset(t, 0, 1, 2, 3)) > 0 })
	agg := &fakeAggregator{totals: map[string]int64{key: 62}, scale: map[string]int{}, offsets: nextOffset(t, 0, 1, 2, 3)}
	res, err := e.reconciler(p, agg, 0, 1, 2, 3).RunOnce(context.Background())
	if err != nil || res.Corrections != 0 {
		t.Fatalf("after replay: %+v, %v", res, err)
	}
}

// A late event does not count against the current period.
func testLateEvent(t *testing.T) {
	e := newEnv(t)
	tn, cust := uuid.New(), uuid.New()
	p := e.startPodNoConsume(podOpts{})
	late := e.event(tn, cust, "api", 99, 0, nil)
	late.PeriodId = "2026-01" // long closed
	ontime := e.event(tn, cust, "api", 1, 0, nil)
	if err := p.applier.Apply(context.Background(), 0, records(0, 0, mustMarshal(t, late), mustMarshal(t, ontime))); err != nil {
		t.Fatal(err)
	}
	if got := counter(t, tn, cust, "api"); got != 1 {
		t.Fatalf("current period counter = %d, want 1 (the late event is not counted)", got)
	}
	if n, _ := shared.rdb.Exists(context.Background(), engine.CounterKey(tn, cust, "api", "2026-01")).Result(); n != 0 {
		t.Fatal("the late event created a counter for its origin period")
	}
}

// A new subscription is visible in CheckEntitlement within 5 s; ending it removes it.
func testEntitlementVisible(t *testing.T) {
	e := newEnv(t)
	tn, cust := uuid.New(), uuid.New()
	p := e.startPod(podOpts{})
	svc := query.New(shared.rdb, p.ents, e.meters, nil)
	ctx := tenant.NewContext(context.Background(), tenant.Info{ID: tn, Timezone: "Europe/Istanbul"})

	before, err := svc.CheckEntitlement(ctx, &limitsv1.CheckEntitlementRequest{CustomerId: cust.String(), MeterCode: "api"})
	if err != nil || !before.GetStatus().GetUnlimited() || !before.GetStatus().GetAllowed() {
		t.Fatalf("without an entitlement: %v, %v (want unlimited and allowed)", before, err)
	}
	e.setEntitlement(tn, cust, "api", 10, 0, 100)
	start := time.Now()
	eventually(t, 5*time.Second, "entitlement visible", func() bool {
		r, err := svc.CheckEntitlement(ctx, &limitsv1.CheckEntitlementRequest{CustomerId: cust.String(), MeterCode: "api"})
		return err == nil && r.GetStatus().GetLimitUnits() == 10
	})
	t.Logf("entitlement visible after %s", time.Since(start))

	for i := 0; i < 10; i++ {
		e.produce(e.event(tn, cust, "api", 1, 0, nil))
	}
	eventually(t, 20*time.Second, "limit reached", func() bool {
		r, err := svc.CheckEntitlement(ctx, &limitsv1.CheckEntitlementRequest{CustomerId: cust.String(), MeterCode: "api"})
		return err == nil && r.GetStatus().GetUsedUnits() == 10
	})
	r, _ := svc.CheckEntitlement(ctx, &limitsv1.CheckEntitlementRequest{CustomerId: cust.String(), MeterCode: "api"})
	st := r.GetStatus()
	if st.GetAllowed() || st.GetUnlimited() || st.GetUsed() != "10" || st.GetLimit() != "10" || st.GetAsOf() == nil {
		t.Fatalf("at the limit: %v", st)
	}
	all, err := svc.GetLimitStatus(ctx, &limitsv1.GetLimitStatusRequest{CustomerId: cust.String()})
	if err != nil || len(all.GetStatuses()) != 1 {
		t.Fatalf("GetLimitStatus = %v, %v", all, err)
	}

	e.produceEntitlement(fmt.Sprintf("%s/%s/api", tn, cust), nil) // tombstone
	eventually(t, 5*time.Second, "entitlement removed", func() bool {
		r, err := svc.CheckEntitlement(ctx, &limitsv1.CheckEntitlementRequest{CustomerId: cust.String(), MeterCode: "api"})
		return err == nil && r.GetStatus().GetUnlimited()
	})
	// Another tenant never sees it.
	other := tenant.NewContext(context.Background(), tenant.Info{ID: uuid.New()})
	if r, err := svc.CheckEntitlement(other, &limitsv1.CheckEntitlementRequest{CustomerId: cust.String(), MeterCode: "api"}); err != nil || r.GetStatus().GetUsedUnits() != 0 {
		t.Fatalf("other tenant sees %v, %v", r, err)
	}
}

// A fractional limit of 10.5 (scale 3) fires its 100 % threshold on the event that reaches 10.500.
func testFractionalLimit(t *testing.T) {
	e := newEnv(t)
	tn, cust := uuid.New(), uuid.New()
	e.setEntitlement(tn, cust, "gb", 10500, 3, 100)
	p := e.startPod(podOpts{})
	eventually(t, 20*time.Second, "entitlement", func() bool { return p.ents.Len() == 1 })
	e.produce(e.event(tn, cust, "gb", 10000, 3, nil))
	e.produce(e.event(tn, cust, "gb", 499, 3, nil))
	eventually(t, 20*time.Second, "10.499", func() bool { return counter(t, tn, cust, "gb") == 10499 })
	if got := e.readLimitEvents(0, 1500*time.Millisecond); len(got) != 0 {
		t.Fatalf("fired at 10.499: %v", got)
	}
	e.produce(e.event(tn, cust, "gb", 1, 3, nil))
	got := e.readLimitEvents(1, 2*time.Second)
	if len(got) != 1 || got[0].GetUsedUnits() != 10500 || got[0].GetLimitUnits() != 10500 || got[0].GetQuantityScale() != 3 {
		t.Fatalf("got %v, want one event at used = limit = 10500 (scale 3)", got)
	}
	svc := query.New(shared.rdb, p.ents, e.meters, nil)
	ctx := tenant.NewContext(context.Background(), tenant.Info{ID: tn, Timezone: "Europe/Istanbul"})
	r, _ := svc.CheckEntitlement(ctx, &limitsv1.CheckEntitlementRequest{CustomerId: cust.String(), MeterCode: "gb"})
	if r.GetStatus().GetUsed() != "10.500" || r.GetStatus().GetLimit() != "10.500" || r.GetStatus().GetAllowed() {
		t.Fatalf("status = %v", r.GetStatus())
	}
}

// COUNT counts events, MAX keeps the maximum, and the meter filter is honoured.
func testMeterSemantics(t *testing.T) {
	e := newEnv(t)
	tn, cust := uuid.New(), uuid.New()
	p := e.startPodNoConsume(podOpts{})
	var raws [][]byte
	for _, u := range []int64{5, 9, 3} {
		raws = append(raws, mustMarshal(t, e.event(tn, cust, "peak", u, 0, nil)), mustMarshal(t, e.event(tn, cust, "calls", u, 0, nil)))
	}
	raws = append(raws,
		mustMarshal(t, e.event(tn, cust, "eu_api", 4, 0, map[string]string{"region": "eu"})),
		mustMarshal(t, e.event(tn, cust, "eu_api", 100, 0, map[string]string{"region": "us"})),
		mustMarshal(t, e.event(tn, cust, "nope", 1, 0, nil)))
	if err := p.applier.Apply(context.Background(), 0, records(0, 0, raws...)); err != nil {
		t.Fatal(err)
	}
	if got := counter(t, tn, cust, "peak"); got != 9 {
		t.Errorf("MAX counter = %d, want 9", got)
	}
	if got := counter(t, tn, cust, "calls"); got != 3 {
		t.Errorf("COUNT counter = %d, want 3", got)
	}
	if got := counter(t, tn, cust, "eu_api"); got != 4 {
		t.Errorf("filtered counter = %d, want 4", got)
	}
	// A later batch with a smaller maximum doesn't lower it.
	if err := p.applier.Apply(context.Background(), 0, records(0, int64(len(raws)), mustMarshal(t, e.event(tn, cust, "peak", 2, 0, nil)))); err != nil {
		t.Fatal(err)
	}
	if got := counter(t, tn, cust, "peak"); got != 9 {
		t.Errorf("MAX counter after a smaller value = %d, want 9", got)
	}
}

// A delta that would push a counter past 2^53 is refused and leaves the key untouched.
func testOverflow(t *testing.T) {
	e := newEnv(t)
	tn, cust := uuid.New(), uuid.New()
	p := e.startPodNoConsume(podOpts{})
	big := mustMarshal(t, e.event(tn, cust, "api", domain.MaxCounter-5, 0, nil))
	small := mustMarshal(t, e.event(tn, cust, "api", 3, 0, nil))
	if err := p.applier.Apply(context.Background(), 0, records(0, 0, big, small)); err != nil {
		t.Fatal(err)
	}
	if got := counter(t, tn, cust, "api"); got != domain.MaxCounter-2 {
		t.Fatalf("counter = %d, want %d", got, domain.MaxCounter-2)
	}
	more := mustMarshal(t, e.event(tn, cust, "api", 10, 0, nil))
	if err := p.applier.Apply(context.Background(), 0, records(0, 2, more)); err != nil {
		t.Fatal(err)
	}
	if got := counter(t, tn, cust, "api"); got != domain.MaxCounter-2 {
		t.Fatalf("counter = %d after a refused delta, want it unchanged", got)
	}
}
