//go:build chaos

// Package chaos is the correctness suite (docs/09-testing-strategy.md): it drives a deterministic workload through the
// public API of the running compose stack while a chaos monkey kills replicas, breaks connections and stops brokers, and
// asserts that every accepted event is counted exactly once (A1-A9, no tolerance).
//
//	make chaos N=100000
package chaos

import (
	"bytes"
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
	"testing"
	"time"
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
	pgPort        = envOr("PAYRUDDER_PG_HOST_PORT", "42432")
	gateway       = envOr("PAYRUDDER_GATEWAY_URL", "http://localhost:42800")
	operatorToken = envOr("PAYRUDDER_OPERATOR_TOKEN", "local-operator-token-change-me-0123456789")
	kafkaBrokers  = envOr("PAYRUDDER_KAFKA_BROKERS", "localhost:42092")
	aggregatorDSN = envOr("PAYRUDDER_AGGREGATOR_DSN", fmt.Sprintf("postgres://aggregator_system:aggregator_system_local@localhost:%s/aggregator_db", pgPort))
	ingestDSN     = envOr("PAYRUDDER_INGEST_DSN", fmt.Sprintf("postgres://ingest_system:ingest_system_local@localhost:%s/ingest_db", pgPort))
	clickhouseURL = envOr("PAYRUDDER_CLICKHOUSE_URL", "http://localhost:42123")
	toxiproxyURL  = envOr("PAYRUDDER_TOXIPROXY_URL", "http://localhost:42474")
	prometheusURL = envOr("PAYRUDDER_PROMETHEUS_URL", "")
	httpc         = &http.Client{Timeout: 30 * time.Second}

	numTenants   = envInt("PAYRUDDER_CHAOS_TENANTS", 10)
	numCustomers = envInt("PAYRUDDER_CHAOS_CUSTOMERS", 20)
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

// pace spreads the workload over time when PAYRUDDER_CHAOS_RATE (events per second, all senders together) is set, so a small
// run lasts long enough for the monkey to inject real faults.
var pace = struct {
	sync.Mutex
	next time.Time
}{}

func throttle(events int) {
	rate := envInt("PAYRUDDER_CHAOS_RATE", 0)
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
	deadline := time.Now().Add(time.Duration(envInt("PAYRUDDER_CHAOS_LIMITS_WAIT_SECONDS", 240)) * time.Second)
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
						if _, there := got[m.code]; !there {
							// The meter is not in the answer at all (the pod that answered has no entitlement for it): a different failure from a counter that is wrong.
							mu.Lock()
							bad = append(bad, fmt.Sprintf("A4 %s/%s/%s: the entitlements answer has no entry for this meter (%d of %d meters answered), aggregator=%s units", tn.id, c, m.code, len(got), len(meterSet), w))
							mu.Unlock()
							continue
						}
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
		return nil, "PAYRUDDER_PROMETHEUS_URL not set"
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
		req, _ := http.NewRequest("POST", clickhouseURL+"/?database=payrudder", strings.NewReader(q))
		req.SetBasicAuth("payrudder", "payrudder_local")
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
