//go:build integration

package itest

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	aggregatorv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/aggregator/v1"
	eventsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/events/v1"
	"github.com/0furkancolak/payrudder/pkg/kafkax"
	"github.com/0furkancolak/payrudder/pkg/period"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/services/aggregator/internal/domain"
	"github.com/0furkancolak/payrudder/services/aggregator/internal/maintenance"
)

// TestAggregator runs every scenario against one shared Postgres and Redpanda.
func TestAggregator(t *testing.T) {
	top = t
	t.Run("ExactTotalsWithCrashAfterCommit", testCrashAfterCommit)
	t.Run("SameEventAtTwoOffsetsCountsOnce", testDuplicateEvent)
	t.Run("ZombieConsumerCannotMoveOffsetsBackOrDoubleCount", testZombie)
	t.Run("PoisonRecordsGoToTheDLQAndTheRestIsApplied", testPoison)
	t.Run("PostgresOutageDoesNotDLQ", testPostgresOutage)
	t.Run("FractionalMeterIsExact", testFractional)
	t.Run("LateEventsLandInLateUsage", testLate)
	t.Run("MeterFilterIgnoresNonMatchingEvents", testFilter)
	t.Run("ClosePeriodEarly", testClosePeriodEarly)
	t.Run("FreezePeriodRoutesLateArrivals", testFreeze)
	t.Run("ReplayFixesAFilterAndPromotesUnderLoad", testReplayFixesAFilterAndPromotesUnderLoad)
	t.Run("ReplayCanBeDiscardedAndRestarted", testReplayDiscard)
	t.Run("ReplayPromoteKeepsPeriodsOutsideTheRange", testReplayPromoteKeepsPeriodsOutsideTheRange)
	t.Run("ReadAPIs", testReadAPIs)
	t.Run("RLSAndPartitions", testRLSAndPartitions) // last: drops old partitions
}

var (
	istanbul = func() *time.Location { l, _ := time.LoadLocation("Europe/Istanbul"); return l }()
	nov      = time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC) // ingestion time of on-time events of "2026-11"
)

// generate builds a deterministic workload: n unique events across tenants, customers and meters, some late, and
// dup% of them published a second time. It returns the events in publish order and the expected totals.
func generate(seed uint64, tenants []uuid.UUID, n int, dupPct int) ([]*eventsv1.UsageEvent, *ledger) {
	rng := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	led := newLedger()
	meterNames := []string{"sum_m", "count_m", "max_m"}
	aggOf := map[string]domain.Aggregation{"sum_m": domain.Sum, "count_m": domain.Count, "max_m": domain.Max}
	customers := map[uuid.UUID][]uuid.UUID{}
	for _, tn := range tenants {
		for range 20 {
			customers[tn] = append(customers[tn], uuid.New())
		}
	}
	var out, pending []*eventsv1.UsageEvent
	for i := 0; i < n; i++ {
		if len(pending) > 0 && rng.IntN(100) < 30 {
			out = append(out, pending[0]) // a republished event, some offsets after the original
			pending = pending[1:]
		}
		tn := tenants[rng.IntN(len(tenants))]
		cust := customers[tn][rng.IntN(20)]
		meter := meterNames[rng.IntN(len(meterNames))]
		units := int64(rng.IntN(1000))
		late := rng.IntN(100) < 2
		e := ev(fmt.Sprintf("E%012d", i), tn, cust, meter, units, 0, "2026-11", nov)
		k := rowKey{tenant: tn, customer: cust, meter: meter, period: "2026-11"}
		if late {
			// Event of October, ingested in November: past October's cutoff (end + 1 h), billed in November.
			e.PeriodId = "2026-10"
			k.period = "2026-11|2026-10"
			led.add(led.late, k, aggOf[meter], units)
		} else {
			led.add(led.onTime, k, aggOf[meter], units)
		}
		out = append(out, e)
		if rng.IntN(100) < dupPct {
			// The relay republished it: the same event again, later in the log.
			pending = append(pending, e)
		}
	}
	out = append(out, pending...)
	return out, led
}

func tenantIDs(n int) []uuid.UUID {
	out := make([]uuid.UUID, n)
	for i := range out {
		out[i] = uuid.New()
	}
	return out
}

