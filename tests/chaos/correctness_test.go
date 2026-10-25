//go:build chaos

package chaos

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"math/rand/v2"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"
)

const (
	// duplicateShare is the percentage of the events a second sender sends again, concurrently (same key, same payload).
	duplicateShare = 5
	// sendDeadline bounds how long a batch may keep retrying.
	sendDeadline = 25 * time.Minute
	// drainSettle is how long the stack sits after the drain before it is re-read, to catch an overcount.
	drainSettle = 5 * time.Second
	// drainPoll is the interval of the drain check.
	drainPoll = 500 * time.Millisecond
)

// finalAnswer is the last answer the API gave for one idempotency key.
type finalAnswer struct {
	accepted bool // accepted or duplicate: the event counts
	id       string
}

// correctnessRun carries the state the phases of TestCorrectness share.
type correctnessRun struct {
	t       *testing.T
	a       api
	seed    uint64
	started time.Time
	sentAt  time.Time

	fx     []*tenantFx
	custID map[string]string
	events []genEvent
	dups   []genEvent

	mu       sync.Mutex
	finals   map[string]finalAnswer // idempotency key -> final result
	rep      report
	deadline time.Time

	// The ledger: what the aggregates must hold, built from the final answers.
	onTime, late                   map[string]*cell
	nowPeriod                      string
	expected, expectedOnTimeEvents int64
	tenantIDs                      []string
	aggregatorConn, ingestConn     *pgx.Conn
	monkey                         *Monkey
}

func TestCorrectness(t *testing.T) {
	r := newCorrectnessRun(t)
	r.sendWorkload()
	r.stopMonkey()
	if t.Failed() {
		return
	}
	r.buildLedger()
	r.connect()
	defer r.close()
	r.drain()
	r.verify()
	r.finish()
}

func newCorrectnessRun(t *testing.T) *correctnessRun {
	n, _ := strconv.Atoi(envOr("PAYRUDDER_CHAOS_EVENTS", "100000"))
	seed, _ := strconv.ParseUint(envOr("PAYRUDDER_CHAOS_SEED", "42"), 10, 64)
	r := &correctnessRun{t: t, a: api{t}, seed: seed, started: time.Now(), finals: map[string]finalAnswer{}}
	r.fx, r.custID = setup(t)
	tag := strconv.FormatInt(r.started.UnixNano(), 36)
	r.events = generate(seed, n, r.fx, tag)

	rng := rand.New(rand.NewPCG(seed+1, seed))
	for _, e := range r.events {
		if rng.IntN(100) < duplicateShare {
			r.dups = append(r.dups, e)
		}
	}
	r.rep.Events, r.rep.Duplicates = n, len(r.dups)
	r.deadline = time.Now().Add(sendDeadline)
	r.tenantIDs = make([]string, len(r.fx))
	for i, f := range r.fx {
		r.tenantIDs[i] = f.id
	}
	return r
}

// sendWorkload sends the events with eight workers and the duplicates concurrently with two, while the monkey (if asked for) injects faults.
func (r *correctnessRun) sendWorkload() {
	if envOr("PAYRUDDER_CHAOS_MONKEY", "0") == "1" {
		expected := max(2*time.Minute, time.Duration(len(r.events)/400)*time.Second)
		r.monkey = StartMonkey(context.Background(), r.seed, expected)
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); r.sendAll(r.events, 8) }()
	go func() { defer wg.Done(); r.sendAll(r.dups, 2) }()
	wg.Wait()
	r.sentAt = time.Now()
}

// sendAll sends list in random batches of up to 500 events, split per tenant because a request carries one tenant's key.
func (r *correctnessRun) sendAll(list []genEvent, workers int) {
	var next atomic.Int64
	var wg sync.WaitGroup
	var batches [][]genEvent
	brng := rand.New(rand.NewPCG(r.seed+uint64(len(list)), 3))
	for i := 0; i < len(list); {
		size := 1 + brng.IntN(500)
		end := min(i+size, len(list))
		batches = append(batches, list[i:end])
		i = end
	}
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				bi := int(next.Add(1)) - 1
				if bi >= len(batches) {
					return
				}
				byTenant := map[int][]genEvent{}
				for _, e := range batches[bi] {
					byTenant[e.tenant] = append(byTenant[e.tenant], e)
				}
				for tn, evs := range byTenant {
					throttle(len(evs))
					res, retries := send(r.a, r.fx[tn].key, evs, r.deadline)
					atomic.AddInt64(&r.rep.Retries, int64(retries))
					if res == nil {
						r.t.Errorf("batch for tenant %d never got through", tn)
						return
					}
					r.record(evs, res)
				}
			}
		}()
	}
	wg.Wait()
}

