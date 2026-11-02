package main

import (
	"encoding/json"
	"net/http"
	"testing"
)

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
	contains(t, "stdout", e.out.String(), "created plan pro", "no version yet", "/v1/plans/pro/versions")
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
		{"webhooks", "redeliver"}, {"webhooks", "redeliver", "a", "b"},
	} {
		if code := e.run("", args...); code != 2 {
			t.Errorf("payrudder %v: exit %d, want 2 (%s)", args, code, e.errOut)
		}
	}
	if e.gw.count() != 0 {
		t.Error("a malformed command line reached the API")
	}
}
