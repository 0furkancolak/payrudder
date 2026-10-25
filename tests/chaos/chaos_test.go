//go:build chaos

// Package chaos is the correctness suite (docs/09-testing-strategy.md): it drives a deterministic workload through the
// public API of the running compose stack while a chaos monkey kills replicas, breaks connections and stops brokers, and
// asserts that every accepted event is counted exactly once (A1-A9, no tolerance).
//
//	make chaos N=100000
package chaos

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"math/rand/v2"
	"net/http"
	"net/url"
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

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func envInt(k string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(k)); err == nil {
		return v
	}
	return def
}

var (
	pgPort        = envOr("QUOTIFY_PG_HOST_PORT", "42432")
	gateway       = envOr("QUOTIFY_GATEWAY_URL", "http://localhost:42800")
	operatorToken = envOr("QUOTIFY_OPERATOR_TOKEN", "local-operator-token-change-me-0123456789")
	kafkaBrokers  = envOr("QUOTIFY_KAFKA_BROKERS", "localhost:42092")
	aggregatorDSN = envOr("QUOTIFY_AGGREGATOR_DSN", fmt.Sprintf("postgres://aggregator_system:aggregator_system_local@localhost:%s/aggregator_db", pgPort))
	ingestDSN     = envOr("QUOTIFY_INGEST_DSN", fmt.Sprintf("postgres://ingest_system:ingest_system_local@localhost:%s/ingest_db", pgPort))
	clickhouseURL = envOr("QUOTIFY_CLICKHOUSE_URL", "http://localhost:42123")
	toxiproxyURL  = envOr("QUOTIFY_TOXIPROXY_URL", "http://localhost:42474")
	prometheusURL = envOr("QUOTIFY_PROMETHEUS_URL", "")
	httpc         = &http.Client{Timeout: 30 * time.Second}

	numTenants   = envInt("QUOTIFY_CHAOS_TENANTS", 10)
	numCustomers = envInt("QUOTIFY_CHAOS_CUSTOMERS", 20)
)

// meterSpec is one meter every tenant gets: a mix of SUM, COUNT and MAX at different scales.
type meterSpec struct {
	code, aggregation string
	scale             int
	maxUnits          int
}

var meterSet = []meterSpec{
	{"m_sum", "AGGREGATION_SUM", 3, 100000},
	{"m_count", "AGGREGATION_COUNT", 0, 1},
	{"m_max", "AGGREGATION_MAX", 0, 1000},
	{"m_sum_i", "AGGREGATION_SUM", 0, 1000},
	{"m_max_d", "AGGREGATION_MAX", 2, 100000},
}

func meterByCode(code string) meterSpec {
	for _, m := range meterSet {
		if m.code == code {
			return m
		}
	}
	return meterSpec{}
}

type tenantFx struct {
	id        string
	key       string
	customers []string // external ids
}

type api struct{ t *testing.T }

func (a api) call(method, path, token string, body any, out any) int {
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, gateway+path, r)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := httpc.Do(req)
	if err != nil {
		return -1
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	if out != nil {
		_ = json.Unmarshal(raw, out)
	}
	if resp.StatusCode == 429 {
		if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil {
			time.Sleep(time.Duration(s) * time.Second)
		}
	}
	return resp.StatusCode
}

func (a api) must(method, path, token string, body any, out any, want int) {
	a.t.Helper()
	if got := a.call(method, path, token, body, out); got != want {
		a.t.Fatalf("%s %s: %d, want %d", method, path, got, want)
	}
}

