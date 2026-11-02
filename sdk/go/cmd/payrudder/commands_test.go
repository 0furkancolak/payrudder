package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
)

func TestEventsSendAcceptedWithPropertiesAndAGeneratedKey(t *testing.T) {
	e := newEnv(t)
	e.gw.handle("POST /v1/events", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 202, map[string]any{"results": []any{map[string]any{"status": "accepted", "event_id": "ev-1"}}})
	})
	e.expect(0, "", "events", "send", "--customer", "cus_42", "--meter", "api_calls", "--quantity", "3.5", "--time", "2026-11-02T10:00:00Z",
		"--property", "model=gpt-x", "--property", "region=eu")
	contains(t, "stdout", e.out.String(), "accepted", "ev-1")

	var body struct {
		Events []struct {
			IdempotencyKey     string            `json:"idempotency_key"`
			CustomerExternalID string            `json:"customer_external_id"`
			MeterCode          string            `json:"meter_code"`
			Quantity           string            `json:"quantity"`
			EventTime          string            `json:"event_time"`
			Properties         map[string]string `json:"properties"`
		} `json:"events"`
	}
	reqs := e.gw.requests("POST", "/v1/events")
	if len(reqs) != 1 {
		t.Fatalf("%d calls", len(reqs))
	}
	if err := json.Unmarshal(reqs[0].body, &body); err != nil || len(body.Events) != 1 {
		t.Fatalf("body %s (%v)", reqs[0].body, err)
	}
	ev := body.Events[0]
	if ev.CustomerExternalID != "cus_42" || ev.MeterCode != "api_calls" || ev.Quantity != "3.5" || ev.EventTime != "2026-11-02T10:00:00Z" ||
		ev.Properties["model"] != "gpt-x" || ev.Properties["region"] != "eu" || !strings.HasPrefix(ev.IdempotencyKey, "sdk_") {
		t.Errorf("event %+v", ev)
	}
	if reqs[0].auth != "Bearer "+testKey {
		t.Errorf("auth %q", reqs[0].auth)
	}
}

func TestEventsSendOwnKeyDuplicateAndJSON(t *testing.T) {
	e := newEnv(t)
	e.gw.handle("POST /v1/events", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 202, map[string]any{"results": []any{map[string]any{"status": "duplicate", "event_id": "ev-0"}}})
	})
	e.expect(0, "", "events", "send", "--customer", "c", "--meter", "m", "--idempotency-key", "order-77")
	contains(t, "stdout", e.out.String(), "duplicate", "nothing was added")
	if !strings.Contains(string(e.gw.requests("POST", "/v1/events")[0].body), `"idempotency_key":"order-77"`) {
		t.Error("the given idempotency key was not sent")
	}
	e.expect(0, "", "events", "send", "--customer", "c", "--meter", "m", "--json")
	var r map[string]any
	if err := json.Unmarshal([]byte(e.out.String()), &r); err != nil || r["status"] != "duplicate" {
		t.Errorf("--json printed %q (%v)", e.out, err)
	}
}

func TestEventsSendRejectedExitsOne(t *testing.T) {
	e := newEnv(t)
	e.gw.handle("POST /v1/events", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 202, map[string]any{"results": []any{map[string]any{"status": "rejected", "error_code": "UNKNOWN_METER", "error_message": "no meter api_callz"}}})
	})
	e.expect(1, "", "events", "send", "--customer", "c", "--meter", "api_callz")
	contains(t, "stdout", e.out.String(), "rejected", "UNKNOWN_METER", "no meter api_callz")
}

func TestEventsSendBadFlagsNeverReachTheAPI(t *testing.T) {
	e := newEnv(t)
	e.expect(2, "", "events", "send", "--customer", "c", "--meter", "m", "--time", "yesterday")
	e.expect(2, "", "events", "send", "--customer", "c", "--meter", "m", "--property", "novalue")
	if e.gw.count() != 0 {
		t.Error("reached the API")
	}
}

