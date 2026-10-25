//go:build integration

package itest

import (
	"context"
	"fmt"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	aggregatorv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/aggregator/v1"
	eventsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/events/v1"
	"github.com/0furkancolak/payrudder/pkg/usage"
	"github.com/0furkancolak/payrudder/services/aggregator/internal/apply"
	"github.com/0furkancolak/payrudder/services/aggregator/internal/replay"
)

// switchMeters serves sum_m with a definition that can be swapped at run time (the "fix"); other codes come from defaultMeters.
type switchMeters struct {
	mu sync.Mutex
	m  usage.Meter
}

func (s *switchMeters) set(m usage.Meter) { s.mu.Lock(); s.m = m; s.mu.Unlock() }

func (s *switchMeters) Get(ctx context.Context, tn uuid.UUID, code string) (usage.Meter, error) {
	if code == "sum_m" {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.m, nil
	}
	return defaultMeters.Get(ctx, tn, code)
}

func newReplayManager(r *run) *replay.Manager {
	return replay.New(replay.Config{
		Pool: shared.system, Brokers: []string{shared.kafka.Brokers}, Topic: r.topic, LiveGroup: r.group,
		Meters: r.handler.Meters, Settings: r.src, Scan: 100 * time.Millisecond, BatchSize: 500,
	})
}

// gen is a tenant's active generation and the row counts per generation.
func activeGeneration(t *testing.T, tn uuid.UUID) int32 {
	t.Helper()
	var g int32
	if err := shared.system.QueryRow(context.Background(), `SELECT COALESCE((SELECT generation FROM aggregate_generations WHERE tenant_id = $1 AND status = 'active'), 1)`, tn).Scan(&g); err != nil {
		t.Fatal(err)
	}
	return g
}

func rowsOfGeneration(t *testing.T, gen int32, tenants ...uuid.UUID) map[rowKey]totals {
	t.Helper()
	rows, err := shared.system.Query(context.Background(),
		`SELECT tenant_id, customer_id, meter_code, period_id, quantity::text, event_count, quantity_scale FROM usage_aggregates WHERE tenant_id = ANY($1) AND generation = $2`, tenants, gen)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[rowKey]totals{}
	for rows.Next() {
		var k rowKey
		var tt totals
		if err := rows.Scan(&k.tenant, &k.customer, &k.meter, &k.period, &tt.qty, &tt.count, &tt.scale); err != nil {
			t.Fatal(err)
		}
		out[k] = tt
	}
	return out
}