// setup creates the tenants, meters and customers. Grace is 0, so an event of a past month is late the moment it
// arrives, with no clock games.
func setup(t *testing.T) ([]*tenantFx, map[string]string) {
	a := api{t}
	custID := map[string]string{} // internal customer id -> tenantID/external id
	var fx []*tenantFx
	run := strconv.FormatInt(time.Now().UnixNano(), 36)
	for i := 0; i < numTenants; i++ {
		var acct struct {
			Tenants []struct {
				ID          string `json:"id"`
				Environment string `json:"environment"`
			} `json:"tenants"`
		}
		a.must("POST", "/admin/v1/accounts", operatorToken, map[string]any{
			"name": fmt.Sprintf("chaos-%s-%d", run, i), "currency": "TRY", "timezone": "Europe/Istanbul",
			"grace_period": "0s", "tier": "TENANT_TIER_ENTERPRISE"}, &acct, 201)
		f := &tenantFx{}
		for _, tn := range acct.Tenants {
			if tn.Environment == "ENVIRONMENT_LIVE" {
				f.id = tn.ID
			}
		}
		var key struct {
			Plaintext string `json:"plaintext"`
		}
		// A live tenant is created inactive and has no keys: activate it the way support does, with a reason.
		a.must("PUT", "/admin/v1/tenants/"+f.id+"/status", operatorToken, map[string]string{"status": "active", "reason": "chaos"}, nil, 200)
		a.must("POST", "/admin/v1/tenants/"+f.id+"/api-keys", operatorToken, nil, &key, 201)
		f.key = key.Plaintext
		var entitlements []any
		for _, m := range meterSet {
			body := map[string]any{"code": m.code, "aggregation": m.aggregation, "quantity_scale": m.scale, "unit_label": "u"}
			if m.aggregation != "AGGREGATION_COUNT" {
				body["value_property"] = "v"
			}
			a.must("POST", "/v1/meters", f.key, body, nil, 201)
			entitlements = append(entitlements, map[string]any{"meter_code": m.code, "limit": "1000000000", "alert_thresholds_pct": []int{100}})
		}
		// A plan with a huge limit per meter, so every customer has a limits-svc counter to compare (A4).
		a.must("POST", "/v1/plans", f.key, map[string]any{"code": "chaos", "name": "Chaos"}, nil, 201)
		a.must("POST", "/v1/plans/chaos/versions", f.key, map[string]any{"base_fee_minor": 0, "entitlements": entitlements,
			"prices": []any{map[string]any{"meter_code": "m_sum", "per_unit": map[string]any{"unit_price": "1"}}}}, nil, 201)
		for c := 0; c < numCustomers; c++ {
			ext := fmt.Sprintf("cus_%d", c)
			var cust struct {
				ID string `json:"id"`
			}
			a.must("POST", "/v1/customers", f.key, map[string]any{"external_id": ext, "name": ext, "email": ext + "@example.com"}, &cust, 201)
			a.must("POST", "/v1/subscriptions", f.key, map[string]any{"customer_external_id": ext, "plan_code": "chaos", "fee_timing": "FEE_TIMING_ARREARS"}, nil, 201)
			f.customers = append(f.customers, ext)
			custID[cust.ID] = f.id + "/" + ext
		}
		fx = append(fx, f)
	}
	return fx, custID
}

type genEvent struct {
	tenant   int
	customer string
	meter    string
	qty      string
	units    int64
	at       time.Time
	key      string
	invalid  bool
	late     bool
}

func format(units int64, scale int) string {
	if scale == 0 {
		return strconv.FormatInt(units, 10)
	}
	p := int64(1)
	for i := 0; i < scale; i++ {
		p *= 10
	}
	return fmt.Sprintf("%d.%0*d", units/p, scale, units%p)
}

var istanbul = func() *time.Location { l, _ := time.LoadLocation("Europe/Istanbul"); return l }()

func monthID(t time.Time) string {
	y, m, _ := t.In(istanbul).Date()
	return fmt.Sprintf("%04d-%02d", y, int(m))
}

func generate(seed uint64, n int, fx []*tenantFx, runTag string) []genEvent {
	rng := rand.New(rand.NewPCG(seed, seed^0xa5a5a5a5))
	now := time.Now()
	monthStart := time.Date(now.In(istanbul).Year(), now.In(istanbul).Month(), 1, 0, 0, 0, 0, istanbul)
	out := make([]genEvent, n)
	for i := range out {
		e := genEvent{tenant: rng.IntN(len(fx)), key: fmt.Sprintf("%s-%d", runTag, i)}
		e.customer = fx[e.tenant].customers[rng.IntN(numCustomers)]
		ms := meterSet[rng.IntN(len(meterSet))]
		e.meter = ms.code
		if ms.aggregation == "AGGREGATION_COUNT" {
			e.units, e.qty = 1, "1"
		} else {
			e.units = int64(rng.IntN(ms.maxUnits))
			e.qty = format(e.units, ms.scale)
		}
		switch r := rng.IntN(1000); {
		case r < 20: // 2% late: an event of the previous month
			e.late = true
			// At most 3 days before the month: with up to 31 days elapsed this month it stays inside ingest's 35-day window.
			e.at = monthStart.Add(-time.Duration(1+rng.IntN(3*24*3600)) * time.Second)
		default:
			e.at = now.Add(-time.Duration(rng.IntN(300)) * time.Second)
			if e.at.Before(monthStart) {
				e.at = now
			}
		}
		if rng.IntN(1000) < 1 { // 0.1% invalid: unknown meter, rejected at ingest, in no total
			e.invalid, e.meter = true, "no_such_meter"
		}
		out[i] = e
	}
	return out
}

