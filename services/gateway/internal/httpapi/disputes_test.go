package httpapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// A fake payments-worker that records the call it got and answers what the test says.
type disputeWorker struct {
	method, path, body, actor string
	status                    int
	answer                    string
}

func (d *disputeWorker) start(t *testing.T) *url.URL {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		d.method, d.path, d.body, d.actor = r.Method, r.URL.Path, string(b), r.Header.Get("X-Payrudder-Audit-Actor-Email")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(d.status)
		_, _ = io.WriteString(w, d.answer)
	}))
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	return u
}

func disputeGateway(t *testing.T, d *disputeWorker) *httptest.Server {
	t.Helper()
	u := d.start(t)
	ts, _, _, _ := newTestServerWith(t, func(deps *Deps) { deps.PaymentsURL, deps.Log = u, slog.New(slog.NewTextHandler(io.Discard, nil)) })
	return ts
}

const workerCase = `{"id":12,"provider":"stripe","dispute_id":"du_1","payment_ref":"pi_1","invoice_id":"d0000000-0000-4000-8000-000000000001","amount_minor":1250,"currency":"EUR","outcome":"open","formal":true,` +
	`"provider_status":"needs_response","evidence_due_time":"2026-11-20T00:00:00Z","opened_time":"2026-11-01T08:00:00Z","fee_minor":1500,"fee_currency":"USD","handled_time":"2026-11-02T10:00:00Z","handled_by":"ada@example.com","note_count":2}`

// The list says what a dispute cost, whether the seller marked it, and how many notes; a viewer is not told who marked it.
func TestDisputesCarryTheFeeTheMarkAndTheNoteCount(t *testing.T) {
	d := &disputeWorker{status: 200, answer: `{"disputes":[` + workerCase + `,{"id":13,"provider":"paddle","dispute_id":"cb_txn_1","payment_ref":"txn_1","amount_minor":500,"currency":"EUR","outcome":"won","formal":false,"provider_status":"x","opened_time":"2026-11-01T08:00:00Z","note_count":0}]}`}
	ts := disputeGateway(t, d)
	resp, m := do(t, ts, "GET", "/v1/disputes", "live-key", "")
	list, _ := m["disputes"].([]any)
	if resp.StatusCode != 200 || len(list) != 2 {
		t.Fatalf("list: %d %v", resp.StatusCode, m)
	}
	first, second := list[0].(map[string]any), list[1].(map[string]any)
	if first["id"] != "12" || first["fee_minor"] != "1500" || first["fee_currency"] != "USD" || first["handled"] != true || first["handled_by"] != "ada@example.com" || first["note_count"] != float64(2) || first["handled_time"] != "2026-11-02T10:00:00Z" {
		t.Errorf("a marked case with a fee: %v", first)
	}
	if second["handled"] != false || second["fee_minor"] != nil || second["fee_currency"] != nil || second["handled_by"] != nil || second["note_count"] != float64(0) {
		t.Errorf("a case with none of them: %v", second)
	}

	var w workerDispute
	if err := json.Unmarshal([]byte(workerCase), &w); err != nil {
		t.Fatal(err)
	}
	if v := w.view(true); v.HandledBy != "" || !v.Handled || v.FeeMinor != "1500" {
		t.Errorf("a viewer's view = %+v; want the mark but not who made it", v)
	}
	var zero workerDispute
	_ = json.Unmarshal([]byte(`{"id":1,"amount_minor":5,"fee_minor":0,"fee_currency":"USD","opened_time":"2026-11-01T08:00:00Z"}`), &zero)
	if v := zero.view(false); v.FeeMinor != "" || v.FeeCurrency != "" {
		t.Errorf("a fee of zero is shown as %q %q: it is not a fee", v.FeeMinor, v.FeeCurrency)
	}
}

