//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/protobuf/proto"

	eventsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/events/v1"
)

var kafkaBrokers = envOr("PAYRUDDER_KAFKA_BROKERS", "localhost:42092")

func event(key, meter string, quantity any, at time.Time) map[string]any {
	e := map[string]any{
		"customer_external_id": "cus_e2e", "meter_code": meter, "quantity": quantity,
		"event_time": at.UTC().Format(time.RFC3339Nano), "properties": map[string]string{"region": "eu"},
	}
	if key != "" {
		e["idempotency_key"] = key
	}
	return e
}

func results(t *testing.T, r result) []map[string]any {
	t.Helper()
	if r.status != 202 {
		t.Fatalf("POST /v1/events: %d %v", r.status, r.body)
	}
	var out []map[string]any
	for _, x := range r.body["results"].([]any) {
		out = append(out, x.(map[string]any))
	}
	return out
}

func TestEventsFlow(t *testing.T) {
	a := newAccount(t, "TENANT_TIER_STANDARD")
	for _, step := range []struct {
		path string
		body map[string]any
	}{
		{"/v1/meters", map[string]any{"code": "gb_hours", "aggregation": "AGGREGATION_SUM", "value_property": "gb_h", "quantity_scale": 3, "unit_label": "GB-h"}},
		{"/v1/customers", map[string]any{"external_id": "cus_e2e", "name": "E2E", "email": "e2e@example.com"}},
	} {
		if r := call(t, "POST", step.path, a.liveKey, step.body); r.status != 201 {
			t.Fatalf("POST %s: %d %v", step.path, r.status, r.body)
		}
	}
	at := time.Now().Add(-time.Minute)
	// A JSON number and a string are both accepted, and 1.10 stays 1.10 (never a float).
	batch := map[string]any{"events": []any{
		event("e2e-1", "gb_hours", "1.250", at),
		event("e2e-2", "gb_hours", 1.10, at),
		event("e2e-3", "no_such_meter", "1", at),
		event("e2e-4", "gb_hours", "1.2345", at),
		event("", "gb_hours", "1", at),
	}}
	res := results(t, call(t, "POST", "/v1/events", a.liveKey, batch))
	wantStatus := []string{"accepted", "accepted", "rejected", "rejected", "rejected"}
	wantCode := []string{"", "", "UNKNOWN_METER", "INVALID_QUANTITY_PRECISION", "IDEMPOTENCY_KEY_REQUIRED"}
	for i := range wantStatus {
		code, _ := res[i]["error_code"].(string)
		if res[i]["status"] != wantStatus[i] || code != wantCode[i] {
			t.Fatalf("event %d = %v, want %s %q", i, res[i], wantStatus[i], wantCode[i])
		}
	}
	id1, id2 := res[0]["event_id"].(string), res[1]["event_id"].(string)

	// Retrying the same batch is safe: accepted events come back as duplicates with the original ids.
	again := results(t, call(t, "POST", "/v1/events", a.liveKey, batch))
	if again[0]["status"] != "duplicate" || again[0]["event_id"] != id1 || again[1]["status"] != "duplicate" || again[1]["event_id"] != id2 {
		t.Fatalf("retry = %v", again[:2])
	}
	// Same key with another quantity is refused.
	reuse := results(t, call(t, "POST", "/v1/events", a.liveKey, map[string]any{"events": []any{event("e2e-1", "gb_hours", "9", at)}}))
	if reuse[0]["status"] != "rejected" || reuse[0]["error_code"] != "IDEMPOTENCY_KEY_REUSED" {
		t.Fatalf("reuse = %v", reuse[0])
	}
	if r := call(t, "POST", "/v1/events", a.liveKey, map[string]any{"events": []any{}}); r.status != 400 {
		t.Fatalf("empty batch: %d", r.status)
	}

	// Both accepted events reach Kafka exactly once, decoded with the shared proto.
	recs := consumeTenant(t, a.live, 2)
	byID := map[string]*kgo.Record{}
	for _, r := range recs {
		e := &eventsv1.UsageEvent{}
		if err := proto.Unmarshal(r.Value, e); err != nil {
			t.Fatal(err)
		}
		byID[e.EventId] = r
		switch e.EventId {
		case id1:
			if e.QuantityUnits != 1250 || e.QuantityScale != 3 {
				t.Errorf("event 1 quantity = %d @%d", e.QuantityUnits, e.QuantityScale)
			}
		case id2:
			if e.QuantityUnits != 1100 || e.QuantityScale != 3 {
				t.Errorf("event 2 quantity = %d @%d (a float64 would give 1100 only by luck)", e.QuantityUnits, e.QuantityScale)
			}
		default:
			t.Errorf("unexpected event %s in Kafka", e.EventId)
		}
		if e.TenantId != a.live || e.MeterCode != "gb_hours" || e.CustomerExternalId != "cus_e2e" || e.Properties["region"] != "eu" ||
			e.PeriodId == "" || e.IngestedAt == nil {
			t.Errorf("event = %v", e)
		}
		h := map[string]string{}
		for _, hd := range r.Headers {
			h[hd.Key] = string(hd.Value)
		}
		if h["ce-type"] != "payrudder.events.v1.UsageEvent" || h["tenant-id"] != a.live || h["ce-id"] != e.EventId || !strings.HasPrefix(h["traceparent"], "00-") {
			t.Errorf("headers = %v", h)
		}
	}
	if len(byID) != 2 || len(recs) != 2 {
		t.Fatalf("kafka has %d records / %d distinct events for tenant, want 2/2", len(recs), len(byID))
	}
}