func testCrashAfterCommit(t *testing.T) {
	n := 50000
	if v := os.Getenv("ITEST_AGG_EVENTS"); v != "" {
		n, _ = strconv.Atoi(v)
	}
	var mu sync.Mutex
	rng := rand.New(rand.NewPCG(7, 8))
	crashes := 0
	r := newRun(t, runOpts{afterCommit: func() error {
		mu.Lock()
		defer mu.Unlock()
		if rng.Float64() < 0.1 { // AGG_CRASH_AFTER_COMMIT=0.1
			crashes++
			return fmt.Errorf("simulated crash after commit")
		}
		return nil
	}})
	tenants := tenantIDs(5)
	events, led := generate(1, tenants, n, 5)
	r.produce(events...)
	r.start(r.group)
	r.drain(3 * time.Minute)

	compare(t, "usage_aggregates", led.onTime, onTimeRows(t, tenants...))
	compare(t, "late_usage", led.late, lateRows(t, tenants...))
	if crashes == 0 {
		t.Fatal("the fault hook never fired")
	}
	t.Logf("%d records (%d unique), %d simulated crashes", len(events), n, crashes)
}

func testDuplicateEvent(t *testing.T) {
	r := newRun(t, runOpts{})
	tn, cust := uuid.New(), uuid.New()
	before := counterTotal(t, "aggregator_duplicates_skipped")
	e := ev("DUP000000001", tn, cust, "sum_m", 10, 0, "2026-11", nov)
	r.produce(e)
	r.produce(ev("OTHER0000001", tn, cust, "sum_m", 5, 0, "2026-11", nov))
	r.produce(e) // the same event at a later offset
	r.start(r.group)
	r.drain(30 * time.Second)

	got := onTimeRows(t, tn)
	k := rowKey{tenant: tn, customer: cust, meter: "sum_m", period: "2026-11"}
	if g := got[k]; g.qty != "15" || g.count != 2 {
		t.Fatalf("aggregate = %+v, want qty 15 over 2 events", g)
	}
	if d := counterTotal(t, "aggregator_duplicates_skipped") - before; d != 1 {
		t.Fatalf("aggregator_duplicates_skipped grew by %d, want 1", d)
	}
}

func testZombie(t *testing.T) {
	r := newRun(t, runOpts{})
	tenants := tenantIDs(4)
	events, led := generate(3, tenants, 20000, 3)
	r.produce(events...)

	// Two replicas read the same partitions independently (different Kafka groups, same database offsets): each is
	// a zombie to the other.
	r.start(r.group + "-a")
	r.start(r.group + "-b")

	// Offsets must never move backwards while both run.
	stopWatch := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		last := map[int32]int64{}
		for {
			select {
			case <-stopWatch:
				return
			case <-time.After(10 * time.Millisecond):
			}
			rows, err := shared.system.Query(context.Background(),
				`SELECT partition, next_offset FROM consumer_offsets WHERE consumer_group = $1 AND topic = $2`, r.group, r.topic)
			if err != nil {
				continue
			}
			for rows.Next() {
				var p int32
				var off int64
				if rows.Scan(&p, &off) == nil {
					if off < last[p] {
						t.Errorf("partition %d offset moved back from %d to %d", p, last[p], off)
					}
					last[p] = off
				}
			}
			rows.Close()
		}
	}()
	r.drain(2 * time.Minute)
	time.Sleep(500 * time.Millisecond)
	close(stopWatch)
	wg.Wait()

	compare(t, "usage_aggregates", led.onTime, onTimeRows(t, tenants...))
	compare(t, "late_usage", led.late, lateRows(t, tenants...))
}