type result struct{ Status, EventID, ErrorCode string }

// pace spreads the workload over time when QUOTIFY_CHAOS_RATE (events per second, all senders together) is set, so a small
// run lasts long enough for the monkey to inject real faults.
var pace = struct {
	sync.Mutex
	next time.Time
}{}

func throttle(events int) {
	rate := envInt("QUOTIFY_CHAOS_RATE", 0)
	if rate <= 0 {
		return
	}
	pace.Lock()
	now := time.Now()
	if pace.next.Before(now) {
		pace.next = now
	}
	wait := time.Until(pace.next)
	pace.next = pace.next.Add(time.Duration(float64(events) / float64(rate) * float64(time.Second)))
	pace.Unlock()
	time.Sleep(wait)
}

// send posts one batch until it gets a 202 and returns the per-event results.
func send(a api, key string, batch []genEvent, deadline time.Time) ([]result, int) {
	evs := make([]map[string]any, len(batch))
	for i, e := range batch {
		evs[i] = map[string]any{"idempotency_key": e.key, "customer_external_id": e.customer, "meter_code": e.meter,
			"quantity": e.qty, "event_time": e.at.UTC().Format(time.RFC3339Nano)}
	}
	retries := 0
	for backoff := 50 * time.Millisecond; time.Now().Before(deadline); backoff = min(backoff*2, 2*time.Second) {
		var resp struct {
			Results []struct {
				Status    string `json:"status"`
				EventID   string `json:"event_id"`
				ErrorCode string `json:"error_code"`
			} `json:"results"`
		}
		if code := a.call("POST", "/v1/events", key, map[string]any{"events": evs}, &resp); code == 202 && len(resp.Results) == len(batch) {
			out := make([]result, len(resp.Results))
			for i, r := range resp.Results {
				out[i] = result{r.Status, r.EventID, r.ErrorCode}
			}
			return out, retries
		}
		retries++
		time.Sleep(backoff)
	}
	return nil, retries
}

type cell struct {
	qty   *big.Int
	count int64
	max   bool
}

func (c *cell) add(units int64, count bool) {
	v := units
	if count {
		v = 1
	}
	c.count++
	if c.max {
		if n := big.NewInt(v); n.Cmp(c.qty) > 0 {
			c.qty = n
		}
		return
	}
	c.qty.Add(c.qty, big.NewInt(v))
}

// Assertion is the verdict on one of A1-A9.
type Assertion struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"` // pass | fail | skipped
	Detail string `json:"detail,omitempty"`
}

type report struct {
	Events, Unique, Duplicates, Invalid int
	Tenants, Customers, Meters          int
	Accepted, DuplicateResponses        int64
	Rejected                            int64
	Retries                             int64
	LedgerRowsOnTime, LedgerRowsLate    int
	Chaos                               MonkeyReport
	Assertions                          []Assertion
	Mismatches                          []string
	DLQRecords                          int64
	AggregatorDuplicatesSkipped         *float64 `json:",omitempty"`
	Seconds                             float64
	DrainSeconds                        float64
}

func (r *report) assert(id, name string, mismatches []string, skip string) {
	a := Assertion{ID: id, Name: name, Status: "pass"}
	switch {
	case skip != "":
		a.Status, a.Detail = "skipped", skip
	case len(mismatches) > 0:
		a.Status, a.Detail = "fail", fmt.Sprintf("%d mismatches, first: %s", len(mismatches), mismatches[0])
		r.Mismatches = append(r.Mismatches, mismatches...)
	}
	r.Assertions = append(r.Assertions, a)
}

