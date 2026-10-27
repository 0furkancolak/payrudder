//go:build integration

package itest

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	eventsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/events/v1"
	"github.com/0furkancolak/payrudder/pkg/period"
	"github.com/0furkancolak/payrudder/pkg/testkit"
	"github.com/0furkancolak/payrudder/pkg/usage"
	"github.com/0furkancolak/payrudder/services/limits-svc/internal/engine"
)

// uniqueAndLastTests are run by TestQuotas, which owns the Redis container: they need Redis only, like the quota tests.
var uniqueAndLastTests = map[string]func(*testing.T){
	"UniqueCountsDistinctValuesPerWindow":    testUniqueDistinct,
	"UniqueSetIsBounded":                     testUniqueCap,
	"UniqueRecoversAfterRedisLoss":           testUniqueRedisLoss,
	"UniqueFiresItsThresholdsOnce":           testUniqueThresholds,
	"LastKeepsTheLatestEventNotTheLatest":    testLastOrder,
	"LastIsRepairedToTheAggregatorsEvent":    testLastReconcile,
	"LastComparesIdsBytewiseInRedis":         testLastBytewise,
	"UniqueAndLastAgreeAfterAReplayOfTheLog": testReplayOfTheLog,
}

// at builds an event of a UNIQUE or LAST meter at an offset in seconds before now (so it stays in the current month and day), with an explicit id.
func (q *quotaPod) at(tn, cust uuid.UUID, meter, id string, secondsAgo int, units int64, user string) *eventsv1.UsageEvent {
	props := map[string]string{}
	if user != "" {
		props["user"] = user
	}
	ev := q.ev.event(tn, cust, meter, units, 0, props)
	ev.EventId, ev.IdempotencyKey = id, id
	ev.EventTime = timestamppb.New(time.Now().Add(-time.Duration(secondsAgo) * time.Second).Truncate(time.Microsecond))
	return ev
}

func (q *quotaPod) reconcileWith(totals map[string]int64, orders map[string]usage.Order) engine.Result {
	q.t.Helper()
	agg := &fakeAggregator{totals: totals, scale: map[string]int{}, offsets: map[int32]int64{0: q.usedOff}, orders: orders}
	rec := engine.NewReconciler(engine.ReconcilerConfig{Applier: q.applier, Redis: q.rdb, Aggregator: agg, Meters: defaultMeters,
		Owned: func() []int32 { return []int32{0} }, CheckpointTimeout: 5 * time.Second, Log: testkit.Logger()})
	res, err := rec.RunOnce(context.Background())
	if err != nil {
		q.t.Fatal(err)
	}
	return res
}

func keys(tn, cust uuid.UUID, meter string) (month, day string) {
	now := time.Now()
	dayID, _, _ := period.WindowOf(period.Day, now, istanbul)
	return engine.CounterKey(tn, cust, meter, period.PeriodOf(now, istanbul)), engine.WindowKey(tn, cust, meter, dayID)
}

// A value counts once however often it comes, per counter: the month and the day each have their own set, an event without the property counts nothing, and a
// batch redelivered after a crash cannot count a value twice (the offset guard) nor can an outbox duplicate (the set).
func testUniqueDistinct(t *testing.T) {
	q := newQuotaPod(t)
	tn, cust, other := uuid.New(), uuid.New(), uuid.New()
	q.setQuota(tn, cust, "users", 100, "day", "hard", false, 100)
	month, day := keys(tn, cust, "users")
	q.apply(q.at(tn, cust, "users", "a1", 10, 1, "alice"), q.at(tn, cust, "users", "a2", 9, 1, "alice"), q.at(tn, cust, "users", "b1", 8, 1, "bob"))
	q.apply(q.at(tn, cust, "users", "a3", 7, 1, "alice"), q.at(tn, cust, "users", "c1", 6, 1, "carol"), q.at(tn, cust, "users", "none", 5, 1, ""))
	// An outbox duplicate: the same event id and value again in a later batch.
	q.apply(q.at(tn, cust, "users", "b1", 8, 1, "bob"))
	q.apply(q.at(tn, other, "users", "x1", 4, 1, "alice")) // another customer has its own counters
	if m, d := q.get(month), q.get(day); m != 3 || d != 3 {
		t.Fatalf("month %d, day %d; want 3 and 3 (alice, bob, carol)", m, d)
	}
	if o, _ := keys(tn, other, "users"); q.get(o) != 1 {
		t.Fatalf("another customer's counter = %d, want 1", q.get(o))
	}
	// The set is bounded by the expiry of its counter and cannot be mistaken for a counter.
	ctx := context.Background()
	if n := q.rdb.SCard(ctx, engine.SetKey(month)).Val(); n != 3 {
		t.Fatalf("set of the month holds %d members, want 3", n)
	}
	if ttl, cttl := q.rdb.TTL(ctx, engine.SetKey(month)).Val(), q.rdb.TTL(ctx, month).Val(); ttl <= 0 || cttl <= 0 || ttl-cttl > time.Second || cttl-ttl > time.Second {
		t.Fatalf("set TTL %s, counter TTL %s: they must expire together", ttl, cttl)
	}
	if _, ok := engine.ParseKey(engine.SetKey(month)); ok {
		t.Fatal("a set key parses as a counter key")
	}
	st := q.status(tn, cust, "users")
	if st.GetUsedUnits() != 3 || st.GetWindow() != "day" {
		t.Fatalf("status = %v", st)
	}
	// Distinct counts are checked against the aggregator's like any counter: here they agree.
	otherMonth, _ := keys(tn, other, "users")
	if res := q.reconcileWith(map[string]int64{month: 3, day: 3, otherMonth: 1}, nil); res.Corrections != 0 || res.Checked != 3 {
		t.Fatalf("reconcile of equal counts = %+v", res)
	}
}