// TestUsageEndToEnd follows events through ingest, Kafka and the aggregator to the usage endpoint (event_count is an int64, so a JSON string).
func TestUsageEndToEnd(t *testing.T) {
	a := newAccount(t, "TENANT_TIER_STANDARD")
	k := a.liveKey
	for _, step := range []struct {
		path string
		body map[string]any
	}{
		{"/v1/meters", map[string]any{"code": "gb_hours", "aggregation": "AGGREGATION_SUM", "value_property": "gb_h", "quantity_scale": 3, "unit_label": "GB-h"}},
		{"/v1/customers", map[string]any{"external_id": "cus_e2e", "name": "E2E", "email": "e2e@example.com"}},
	} {
		if r := call(t, "POST", step.path, k, step.body); r.status != 201 {
			t.Fatalf("POST %s: %d %v", step.path, r.status, r.body)
		}
	}
	at := time.Now().Add(-time.Minute)
	batch := map[string]any{"events": []any{
		event("use-1", "gb_hours", "0.001", at), event("use-2", "gb_hours", 1.5, at), event("use-3", "gb_hours", "2", at),
		event("use-1", "gb_hours", "0.001", at), // a retry: counted once
	}}
	if res := results(t, call(t, "POST", "/v1/events", k, batch)); res[3]["status"] != "duplicate" {
		t.Fatalf("retry = %v", res[3])
	}
	deadline := time.Now().Add(30 * time.Second)
	var last result
	for time.Now().Before(deadline) {
		last = call(t, "GET", "/v1/customers/cus_e2e/usage", k, nil)
		if rows, _ := last.body["usage"].([]any); last.status == 200 && len(rows) == 1 && rows[0].(map[string]any)["event_count"] == "3" {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	rows, _ := last.body["usage"].([]any)
	if last.status != 200 || len(rows) != 1 {
		t.Fatalf("usage after 30 s: %d %v", last.status, last.body)
	}
	row := rows[0].(map[string]any)
	if row["meter_code"] != "gb_hours" || row["quantity"] != "3.501" || row["event_count"] != "3" {
		t.Fatalf("usage row = %#v, want 3.501 over 3 events", row)
	}
	if r := call(t, "GET", "/v1/customers/nobody/usage", k, nil); r.status != 404 {
		t.Fatalf("unknown customer: %d", r.status)
	}
	if r := call(t, "GET", "/v1/customers/cus_e2e/usage?period=nope", k, nil); r.status != 400 {
		t.Fatalf("bad period: %d", r.status)
	}
}

func TestSandboxEventsNeedNoKey(t *testing.T) {
	a := newAccount(t, "TENANT_TIER_STANDARD")
	k := a.sandboxKey
	for _, step := range []struct {
		path string
		body map[string]any
	}{
		{"/v1/meters", map[string]any{"code": "api_calls", "aggregation": "AGGREGATION_COUNT", "unit_label": "calls"}},
		{"/v1/customers", map[string]any{"external_id": "cus_e2e", "name": "E2E", "email": "e2e@example.com", "payment_method": "pm_sandbox_success"}},
	} {
		if r := call(t, "POST", step.path, k, step.body); r.status != 201 {
			t.Fatalf("POST %s: %d %v", step.path, r.status, r.body)
		}
	}
	at := time.Now().Add(-time.Minute)
	res := results(t, call(t, "POST", "/v1/events", k, map[string]any{"events": []any{
		event("", "api_calls", 1, at), event("", "api_calls", 1, at)}}))
	if res[0]["status"] != "accepted" || res[1]["status"] != "accepted" || res[0]["event_id"] == res[1]["event_id"] {
		t.Fatalf("sandbox events without keys = %v", res)
	}
}

func TestEventsRateLimitedPerEvent(t *testing.T) {
	a := newAccount(t, "TENANT_TIER_FREE")
	// free tier ingest: 100 events/s, burst 500. 400 events pass; the next 400 exceed the burst.
	mk := func(n int) map[string]any {
		evs := make([]any, n)
		for i := range evs {
			evs[i] = event(fmt.Sprintf("rl-%d-%d", time.Now().UnixNano(), i), "nope", "1", time.Now())
		}
		return map[string]any{"events": evs}
	}
	if r := call(t, "POST", "/v1/events", a.liveKey, mk(400)); r.status != 202 {
		t.Fatalf("first batch: %d %v", r.status, r.body)
	}
	r := call(t, "POST", "/v1/events", a.liveKey, mk(400))
	if r.status != 429 || r.header.Get("Retry-After") == "" {
		t.Fatalf("second batch: %d retry=%q %v (rate limit must be charged per event)", r.status, r.header.Get("Retry-After"), r.body)
	}
}

// consumeTenant reads usage.events.v1 and returns the records of the tenant, waiting for at least want and then
// a short quiet period to catch duplicates.
func consumeTenant(t *testing.T, tenantID string, want int) []*kgo.Record {
	t.Helper()
	cl, err := kgo.NewClient(kgo.SeedBrokers(strings.Split(kafkaBrokers, ",")...), kgo.ConsumeTopics("usage.events.v1"),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()))
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	var out []*kgo.Record
	deadline := time.Now().Add(30 * time.Second)
	lastNew := time.Now()
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		fetches := cl.PollFetches(ctx)
		cancel()
		fetches.EachRecord(func(r *kgo.Record) {
			if string(r.Key) == tenantID {
				out = append(out, r)
				lastNew = time.Now()
			}
		})
		if len(out) >= want && time.Since(lastNew) >= 2*time.Second {
			break
		}
	}
	return out
}
