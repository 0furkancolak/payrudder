//go:build chaos

// Package chaos is the correctness suite (docs/09-testing-strategy.md): it drives a deterministic workload through the
// public API of the running compose stack and asserts that every accepted event is counted exactly once.
//
// v0 (Phase 03) injects faults only through the compiled-in hooks RELAY_CRASH_AFTER_PRODUCE and AGG_CRASH_AFTER_COMMIT
// (see compose.chaos.yaml); killing containers and breaking connections comes with Phase 09.
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

var (
	gateway       = envOr("QUOTIFY_GATEWAY_URL", "http://localhost:8000")
	operatorToken = envOr("QUOTIFY_OPERATOR_TOKEN", "local-operator-token-change-me-0123456789")
	kafkaBrokers  = envOr("QUOTIFY_KAFKA_BROKERS", "localhost:19092")
	aggregatorDSN = envOr("QUOTIFY_AGGREGATOR_DSN", fmt.Sprintf("postgres://aggregator_system:aggregator_system_local@localhost:%s/aggregator_db", envOr("QUOTIFY_PG_HOST_PORT", "5432")))
	httpc         = &http.Client{Timeout: 30 * time.Second}
)

const (
	numTenants   = 5
	numCustomers = 20
	scaleSum     = 3
)

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
		a.must("POST", "/admin/v1/tenants/"+f.id+"/api-keys", operatorToken, nil, &key, 201)
		f.key = key.Plaintext
		for _, m := range []map[string]any{
			{"code": "m_sum", "aggregation": "AGGREGATION_SUM", "value_property": "v", "quantity_scale": scaleSum, "unit_label": "u"},
			{"code": "m_count", "aggregation": "AGGREGATION_COUNT", "unit_label": "calls"},
			{"code": "m_max", "aggregation": "AGGREGATION_MAX", "value_property": "v", "quantity_scale": 0, "unit_label": "u"},
		} {
			a.must("POST", "/v1/meters", f.key, m, nil, 201)
		}
		for c := 0; c < numCustomers; c++ {
			ext := fmt.Sprintf("cus_%d", c)
			var cust struct {
				ID string `json:"id"`
			}
			a.must("POST", "/v1/customers", f.key, map[string]any{"external_id": ext, "name": ext, "email": ext + "@example.com"}, &cust, 201)
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
		switch rng.IntN(3) {
		case 0:
			e.meter, e.units = "m_sum", int64(rng.IntN(100000))
			e.qty = format(e.units, scaleSum)
		case 1:
			e.meter, e.units, e.qty = "m_count", 1, "1"
		default:
			e.meter, e.units = "m_max", int64(rng.IntN(1000))
			e.qty = format(e.units, 0)
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

type report struct {
	Events, Unique, Duplicates, Invalid int
	Accepted, DuplicateResponses        int64
	Rejected                            int64
	Retries                             int64
	LedgerRowsOnTime, LedgerRowsLate    int
	Mismatches                          []string
	DLQRecords                          int64
	Seconds                             float64
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
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); sendAll(events, 8) }()
	go func() { defer wg.Done(); sendAll(dups, 2) }()
	wg.Wait()
	if t.Failed() {
		return
	}

	// The ledger: every event whose final answer was accepted or duplicate, exactly once.
	onTime := map[string]*cell{}
	late := map[string]*cell{}
	nowPeriod := monthID(time.Now())
	var expected int64
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
		}
		c := m[key]
		if c == nil {
			c = &cell{qty: big.NewInt(0), max: e.meter == "m_max"}
			m[key] = c
		}
		c.add(e.units, e.meter == "m_count")
	}
	rep.LedgerRowsOnTime, rep.LedgerRowsLate = len(onTime), len(late)

	// Drain: wait until the aggregator has applied every event, then let it sit and re-read to catch any overcount.
	conn, err := pgx.Connect(context.Background(), aggregatorDSN)
	if err != nil {
		t.Fatalf("connect aggregator_db: %v", err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
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
	for drainBy := time.Now().Add(10 * time.Minute); counted() < expected && time.Now().Before(drainBy); time.Sleep(500 * time.Millisecond) {
	}
	time.Sleep(5 * time.Second)

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
	diff := func(what string, want, got map[string]*cell) {
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
				rep.Mismatches = append(rep.Mismatches, fmt.Sprintf("%s %s: unexpected row qty=%s count=%d", what, k, g.qty, g.count))
			case g == nil:
				rep.Mismatches = append(rep.Mismatches, fmt.Sprintf("%s %s: missing, want qty=%s count=%d", what, k, w.qty, w.count))
			case w.qty.Cmp(g.qty) != 0 || w.count != g.count:
				rep.Mismatches = append(rep.Mismatches, fmt.Sprintf("%s %s: qty %s count %d, want qty %s count %d", what, k, g.qty, g.count, w.qty, w.count))
			}
		}
	}
	diff("A1 usage_aggregates", onTime, gotOn)
	diff("A2 late_usage", late, gotLate)
	if c := counted(); c != expected {
		rep.Mismatches = append(rep.Mismatches, fmt.Sprintf("A3 events counted = %d, accepted = %d", c, expected))
	}

	// A9: nothing reached the dead-letter topic.
	if cl, err := kgo.NewClient(kgo.SeedBrokers(strings.Split(kafkaBrokers, ",")...)); err == nil {
		defer cl.Close()
		if ends, err := kadm.NewClient(cl).ListEndOffsets(context.Background(), "usage.events.dlq.v1"); err == nil {
			ends.Each(func(o kadm.ListedOffset) { rep.DLQRecords += o.Offset })
		}
	}
	if rep.DLQRecords != 0 {
		rep.Mismatches = append(rep.Mismatches, fmt.Sprintf("A9 usage.events.dlq.v1 has %d records", rep.DLQRecords))
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