// A set is bounded: when it is full a new value cannot be recognised, so the counter lags until reconciliation sets it to the aggregator's exact count, and a
// value that is already in the set still counts as seen.
func testUniqueCap(t *testing.T) {
	q := newQuotaPodWith(t, 3)
	tn, cust := uuid.New(), uuid.New()
	month, _ := keys(tn, cust, "users")
	for i := range 5 {
		q.apply(q.at(tn, cust, "users", fmt.Sprintf("e%d", i), 10-i, 1, fmt.Sprintf("u%d", i)))
	}
	if q.get(month) != 3 || q.rdb.SCard(context.Background(), engine.SetKey(month)).Val() != 3 {
		t.Fatalf("counter %d, set %d; want both held at the cap 3", q.get(month), q.rdb.SCard(context.Background(), engine.SetKey(month)).Val())
	}
	// The aggregator counted all five: the counter is set to it.
	if res := q.reconcileWith(map[string]int64{month: 5}, nil); res.Corrections != 1 || q.get(month) != 5 {
		t.Fatalf("reconcile = %+v, counter %d, want 5", res, q.get(month))
	}
	q.apply(q.at(tn, cust, "users", "again", 2, 1, "u0")) // in the set: nothing
	if q.get(month) != 5 {
		t.Fatalf("a remembered value moved the counter to %d", q.get(month))
	}
}

// Redis loses its counters and sets but keeps its offsets: reconciliation restores the count, and the value that was forgotten is counted again by the next
// event that carries it (over by one) until the next reconciliation takes the difference out.
func testUniqueRedisLoss(t *testing.T) {
	q := newQuotaPod(t)
	tn, cust := uuid.New(), uuid.New()
	month, _ := keys(tn, cust, "users")
	q.apply(q.at(tn, cust, "users", "1", 5, 1, "a"), q.at(tn, cust, "users", "2", 4, 1, "b"))
	ctx := context.Background()
	if err := q.rdb.Del(ctx, month, engine.SetKey(month)).Err(); err != nil {
		t.Fatal(err)
	}
	q.apply(q.at(tn, cust, "users", "3", 3, 1, "a"))
	if q.get(month) != 1 {
		t.Fatalf("after the loss the counter restarted at %d", q.get(month))
	}
	q.reconcileWith(map[string]int64{month: 2}, nil)
	if q.get(month) != 2 {
		t.Fatalf("reconcile did not restore the count: %d", q.get(month))
	}
	q.apply(q.at(tn, cust, "users", "4", 2, 1, "b")) // b was forgotten with the set
	if q.get(month) != 3 {
		t.Fatalf("a forgotten value is counted again until reconciled: %d", q.get(month))
	}
	q.reconcileWith(map[string]int64{month: 2}, nil)
	if q.get(month) != 2 {
		t.Fatalf("reconcile did not take the double count out: %d", q.get(month))
	}
}

