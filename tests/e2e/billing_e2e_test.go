//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

var (
	mockPSPURL = envOr("MOCKPSP_URL", "http://localhost:8090")
	mailpitURL = envOr("MAILPIT_URL", "http://localhost:8025")
)

func getJSON(t *testing.T, u string, out any) {
	t.Helper()
	resp, err := http.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		t.Fatalf("GET %s: %v", u, err)
	}
}

type pspCharge struct {
	Key         string `json:"idempotency_key"`
	Status      string `json:"status"`
	AmountMinor int64  `json:"amount_minor"`
}

func pspCharges(t *testing.T, invoiceID string) []pspCharge {
	t.Helper()
	var out struct {
		Charges []pspCharge `json:"charges"`
	}
	getJSON(t, mockPSPURL+"/v1/charges?invoice_id="+url.QueryEscape(invoiceID), &out)
	return out.Charges
}

type mail struct {
	Subject string
	To      []struct{ Address string }
}

func mailTo(t *testing.T, addr string) []mail {
	t.Helper()
	var out struct {
		Messages []mail `json:"messages"`
	}
	getJSON(t, mailpitURL+"/api/v1/search?query="+url.QueryEscape("to:"+addr), &out)
	return out.Messages
}

func setupSandbox(t *testing.T, a account, customers map[string]string) {
	t.Helper()
	k := a.sandboxKey
	for _, step := range []struct {
		path string
		body map[string]any
	}{
		{"/v1/meters", map[string]any{"code": "seats", "aggregation": "AGGREGATION_SUM", "value_property": "n", "quantity_scale": 0, "unit_label": "seats"}},
		{"/v1/plans", map[string]any{"code": "team", "name": "Team"}},
		{"/v1/plans/team/versions", map[string]any{"base_fee_minor": 1000,
			"prices": []any{map[string]any{"meter_code": "seats", "per_unit": map[string]any{"unit_price": "1"}}}}},
	} {
		if r := call(t, "POST", step.path, k, step.body); r.status != 201 {
			t.Fatalf("POST %s: %d %v", step.path, r.status, r.body)
		}
	}
	for ext, token := range customers {
		if r := call(t, "POST", "/v1/customers", k, map[string]any{"external_id": ext, "name": "Customer " + ext, "email": ext + "@example.com", "payment_method": token}); r.status != 201 {
			t.Fatalf("customer %s: %d %v", ext, r.status, r.body)
		}
		if r := call(t, "POST", "/v1/subscriptions", k, map[string]any{"customer_external_id": ext, "plan_code": "team"}); r.status != 201 {
			t.Fatalf("subscription %s: %d %v", ext, r.status, r.body)
		}
	}
}

