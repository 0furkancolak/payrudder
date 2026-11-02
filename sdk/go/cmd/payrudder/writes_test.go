package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// body decodes the one request the gateway saw for the route.
func (g *gateway) body(t *testing.T, method, path string) map[string]any {
	t.Helper()
	reqs := g.requests(method, path)
	if len(reqs) != 1 {
		t.Fatalf("%s %s: %d calls, want 1", method, path, len(reqs))
	}
	var b map[string]any
	if err := json.Unmarshal(reqs[0].body, &b); err != nil {
		t.Fatalf("body %s (%v)", reqs[0].body, err)
	}
	return b
}

func TestCustomersCreateSendsTheCustomerAndPrintsIt(t *testing.T) {
	e := newEnv(t)
	e.gw.handle("POST /v1/customers", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 201, map[string]any{"id": "c-1", "external_id": "cus_42", "name": "Ada Lovelace", "email": "ada@example.com", "country": "TR"})
	})
	e.expect(0, "", "customers", "create", "--id", "cus_42", "--name", "Ada Lovelace", "--email", "ada@example.com", "--country", "TR", "--locale", "tr", "--type", "individual")
	contains(t, "stdout", e.out.String(), "created customer cus_42", "Ada Lovelace", "ada@example.com")

	reqs := e.gw.requests("POST", "/v1/customers")
	if len(reqs) != 1 {
		t.Fatalf("%d calls", len(reqs))
	}
	var body map[string]any
	if err := json.Unmarshal(reqs[0].body, &body); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"external_id": "cus_42", "name": "Ada Lovelace", "email": "ada@example.com", "country": "TR", "locale": "tr", "customer_type": "individual"}
	for k, v := range want {
		if body[k] != v {
			t.Errorf("%s = %v, want %s (body %s)", k, body[k], v, reqs[0].body)
		}
	}
	if _, ok := body["billing_currency"]; ok {
		t.Errorf("a flag that was not given was sent: %s", reqs[0].body)
	}

	e.expect(0, "", "customers", "create", "--id", "cus_42", "--name", "n", "--email", "e@example.com", "--json")
	var c map[string]any
	if err := json.Unmarshal([]byte(e.out.String()), &c); err != nil || c["external_id"] != "cus_42" {
		t.Errorf("--json printed %q (%v)", e.out, err)
	}
}

func TestCustomersCreateTwiceIsTheAPIs409AndExitsOne(t *testing.T) {
	e := newEnv(t)
	e.gw.handle("POST /v1/customers", func(w http.ResponseWriter, _ *http.Request) {
		apiError(w, 409, "ALREADY_EXISTS", "customer.already_exists", "a customer with this external id exists")
	})
	e.expect(1, "", "customers", "create", "--id", "cus_42", "--name", "n", "--email", "e@example.com")
	contains(t, "stderr", e.errOut.String(), "customer.already_exists")
}

func TestPlansCreateSendsCodeAndNameAndSaysAVersionIsNext(t *testing.T) {
	e := newEnv(t)
	e.gw.json("POST /v1/plans", 201, map[string]any{"id": "p-1", "code": "pro", "name": "Pro", "latest_version": 0})
	e.expect(0, "", "plans", "create", "--code", "pro", "--name", "Pro")
	contains(t, "stdout", e.out.String(), "created plan pro", "no version yet", "plans publish --code pro -f")
	reqs := e.gw.requests("POST", "/v1/plans")
	if len(reqs) != 1 {
		t.Fatalf("%d calls", len(reqs))
	}
	var body map[string]string
	if err := json.Unmarshal(reqs[0].body, &body); err != nil || body["code"] != "pro" || body["name"] != "Pro" || len(body) != 2 {
		t.Fatalf("body %s (%v)", reqs[0].body, err)
	}
	e.expect(0, "", "plans", "create", "--code", "pro", "--name", "Pro", "--json")
	var p map[string]any
	if err := json.Unmarshal([]byte(e.out.String()), &p); err != nil || p["code"] != "pro" {
		t.Errorf("--json printed %q (%v)", e.out, err)
	}
}