func testPoison(t *testing.T) {
	r := newRun(t, runOpts{})
	tn, cust := uuid.New(), uuid.New()
	good := func(id string, units int64) *kgo.Record {
		b, _ := marshal(ev(id, tn, cust, "sum_m", units, 0, "2026-11", nov))
		return &kgo.Record{Topic: r.topic, Key: []byte(tn.String()), Value: b}
	}
	badTenant := ev("BADTENANT001", tn, cust, "sum_m", 1, 0, "2026-11", nov)
	badTenant.TenantId = "not-a-uuid"
	badBytes, _ := marshal(badTenant)
	ghost, _ := marshal(ev("GHOST0000001", tn, cust, "no_such_meter", 1, 0, "2026-11", nov))
	r.produceRecords(
		good("GOOD00000001", 3),
		&kgo.Record{Topic: r.topic, Key: []byte(tn.String()), Value: []byte("\xff\xff garbage, not protobuf")},
		good("GOOD00000002", 4),
		&kgo.Record{Topic: r.topic, Key: []byte(tn.String()), Value: badBytes},
		&kgo.Record{Topic: r.topic, Key: []byte(tn.String()), Value: ghost},
		good("GOOD00000003", 5),
	)
	r.start(r.group)
	r.drain(30 * time.Second)

	k := rowKey{tenant: tn, customer: cust, meter: "sum_m", period: "2026-11"}
	if g := onTimeRows(t, tn)[k]; g.qty != "12" || g.count != 3 {
		t.Fatalf("aggregate = %+v, want 12 over the 3 good events", g)
	}
	dlq := consumeTopic(t, r.dlqTopic, 3*time.Second)
	if len(dlq) != 3 {
		t.Fatalf("DLQ has %d records, want 3", len(dlq))
	}
	reasons := map[string]bool{}
	for _, rec := range dlq {
		h := kafkax.HeaderMap(rec.Headers)
		for _, name := range []string{"x-dlq-reason", "x-dlq-error", "x-dlq-consumer", "x-original-topic", "x-original-partition", "x-original-offset", "x-dlq-at"} {
			if h[name] == "" {
				t.Errorf("DLQ record is missing header %s: %v", name, h)
			}
		}
		if h["x-original-topic"] != r.topic || h["x-dlq-consumer"] != r.group {
			t.Errorf("DLQ headers = %v", h)
		}
		reasons[h["x-dlq-reason"]] = true
	}
	for _, want := range []string{"DECODE_ERROR", "INVALID_EVENT", "UNKNOWN_METER"} {
		if !reasons[want] {
			t.Errorf("no DLQ record with reason %s (got %v)", want, reasons)
		}
	}
}

func testPostgresOutage(t *testing.T) {
	r := newRun(t, runOpts{})
	tenants := tenantIDs(3)
	events, led := generate(5, tenants, 10000, 2)
	half := len(events) / 2
	pause := 20 * time.Second
	if v := os.Getenv("ITEST_PG_PAUSE"); v != "" {
		pause, _ = time.ParseDuration(v)
	}

	r.start(r.group)
	r.produce(events[:half]...)
	dockerPause(t, true)
	paused := true
	defer func() {
		if paused {
			dockerPause(t, false)
		}
	}()
	r.produce(events[half:]...)
	time.Sleep(pause)
	dockerPause(t, false)
	paused = false

	r.drain(2 * time.Minute)
	compare(t, "usage_aggregates", led.onTime, onTimeRows(t, tenants...))
	compare(t, "late_usage", led.late, lateRows(t, tenants...))
	if dlq := consumeTopic(t, r.dlqTopic, 2*time.Second); len(dlq) != 0 {
		t.Fatalf("an infrastructure outage produced %d DLQ records", len(dlq))
	}
}

func testFractional(t *testing.T) {
	r := newRun(t, runOpts{})
	tn, cust := uuid.New(), uuid.New()
	var events []*eventsv1.UsageEvent
	for i := 0; i < 1000; i++ {
		events = append(events, ev(fmt.Sprintf("FRAC%08d", i), tn, cust, "sum_m", 1, 3, "2026-11", nov)) // 0.001
	}
	r.produce(events...)
	r.start(r.group)
	r.drain(30 * time.Second)

	c := queryClient(t, r)
	resp, err := c.GetCustomerUsage(as(tn, "live"), &aggregatorv1.GetCustomerUsageRequest{CustomerId: cust.String(), PeriodId: "2026-11"})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Usage) != 1 || resp.Usage[0].Quantity != "1.000" || resp.Usage[0].QuantityScale != 3 || resp.Usage[0].EventCount != 1000 {
		t.Fatalf("usage = %v, want 1.000 over 1000 events", resp.Usage)
	}
}