func waitJob(t *testing.T, m *replay.Manager, tn, id uuid.UUID, want string, within time.Duration) *replay.Job {
	t.Helper()
	deadline := time.Now().Add(within)
	var j *replay.Job
	for time.Now().Before(deadline) {
		var err error
		if j, err = m.Get(context.Background(), tn, id); err != nil {
			t.Fatal(err)
		}
		if j.Status == want {
			return j
		}
		if j.Status == replay.Failed {
			t.Fatalf("replay failed: %s", j.LastError)
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("job still %s after %s (want %s)", j.Status, within, want)
	return nil
}

// A meter filter that wrongly dropped the "us" events is fixed, the tenant is replayed and promoted while live traffic flows
// (for the tenant and for a bystander). The promoted generation equals the corrected ledger of every event, none lost, none
// counted twice; the retired one is untouched; the bystander never leaves generation 1.
func testReplayFixesAFilterAndPromotesUnderLoad(t *testing.T) {
	buggy := usage.Meter{Code: "sum_m", Aggregation: usage.Sum, Filter: map[string][]string{"region": {"eu"}}}
	fixed := usage.Meter{Code: "sum_m", Aggregation: usage.Sum}
	ms := &switchMeters{m: buggy}
	r := newRun(t, runOpts{meters: ms})
	r.start(r.group)
	mgr := newReplayManager(r)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = mgr.Run(ctx) }()

	a, b := uuid.New(), uuid.New()
	custA, custB := make([]uuid.UUID, 8), make([]uuid.UUID, 8)
	for i := range custA {
		custA[i], custB[i] = uuid.New(), uuid.New()
	}
	led := newLedger()
	var seq atomic.Int64
	rng := rand.New(rand.NewPCG(7, 8))
	var rngMu sync.Mutex
	mk := func(tn uuid.UUID, cust []uuid.UUID, meter string, countedUnderBug bool) *eventsv1.UsageEvent {
		rngMu.Lock()
		defer rngMu.Unlock()
		c := cust[rng.IntN(len(cust))]
		region := "eu"
		if rng.IntN(2) == 0 {
			region = "us"
		}
		units := int64(1 + rng.IntN(100))
		e := ev(fmt.Sprintf("RP%08d", seq.Add(1)), tn, c, meter, units, 0, "2026-11", nov)
		e.Properties = map[string]string{"region": region}
		return e
	}
	// expected: every event of A is counted once the filter is fixed; B's meter has no filter.
	addExpect := func(e *eventsv1.UsageEvent, agg usage.Aggregation) {
		k := rowKey{tenant: uuid.MustParse(e.TenantId), customer: uuid.MustParse(e.CustomerId), meter: e.MeterCode, period: "2026-11"}
		led.add(led.onTime, k, agg, e.QuantityUnits)
	}

	// History, produced under the bug.
	var history []*eventsv1.UsageEvent
	for i := 0; i < 4000; i++ {
		e := mk(a, custA, "sum_m", false)
		history = append(history, e)
		addExpect(e, usage.Sum)
		eb := mk(b, custB, "count_m", true)
		history = append(history, eb)
		addExpect(eb, usage.Count)
	}
	r.produce(history...)
	r.drain(60 * time.Second)
	buggyRows := rowsOfGeneration(t, 1, a)
	var buggyCount int64
	for _, v := range buggyRows {
		buggyCount += v.count
	}
	if buggyCount == 0 || buggyCount >= 4000 {
		t.Fatalf("under the bug %d of 4000 events were counted, want some but not all", buggyCount)
	}

	// The fix, then the replay, with live traffic flowing for both tenants.
	ms.set(fixed)
	job, err := mgr.Start(context.Background(), a, "2026-11", "2026-11", "meter filter forgot the us region")
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != replay.Running || job.SourceGeneration != 1 || job.TargetGeneration != 2 {
		t.Fatalf("job = %+v, want running generation 1 -> 2", job)
	}
	if _, err := mgr.Start(context.Background(), a, "2026-11", "2026-11", "again"); err != replay.ErrOpen {
		t.Fatalf("a second open replay: %v, want ErrOpen", err)
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	var liveMu sync.Mutex
	produceLive := func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			var batch []*eventsv1.UsageEvent
			for i := 0; i < 20; i++ {
				ea := mk(a, custA, "sum_m", false)
				eb := mk(b, custB, "count_m", true)
				batch = append(batch, ea, eb)
				liveMu.Lock()
				addExpect(ea, usage.Sum)
				addExpect(eb, usage.Count)
				liveMu.Unlock()
			}
			r.produce(batch...)
			time.Sleep(5 * time.Millisecond)
		}
	}
	wg.Add(1)
	go produceLive()

	ready := waitJob(t, mgr, a, job.ID, replay.Ready, 2*time.Minute)
	if ready.DualWriteFrom == nil {
		t.Fatal("a ready job must have recorded where dual writing began")
	}
	v := ready.Verification
	if v == nil || v.RowsChanged == 0 || v.RowsOnlyInOld != 0 {
		t.Fatalf("verification = %+v, want changed rows (the us events) and nothing only in the old generation", v)
	}
	// Promote with the traffic still running.
	time.Sleep(100 * time.Millisecond)
	if _, err := mgr.Promote(context.Background(), a, job.ID); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond) // some live batches after the flip too
	close(stop)
	wg.Wait()
	r.drain(60 * time.Second)

	if g := activeGeneration(t, a); g != 2 {
		t.Fatalf("A's active generation = %d, want 2", g)
	}
	if g := activeGeneration(t, b); g != 1 {
		t.Fatalf("the bystander's generation = %d, want 1", g)
	}
	var bRows int
	if err := shared.system.QueryRow(context.Background(), `SELECT count(*) FROM aggregate_generations WHERE tenant_id = $1`, b).Scan(&bRows); err != nil || bRows != 0 {
		t.Fatalf("the bystander has %d generation rows (%v), want none", bRows, err)
	}
	// Exactness: generation 2 of A, and generation 1 of B, equal the ledger of every event.
	wantA, wantB := newLedger(), newLedger()
	for k, e := range led.onTime {
		if k.tenant == a {
			wantA.onTime[k] = e
		} else {
			wantB.onTime[k] = e
		}
	}
	defer func() {
		if !t.Failed() {
			return
		}
		// Diagnose: which events are in generation 1 but not in generation 2, and where are they in the log?
		rows, err := shared.system.Query(context.Background(), `SELECT p1.event_id FROM processed_events p1 WHERE p1.tenant_id = $1 AND p1.generation = 1
			AND NOT EXISTS (SELECT 1 FROM processed_events p2 WHERE p2.tenant_id = p1.tenant_id AND p2.event_id = p1.event_id AND p2.generation = 2)`, a)
		if err != nil {
			t.Log(err)
			return
		}
		missing := map[string]bool{}
		for rows.Next() {
			var id string
			_ = rows.Scan(&id)
			missing[id] = true
		}
		rows.Close()
		t.Logf("%d events of A are in generation 1 and not in generation 2; job offsets: start %v end %v dual %v", len(missing), ready.StartOffsets, ready.EndOffsets, ready.DualWriteFrom)
		shown := 0
		for _, rec := range consumeTopic(t, r.topic, 3*time.Second) {
			var e eventsv1.UsageEvent
			if proto.Unmarshal(rec.Value, &e) == nil && missing[e.EventId] && shown < 10 {
				t.Logf("missing %s at partition %d offset %d", e.EventId, rec.Partition, rec.Offset)
				shown++
			}
		}
	}()
	compare(t, "A generation 2", wantA.onTime, rowsOfGeneration(t, 2, a))
	compare(t, "B generation 1", wantB.onTime, rowsOfGeneration(t, 1, b))

	// A billing run pinned to generation 1 reads identical totals before and after: the retired generation no longer moves.
	frozenOld := rowsOfGeneration(t, 1, a)
	r.produce(ev("RPAFTER000001", a, custA[0], "sum_m", 1, 0, "2026-11", nov))
	r.drain(30 * time.Second)
	if after := rowsOfGeneration(t, 1, a); fmt.Sprint(after) != fmt.Sprint(frozenOld) {
		t.Fatal("the retired generation changed after the promotion")
	}
	if got := rowsOfGeneration(t, 2, a)[rowKey{tenant: a, customer: custA[0], meter: "sum_m", period: "2026-11"}]; got.count == 0 {
		t.Fatal("new events must land in the promoted generation")
	}
	var statuses []string
	rows, err := shared.system.Query(context.Background(), `SELECT status FROM aggregate_generations WHERE tenant_id = $1 ORDER BY generation`, a)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var s string
		_ = rows.Scan(&s)
		statuses = append(statuses, s)
	}
	rows.Close()
	if fmt.Sprint(statuses) != "[retired active]" {
		t.Fatalf("generation statuses = %v, want [retired active]", statuses)
	}
	_ = aggregatorv1.ReplayStatus_REPLAY_STATUS_READY
	_ = apply.ErrMeterNotFound
}