func TestWebhooksRedeliverQueuesTheDeliveryAgain(t *testing.T) {
	e := newEnv(t)
	e.gw.json("POST /v1/webhook-deliveries/d-1/redeliver", 202, map[string]any{
		"id": "d-1", "event_id": "ev-9", "event_type": "invoice.paid", "endpoint_id": "ep-1", "state": "WEBHOOK_DELIVERY_STATE_PENDING", "attempts": 0,
	})
	e.expect(0, "", "webhooks", "redeliver", "d-1")
	contains(t, "stdout", e.out.String(), "queued delivery d-1 again", "invoice.paid", "ev-9", "--endpoint ep-1")
	if n := len(e.gw.requests("POST", "/v1/webhook-deliveries/d-1/redeliver")); n != 1 {
		t.Fatalf("%d calls", n)
	}
	e.expect(0, "", "webhooks", "redeliver", "d-1", "--json")
	var d map[string]any
	if err := json.Unmarshal([]byte(e.out.String()), &d); err != nil || d["event_id"] != "ev-9" {
		t.Errorf("--json printed %q (%v)", e.out, err)
	}
	e.gw.handle("POST /v1/webhook-deliveries/ghost/redeliver", func(w http.ResponseWriter, _ *http.Request) {
		apiError(w, 404, "NOT_FOUND", "webhook.delivery.not_found", "no such delivery")
	})
	e.expect(1, "", "webhooks", "redeliver", "ghost")
	contains(t, "stderr", e.errOut.String(), "webhook.delivery.not_found")
}

func TestTheWritingCommandsRefuseAnIncompleteCommandLineBeforeTheAPI(t *testing.T) {
	e := newEnv(t)
	for _, args := range [][]string{
		{"customers", "create"}, {"customers", "create", "--id", "c", "--name", "n"}, {"plans", "create", "--code", "pro"}, {"plans"}, {"plans", "list"},
		{"webhooks", "redeliver"}, {"webhooks", "redeliver", "a", "b"}, {"webhooks", "create"}, {"meters", "create", "--code", "m", "--unit", "u"}, {"meters", "create", "--code", "m", "--aggregation", "sum"},
		{"meters", "create", "--code", "m", "--aggregation", "sum", "--unit", "u", "--filter", "novalue"}, {"subscriptions", "create", "--customer", "c"}, {"subscriptions", "create", "--plan", "p"},
		{"subscriptions", "create", "--customer", "c", "--plan", "p", "--start-time", "yesterday"}, {"plans", "publish", "-f", "v.json"}, {"plans", "publish", "--code", "pro"}, {"meters"}, {"subscriptions", "list"},
	} {
		if code := e.run("", args...); code != 2 {
			t.Errorf("payrudder %v: exit %d, want 2 (%s)", args, code, e.errOut)
		}
	}
	if e.gw.count() != 0 {
		t.Error("a malformed command line reached the API")
	}
}

func TestWebhooksCreateRegistersTheEndpointPrintsTheSecretAndNeverRetries(t *testing.T) {
	e := newEnv(t)
	e.gw.json("POST /v1/webhook-endpoints", 201, map[string]any{
		"id": "ep-1", "url": "https://example.com/hook", "event_types": []string{"invoice.paid", "invoice.voided"}, "active": true, "secret": "whsec_abc123",
	})
	e.expect(0, "", "webhooks", "create", "--url", "https://example.com/hook", "--description", "prod", "--events", "invoice.paid, invoice.voided")
	contains(t, "stdout", e.out.String(), "created endpoint ep-1", "https://example.com/hook", "invoice.paid,invoice.voided", "shown only now", "whsec_abc123")
	b := e.gw.body(t, "POST", "/v1/webhook-endpoints")
	if b["url"] != "https://example.com/hook" || b["description"] != "prod" || toString(b["event_types"]) != `["invoice.paid","invoice.voided"]` {
		t.Errorf("body %v", b)
	}
	e.expect(0, "", "webhooks", "create", "--url", "https://example.com/hook", "--json")
	var ep map[string]any
	if err := json.Unmarshal([]byte(e.out.String()), &ep); err != nil || ep["secret"] != "whsec_abc123" {
		t.Errorf("--json printed %q (%v)", e.out, err)
	}

	// No key and no natural unique key: a 503 is shown, not retried into a second endpoint.
	e2 := newEnv(t)
	e2.gw.handle("POST /v1/webhook-endpoints", func(w http.ResponseWriter, _ *http.Request) {
		apiError(w, 503, "UNAVAILABLE", "gateway.unavailable", "try later")
	})
	e2.expect(1, "", "webhooks", "create", "--url", "https://example.com/hook")
	if n := len(e2.gw.requests("POST", "/v1/webhook-endpoints")); n != 1 {
		t.Errorf("%d attempts, want 1", n)
	}
}

func toString(v any) string { b, _ := json.Marshal(v); return string(b) }