// record folds the answers to one batch into the final answers and the counters.
func (r *correctnessRun) record(evs []genEvent, res []result) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, x := range res {
		e := evs[i]
		switch x.Status {
		case "accepted":
			atomic.AddInt64(&r.rep.Accepted, 1)
			r.finals[e.key] = finalAnswer{true, x.EventID}
		case "duplicate":
			atomic.AddInt64(&r.rep.DuplicateResponses, 1)
			if f, ok := r.finals[e.key]; !ok || !f.accepted {
				r.finals[e.key] = finalAnswer{true, x.EventID}
			} else if f.id != x.EventID {
				r.t.Errorf("key %s: duplicate reported event id %s, accepted as %s", e.key, x.EventID, f.id)
			}
		default:
			atomic.AddInt64(&r.rep.Rejected, 1)
			if !e.invalid {
				r.t.Errorf("valid event %s rejected: %s", e.key, x.ErrorCode)
			} else if x.ErrorCode != "UNKNOWN_METER" {
				r.t.Errorf("invalid event %s rejected with %s, want UNKNOWN_METER", e.key, x.ErrorCode)
			}
		}
	}
}

// stopMonkey ends the faults once the workload is in, so the stack can drain.
func (r *correctnessRun) stopMonkey() {
	if r.monkey == nil {
		return
	}
	// A fast stack finishes the workload before the schedule has injected the required chaos: keep the faults coming (faster) while the
	// backlog drains, up to PAYRUDDER_CHAOS_TOPUP_MINUTES, so the requirement is about the chaos and not about the runner's speed.
	r.monkey.Meet(envInt("PAYRUDDER_CHAOS_MIN_KILLS", 0), envInt("PAYRUDDER_CHAOS_MIN_FAULTS", 0), time.Duration(envInt("PAYRUDDER_CHAOS_TOPUP_MINUTES", 8))*time.Minute)
	r.rep.Chaos = r.monkey.Stop()
	for _, e := range r.rep.Chaos.Errors {
		r.t.Logf("chaos: %s", e)
	}
}

// buildLedger computes what the aggregates must hold: every event whose final answer was accepted or duplicate, exactly once.
func (r *correctnessRun) buildLedger() {
	r.onTime, r.late = map[string]*cell{}, map[string]*cell{}
	r.nowPeriod = monthID(time.Now())
	for _, e := range r.events {
		f, ok := r.finals[e.key]
		if e.invalid {
			r.rep.Invalid++
			if ok {
				r.t.Errorf("invalid event %s was accepted", e.key)
			}
			continue
		}
		if !ok || !f.accepted {
			r.t.Errorf("event %s has no accepted/duplicate answer", e.key)
			continue
		}
		r.rep.Unique++
		r.expected++
		tn := r.fx[e.tenant]
		m := r.onTime
		key := fmt.Sprintf("%s/%s/%s/%s", tn.id, tn.id+"/"+e.customer, e.meter, monthID(e.at))
		if e.late {
			m, key = r.late, fmt.Sprintf("%s/%s/%s/%s|%s", tn.id, tn.id+"/"+e.customer, e.meter, r.nowPeriod, monthID(e.at))
		} else {
			r.expectedOnTimeEvents++
		}
		c := m[key]
		if c == nil {
			c = &cell{qty: big.NewInt(0), max: meterByCode(e.meter).aggregation == "AGGREGATION_MAX"}
			m[key] = c
		}
		c.add(e.units, meterByCode(e.meter).aggregation == "AGGREGATION_COUNT")
	}
	r.rep.LedgerRowsOnTime, r.rep.LedgerRowsLate = len(r.onTime), len(r.late)
	r.rep.Tenants, r.rep.Customers, r.rep.Meters = len(r.fx), len(r.fx)*numCustomers, len(meterSet)
}

func (r *correctnessRun) connect() {
	var err error
	if r.aggregatorConn, err = pgx.Connect(context.Background(), aggregatorDSN); err != nil {
		r.t.Fatalf("connect aggregator_db: %v", err)
	}
	if r.ingestConn, err = pgx.Connect(context.Background(), ingestDSN); err != nil {
		r.t.Fatalf("connect ingest_db: %v", err)
	}
}

