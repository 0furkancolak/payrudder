//go:build bench

package bench

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// TestUsageSeriesLatency measures the 30-day hourly series of one tenant (docs/phases/phase-07-analytics-assets.md:
// target p95 < 300 ms): ROWS synthetic events spread over 30 days are inserted into ClickHouse for a fresh tenant, then
// GET /v1/analytics/usage is called N times through the gateway.
func TestUsageSeriesLatency(t *testing.T) {
	rows := envInt("PAYRUDDER_BENCH_ROWS", 3000000)
	calls := envInt("PAYRUDDER_BENCH_CALLS", 200)
	var acct struct {
		Tenants []struct{ ID, Environment string } `json:"tenants"`
	}
	if c := api(t, "POST", "/admin/v1/accounts", operator, map[string]any{"name": "bench-series-" + fmt.Sprint(time.Now().UnixNano()),
		"currency": "TRY", "timezone": "Europe/Istanbul", "grace_period": "0s", "tier": "TENANT_TIER_ENTERPRISE"}, &acct); c != 201 {
		t.Fatalf("account: %d", c)
	}
	var tenant, key string
	for _, tn := range acct.Tenants {
		if tn.Environment == "ENVIRONMENT_LIVE" {
			tenant = tn.ID
			var k struct{ Plaintext string }
			// A live tenant is created inactive and has no keys: activate it the way support does, with a reason.
			if c := api(t, "PUT", "/admin/v1/tenants/"+tn.ID+"/status", operator, map[string]string{"status": "active", "reason": "bench"}, nil); c != 200 {
				t.Fatalf("activate: %d", c)
			}
			api(t, "POST", "/admin/v1/tenants/"+tn.ID+"/api-keys", operator, nil, &k)
			key = k.Plaintext
		}
	}
	if c := api(t, "POST", "/v1/meters", key, map[string]any{"code": "calls", "aggregation": "AGGREGATION_COUNT", "quantity_scale": 0, "unit_label": "calls"}, nil); c != 201 {
		t.Fatalf("meter: %d", c)
	}

	chURL := envOr("PAYRUDDER_CLICKHOUSE_URL", "http://localhost:42123")
	exec := func(q string) string {
		req, _ := http.NewRequest("POST", chURL+"/?database=payrudder", strings.NewReader(q))
		req.SetBasicAuth("payrudder", "payrudder_local")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != 200 {
			t.Fatalf("clickhouse: %d %s", resp.StatusCode, b)
		}
		return strings.TrimSpace(string(b))
	}
	start := time.Now()
	// ClickHouse runs under a 768 MiB cap locally: insert in chunks.
	const chunk = 200000
	for off := 0; off < rows; off += chunk {
		exec(fmt.Sprintf(`INSERT INTO usage_events (tenant_id, event_id, customer_id, customer_external_id, meter_code, quantity_units, quantity_scale, period_id, event_time, ingested_at, properties)
			SELECT toUUID('%s'), concat('bench-', toString(number)), toUUID(concat('00000000-0000-0000-0000-', lpad(toString(number %% 1000), 12, '0'))),
			       concat('cus_', toString(number %% 1000)), 'calls', 1, 0, formatDateTime(now() - toIntervalSecond(intHash32(number) %% (30*86400)), '%%Y-%%m'),
			       now() - toIntervalSecond(intHash32(number) %% (30*86400)), now(), map('region', 'eu')
			FROM numbers(%d, %d)`, tenant, off, min(chunk, rows-off)))
	}
	t.Logf("inserted %d events in %s; usage_hourly rows for the tenant: %s", rows, time.Since(start).Round(time.Millisecond),
		exec(fmt.Sprintf(`SELECT count() FROM usage_hourly WHERE tenant_id = toUUID('%s')`, tenant)))

	to := time.Now().UTC()
	from := to.Add(-30 * 24 * time.Hour)
	for _, gran := range []string{"hour", "day"} {
		q := url.Values{"meter": {"calls"}, "from": {from.Format(time.RFC3339)}, "to": {to.Format(time.RFC3339)}, "granularity": {gran}}
		var lat []time.Duration
		points := 0
		for i := 0; i < calls+5; i++ {
			s := time.Now()
			req, _ := http.NewRequest("GET", gateway+"/v1/analytics/usage?"+q.Encode(), nil)
			req.Header.Set("Authorization", "Bearer "+key)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			b, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == 429 { // analytics is rate limited (enterprise: 20/s)
				time.Sleep(100 * time.Millisecond)
				i--
				continue
			}
			if resp.StatusCode != 200 {
				t.Fatalf("usage: %d %s", resp.StatusCode, b)
			}
			points = strings.Count(string(b), `"event_count"`)
			if i >= 5 { // the first five warm the caches
				lat = append(lat, time.Since(s))
			}
			time.Sleep(60 * time.Millisecond)
		}
		p50, p99 := quantiles(append([]time.Duration(nil), lat...))
		p95 := func() time.Duration {
			l := append([]time.Duration(nil), lat...)
			quantilesSort(l)
			return l[len(l)*95/100]
		}()
		t.Logf("30-day series, granularity=%s: %d points, %d calls: p50 %s p95 %s p99 %s", gran, points, len(lat), p50.Round(time.Microsecond), p95.Round(time.Microsecond), p99.Round(time.Microsecond))
	}
}

func quantilesSort(d []time.Duration) {
	for i := 1; i < len(d); i++ {
		for j := i; j > 0 && d[j] < d[j-1]; j-- {
			d[j], d[j-1] = d[j-1], d[j]
		}
	}
}