// What a seller does with a case goes to the case of the caller's own tenant (the path never names another), as the person the gateway says; the answers are the API's, not the
// worker's, and a refusal of the worker comes back with its key.
func TestANoteAndTheHandledMarkGoToTheCallersOwnCase(t *testing.T) {
	d := &disputeWorker{status: 201, answer: `{"id":7,"action":"note","note":"signed for","actor":"ada@example.com","create_time":"2026-11-02T10:00:00Z"}`}
	ts := disputeGateway(t, d)
	resp, m := do(t, ts, "POST", "/v1/disputes/12/notes", "live-key", `{"note":"signed for"}`)
	if resp.StatusCode != 201 || m["id"] != "7" || m["action"] != "note" || m["note"] != "signed for" || m["create_time"] != "2026-11-02T10:00:00Z" {
		t.Fatalf("note: %d %v", resp.StatusCode, m)
	}
	if want := "/admin/tenants/" + liveTenant.String() + "/disputes/12/notes"; d.method != "POST" || d.path != want || !strings.Contains(d.body, `"note":"signed for"`) {
		t.Errorf("the worker was asked %s %q %q; want POST %q with the note", d.method, d.path, d.body, want)
	}

	d.status, d.answer = 200, workerCase
	if resp, m := do(t, ts, "PUT", "/v1/disputes/12/handled", "live-key", `{"handled":true}`); resp.StatusCode != 200 || m["handled"] != true || m["id"] != "12" || m["amount_minor"] != "1250" {
		t.Errorf("handled: %d %v; want the case as the API says it (amounts as text)", resp.StatusCode, m)
	}
	if d.method != "PUT" || !strings.HasSuffix(d.path, "/disputes/12/handled") || !strings.Contains(d.body, `"handled":true`) {
		t.Errorf("the worker was asked %s %q %q", d.method, d.path, d.body)
	}

	for _, tc := range []struct{ method, path, body string }{
		{"POST", "/v1/disputes/abc/notes", `{"note":"x"}`}, {"POST", "/v1/disputes/0/notes", `{"note":"x"}`}, {"PUT", "/v1/disputes/-3/handled", `{"handled":true}`}, {"GET", "/v1/disputes/x/history", ""},
		{"PUT", "/v1/disputes/12/handled", `{}`}, {"POST", "/v1/disputes/12/notes", `not json`},
	} {
		d.method = ""
		if resp, m := do(t, ts, tc.method, tc.path, "live-key", tc.body); resp.StatusCode != 400 || errCode(m) != "INVALID_ARGUMENT" || d.method != "" {
			t.Errorf("%s %s %q: %d %v, worker asked %q; want 400 before the worker is asked", tc.method, tc.path, tc.body, resp.StatusCode, m, d.method)
		}
	}

	// A refusal the worker makes on purpose comes back with its key and status; anything else is the payment service not answering.
	d.status, d.answer = 400, `{"error":{"code":"INVALID_ARGUMENT","key":"payments.dispute.note_too_long","message":"a note is at most 2000 characters"}}`
	if resp, m := do(t, ts, "POST", "/v1/disputes/12/notes", "live-key", `{"note":"x"}`); resp.StatusCode != 400 || errKey(m) != "payments.dispute.note_too_long" {
		t.Errorf("a refused note: %d %v", resp.StatusCode, m)
	}
	d.status, d.answer = 404, `{"error":{"code":"NOT_FOUND","key":"payments.dispute.not_found","message":"no such dispute"}}`
	if resp, m := do(t, ts, "PUT", "/v1/disputes/99/handled", "live-key", `{"handled":true}`); resp.StatusCode != 404 || errKey(m) != "payments.dispute.not_found" {
		t.Errorf("a case that is not the caller's: %d %v", resp.StatusCode, m)
	}
	d.status, d.answer = 500, `{"error":{"code":"INTERNAL","key":"common.internal","message":"pq: connection refused at 10.1.2.3"}}`
	if resp, m := do(t, ts, "GET", "/v1/disputes/12/history", "live-key", ""); resp.StatusCode != 503 || strings.Contains(m["error"].(map[string]any)["message"].(string), "10.1.2.3") {
		t.Errorf("a failing worker: %d %v; want 503 and none of its text", resp.StatusCode, m)
	}
}

// Writing on a case is invoices:write like the rest of disputes: a read-only key and a frozen tenant are refused before the worker is asked; reading the history needs invoices:read.
func TestDisputeNotesNeedTheInvoicesPermission(t *testing.T) {
	d := &disputeWorker{status: 200, answer: `{"events":[{"id":7,"action":"handled","actor":"ada@example.com","create_time":"2026-11-02T10:00:00Z"},{"id":6,"action":"note","note":"hello","actor":"bob@example.com","create_time":"2026-11-01T10:00:00Z"}]}`}
	ts := disputeGateway(t, d)
	for _, key := range []string{"read-key", "custom-key", "checkout-key"} {
		d.method = ""
		if resp, m := do(t, ts, "POST", "/v1/disputes/12/notes", key, `{"note":"x"}`); resp.StatusCode != 403 || errKey(m) != "gateway.auth.permission_denied" || d.method != "" {
			t.Errorf("a note with %s: %d %v (worker asked %q); want 403 before the worker", key, resp.StatusCode, m, d.method)
		}
		if resp, _ := do(t, ts, "PUT", "/v1/disputes/12/handled", key, `{"handled":true}`); resp.StatusCode != 403 {
			t.Errorf("a mark with %s: %d, want 403", key, resp.StatusCode)
		}
	}
	if resp, m := do(t, ts, "PUT", "/v1/disputes/12/handled", "frozen-key", `{"handled":true}`); resp.StatusCode != 403 || errKey(m) != "gateway.auth.tenant_read_only" {
		t.Errorf("a mark on a frozen tenant: %d %v", resp.StatusCode, m)
	}
	resp, m := do(t, ts, "GET", "/v1/disputes/12/history", "custom-key", "")
	events, _ := m["events"].([]any)
	if resp.StatusCode != 200 || len(events) != 2 || events[0].(map[string]any)["id"] != "7" || events[1].(map[string]any)["note"] != "hello" || events[1].(map[string]any)["actor"] != "bob@example.com" {
		t.Fatalf("history: %d %v", resp.StatusCode, m)
	}
	if resp, _ := do(t, ts, "GET", "/v1/disputes/12/history", "audit-key", ""); resp.StatusCode != 403 {
		t.Errorf("history without invoices:read: %d, want 403", resp.StatusCode)
	}
	var e workerDisputeEvent
	_ = json.Unmarshal([]byte(`{"id":6,"action":"note","note":"hello","actor":"bob@example.com","create_time":"2026-11-01T10:00:00Z"}`), &e)
	if v := e.view(true); v.Actor != "" || v.Note != "hello" || !v.Created.Equal(time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("a viewer's view of an event = %+v; want the note without who wrote it", v)
	}
}
