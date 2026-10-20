//go:build e2e

package e2e

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/protobuf/proto"

	eventsv1 "github.com/0furkancolak/quotify/gen/go/quotify/events/v1"
)

// consumeLimitEvents reads usage.limit-exceeded.v1 for a tenant until want events arrived and 3 s passed without more.
func consumeLimitEvents(t *testing.T, tenantID string, want int) []*eventsv1.LimitExceeded {
	t.Helper()
	cl, err := kgo.NewClient(kgo.SeedBrokers(strings.Split(kafkaBrokers, ",")...), kgo.ConsumeTopics("usage.limit-exceeded.v1"),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()))
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	var out []*eventsv1.LimitExceeded
	deadline := time.Now().Add(40 * time.Second)
	lastNew := time.Now()
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		cl.PollFetches(ctx).EachRecord(func(r *kgo.Record) {
			if string(r.Key) != tenantID {
				return
			}
			var le eventsv1.LimitExceeded
			if err := proto.Unmarshal(r.Value, &le); err != nil {
				t.Fatal(err)
			}
			out = append(out, &le)
			lastNew = time.Now()
		})
		cancel()
		if len(out) >= want && time.Since(lastNew) >= 3*time.Second {
			break
		}
	}
	return out
}

// TestEntitlementsEndToEnd follows a subscription and usage through plans-svc's outbox, ingest, Kafka and limits-svc
// to the entitlement endpoint and the LimitExceeded topic.
func TestEntitlementsEndToEnd(t *testing.T) {
	a := newAccount(t, "TENANT_TIER_STANDARD")
	k := a.liveKey
	var subID string
	for _, step := range []struct {
		path string
		body map[string]any
	}{
		{"/v1/meters", map[string]any{"code": "seats", "aggregation": "AGGREGATION_SUM", "value_property": "n", "quantity_scale": 0, "unit_label": "seats"}},
		{"/v1/plans", map[string]any{"code": "team", "name": "Team"}},
		{"/v1/plans/team/versions", map[string]any{"base_fee_minor": 1000,
			"prices":       []any{map[string]any{"meter_code": "seats", "per_unit": map[string]any{"unit_price": "1"}}},
			"entitlements": []any{map[string]any{"meter_code": "seats", "limit": "10", "alert_thresholds_pct": []int{80, 100}}}}},
		{"/v1/customers", map[string]any{"external_id": "cus_e2e", "name": "E2E", "email": "e2e@example.com"}},
		{"/v1/subscriptions", map[string]any{"customer_external_id": "cus_e2e", "plan_code": "team", "fee_timing": "FEE_TIMING_ARREARS"}},
	} {
		if r := call(t, "POST", step.path, k, step.body); r.status != 201 {
			t.Fatalf("POST %s: %d %v", step.path, r.status, r.body)
		} else if step.path == "/v1/subscriptions" {
			subID, _ = r.body["id"].(string)
		}
	}
	waitEntitlement := func(cond func(body map[string]any) bool, what string) map[string]any {
		t.Helper()
		var last result
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			last = call(t, "GET", "/v1/customers/cus_e2e/entitlements/seats", k, nil)
			if last.status == 200 && cond(last.body) {
				return last.body
			}
			time.Sleep(250 * time.Millisecond)
		}
		t.Fatalf("%s: last answer %d %v", what, last.status, last.body)
		return nil
	}
	// The subscription's outbox row reaches limits-svc.
	waitEntitlement(func(b map[string]any) bool { return b["limit"] == "10" }, "entitlement to become visible")

	at := time.Now().Add(-time.Minute)
	send := func(prefix string, n int) {
		evs := make([]any, n)
		for i := range evs {
			evs[i] = event(prefix+string(rune('a'+i)), "seats", 1, at)
		}
		if r := call(t, "POST", "/v1/events", k, map[string]any{"events": evs}); r.status != 202 {
			t.Fatalf("events: %d %v", r.status, r.body)
		}
	}
	send("first-", 8)
	body := waitEntitlement(func(b map[string]any) bool { return b["used"] == "8" }, "80% usage")
	if body["allowed"] != true || body["unlimited"] != false || body["as_of"] == nil {
		t.Fatalf("at 8/10: %v", body)
	}
	send("second-", 2)
	body = waitEntitlement(func(b map[string]any) bool { return b["used"] == "10" }, "100% usage")
	if body["allowed"] != false {
		t.Fatalf("at 10/10 the customer must not be allowed: %v", body)
	}

	got := consumeLimitEvents(t, a.live, 2)
	seen := map[int32]int{}
	for _, le := range got {
		seen[le.GetThresholdPct()]++
		if le.GetCustomerId() == "" || le.GetMeterCode() != "seats" || le.GetLimitUnits() != 10 {
			t.Errorf("unexpected LimitExceeded %v", le)
		}
	}
	if len(got) != 2 || seen[80] != 1 || seen[100] != 1 {
		t.Fatalf("LimitExceeded events = %v, want exactly one at 80 and one at 100", got)
	}

	all := call(t, "GET", "/v1/customers/cus_e2e/entitlements", k, nil)
	if items, _ := all.body["items"].([]any); all.status != 200 || len(items) != 1 {
		t.Fatalf("list: %d %v", all.status, all.body)
	}
	// Cancelling removes the entitlement: the meter is unlimited again.
	if r := call(t, "DELETE", "/v1/subscriptions/"+subID, k, nil); r.status != 200 {
		t.Fatalf("cancel: %d %v", r.status, r.body)
	}
	waitEntitlement(func(b map[string]any) bool { return b["unlimited"] == true }, "entitlement to be removed after cancel")
}