func testLate(t *testing.T) {
	r := newRun(t, runOpts{})
	tn, cust := uuid.New(), uuid.New()
	// October ended 2026-10-31T21:00Z in Istanbul; with 1 h of grace the cutoff is 22:00Z.
	justBefore := time.Date(2026, 10, 31, 21, 59, 59, 999_000_000, time.UTC)
	atCutoff := time.Date(2026, 10, 31, 22, 0, 0, 0, time.UTC)
	// Ingested 2026-11-05 in Istanbul's November: billed in 2026-11.
	r.produce(
		ev("LATE00000001", tn, cust, "sum_m", 7, 0, "2026-10", justBefore),
		ev("LATE00000002", tn, cust, "sum_m", 11, 0, "2026-10", atCutoff),
		ev("LATE00000003", tn, cust, "sum_m", 13, 0, "2026-10", nov),
		// Ingested in Istanbul's December already (2026-11-30T21:30Z is 2026-12-01 00:30 local) but event of October.
		ev("LATE00000004", tn, cust, "sum_m", 17, 0, "2026-10", time.Date(2026, 11, 30, 21, 30, 0, 0, time.UTC)),
	)
	r.start(r.group)
	r.drain(30 * time.Second)

	if g := onTimeRows(t, tn)[rowKey{tenant: tn, customer: cust, meter: "sum_m", period: "2026-10"}]; g.qty != "7" || g.count != 1 {
		t.Fatalf("on-time aggregate = %+v, want only the event 1 ms before the cutoff", g)
	}
	late := lateRows(t, tn)
	want := map[string]string{"2026-11|2026-10": "24", "2026-12|2026-10": "17"}
	if len(late) != 2 {
		t.Fatalf("late rows = %v", late)
	}
	for k, g := range late {
		if g.qty != want[k.period] || g.lateOf != "2026-10" {
			t.Errorf("late row %s = %+v, want %s", k.period, g, want[k.period])
		}
	}
	// And through the API, as billing reads it.
	c := queryClient(t, r)
	stream, err := c.StreamLateUsage(as(tn, "live"), &aggregatorv1.StreamLateUsageRequest{BilledPeriodId: "2026-11"})
	if err != nil {
		t.Fatal(err)
	}
	msg, err := stream.Recv()
	if err != nil {
		t.Fatal(err)
	}
	u := msg.GetLateUsage()
	if u.Quantity != "24" || u.OriginPeriodId != "2026-10" || u.BilledPeriodId != "2026-11" || u.EventCount != 2 {
		t.Fatalf("late usage = %v", u)
	}
}

func testFilter(t *testing.T) {
	r := newRun(t, runOpts{})
	tn, cust := uuid.New(), uuid.New()
	mk := func(id, region string, units int64) *eventsv1.UsageEvent {
		e := ev(id, tn, cust, "eu_only", units, 0, "2026-11", nov)
		e.Properties = map[string]string{"region": region}
		return e
	}
	noProps := ev("FILT00000004", tn, cust, "eu_only", 100, 0, "2026-11", nov)
	r.produce(mk("FILT00000001", "eu", 1), mk("FILT00000002", "us", 10), mk("FILT00000003", "eu", 2), noProps)
	r.start(r.group)
	r.drain(30 * time.Second)
	if g := onTimeRows(t, tn)[rowKey{tenant: tn, customer: cust, meter: "eu_only", period: "2026-11"}]; g.qty != "3" || g.count != 2 {
		t.Fatalf("aggregate = %+v, want only the two eu events (3)", g)
	}
}

func testClosePeriodEarly(t *testing.T) {
	r := newRun(t, runOpts{})
	c := queryClient(t, r)
	sandbox, live := uuid.New(), uuid.New()
	cust := uuid.New()
	cur := period.PeriodOf(time.Now(), istanbul)

	if _, err := c.ClosePeriodEarly(as(live, "live"), &aggregatorv1.ClosePeriodEarlyRequest{PeriodId: cur}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("live tenant: %v, want FailedPrecondition", err)
	}

	// Wave 1 runs while the period closes. Every event is ingested "now": below both the old and the new cutoff,
	// so each is on time whichever cutoff its batch saw, and none may be lost or counted twice.
	r.start(r.group)
	const wave1 = 20000
	var events []*eventsv1.UsageEvent
	for i := 0; i < wave1; i++ {
		events = append(events, ev(fmt.Sprintf("CLOS%08d", i), sandbox, cust, "sum_m", 1, 0, cur, time.Now()))
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); r.produce(events...) }()
	time.Sleep(100 * time.Millisecond)
	resp, err := c.ClosePeriodEarly(as(sandbox, "sandbox"), &aggregatorv1.ClosePeriodEarlyRequest{PeriodId: cur})
	if err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	r.drain(time.Minute)
	cutoff := resp.CutoffAt.AsTime()
	if d := time.Until(cutoff); d > 6*time.Second || d < -time.Minute {
		t.Fatalf("cutoff %s is not about 5 s from now", cutoff)
	}
	k := rowKey{tenant: sandbox, customer: cust, meter: "sum_m", period: cur}
	if g := onTimeRows(t, sandbox)[k]; g.qty != strconv.Itoa(wave1) || g.count != wave1 || len(lateRows(t, sandbox)) != 0 {
		t.Fatalf("after wave 1: %+v, late rows %d; want %d on time and none late", g, len(lateRows(t, sandbox)), wave1)
	}

	// Wave 2 arrives after the new cutoff was recorded: ingestion times at or after it are late, before it on time.
	after := ev("CLOS-AFTER-01", sandbox, cust, "sum_m", 100, 0, cur, cutoff.Add(time.Second))
	before := ev("CLOS-BEFOR-01", sandbox, cust, "sum_m", 1000, 0, cur, cutoff.Add(-time.Second))
	r.produce(after, before)
	r.drain(30 * time.Second)
	if g := onTimeRows(t, sandbox)[k]; g.qty != strconv.Itoa(wave1+1000) {
		t.Fatalf("on-time total = %s, want %d", g.qty, wave1+1000)
	}
	var lateQty string
	for _, g := range lateRows(t, sandbox) {
		lateQty = g.qty
	}
	if lateQty != "100" {
		t.Fatalf("late total = %q, want 100", lateQty)
	}
	// The period already closes within 5 s: closing again is refused.
	if _, err := c.ClosePeriodEarly(as(sandbox, "sandbox"), &aggregatorv1.ClosePeriodEarlyRequest{PeriodId: cur}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("second close: %v, want FailedPrecondition", err)
	}
}