// The count crossing a threshold fires it once, however the count grows afterwards.
func testUniqueThresholds(t *testing.T) {
	q := newQuotaPod(t)
	tn, cust := uuid.New(), uuid.New()
	q.setQuota(tn, cust, "users", 4, "month", "hard", false, 50, 100)
	p := period.PeriodOf(time.Now(), istanbul)
	fired := func(pct int32) bool {
		return q.rdb.Exists(context.Background(), engine.FiredKey(tn, cust, "users", p, pct)).Val() == 1
	}
	q.apply(q.at(tn, cust, "users", "1", 9, 1, "a"), q.at(tn, cust, "users", "2", 8, 1, "a"), q.at(tn, cust, "users", "3", 7, 1, "a"))
	if fired(50) || fired(100) {
		t.Fatal("repeated values crossed a threshold")
	}
	q.apply(q.at(tn, cust, "users", "4", 6, 1, "b"))
	if !fired(50) || fired(100) {
		t.Fatal("two of four distinct values is 50 %")
	}
	q.apply(q.at(tn, cust, "users", "5", 5, 1, "c"), q.at(tn, cust, "users", "6", 4, 1, "d"))
	if !fired(100) {
		t.Fatal("four of four distinct values is 100 %")
	}
}

// LAST is the value of the latest event by (time, id), not of the last one to arrive: a late-arriving earlier event does not replace it, a redelivery does not
// change it, and the day and the month each hold their own latest.
func testLastOrder(t *testing.T) {
	q := newQuotaPod(t)
	tn, cust := uuid.New(), uuid.New()
	q.setQuota(tn, cust, "seats", 100, "day", "hard", false, 100)
	month, day := keys(tn, cust, "seats")
	q.apply(q.at(tn, cust, "seats", "m", 5, 12, ""), q.at(tn, cust, "seats", "b", 20, 4, "")) // the later event first within the batch, the earlier after it
	if q.get(month) != 12 || q.get(day) != 12 {
		t.Fatalf("month %d, day %d; want 12 (the event 5 s ago)", q.get(month), q.get(day))
	}
	q.apply(q.at(tn, cust, "seats", "old", 60, 99, "")) // arrives later, happened earlier
	if q.get(month) != 12 {
		t.Fatalf("an earlier event took the value: %d", q.get(month))
	}
	q.apply(q.at(tn, cust, "seats", "m", 5, 12, "")) // redelivered
	if q.get(month) != 12 {
		t.Fatalf("a redelivery changed the value: %d", q.get(month))
	}
	q.apply(q.at(tn, cust, "seats", "n", 1, 7, "")) // later, and smaller
	if q.get(month) != 7 || q.get(day) != 7 {
		t.Fatalf("the latest event, whatever its value: month %d day %d, want 7", q.get(month), q.get(day))
	}
	held := q.rdb.Get(context.Background(), engine.OrderKey(month)).Val()
	if want := fmt.Sprintf(":n"); held[len(held)-2:] != want {
		t.Fatalf("order key %q does not end with the event id", held)
	}
	if st := q.status(tn, cust, "seats"); st.GetUsedUnits() != 7 {
		t.Fatalf("status used %d, want 7", st.GetUsedUnits())
	}
}

// Ties on the microsecond fall to the greater id compared bytewise, in Redis as in Postgres: "a" (0x61) comes after "Z" (0x5a) and "ev10" before "ev9".
func testLastBytewise(t *testing.T) {
	q := newQuotaPod(t)
	tn, cust := uuid.New(), uuid.New()
	month, _ := keys(tn, cust, "seats")
	at := time.Now().Add(-time.Minute).Truncate(time.Microsecond)
	mk := func(id string, units int64) *eventsv1.UsageEvent {
		ev := q.at(tn, cust, "seats", id, 0, units, "")
		ev.EventTime = timestamppb.New(at)
		return ev
	}
	for _, c := range []struct {
		batches [][]string
		want    int64
	}{
		{[][]string{{"Z"}, {"a"}}, 2},
		{[][]string{{"a"}, {"Z"}}, 2},
		{[][]string{{"ev9"}, {"ev10"}}, 9},
		{[][]string{{"ev10"}, {"ev9"}}, 9},
		{[][]string{{"abc"}, {"ab"}}, 3},
	} {
		q = newQuotaPod(t) // a clean Redis per case
		vals := map[string]int64{"Z": 1, "a": 2, "ev9": 9, "ev10": 10, "abc": 3, "ab": 4}
		for _, b := range c.batches {
			q.apply(mk(b[0], vals[b[0]]))
		}
		if got := q.get(month); got != c.want {
			t.Errorf("%v: counter %d, want %d", c.batches, got, c.want)
		}
	}
}