func TestEventsTailPrintsAChangeWhenAMeterMoves(t *testing.T) {
	e := newEnv(t)
	var mu sync.Mutex
	polls := 0
	steps := []map[string]any{
		{"customer_external_id": "cus_42", "period_id": "2026-11", "usage": []any{map[string]any{"meter_code": "api_calls", "quantity": "5", "event_count": "5"}}},
		{"customer_external_id": "cus_42", "period_id": "2026-11", "usage": []any{map[string]any{"meter_code": "api_calls", "quantity": "5", "event_count": "5"}}},
		{"customer_external_id": "cus_42", "period_id": "2026-11", "usage": []any{
			map[string]any{"meter_code": "api_calls", "quantity": "8", "event_count": "8"}, map[string]any{"meter_code": "gb", "quantity": "1.5", "event_count": "1"}}},
	}
	e.gw.handle("GET /v1/customers/cus_42/usage", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Query().Get("period") != "2026-11" {
			t.Errorf("period %q", r.URL.RawQuery)
		}
		writeJSON(w, 200, steps[min(polls, len(steps)-1)])
		polls++
	})
	e.expect(0, "", "events", "tail", "--customer", "cus_42", "--period", "2026-11", "--interval", "1ms", "--count", "3")
	out := e.out.String()
	contains(t, "stdout", out, "usage of cus_42 in 2026-11", "api_calls", "5 (5 events)", "8 (+3 events)", "gb", "(+1 events, new meter)")
	if strings.Count(out, "api_calls") != 2 {
		t.Errorf("the unchanged poll should print nothing, so api_calls appears twice:\n%s", out)
	}
}

func TestEventsTailWithNoUsageSaysSoAndNotFound(t *testing.T) {
	e := newEnv(t)
	e.gw.json("GET /v1/customers/cus_1/usage", 200, map[string]any{"customer_external_id": "cus_1", "period_id": "2026-11", "usage": []any{}})
	e.expect(0, "", "events", "tail", "--customer", "cus_1", "--count", "1")
	contains(t, "stdout", e.out.String(), "no usage yet")
	e.gw.handle("GET /v1/customers/ghost/usage", func(w http.ResponseWriter, _ *http.Request) {
		apiError(w, 404, "NOT_FOUND", "customer.not_found", "no such customer")
	})
	e.expect(1, "", "events", "tail", "--customer", "ghost", "--count", "1")
	contains(t, "stderr", e.errOut.String(), "customer.not_found")
}

func page(items []map[string]any, key, next string) map[string]any {
	return map[string]any{key: items, "next_page_token": next}
}

func TestCustomersListPagesAndStopsAtTheLimit(t *testing.T) {
	e := newEnv(t)
	e.gw.handle("GET /v1/customers", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page_size") != "3" {
			t.Errorf("page_size %q: the limit should size the page", r.URL.RawQuery)
		}
		if r.URL.Query().Get("page_token") == "p2" {
			writeJSON(w, 200, page([]map[string]any{{"external_id": "cus_3", "name": "C", "block_time": "2026-10-01T00:00:00Z"}, {"external_id": "cus_4"}}, "customers", ""))
			return
		}
		writeJSON(w, 200, page([]map[string]any{{"external_id": "cus_1", "name": "Acme", "email": "a@x.io", "country": "TR", "billing_currency": "TRY", "create_time": "2026-10-20T10:00:00Z"}, {"external_id": "cus_2"}}, "customers", "p2"))
	})
	e.expect(0, "", "customers", "list", "--limit", "3")
	out := e.out.String()
	contains(t, "stdout", out, "EXTERNAL ID", "cus_1", "Acme", "a@x.io", "TR", "TRY", "2026-10-20", "cus_3", "blocked")
	lacks(t, "stdout", out, "cus_4")

	e.expect(0, "", "customers", "list", "--limit", "3", "--json")
	var list []map[string]any
	if err := json.Unmarshal([]byte(e.out.String()), &list); err != nil || len(list) != 3 || list[0]["external_id"] != "cus_1" {
		t.Errorf("--json: %v %s", err, e.out)
	}
}

func TestCustomersListEmptyJSONIsAnArray(t *testing.T) {
	e := newEnv(t)
	e.gw.json("GET /v1/customers", 200, page(nil, "customers", ""))
	e.expect(0, "", "customers", "list", "--json")
	if strings.TrimSpace(e.out.String()) != "[]" {
		t.Errorf("printed %q, want []", e.out)
	}
}

func TestInvoicesListPassesTheFilters(t *testing.T) {
	e := newEnv(t)
	e.gw.handle("GET /v1/invoices", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("customer") != "cus_1" || q.Get("status") != "paid" || q.Get("period") != "2026-10" {
			t.Errorf("query %q", r.URL.RawQuery)
		}
		writeJSON(w, 200, page([]map[string]any{{"id": "9f2c", "number": "INV-2026-000007", "customer_external_id": "cus_1", "period_id": "2026-10", "status": "paid", "total_minor": "1116", "currency": "TRY", "create_time": "2026-11-01T08:00:00Z"}}, "invoices", ""))
	})
	e.expect(0, "", "invoices", "list", "--customer", "cus_1", "--status", "paid", "--period", "2026-10")
	contains(t, "stdout", e.out.String(), "NUMBER", "INV-2026-000007", "cus_1", "paid", "1116 TRY", "2026-11-01", "9f2c", "MINOR UNITS")
	e.expect(0, "", "invoices", "list", "--customer", "cus_1", "--status", "paid", "--period", "2026-10", "--json")
	contains(t, "json", e.out.String(), `"total_minor": "1116"`)
}