func testFreeze(t *testing.T) {
	r := newRun(t, runOpts{})
	c := queryClient(t, r)
	tn, cust := uuid.New(), uuid.New()
	cur := period.PeriodOf(time.Now(), istanbul)
	r.start(r.group)
	r.produce(ev("FRZ000000001", tn, cust, "sum_m", 1, 0, cur, time.Now()))
	r.drain(30 * time.Second)

	at1, err := c.FreezePeriod(as(tn, "live"), &aggregatorv1.FreezePeriodRequest{PeriodId: cur})
	if err != nil {
		t.Fatal(err)
	}
	at2, err := c.FreezePeriod(as(tn, "live"), &aggregatorv1.FreezePeriodRequest{PeriodId: cur})
	if err != nil || !at1.FrozenAt.AsTime().Equal(at2.FrozenAt.AsTime()) {
		t.Fatalf("FreezePeriod is not idempotent: %v %v %v", at1, at2, err)
	}
	before := counterTotal(t, "aggregator_frozen_period_violations")
	// An event that is on time by its timestamps arrives for the frozen period: it must not change the frozen aggregate.
	r.produce(ev("FRZ000000002", tn, cust, "sum_m", 5, 0, cur, time.Now()))
	r.drain(30 * time.Second)
	k := rowKey{tenant: tn, customer: cust, meter: "sum_m", period: cur}
	if g := onTimeRows(t, tn)[k]; g.qty != "1" {
		t.Fatalf("frozen aggregate changed to %s", g.qty)
	}
	if d := counterTotal(t, "aggregator_frozen_period_violations") - before; d != 1 {
		t.Fatalf("violations grew by %d, want 1", d)
	}
	var lateQty string
	for _, g := range lateRows(t, tn) {
		lateQty = g.qty
	}
	if lateQty != "5" {
		t.Fatalf("late total = %q, want 5", lateQty)
	}
}