// A discarded replay leaves the live generation and its rows alone and deletes what it built; another replay can follow.
func testReplayDiscard(t *testing.T) {
	r := newRun(t, runOpts{})
	r.start(r.group)
	mgr := newReplayManager(r)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = mgr.Run(ctx) }()
	tn, cust := uuid.New(), uuid.New()
	var evs []*eventsv1.UsageEvent
	for i := 0; i < 300; i++ {
		evs = append(evs, ev(fmt.Sprintf("DS%08d", i), tn, cust, "sum_m", 2, 0, "2026-11", nov))
	}
	r.produce(evs...)
	r.drain(30 * time.Second)

	job, err := mgr.Start(context.Background(), tn, "2026-11", "2026-11", "test")
	if err != nil {
		t.Fatal(err)
	}
	waitJob(t, mgr, tn, job.ID, replay.Ready, time.Minute)
	if got := rowsOfGeneration(t, 2, tn)[rowKey{tenant: tn, customer: cust, meter: "sum_m", period: "2026-11"}]; got.qty != "600" {
		t.Fatalf("generation 2 = %+v, want 600", got)
	}
	d, err := mgr.Discard(context.Background(), tn, job.ID)
	if err != nil || d.Status != replay.Discarded {
		t.Fatalf("discard = %+v, %v", d, err)
	}
	if n := len(rowsOfGeneration(t, 2, tn)); n != 0 {
		t.Fatalf("%d rows of the discarded generation remain", n)
	}
	if got := rowsOfGeneration(t, 1, tn)[rowKey{tenant: tn, customer: cust, meter: "sum_m", period: "2026-11"}]; got.qty != "600" {
		t.Fatalf("the live generation = %+v, want 600", got)
	}
	// Discarding twice is a no-op, a promoted job cannot be discarded, and a ready job cannot be promoted after a discard.
	if _, err := mgr.Discard(context.Background(), tn, job.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.Promote(context.Background(), tn, job.ID); err == nil {
		t.Fatal("a discarded job was promoted")
	}
	// And a new replay is allowed again.
	if _, err := mgr.Start(context.Background(), tn, "2026-11", "2026-11", "second try"); err != nil {
		t.Fatalf("a new replay after a discard: %v", err)
	}
}