func TestMetersCreateSpellsTheAggregationAndSendsOnlyWhatWasGiven(t *testing.T) {
	e := newEnv(t)
	e.gw.json("POST /v1/meters", 201, map[string]any{"code": "api_calls", "aggregation": "AGGREGATION_SUM", "value_property": "n", "unit_label": "calls", "quantity_scale": 2, "version": 1})
	e.expect(0, "", "meters", "create", "--code", "api_calls", "--aggregation", "sum", "--unit", "calls", "--value-property", "n", "--scale", "2", "--category", "ai",
		"--filter", "model=gpt-x,gpt-y", "--filter", "region=eu")
	contains(t, "stdout", e.out.String(), "meter api_calls is at version 1", "sum", "unit calls")
	b := e.gw.body(t, "POST", "/v1/meters")
	if b["aggregation"] != "AGGREGATION_SUM" || b["unit_label"] != "calls" || b["value_property"] != "n" || b["quantity_scale"] != float64(2) || b["category"] != "ai" {
		t.Errorf("body %v", b)
	}
	if f := toString(b["filter"]); f != `{"model":{"values":["gpt-x","gpt-y"]},"region":{"values":["eu"]}}` {
		t.Errorf("filter %s", f)
	}

	e = newEnv(t)
	e.gw.json("POST /v1/meters", 201, map[string]any{"code": "seats", "aggregation": "AGGREGATION_COUNT", "unit_label": "seats", "version": 2})
	e.expect(0, "", "meters", "create", "--code", "seats", "--aggregation", "AGGREGATION_COUNT", "--unit", "seats", "--json")
	b = e.gw.body(t, "POST", "/v1/meters")
	if b["aggregation"] != "AGGREGATION_COUNT" {
		t.Errorf("the full enum name was changed: %v", b)
	}
	for _, k := range []string{"quantity_scale", "value_property", "filter", "category"} {
		if _, ok := b[k]; ok {
			t.Errorf("%s was sent without being given: %v", k, b)
		}
	}
	// A scale of 0 is a choice (whole numbers), not "not given".
	e.expect(0, "", "meters", "create", "--code", "seats", "--aggregation", "count", "--unit", "seats", "--scale", "0")
	if got := e.gw.requests("POST", "/v1/meters"); len(got) != 2 || !strings.Contains(string(got[1].body), `"quantity_scale":0`) {
		t.Errorf("a scale of 0 was left out: %+v", got)
	}

	e = newEnv(t)
	e.gw.handle("POST /v1/meters", func(w http.ResponseWriter, _ *http.Request) {
		apiError(w, 400, "INVALID_ARGUMENT", "meter.invalid", "value_property is required")
	})
	e.expect(1, "", "meters", "create", "--code", "m", "--aggregation", "sum", "--unit", "u")
	contains(t, "stderr", e.errOut.String(), "meter.invalid")
}

func TestSubscriptionsCreateSendsTheChoicesAndTheAPIsConflictExitsOne(t *testing.T) {
	e := newEnv(t)
	e.gw.json("POST /v1/subscriptions", 201, map[string]any{
		"id": "sub-1", "customer_external_id": "cus_42", "plan_code": "pro", "plan_version": 3, "state": "SUBSCRIPTION_STATE_TRIALING", "interval": "SUBSCRIPTION_INTERVAL_YEAR",
	})
	e.expect(0, "", "subscriptions", "create", "--customer", "cus_42", "--plan", "pro", "--plan-version", "3", "--interval", "year", "--trial-days", "0", "--quantity", "5",
		"--fee-timing", "arrears", "--currency", "EUR", "--coupon", "WELCOME", "--provider", "paddle", "--start-time", "2026-11-02T10:00:00Z")
	contains(t, "stdout", e.out.String(), "subscribed cus_42 to pro version 3", "subscription sub-1", "trialing", "year")
	b := e.gw.body(t, "POST", "/v1/subscriptions")
	want := map[string]any{
		"customer_external_id": "cus_42", "plan_code": "pro", "plan_version": float64(3), "interval": "SUBSCRIPTION_INTERVAL_YEAR", "trial_days": float64(0), "quantity": float64(5),
		"fee_timing": "FEE_TIMING_ARREARS", "currency": "EUR", "coupon_code": "WELCOME", "provider": "paddle", "start_time": "2026-11-02T10:00:00Z",
	}
	for k, v := range want {
		if b[k] != v {
			t.Errorf("%s = %v, want %v (body %v)", k, b[k], v, b)
		}
	}

	e = newEnv(t)
	e.gw.json("POST /v1/subscriptions", 201, map[string]any{"id": "sub-2", "customer_external_id": "c", "plan_code": "pro", "plan_version": 1, "state": "SUBSCRIPTION_STATE_ACTIVE", "interval": "SUBSCRIPTION_INTERVAL_MONTH"})
	e.expect(0, "", "subscriptions", "create", "--customer", "c", "--plan", "pro", "--json")
	b = e.gw.body(t, "POST", "/v1/subscriptions")
	for _, k := range []string{"plan_version", "trial_days", "quantity", "interval", "fee_timing", "start_time"} {
		if _, ok := b[k]; ok {
			t.Errorf("%s was sent without being given: %v", k, b)
		}
	}

	e = newEnv(t)
	e.gw.handle("POST /v1/subscriptions", func(w http.ResponseWriter, _ *http.Request) {
		apiError(w, 409, "ALREADY_EXISTS", "subscription.overlap", "the customer has a subscription")
	})
	e.expect(1, "", "subscriptions", "create", "--customer", "c", "--plan", "pro")
	contains(t, "stderr", e.errOut.String(), "subscription.overlap")
}

