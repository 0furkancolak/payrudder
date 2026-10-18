//go:build e2e

package e2e

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/0furkancolak/quotify/pkg/stubworker"
)

var rabbitURL = envOr("QUOTIFY_RABBITMQ_URL", "amqp://quotify:quotify_local@localhost:5672/")

// TestSandboxBillingEndToEnd closes a sandbox period on demand and follows the real flow: early close, watermark,
// rating, numbering, the invoice saga over RabbitMQ (answered by the stub worker until the real workers exist) and the
// invoice API.
func TestSandboxBillingEndToEnd(t *testing.T) {
	a := newAccount(t, "TENANT_TIER_STANDARD")
	k := a.sandboxKey

	stub := stubworker.New(rabbitURL, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = stub.Run(ctx) }()

	for _, step := range []struct {
		path string
		body map[string]any
	}{
		{"/v1/meters", map[string]any{"code": "seats", "aggregation": "AGGREGATION_SUM", "value_property": "n", "quantity_scale": 0, "unit_label": "seats"}},
		{"/v1/plans", map[string]any{"code": "team", "name": "Team"}},
		{"/v1/plans/team/versions", map[string]any{"base_fee_minor": 1000,
			"prices": []any{map[string]any{"meter_code": "seats", "per_unit": map[string]any{"unit_price": "1"}}}}},
		{"/v1/customers", map[string]any{"external_id": "cus_e2e", "name": "E2E", "email": "e2e@example.com", "payment_method": "pm_sandbox_success"}},
		{"/v1/subscriptions", map[string]any{"customer_external_id": "cus_e2e", "plan_code": "team"}},
	} {
		if r := call(t, "POST", step.path, k, step.body); r.status != 201 {
			t.Fatalf("POST %s: %d %v", step.path, r.status, r.body)
		}
	}
	at := time.Now().Add(-time.Minute)
	evs := make([]any, 5)
	for i := range evs {
		evs[i] = event("", "seats", 1, at)
	}
	if r := call(t, "POST", "/v1/events", k, map[string]any{"events": evs}); r.status != 202 {
		t.Fatalf("events: %d %v", r.status, r.body)
	}
	// Let the aggregator see them before the period closes (the watermark would wait for it anyway).
	if r := call(t, "POST", "/v1/sandbox/close-period", a.liveKey, nil); r.status != 403 {
		t.Fatalf("a live key must not close periods early: %d %v", r.status, r.body)
	}
	if r := call(t, "POST", "/v1/sandbox/close-period", k, nil); r.status != 202 {
		t.Fatalf("close-period: %d %v", r.status, r.body)
	}

	var inv map[string]any
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		r := call(t, "GET", "/v1/invoices?customer=cus_e2e", k, nil)
		if items, _ := r.body["invoices"].([]any); r.status == 200 && len(items) == 1 {
			inv = items[0].(map[string]any)
			if inv["saga_state"] == "COMPLETED" {
				break
			}
		}
		time.Sleep(2 * time.Second)
	}
	if inv == nil || inv["saga_state"] != "COMPLETED" {
		t.Fatalf("the invoice did not complete within 3 minutes: %v", inv)
	}
	if !strings.HasPrefix(inv["number"].(string), "SBX-") || inv["total_minor"] != "1005" || inv["status"] != "paid" || inv["currency"] != "TRY" {
		t.Fatalf("invoice = %v, want SBX-… number, 1005 TRY, paid", inv)
	}
	full := call(t, "GET", "/v1/invoices/"+inv["id"].(string), k, nil)
	if lines, _ := full.body["lines"].([]any); full.status != 200 || len(lines) != 2 {
		t.Fatalf("invoice detail: %d %v", full.status, full.body)
	}
	for _, c := range stub.Commands() {
		if c.GetEnvironment().String() != "ENVIRONMENT_SANDBOX" {
			t.Fatalf("a %s command carried environment %v", stubworker.Kind(c), c.GetEnvironment())
		}
	}
	// The same close again is idempotent: still one invoice.
	call(t, "POST", "/v1/sandbox/close-period", k, nil)
	time.Sleep(3 * time.Second)
	if r := call(t, "GET", "/v1/invoices?customer=cus_e2e", k, nil); len(r.body["invoices"].([]any)) != 1 {
		t.Fatalf("second close created another invoice: %v", r.body)
	}
}