func (r *correctnessRun) close() {
	if r.aggregatorConn != nil {
		_ = r.aggregatorConn.Close(context.Background())
	}
	if r.ingestConn != nil {
		_ = r.ingestConn.Close(context.Background())
	}
}

// counted is the number of events the aggregator has applied for the suite's tenants.
func (r *correctnessRun) counted() int64 {
	var c int64
	err := r.aggregatorConn.QueryRow(context.Background(), `SELECT
			COALESCE((SELECT sum(event_count) FROM usage_aggregates WHERE tenant_id::text = ANY($1)), 0) +
			COALESCE((SELECT sum(event_count) FROM late_usage WHERE tenant_id::text = ANY($1)), 0)`, r.tenantIDs).Scan(&c)
	if err != nil {
		r.t.Fatal(err)
	}
	return c
}

func (r *correctnessRun) outboxRows() int64 {
	var c int64
	if err := r.ingestConn.QueryRow(context.Background(), `SELECT count(*) FROM outbox`).Scan(&c); err != nil {
		r.t.Fatal(err)
	}
	return c
}

// drain waits for ingest's outbox to empty and the aggregator to apply every event. Then it sits for a few seconds so a later read
// catches any overcount.
func (r *correctnessRun) drain() {
	drainBy := time.Now().Add(time.Duration(envInt("PAYRUDDER_CHAOS_DRAIN_MINUTES", 40)) * time.Minute)
	for (r.counted() < r.expected || r.outboxRows() > 0) && time.Now().Before(drainBy) {
		time.Sleep(drainPoll)
	}
	time.Sleep(drainSettle)
	r.rep.DrainSeconds = time.Since(r.sentAt).Seconds()
}

// verify checks A1 to A9 against the ledger.
func (r *correctnessRun) verify() {
	r.checkAggregates()
	r.checkDownstream()
}

// checkAggregates is A1 and A2 (every key, exactly) and A3 (the totals).
func (r *correctnessRun) checkAggregates() {
	gotOn, gotLate := map[string]*cell{}, map[string]*cell{}
	r.readAggregates(`SELECT tenant_id::text, customer_id::text, meter_code, period_id, quantity::text, event_count FROM usage_aggregates WHERE tenant_id::text = ANY($1) AND generation = 1`, gotOn, false)
	r.readAggregates(`SELECT tenant_id::text, customer_id::text, meter_code, billed_period_id, origin_period_id, quantity::text, event_count FROM late_usage WHERE tenant_id::text = ANY($1) AND generation = 1`, gotLate, true)
	r.rep.assert("A1", "usage_aggregates == ledger (on-time)", diffCells("A1 usage_aggregates", r.onTime, gotOn), "")
	r.rep.assert("A2", "late_usage == ledger (late)", diffCells("A2 late_usage", r.late, gotLate), "")
	var a3 []string
	if c := r.counted(); c != r.expected {
		a3 = append(a3, fmt.Sprintf("A3 events counted = %d, accepted = %d", c, r.expected))
	}
	r.rep.assert("A3", "accepted events == aggregated events", a3, "")
}

func (r *correctnessRun) readAggregates(q string, dst map[string]*cell, late bool) {
	rows, err := r.aggregatorConn.Query(context.Background(), q, r.tenantIDs)
	if err != nil {
		r.t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var tn, cust, meter, p, origin, qty string
		var count int64
		if late {
			err = rows.Scan(&tn, &cust, &meter, &p, &origin, &qty, &count)
			p = p + "|" + origin
		} else {
			err = rows.Scan(&tn, &cust, &meter, &p, &qty, &count)
		}
		if err != nil {
			r.t.Fatal(err)
		}
		q, _ := new(big.Int).SetString(qty, 10)
		dst[fmt.Sprintf("%s/%s/%s/%s", tn, r.custID[cust], meter, p)] = &cell{qty: q, count: count}
	}
}

// diffCells lists the keys on which the two maps disagree, in key order.
func diffCells(what string, want, got map[string]*cell) []string {
	keys := map[string]bool{}
	for k := range want {
		keys[k] = true
	}
	for k := range got {
		keys[k] = true
	}
	var sorted []string
	for k := range keys {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)
	var out []string
	for _, k := range sorted {
		w, g := want[k], got[k]
		switch {
		case w == nil:
			out = append(out, fmt.Sprintf("%s %s: unexpected row qty=%s count=%d", what, k, g.qty, g.count))
		case g == nil:
			out = append(out, fmt.Sprintf("%s %s: missing, want qty=%s count=%d", what, k, w.qty, w.count))
		case w.qty.Cmp(g.qty) != 0 || w.count != g.count:
			out = append(out, fmt.Sprintf("%s %s: qty %s count %d, want qty %s count %d", what, k, g.qty, g.count, w.qty, w.count))
		}
	}
	return out
}