func TestCorrectness(t *testing.T) {
	n, _ := strconv.Atoi(envOr("QUOTIFY_CHAOS_EVENTS", "100000"))
	seed, _ := strconv.ParseUint(envOr("QUOTIFY_CHAOS_SEED", "42"), 10, 64)
	started := time.Now()
	a := api{t}
	fx, custID := setup(t)
	tag := strconv.FormatInt(started.UnixNano(), 36)
	events := generate(seed, n, fx, tag)

	// 5% of the events are sent a second time, concurrently, by a second sender (same key, same payload).
	rng := rand.New(rand.NewPCG(seed+1, seed))
	var dups []genEvent
	for _, e := range events {
		if rng.IntN(100) < 5 {
			dups = append(dups, e)
		}
	}

	type final struct {
		accepted bool // accepted or duplicate: the event counts
		id       string
	}
	var mu sync.Mutex
	finals := map[string]final{} // idempotency key -> final result
	var rep report
	rep.Events, rep.Duplicates = n, len(dups)
	deadline := time.Now().Add(25 * time.Minute)

	sendAll := func(list []genEvent, workers int) {
		var next atomic.Int64
		var wg sync.WaitGroup
		var batches [][]genEvent
		brng := rand.New(rand.NewPCG(seed+uint64(len(list)), 3))
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
					// A batch carries one tenant's key; split it per tenant.
					byTenant := map[int][]genEvent{}
					for _, e := range batches[bi] {
						byTenant[e.tenant] = append(byTenant[e.tenant], e)
					}
					for tn, evs := range byTenant {
						throttle(len(evs))
						res, retries := send(a, fx[tn].key, evs, deadline)
						atomic.AddInt64(&rep.Retries, int64(retries))
						if res == nil {
							t.Errorf("batch for tenant %d never got through", tn)
							return
						}
						mu.Lock()
						for i, r := range res {
							e := evs[i]
							switch r.Status {
							case "accepted":
								atomic.AddInt64(&rep.Accepted, 1)
								finals[e.key] = final{true, r.EventID}
							case "duplicate":
								atomic.AddInt64(&rep.DuplicateResponses, 1)
								if f, ok := finals[e.key]; !ok || !f.accepted {
									finals[e.key] = final{true, r.EventID}
								} else if f.id != r.EventID {
									t.Errorf("key %s: duplicate reported event id %s, accepted as %s", e.key, r.EventID, f.id)
								}
							default:
								atomic.AddInt64(&rep.Rejected, 1)
								if !e.invalid {
									t.Errorf("valid event %s rejected: %s", e.key, r.ErrorCode)
								} else if r.ErrorCode != "UNKNOWN_METER" {
									t.Errorf("invalid event %s rejected with %s, want UNKNOWN_METER", e.key, r.ErrorCode)
								}
							}
						}
						mu.Unlock()
					}
				}
			}()
		}
		wg.Wait()
	}
	var monkey *Monkey
	if envOr("QUOTIFY_CHAOS_MONKEY", "0") == "1" {
		expected := time.Duration(max(2*time.Minute, time.Duration(n/400)*time.Second))
		monkey = StartMonkey(context.Background(), seed, expected)
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); sendAll(events, 8) }()
	go func() { defer wg.Done(); sendAll(dups, 2) }()
	wg.Wait()
	sentAt := time.Now()
	// The faults run until the workload is in; then everything is healed so the stack can drain.
	if monkey != nil {
		rep.Chaos = monkey.Stop()
		for _, e := range rep.Chaos.Errors {
			t.Logf("chaos: %s", e)
		}
	}
	if t.Failed() {
		return
	}

	// The ledger: every event whose final answer was accepted or duplicate, exactly once.
	onTime := map[string]*cell{}
	late := map[string]*cell{}
	nowPeriod := monthID(time.Now())
	var expected int64
	var expectedOnTimeEvents, expectedLateEvents int64
	for _, e := range events {
		f, ok := finals[e.key]
		if e.invalid {
			rep.Invalid++
			if ok {
				t.Errorf("invalid event %s was accepted", e.key)
			}
			continue
		}
		if !ok || !f.accepted {
			t.Errorf("event %s has no accepted/duplicate answer", e.key)
			continue
		}
		rep.Unique++
		expected++
		tn := fx[e.tenant]
		m := onTime
		key := fmt.Sprintf("%s/%s/%s/%s", tn.id, tn.id+"/"+e.customer, e.meter, monthID(e.at))
		if e.late {
			m, key = late, fmt.Sprintf("%s/%s/%s/%s|%s", tn.id, tn.id+"/"+e.customer, e.meter, nowPeriod, monthID(e.at))
			expectedLateEvents++
		} else {
			expectedOnTimeEvents++
		}
		c := m[key]
		if c == nil {
			c = &cell{qty: big.NewInt(0), max: meterByCode(e.meter).aggregation == "AGGREGATION_MAX"}
			m[key] = c
		}
		c.add(e.units, meterByCode(e.meter).aggregation == "AGGREGATION_COUNT")
	}
	rep.LedgerRowsOnTime, rep.LedgerRowsLate = len(onTime), len(late)
	rep.Tenants, rep.Customers, rep.Meters = len(fx), len(fx)*numCustomers, len(meterSet)

	// Drain: ingest's outbox empty, the aggregator has applied every event. Then it sits for a few seconds and is re-read
	// to catch any overcount.
	conn, err := pgx.Connect(context.Background(), aggregatorDSN)
	if err != nil {
		t.Fatalf("connect aggregator_db: %v", err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	ingestConn, err := pgx.Connect(context.Background(), ingestDSN)
	if err != nil {
		t.Fatalf("connect ingest_db: %v", err)
	}
	defer func() { _ = ingestConn.Close(context.Background()) }()
	tenantIDs := make([]string, len(fx))
	for i, f := range fx {
		tenantIDs[i] = f.id
	}
	counted := func() int64 {
		var c int64
		err := conn.QueryRow(context.Background(), `SELECT
			COALESCE((SELECT sum(event_count) FROM usage_aggregates WHERE tenant_id::text = ANY($1)), 0) +
			COALESCE((SELECT sum(event_count) FROM late_usage WHERE tenant_id::text = ANY($1)), 0)`, tenantIDs).Scan(&c)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	outboxRows := func() int64 {
		var c int64
		if err := ingestConn.QueryRow(context.Background(), `SELECT count(*) FROM outbox`).Scan(&c); err != nil {
			t.Fatal(err)
		}
		return c
	}
	drainBy := time.Now().Add(15 * time.Minute)
	for (counted() < expected || outboxRows() > 0) && time.Now().Before(drainBy) {
		time.Sleep(500 * time.Millisecond)
	}
	time.Sleep(5 * time.Second)
	rep.DrainSeconds = time.Since(sentAt).Seconds()

	// A1, A2: every key, exactly. A3: the totals.
	gotOn, gotLate := map[string]*cell{}, map[string]*cell{}
	read := func(q string, dst map[string]*cell, late bool) {
		rows, err := conn.Query(context.Background(), q, tenantIDs)
		if err != nil {
			t.Fatal(err)
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
				t.Fatal(err)
			}
			q, _ := new(big.Int).SetString(qty, 10)
			dst[fmt.Sprintf("%s/%s/%s/%s", tn, custID[cust], meter, p)] = &cell{qty: q, count: count}
		}
	}
	read(`SELECT tenant_id::text, customer_id::text, meter_code, period_id, quantity::text, event_count FROM usage_aggregates WHERE tenant_id::text = ANY($1) AND generation = 1`, gotOn, false)
	read(`SELECT tenant_id::text, customer_id::text, meter_code, billed_period_id, origin_period_id, quantity::text, event_count FROM late_usage WHERE tenant_id::text = ANY($1) AND generation = 1`, gotLate, true)
	diff := func(what string, want, got map[string]*cell) []string {
		var out []string
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
	rep.assert("A1", "usage_aggregates == ledger (on-time)", diff("A1 usage_aggregates", onTime, gotOn), "")
	rep.assert("A2", "late_usage == ledger (late)", diff("A2 late_usage", late, gotLate), "")
	var a3 []string
	if c := counted(); c != expected {
		a3 = append(a3, fmt.Sprintf("A3 events counted = %d, accepted = %d", c, expected))
	}
	rep.assert("A3", "accepted events == aggregated events", a3, "")

	// A4: limits-svc's counters equal the aggregator's on-time totals once the reconcile loop has run. The counters were
	// wiped when the monkey restarted Redis, so this is also the proof that a lost Redis rebuilds itself.
	a4, a4Skip := checkLimits(t, fx, onTime, nowPeriod, expectedOnTimeEvents > 0)
	rep.assert("A4", "limits counters == aggregator (on-time)", a4, a4Skip)

	// A5/A6 (invoices, charges) are covered by the month-close scenario (billing_chaos_test.go): live tenants here never
	// close a period.
	rep.Assertions = append(rep.Assertions,
		Assertion{ID: "A5", Name: "invoice lines == aggregates", Status: "skipped", Detail: "covered by TestMonthCloseSurvivesBrokerRestart"},
		Assertion{ID: "A6", Name: "at most one successful charge per invoice attempt", Status: "skipped", Detail: "covered by TestMonthCloseSurvivesBrokerRestart"})

	// A7: no frozen-period violation (needs Prometheus; chaos tenants never freeze a period, so this guards regressions).
	a7, a7Skip := checkFrozenViolations()
	rep.assert("A7", "aggregator_frozen_period_violations_total == 0", a7, a7Skip)

	// A8: ClickHouse (eventually) holds exactly the accepted events.
	a8, a8Skip := checkClickHouse(tenantIDs, expected)
	rep.assert("A8", "ClickHouse usage_events FINAL == accepted events", a8, a8Skip)

	// A9: nothing reached a dead-letter topic.
	var a9 []string
	if cl, err := kgo.NewClient(kgo.SeedBrokers(strings.Split(kafkaBrokers, ",")...)); err == nil {
		defer cl.Close()
		for _, topic := range []string{"usage.events.dlq.v1", "usage.limit-exceeded.dlq.v1", "billing.invoices.dlq.v1"} {
			var n int64
			if ends, err := kadm.NewClient(cl).ListEndOffsets(context.Background(), topic); err == nil {
				ends.Each(func(o kadm.ListedOffset) { n += o.Offset })
			}
			rep.DLQRecords += n
			if n != 0 {
				a9 = append(a9, fmt.Sprintf("A9 %s has %d records", topic, n))
			}
		}
	}
	rep.assert("A9", "dead-letter topics empty", a9, "")

	if v, ok := promValue(`sum(aggregator_duplicates_skipped_total)`); ok {
		rep.AggregatorDuplicatesSkipped = &v
	}
	if min := envInt("QUOTIFY_CHAOS_MIN_KILLS", 0); rep.Chaos.Kills < min {
		rep.Mismatches = append(rep.Mismatches, fmt.Sprintf("chaos: %d container kills injected, at least %d required", rep.Chaos.Kills, min))
	}
	if min := envInt("QUOTIFY_CHAOS_MIN_FAULTS", 0); rep.Chaos.Faults < min {
		rep.Mismatches = append(rep.Mismatches, fmt.Sprintf("chaos: %d network faults injected, at least %d required", rep.Chaos.Faults, min))
	}

	rep.Seconds = time.Since(started).Seconds()
	b, _ := json.MarshalIndent(rep, "", "  ")
	_ = os.WriteFile(envOr("QUOTIFY_CHAOS_REPORT", "chaos-report.json"), b, 0o644)
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

// parseUnits turns a decimal string such as "12.345" into scaled units for scale 3.
func parseUnits(s string, scale int) (*big.Int, bool) {
	whole, frac, _ := strings.Cut(s, ".")
	if len(frac) > scale {
		return nil, false
	}
	frac += strings.Repeat("0", scale-len(frac))
	return new(big.Int).SetString(whole+frac, 10)
}

// checkLimits polls GET /v1/customers/{c}/entitlements until every counter equals the aggregator total or time is up.
func checkLimits(t *testing.T, fx []*tenantFx, onTime map[string]*cell, period string, any bool) ([]string, string) {
	if !any {
		return nil, "no on-time events"
	}
	type key struct{ tenant, customer, meter string }
	want := map[key]*big.Int{}
	for _, tn := range fx {
		for _, c := range tn.customers {
			for _, m := range meterSet {
				k := fmt.Sprintf("%s/%s/%s/%s", tn.id, tn.id+"/"+c, m.code, period)
				v := big.NewInt(0)
				if cl := onTime[k]; cl != nil {
					v = cl.qty
				}
				want[key{tn.id, c, m.code}] = v
			}
		}
	}
	keyOf := map[string]string{}
	for _, tn := range fx {
		keyOf[tn.id] = tn.key
	}
	a := api{t}
	deadline := time.Now().Add(time.Duration(envInt("QUOTIFY_CHAOS_LIMITS_WAIT_SECONDS", 240)) * time.Second)
	var bad []string
	for {
		bad = bad[:0]
		var mu sync.Mutex
		var wg sync.WaitGroup
		sem := make(chan struct{}, 8)
		for _, tn := range fx {
			for _, c := range tn.customers {
				wg.Add(1)
				sem <- struct{}{}
				go func() {
					defer func() { <-sem; wg.Done() }()
					var resp struct {
						Items []struct {
							MeterCode string `json:"meter_code"`
							Used      string `json:"used"`
						} `json:"items"`
					}
					if code := a.call("GET", "/v1/customers/"+url.PathEscape(c)+"/entitlements", tn.key, nil, &resp); code != 200 {
						mu.Lock()
						bad = append(bad, fmt.Sprintf("A4 %s/%s: entitlements answered %d", tn.id, c, code))
						mu.Unlock()
						return
					}
					got := map[string]string{}
					for _, it := range resp.Items {
						got[it.MeterCode] = it.Used
					}
					for _, m := range meterSet {
						w := want[key{tn.id, c, m.code}]
						u, ok := parseUnits(got[m.code], m.scale)
						if !ok || u.Cmp(w) != 0 {
							mu.Lock()
							bad = append(bad, fmt.Sprintf("A4 %s/%s/%s: limits used=%q, aggregator=%s units", tn.id, c, m.code, got[m.code], w))
							mu.Unlock()
						}
					}
				}()
			}
		}
		wg.Wait()
		if len(bad) == 0 || time.Now().After(deadline) {
			sort.Strings(bad)
			return append([]string(nil), bad...), ""
		}
		time.Sleep(10 * time.Second)
	}
}

func promValue(q string) (float64, bool) {
	if prometheusURL == "" {
		return 0, false
	}
	resp, err := httpc.Get(prometheusURL + "/api/v1/query?query=" + url.QueryEscape(q))
	if err != nil {
		return 0, false
	}
	defer func() { _ = resp.Body.Close() }()
	var out struct {
		Data struct {
			Result []struct {
				Value [2]any `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}
	if json.NewDecoder(resp.Body).Decode(&out) != nil || len(out.Data.Result) == 0 {
		return 0, false
	}
	s, _ := out.Data.Result[0].Value[1].(string)
	v, err := strconv.ParseFloat(s, 64)
	return v, err == nil
}

func checkFrozenViolations() ([]string, string) {
	if prometheusURL == "" {
		return nil, "QUOTIFY_PROMETHEUS_URL not set"
	}
	v, ok := promValue(`sum(aggregator_frozen_period_violations_total) or vector(0)`)
	if !ok {
		return nil, "Prometheus did not answer"
	}
	if v != 0 {
		return []string{fmt.Sprintf("A7 aggregator_frozen_period_violations_total = %v", v)}, ""
	}
	return nil, ""
}

// checkClickHouse polls (analytics is eventually consistent, up to 2 min after drain) for the exact row count.
func checkClickHouse(tenants []string, want int64) ([]string, string) {
	q := fmt.Sprintf("SELECT count() FROM usage_events FINAL WHERE toString(tenant_id) IN ('%s')", strings.Join(tenants, "','"))
	var got int64 = -1
	for deadline := time.Now().Add(2 * time.Minute); time.Now().Before(deadline); time.Sleep(3 * time.Second) {
		req, _ := http.NewRequest("POST", clickhouseURL+"/?database=quotify", strings.NewReader(q))
		req.SetBasicAuth("quotify", "quotify_local")
		resp, err := httpc.Do(req)
		if err != nil {
			continue
		}
		raw, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != 200 {
			continue
		}
		got, _ = strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
		if got == want {
			return nil, ""
		}
	}
	return []string{fmt.Sprintf("A8 ClickHouse has %d events, accepted %d", got, want)}, ""
}

func jsonDecode(r io.Reader, out any) error { return json.NewDecoder(r).Decode(out) }