// A replay of one period must not lose the history of the periods outside its range: the live consumer dual-writes every
// period into the catching-up generation, so promoting it has to carry the rest of the tenant's history over.
func testReplayPromoteKeepsPeriodsOutsideTheRange(t *testing.T) {
	r := newRun(t, runOpts{})
	r.start(r.group)
	mgr := newReplayManager(r)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = mgr.Run(ctx) }()
	tn, cust := uuid.New(), uuid.New()
	oct := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	var evs []*eventsv1.UsageEvent
	for i := 0; i < 100; i++ {
		evs = append(evs, ev(fmt.Sprintf("OR%08d", i), tn, cust, "sum_m", 2, 0, "2026-10", oct), ev(fmt.Sprintf("NV%08d", i), tn, cust, "sum_m", 3, 0, "2026-11", nov))
	}
	r.produce(evs...)
	r.drain(30 * time.Second)

	job, err := mgr.Start(context.Background(), tn, "2026-11", "2026-11", "only november")
	if err != nil {
		t.Fatal(err)
	}
	waitJob(t, mgr, tn, job.ID, replay.Ready, time.Minute)
	// October events arrive while the new generation catches up: they are written to both generations.
	var live []*eventsv1.UsageEvent
	for i := 0; i < 10; i++ {
		live = append(live, ev(fmt.Sprintf("OL%08d", i), tn, cust, "sum_m", 5, 0, "2026-10", oct))
	}
	r.produce(live...)
	r.drain(30 * time.Second)
	if _, err := mgr.Promote(context.Background(), tn, job.ID); err != nil {
		t.Fatal(err)
	}
	key := func(p string) rowKey { return rowKey{tenant: tn, customer: cust, meter: "sum_m", period: p} }
	got := rowsOfGeneration(t, 2, tn)
	if g := got[key("2026-10")]; g.qty != "250" || g.count != 110 {
		t.Fatalf("2026-10 in the promoted generation = %+v, want qty 250 over 110 events (the whole history)", g)
	}
	if g := got[key("2026-11")]; g.qty != "300" || g.count != 100 {
		t.Fatalf("2026-11 in the promoted generation = %+v, want qty 300 over 100 events", g)
	}
	// A redelivered October event is still recognised in the promoted generation.
	r.produce(evs[0])
	r.drain(30 * time.Second)
	if g := rowsOfGeneration(t, 2, tn)[key("2026-10")]; g.count != 110 {
		t.Fatalf("a redelivered old event was counted again: %+v", g)
	}
}