// checkDownstream is A4 to A9: limits, invoices (covered elsewhere), the frozen-period guard, ClickHouse and the dead-letter topics.
func (r *correctnessRun) checkDownstream() {
	// A4: limits-svc's counters equal the aggregator's on-time totals once the reconcile loop has run. The counters were
	// wiped when the monkey restarted Redis, so this is also the proof that a lost Redis rebuilds itself.
	a4, a4Skip := checkLimits(r.t, r.fx, r.onTime, r.nowPeriod, r.expectedOnTimeEvents > 0)
	r.rep.assert("A4", "limits counters == aggregator (on-time)", a4, a4Skip)

	// A5/A6 (invoices, charges) are covered by the month-close scenario (billing_chaos_test.go): live tenants here never
	// close a period.
	r.rep.Assertions = append(r.rep.Assertions,
		Assertion{ID: "A5", Name: "invoice lines == aggregates", Status: "skipped", Detail: "covered by TestMonthCloseSurvivesBrokerRestart"},
		Assertion{ID: "A6", Name: "at most one successful charge per invoice attempt", Status: "skipped", Detail: "covered by TestMonthCloseSurvivesBrokerRestart"})

	// A7: no frozen-period violation (needs Prometheus; chaos tenants never freeze a period, so this guards regressions).
	a7, a7Skip := checkFrozenViolations()
	r.rep.assert("A7", "aggregator_frozen_period_violations_total == 0", a7, a7Skip)

	// A8: ClickHouse (eventually) holds exactly the accepted events.
	a8, a8Skip := checkClickHouse(r.tenantIDs, r.expected)
	r.rep.assert("A8", "ClickHouse usage_events FINAL == accepted events", a8, a8Skip)

	r.rep.assert("A9", "dead-letter topics empty", r.checkDeadLetters(), "")
}

// checkDeadLetters is A9: nothing reached a dead-letter topic.
func (r *correctnessRun) checkDeadLetters() []string {
	var a9 []string
	cl, err := kgo.NewClient(kgo.SeedBrokers(strings.Split(kafkaBrokers, ",")...))
	if err != nil {
		return nil
	}
	defer cl.Close()
	for _, topic := range []string{"usage.events.dlq.v1", "usage.limit-exceeded.dlq.v1", "billing.invoices.dlq.v1"} {
		var n int64
		if ends, err := kadm.NewClient(cl).ListEndOffsets(context.Background(), topic); err == nil {
			ends.Each(func(o kadm.ListedOffset) { n += o.Offset })
		}
		r.rep.DLQRecords += n
		if n != 0 {
			a9 = append(a9, fmt.Sprintf("A9 %s has %d records", topic, n))
		}
	}
	return a9
}

// finish checks the chaos minimums, writes the report and fails the test on any mismatch.
func (r *correctnessRun) finish() {
	t, rep := r.t, &r.rep
	if v, ok := promValue(`sum(aggregator_duplicates_skipped_total)`); ok {
		rep.AggregatorDuplicatesSkipped = &v
	}
	if min := envInt("PAYRUDDER_CHAOS_MIN_KILLS", 0); rep.Chaos.Kills < min {
		rep.Mismatches = append(rep.Mismatches, fmt.Sprintf("chaos: %d container kills injected, at least %d required", rep.Chaos.Kills, min))
	}
	if min := envInt("PAYRUDDER_CHAOS_MIN_FAULTS", 0); rep.Chaos.Faults < min {
		rep.Mismatches = append(rep.Mismatches, fmt.Sprintf("chaos: %d network faults injected, at least %d required", rep.Chaos.Faults, min))
	}

	rep.Seconds = time.Since(r.started).Seconds()
	b, _ := json.MarshalIndent(rep, "", "  ")
	_ = os.WriteFile(envOr("PAYRUDDER_CHAOS_REPORT", "chaos-report.json"), b, 0o644)
	t.Logf("%s", b)
	if len(rep.Mismatches) > 0 {
		for i, m := range rep.Mismatches {
			if i >= 20 {
				break
			}
			t.Error(m)
		}
		t.Fatalf("%d mismatches: the suite tolerates none", len(rep.Mismatches))
	}
}