func TestWebhooksListShowsEndpointsWithoutSecrets(t *testing.T) {
	e := newEnv(t)
	e.gw.json("GET /v1/webhook-endpoints", 200, map[string]any{"endpoints": []any{
		map[string]any{"id": "ep1", "url": "https://app.example.com/hook", "active": true, "event_types": []string{}, "secret": ""},
		map[string]any{"id": "ep2", "url": "https://b.example.com/hook", "active": false, "event_types": []string{"invoice.paid", "invoice.voided"}},
	}})
	e.expect(0, "", "webhooks", "list")
	contains(t, "stdout", e.out.String(), "ep1", "https://app.example.com/hook", "active", "all events", "ep2", "paused", "invoice.paid,invoice.voided")
}

func delivery(id, state string, attempts, status int) map[string]any {
	return map[string]any{"id": id, "event_id": "evt-" + id, "event_type": "ping", "endpoint_id": "ep1", "state": state, "attempts": attempts, "last_status": status, "create_time": "2026-11-02T10:00:00Z"}
}

func TestWebhooksDeliveriesWatchPrintsOnlyWhatChanged(t *testing.T) {
	e := newEnv(t)
	var mu sync.Mutex
	polls := 0
	seq := [][]map[string]any{
		{delivery("d2", "WEBHOOK_DELIVERY_STATE_PENDING", 0, 0), delivery("d1", "WEBHOOK_DELIVERY_STATE_DELIVERED", 1, 200)},
		{delivery("d2", "WEBHOOK_DELIVERY_STATE_PENDING", 0, 0), delivery("d1", "WEBHOOK_DELIVERY_STATE_DELIVERED", 1, 200)},
		{delivery("d2", "WEBHOOK_DELIVERY_STATE_FAILED", 1, 500), delivery("d1", "WEBHOOK_DELIVERY_STATE_DELIVERED", 1, 200)},
	}
	e.gw.handle("GET /v1/webhook-deliveries", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if q := r.URL.Query(); q.Get("endpoint_id") != "ep1" || q.Get("state") != "" || q.Get("page_size") != "20" {
			t.Errorf("query %q", r.URL.RawQuery)
		}
		writeJSON(w, 200, page(seq[min(polls, len(seq)-1)], "deliveries", ""))
		polls++
	})
	e.expect(0, "", "webhooks", "deliveries", "--endpoint", "ep1", "--watch", "--interval", "1ms", "--count", "3")
	lines := strings.Split(strings.TrimSpace(e.out.String()), "\n")
	// Oldest first on the first poll (d1, d2), nothing for the unchanged second poll, then d2 again with its new state.
	if len(lines) != 3 || !strings.Contains(lines[0], "d1") || !strings.Contains(lines[0], "delivered") || !strings.Contains(lines[0], "HTTP 200") ||
		!strings.Contains(lines[1], "d2") || !strings.Contains(lines[1], "pending") || !strings.Contains(lines[2], "d2") || !strings.Contains(lines[2], "failed") || !strings.Contains(lines[2], "HTTP 500") {
		t.Errorf("lines:\n%s", e.out)
	}
}

func TestWebhooksDeliveriesOnceAndJSONAndLimits(t *testing.T) {
	e := newEnv(t)
	e.gw.json("GET /v1/webhook-deliveries", 200, page(nil, "deliveries", ""))
	e.expect(0, "", "webhooks", "deliveries")
	contains(t, "stdout", e.out.String(), "no deliveries")
	e.expect(0, "", "webhooks", "deliveries", "--json")
	e.expect(2, "", "webhooks", "deliveries", "--limit", "500")
	e.expect(2, "", "webhooks", "deliveries", "--watch", "--json")
}

// twoEndpoints and oneEndpoint answer GET /v1/webhook-endpoints.
func endpoints(ids ...string) map[string]any {
	var list []any
	for _, id := range ids {
		list = append(list, map[string]any{"id": id, "url": "https://" + id + ".example.com/hook", "active": true})
	}
	return map[string]any{"endpoints": list}
}