func TestPlansPublishSendsTheFileAsTheVersionAndRefusesWhatItCannotRead(t *testing.T) {
	e := newEnv(t)
	e.gw.json("POST /v1/plans/pro/versions", 201, map[string]any{"id": "v-3", "plan_code": "pro", "version": 3, "base_fee_minor": "1900", "prices": []any{map[string]any{}, map[string]any{}}, "entitlements": []any{map[string]any{}}})
	doc := `{"base_fee_minor": "1900", "trial_days": 14, "prices": [{"meter_code": "api_calls", "per_unit": {"unit_price": "0.002"}}, {"meter_code": "gb", "per_unit": {"unit_price": "0.1"}}],
	  "entitlements": [{"meter_code": "api_calls", "limit": "100000"}], "fees": [{"currency": "EUR", "base_fee_minor": "1900", "interval": "SUBSCRIPTION_INTERVAL_YEAR"}]}`
	path := filepath.Join(t.TempDir(), "pro.json")
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	e.expect(0, "", "plans", "publish", "--code", "pro", "-f", path)
	contains(t, "stdout", e.out.String(), "published version 3 of plan pro", "base fee 1900 minor units", "2 prices", "1 entitlements")
	b := e.gw.body(t, "POST", "/v1/plans/pro/versions")
	if b["base_fee_minor"] != "1900" || b["trial_days"] != float64(14) || len(b["prices"].([]any)) != 2 || len(b["entitlements"].([]any)) != 1 || len(b["fees"].([]any)) != 1 {
		t.Errorf("body %v", b)
	}

	// "-f -" and --file read the standard input and the same file.
	e.expect(0, doc, "plans", "publish", "--code", "pro", "--file", "-", "--json")
	var v map[string]any
	if err := json.Unmarshal([]byte(e.out.String()), &v); err != nil || v["version"] != float64(3) {
		t.Errorf("--json printed %q (%v)", e.out, err)
	}

	// An immutable version: a field the API does not know (a typo would publish a free plan), a second document and an unreadable file stop before the request.
	before := len(e.gw.requests("POST", "/v1/plans/pro/versions"))
	for name, in := range map[string]string{"typo": `{"base_fee": "1900"}`, "two documents": `{} {}`, "not json": `base fee 19`, "wrong type": `{"prices": "none"}`} {
		if code := e.run(in, "plans", "publish", "--code", "pro", "-f", "-"); code != 1 {
			t.Errorf("%s: exit %d, want 1 (%s)", name, code, e.errOut)
		}
	}
	if code := e.run("", "plans", "publish", "--code", "pro", "-f", filepath.Join(t.TempDir(), "missing.json")); code != 1 {
		t.Errorf("a missing file: exit %d, want 1", code)
	}
	if after := len(e.gw.requests("POST", "/v1/plans/pro/versions")); after != before {
		t.Errorf("%d requests reached the API for a file that cannot be a version", after-before)
	}

	e.gw.handle("POST /v1/plans/ghost/versions", func(w http.ResponseWriter, _ *http.Request) {
		apiError(w, 404, "NOT_FOUND", "plan.not_found", "no such plan")
	})
	e.expect(1, "{}", "plans", "publish", "--code", "ghost", "-f", "-")
	contains(t, "stderr", e.errOut.String(), "plan.not_found")
}