// waitInvoices polls until every customer's invoice saga is COMPLETED and returns the invoices by customer.
func waitInvoices(t *testing.T, key string, customers []string, within time.Duration) map[string]map[string]any {
	t.Helper()
	got := map[string]map[string]any{}
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		r := call(t, "GET", "/v1/invoices?page_size=100", key, nil)
		items, _ := r.body["invoices"].([]any)
		got = map[string]map[string]any{}
		done := 0
		for _, it := range items {
			inv := it.(map[string]any)
			got[inv["customer_external_id"].(string)] = inv
			if inv["saga_state"] == "COMPLETED" {
				done++
			}
		}
		if r.status == 200 && len(got) == len(customers) && done == len(customers) {
			return got
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("invoices did not complete within %s: %v", within, got)
	return nil
}

// TestSandboxBillingEndToEnd closes a sandbox period on demand and follows the real flow with the real workers: early
// close, watermark, rating, numbering, the invoice saga over RabbitMQ, the PDF in MinIO, the payment in mock-psp and the
// email in Mailpit.
func TestSandboxBillingEndToEnd(t *testing.T) {
	a := newAccount(t, "TENANT_TIER_STANDARD")
	k := a.sandboxKey
	setupSandbox(t, a, map[string]string{"cus_e2e": "pm_sandbox_success"})
	at := time.Now().Add(-time.Minute)
	evs := make([]any, 5)
	for i := range evs {
		evs[i] = event("", "seats", 1, at)
	}
	if r := call(t, "POST", "/v1/events", k, map[string]any{"events": evs}); r.status != 202 {
		t.Fatalf("events: %d %v", r.status, r.body)
	}
	if r := call(t, "POST", "/v1/sandbox/close-period", a.liveKey, nil); r.status != 403 {
		t.Fatalf("a live key must not close periods early: %d %v", r.status, r.body)
	}
	if r := call(t, "POST", "/v1/sandbox/close-period", k, nil); r.status != 202 {
		t.Fatalf("close-period: %d %v", r.status, r.body)
	}

	inv := waitInvoices(t, k, []string{"cus_e2e"}, 4*time.Minute)["cus_e2e"]
	if !strings.HasPrefix(inv["number"].(string), "SBX-") || inv["total_minor"] != "1005" || inv["status"] != "paid" || inv["currency"] != "TRY" {
		t.Fatalf("invoice = %v, want SBX-… number, 1005 TRY, paid", inv)
	}
	id := inv["id"].(string)

	// The PDF is in object storage and reachable through the presigned link.
	pdf := call(t, "GET", "/v1/invoices/"+id+"/pdf", k, nil)
	if pdf.status != 200 {
		t.Fatalf("pdf link: %d %v", pdf.status, pdf.body)
	}
	resp, err := http.Get(pdf.body["url"].(string))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.HasPrefix(string(b), "%PDF-") || !strings.Contains(string(b), "SANDBOX") {
		t.Fatalf("presigned download: %d, %d bytes, sandbox watermark %v", resp.StatusCode, len(b), strings.Contains(string(b), "SANDBOX"))
	}
	// Exactly one charge reached the provider, for the invoice total.
	if ch := pspCharges(t, id); len(ch) != 1 || ch[0].Status != "succeeded" || ch[0].AmountMinor != 1005 {
		t.Fatalf("mock-psp charges = %+v, want one succeeded charge of 1005", ch)
	}
	// And the customer got the receipt, marked as sandbox.
	deadline := time.Now().Add(30 * time.Second)
	var msgs []mail
	for time.Now().Before(deadline) && len(msgs) == 0 {
		msgs = mailTo(t, "cus_e2e@example.com")
		time.Sleep(500 * time.Millisecond)
	}
	if len(msgs) != 1 || !strings.HasPrefix(msgs[0].Subject, "[SANDBOX] Receipt for invoice SBX-") {
		t.Fatalf("mailbox = %+v", msgs)
	}

	// The same close again is idempotent: still one invoice.
	call(t, "POST", "/v1/sandbox/close-period", k, nil)
	time.Sleep(3 * time.Second)
	if r := call(t, "GET", "/v1/invoices?customer=cus_e2e", k, nil); len(r.body["invoices"].([]any)) != 1 {
		t.Fatalf("second close created another invoice: %v", r.body)
	}
}

// TestSandboxPaymentScenarios gives each pm_sandbox_* token its own customer and checks where each saga ends.
func TestSandboxPaymentScenarios(t *testing.T) {
	a := newAccount(t, "TENANT_TIER_STANDARD")
	k := a.sandboxKey
	tokens := map[string]string{
		"cus_success": "pm_sandbox_success", "cus_declined": "pm_sandbox_declined", "cus_nsf": "pm_sandbox_insufficient_funds_once",
		"cus_errors": "pm_sandbox_error_then_success", "cus_tcommit": "pm_sandbox_timeout_committed", "cus_tnot": "pm_sandbox_timeout_not_committed",
	}
	setupSandbox(t, a, tokens)
	if r := call(t, "POST", "/v1/sandbox/close-period", k, nil); r.status != 202 {
		t.Fatalf("close-period: %d %v", r.status, r.body)
	}
	names := make([]string, 0, len(tokens))
	for n := range tokens {
		names = append(names, n)
	}
	invs := waitInvoices(t, k, names, 8*time.Minute)

	want := map[string]struct {
		status    string
		succeeded int
		total     int
	}{
		"cus_success":  {"paid", 1, 1},
		"cus_declined": {"uncollectible", 0, 4}, // the charge and three dunning retries, all declined
		"cus_nsf":      {"paid", 1, 2},          // declined once, the dunning retry succeeds
		"cus_errors":   {"paid", 1, 1},          // two 5xx answers are retried by the worker under the same key
		"cus_tcommit":  {"paid", 1, 1},          // PAYMENT_UNKNOWN → status query finds the committed charge
		"cus_tnot":     {"paid", 1, 1},          // PAYMENT_UNKNOWN → not found → re-charge under the same key
	}
	for name, w := range want {
		inv := invs[name]
		if inv["status"] != w.status {
			t.Errorf("%s: invoice status %v, want %s", name, inv["status"], w.status)
		}
		ch := pspCharges(t, inv["id"].(string))
		succeeded := 0
		for _, c := range ch {
			if c.Status == "succeeded" {
				succeeded++
			}
		}
		if succeeded != w.succeeded || len(ch) != w.total {
			t.Errorf("%s: %d charges (%d succeeded), want %d (%d succeeded): %s", name, len(ch), succeeded, w.total, w.succeeded, fmt.Sprint(ch))
		}
	}
}