// The aggregator names the event its LAST value is of; a counter that disagrees, in value or in event, is set to it, so that an event ordered between the two
// cannot take the value back afterwards. An aggregator with no event sets the counter to zero and forgets the event.
func testLastReconcile(t *testing.T) {
	q := newQuotaPod(t)
	tn, cust := uuid.New(), uuid.New()
	month, _ := keys(tn, cust, "seats")
	q.apply(q.at(tn, cust, "seats", "e1", 100, 5, ""))
	now := time.Now()
	t2 := now.Add(-50 * time.Second).Truncate(time.Microsecond)
	res := q.reconcileWith(map[string]int64{month: 7}, map[string]usage.Order{month: {Micros: t2.UnixMicro(), ID: "e2"}})
	if res.Corrections != 1 || q.get(month) != 7 {
		t.Fatalf("reconcile = %+v, counter %d, want 7", res, q.get(month))
	}
	if got, want := q.rdb.Get(context.Background(), engine.OrderKey(month)).Val(), fmt.Sprintf("%d:e2", t2.UnixMicro()); got != want {
		t.Fatalf("order key = %q, want %q", got, want)
	}
	q.apply(q.at(tn, cust, "seats", "e1b", 75, 9, "")) // between e1 and e2 in time: must not take the value
	if q.get(month) != 7 {
		t.Fatalf("an event earlier than the repaired one took the value: %d", q.get(month))
	}
	// The same value from a different event is drift too (the counter is of the wrong event).
	if res := q.reconcileWith(map[string]int64{month: 7}, map[string]usage.Order{month: {Micros: t2.UnixMicro(), ID: "e3"}}); res.Corrections != 1 {
		t.Fatalf("a different event of the same value is not drift: %+v", res)
	}
	// Nothing in the aggregator.
	if res := q.reconcileWith(map[string]int64{month: 0}, nil); res.Corrections != 1 || q.get(month) != 0 || q.rdb.Exists(context.Background(), engine.OrderKey(month)).Val() != 0 {
		t.Fatalf("reconcile to nothing = %+v, counter %d", res, q.get(month))
	}
	// The corrected keys expire with the counter's usual expiry, not never.
	q.reconcileWith(map[string]int64{month: 3}, map[string]usage.Order{month: {Micros: t2.UnixMicro(), ID: "e9"}})
	if ttl := q.rdb.TTL(context.Background(), engine.OrderKey(month)).Val(); ttl <= 0 {
		t.Fatalf("order key TTL %s", ttl)
	}
}

// Replaying the whole log into an empty Redis (what a lost offset key does) gives the same counters as the first pass, for both aggregations.
func testReplayOfTheLog(t *testing.T) {
	q := newQuotaPod(t)
	tn, cust := uuid.New(), uuid.New()
	var events []*eventsv1.UsageEvent
	for i := range 12 {
		events = append(events, q.at(tn, cust, "users", fmt.Sprintf("u%d", i), 100-i, 1, fmt.Sprintf("v%d", i%5)))
		events = append(events, q.at(tn, cust, "seats", fmt.Sprintf("s%d", i), 100-(i*7)%13, int64(i+1), ""))
	}
	q.apply(events...)
	uMonth, _ := keys(tn, cust, "users")
	sMonth, _ := keys(tn, cust, "seats")
	wantU, wantS := q.get(uMonth), q.get(sMonth)
	if wantU != 5 {
		t.Fatalf("distinct users = %d, want 5", wantU)
	}
	if err := q.rdb.FlushAll(context.Background()).Err(); err != nil {
		t.Fatal(err)
	}
	// The apply loop notices that Redis lost its offset key and asks to be fed the partition from the start (ErrRedisReset), as the consumer would.
	err := q.applier.Apply(context.Background(), 0, records(0, 0, mustMarshal(t, events[len(events)-1])))
	if !errors.Is(err, engine.ErrRedisReset) {
		t.Fatalf("first batch after the loss = %v, want ErrRedisReset", err)
	}
	q.usedOff = 0
	for i := len(events) - 1; i >= 0; i-- { // in the opposite order, one event per batch
		q.apply(events[i])
	}
	if q.get(uMonth) != wantU || q.get(sMonth) != wantS {
		t.Fatalf("after a replay in the opposite order: users %d (was %d), seats %d (was %d)", q.get(uMonth), wantU, q.get(sMonth), wantS)
	}
}