func TestWebhooksTriggerPingWaitsForTheAnswer(t *testing.T) {
	e := newEnv(t)
	e.gw.json("GET /v1/webhook-endpoints", 200, endpoints("ep1"))
	e.gw.json("POST /v1/webhook-endpoints/ep1/test", 202, delivery("d1", "WEBHOOK_DELIVERY_STATE_PENDING", 0, 0))
	var mu sync.Mutex
	asked := 0
	e.gw.handle("GET /v1/webhook-deliveries", func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		asked++
		d := delivery("d1", "WEBHOOK_DELIVERY_STATE_PENDING", 0, 0)
		if asked >= 3 {
			d = delivery("d1", "WEBHOOK_DELIVERY_STATE_DELIVERED", 1, 204)
		}
		writeJSON(w, 200, page([]map[string]any{delivery("other", "WEBHOOK_DELIVERY_STATE_FAILED", 3, 500), d}, "deliveries", ""))
	})
	e.expect(0, "", "webhooks", "trigger", "ping", "--interval", "1ms")
	contains(t, "stdout", e.out.String(), "Sent a ping", "evt-d1", "https://ep1.example.com/hook", "Delivered: your endpoint answered HTTP 204")
	if asked < 3 {
		t.Errorf("asked %d times: it should wait for the answer", asked)
	}
}

func TestWebhooksTriggerReportsAnEndpointThatRefused(t *testing.T) {
	e := newEnv(t)
	e.gw.json("GET /v1/webhook-endpoints", 200, endpoints("ep1"))
	e.gw.json("POST /v1/webhook-endpoints/ep1/test", 202, delivery("d1", "WEBHOOK_DELIVERY_STATE_PENDING", 0, 0))
	failed := delivery("d1", "WEBHOOK_DELIVERY_STATE_PENDING", 1, 500)
	failed["last_error"] = `{"error":"boom"}`
	e.gw.json("GET /v1/webhook-deliveries", 200, page([]map[string]any{failed}, "deliveries", ""))
	e.expect(1, "", "webhooks", "trigger", "ping", "--interval", "1ms")
	contains(t, "stdout", e.out.String(), "Not accepted: HTTP 500", "boom", "will try again")
}

func TestWebhooksTriggerGivesUpWhenNothingAnswers(t *testing.T) {
	e := newEnv(t)
	e.gw.json("GET /v1/webhook-endpoints", 200, endpoints("ep1"))
	e.gw.json("POST /v1/webhook-endpoints/ep1/test", 202, delivery("d1", "WEBHOOK_DELIVERY_STATE_PENDING", 0, 0))
	e.gw.json("GET /v1/webhook-deliveries", 200, page([]map[string]any{delivery("d1", "WEBHOOK_DELIVERY_STATE_PENDING", 0, 0)}, "deliveries", ""))
	e.expect(1, "", "webhooks", "trigger", "ping", "--interval", "1ms", "--wait", "30ms")
	contains(t, "stdout", e.out.String(), "No answer after 30ms", "still queued")
}

func TestWebhooksTriggerChoosesTheEndpointCarefully(t *testing.T) {
	e := newEnv(t)
	e.gw.json("GET /v1/webhook-endpoints", 200, endpoints("ep1", "ep2"))
	e.gw.json("POST /v1/webhook-endpoints/ep2/test", 202, delivery("d9", "WEBHOOK_DELIVERY_STATE_DELIVERED", 1, 200))
	e.gw.json("GET /v1/webhook-deliveries", 200, page([]map[string]any{delivery("d9", "WEBHOOK_DELIVERY_STATE_DELIVERED", 1, 200)}, "deliveries", ""))

	e.expect(2, "", "webhooks", "trigger", "ping")
	contains(t, "stderr", e.errOut.String(), "several endpoints", "--endpoint")
	if len(e.gw.requests("POST", "/v1/webhook-endpoints/ep2/test"))+len(e.gw.requests("POST", "/v1/webhook-endpoints/ep1/test")) != 0 {
		t.Error("an ambiguous trigger sent a test")
	}
	e.expect(1, "", "webhooks", "trigger", "ping", "--endpoint", "nope")
	contains(t, "stderr", e.errOut.String(), "no endpoint with id nope")
	e.expect(0, "", "webhooks", "trigger", "ping", "--endpoint", "ep2", "--interval", "1ms")
}

func TestWebhooksTriggerOnlyKnowsPingAndNeedsAnEndpoint(t *testing.T) {
	e := newEnv(t)
	e.gw.json("GET /v1/webhook-endpoints", 200, endpoints())
	e.expect(2, "", "webhooks", "trigger", "invoice.paid")
	contains(t, "stderr", e.errOut.String(), `only "ping"`, "invoice.paid")
	e.expect(2, "", "webhooks", "trigger")
	e.expect(1, "", "webhooks", "trigger", "ping")
	contains(t, "stderr", e.errOut.String(), "no webhook endpoint yet")
}

func TestUserAgentIsTheCLI(t *testing.T) {
	e := newEnv(t)
	e.gw.json("GET /v1/customers", 200, page(nil, "customers", ""))
	e.expect(0, "", "customers", "list")
	if ua := e.gw.requests("GET", "/v1/customers")[0].ua; ua != fmt.Sprintf("payrudder-cli/%s", "0.0.0") {
		t.Errorf("user agent %q", ua)
	}
}