func testReadAPIs(t *testing.T) {
	r := newRun(t, runOpts{})
	c := queryClient(t, r)
	a, b := uuid.New(), uuid.New()
	cust := uuid.New()
	r.produce(
		ev("READ00000001", a, cust, "sum_m", 5, 0, "2026-11", nov),
		ev("READ00000002", a, cust, "count_m", 99, 0, "2026-11", nov),
		ev("READ00000003", b, cust, "sum_m", 1000, 0, "2026-11", nov),
	)
	r.start(r.group)
	r.drain(30 * time.Second)

	resp, err := c.GetCustomerUsage(as(a, "live"), &aggregatorv1.GetCustomerUsageRequest{CustomerId: cust.String(), PeriodId: "2026-11"})
	if err != nil || len(resp.Usage) != 2 || resp.Usage[0].MeterCode != "count_m" || resp.Usage[0].Quantity != "1" || resp.Usage[1].Quantity != "5" {
		t.Fatalf("usage = %v, %v", resp, err)
	}
	// Tenant b's customer row with the same customer id is invisible to tenant a, and vice versa.
	respB, _ := c.GetCustomerUsage(as(b, "live"), &aggregatorv1.GetCustomerUsageRequest{CustomerId: cust.String(), PeriodId: "2026-11"})
	if len(respB.Usage) != 1 || respB.Usage[0].Quantity != "1000" {
		t.Fatalf("tenant b usage = %v", respB.Usage)
	}
	if _, err := c.GetCustomerUsage(context.Background(), &aggregatorv1.GetCustomerUsageRequest{CustomerId: cust.String(), PeriodId: "2026-11"}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("no tenant: %v", err)
	}
	if _, err := c.GetCustomerUsage(as(a, "live"), &aggregatorv1.GetCustomerUsageRequest{CustomerId: "x", PeriodId: "2026-11"}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("bad customer: %v", err)
	}

	batch, err := c.GetUsageBatch(as(a, "live"), &aggregatorv1.GetUsageBatchRequest{Keys: []*aggregatorv1.UsageKey{
		{CustomerId: cust.String(), MeterCode: "sum_m", PeriodId: "2026-11"},
		{CustomerId: cust.String(), MeterCode: "max_m", PeriodId: "2026-11"},
		{CustomerId: uuid.NewString(), MeterCode: "sum_m", PeriodId: "2026-11"},
	}})
	if err != nil || len(batch.Usage) != 3 || batch.Usage[0].Quantity != "5" || batch.Usage[1].Quantity != "0" || batch.Usage[2].Quantity != "0" {
		t.Fatalf("batch = %v, %v", batch, err)
	}
	var sum int64
	for _, off := range batch.ConsumerOffsets {
		sum += off
	}
	if sum != 3 {
		t.Fatalf("snapshot offsets %v sum to %d, want 3", batch.ConsumerOffsets, sum)
	}
	if _, err := c.GetUsageBatch(as(a, "live"), &aggregatorv1.GetUsageBatchRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("empty batch: %v", err)
	}

	stream, err := c.StreamPeriodUsage(as(a, "live"), &aggregatorv1.StreamPeriodUsageRequest{PeriodId: "2026-11"})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for {
		if _, err := stream.Recv(); err != nil {
			break
		}
		n++
	}
	if n != 2 {
		t.Fatalf("streamed %d aggregates, want 2", n)
	}

	// Watermark: caught up to what was consumed, not beyond.
	w, err := c.GetIngestWatermark(context.Background(), &aggregatorv1.GetIngestWatermarkRequest{RequiredOffsets: map[int32]int64{0: 1 << 40}})
	if err != nil || w.CaughtUp {
		t.Fatalf("watermark beyond the log: %v %v", w, err)
	}
	req := map[int32]int64{}
	for p, off := range w.CommittedOffsets {
		req[p] = off
	}
	if w, _ = c.GetIngestWatermark(context.Background(), &aggregatorv1.GetIngestWatermarkRequest{RequiredOffsets: req}); !w.CaughtUp {
		t.Fatal("watermark equal to the committed offsets must be caught up")
	}
}

func testRLSAndPartitions(t *testing.T) {
	infra(t)
	ctx := context.Background()
	if err := pgxx.VerifyForcedRLS(ctx, shared.system, []string{"processed_events", "replay_jobs"}); err != nil {
		t.Fatalf("forced RLS: %v", err)
	}
	// The app role sees nothing without a tenant, and can't touch the inbox or write aggregates.
	var n int
	if err := shared.app.QueryRow(ctx, `SELECT count(*) FROM usage_aggregates`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("app without tenant sees %d aggregate rows (%v)", n, err)
	}
	for _, stmt := range []string{`SELECT 1 FROM processed_events`, `DELETE FROM usage_aggregates`, `INSERT INTO usage_aggregates VALUES (gen_random_uuid(), 'x', gen_random_uuid(), 'm', 1, 1, 0, 1, now())`} {
		if _, err := shared.app.Exec(ctx, stmt); pgxx.ErrCode(err) != "42501" {
			t.Errorf("app role ran %q: %v", stmt, err)
		}
	}
	// Inbox partitions: the workloads created 2026-10 and 2026-11. On 2026-12-31 October ended more than 45 days
	// ago (Nov 1 + 1 day of slack + 45 days = Dec 17) and November has not (Jan 16).
	now := time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)
	dropped, err := maintenance.Once(ctx, shared.system, now)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(dropped, "processed_events_2026_10") || contains(dropped, "processed_events_2026_11") {
		t.Fatalf("dropped = %v: want October gone and November kept", dropped)
	}
	if again, err := maintenance.Once(ctx, shared.system, now); err != nil || len(again) != 0 {
		t.Fatalf("second run: %v %v", again, err)
	}
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
