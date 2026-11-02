//go:build integration

package itest

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/0furkancolak/payrudder/pkg/audit"
	"github.com/0furkancolak/payrudder/pkg/payments"
	"github.com/0furkancolak/payrudder/pkg/payments/stripe"
	"github.com/0furkancolak/payrudder/pkg/secretbox"
	"github.com/0furkancolak/payrudder/pkg/testkit"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/handler"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/webhook"
)

// What a seller can do about a dispute inside Payrudder: the fee a notification names is a ledger row of its own, written once, and stays on the case when a later notification
// does not repeat it; a note and the mark "handled" are the seller's, each with its history row and its audit entry in the same transaction (a note's text stays out of the
// audit log), setting what is set writes nothing, a blank or too long note is refused, and a case of another tenant is no case.
func TestASellerNotesAndHandlesADisputeAndItsFeeIsALedgerRow(t *testing.T) {
	cdb := openDB(t, testkit.StartPostgres(t))
	ctx := context.Background()
	box, _ := secretbox.New(bytes.Repeat([]byte{9}, 32))
	registry := payments.NewRegistry()
	stripe.Register(registry)
	accounts := store.Accounts{DB: cdb.DB, Box: box}
	resolver := &handler.Resolver{Registry: registry, Accounts: accounts}
	ledger := store.Ledger{DB: cdb.DB}
	srv := &webhook.Server{Registry: registry, Resolver: resolver, Accounts: accounts, Events: store.Events{DB: cdb.DB}, Attempts: store.Attempts{DB: cdb.DB}, Ledger: ledger,
		Subscriptions: store.Subscriptions{DB: cdb.DB}, Disputes: &announced{}, DisputeCases: store.Disputes{DB: cdb.DB}, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Token: "internal"}
	api := httptest.NewServer(audit.Middleware(srv.Handler()))
	defer api.Close()

	tenant, other, invoice := uuid.New(), uuid.New(), uuid.New()
	const secret = "whsec_handling"
	if err := accounts.Put(ctx, tenant, "stripe", "live", []byte(`{"api_key":"sk_live_abc","webhook_secret":"`+secret+`"}`), true); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Append(ctx, store.LedgerEntry{TenantID: tenant, InvoiceID: invoice, Provider: "stripe", Environment: "live", Kind: "charge", Status: "succeeded", AmountMinor: 1250,
		Currency: "EUR", ProviderRef: "pi_1", IdempotencyKey: invoice.String() + ":charge:0", Source: "command"}); err != nil {
		t.Fatal(err)
	}
	post := func(id, typ, object string) {
		t.Helper()
		body := `{"id":"` + id + `","type":"` + typ + `","created":1790000000,"data":{"object":` + object + `}}`
		req, _ := http.NewRequest("POST", api.URL+"/webhooks/stripe/"+tenant.String()+"/live", strings.NewReader(body))
		req.Header.Set("Stripe-Signature", stripe.SignatureHeader(secret, time.Now(), []byte(body)))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: %d", typ, resp.StatusCode)
		}
	}
	count := func(sql string, args ...any) (n int) {
		t.Helper()
		if err := cdb.At(tenant).QueryRow(ctx, sql, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	feeRows := func() int {
		return count(`SELECT count(*) FROM payment_ledger WHERE tenant_id = $1 AND kind = 'dispute_fee' AND provider_ref = 'du_1:fee' AND amount_minor = 1500 AND currency = 'USD' AND invoice_id = $2`, tenant, invoice)
	}
	cases := store.Disputes{DB: cdb.DB}
	one := func() store.Dispute {
		t.Helper()
		list, err := cases.List(ctx, tenant, invoice, "", 10)
		if err != nil || len(list) != 1 {
			t.Fatalf("the case = %+v, %v", list, err)
		}
		return list[0]
	}

	// The fee: a ledger row of its own, once, and on the case; a later notification that says nothing of it leaves it.
	withFee := `{"id":"du_1","amount":1250,"currency":"eur","payment_intent":"pi_1","reason":"fraudulent","status":"needs_response","evidence_details":{"due_by":1790864000},"balance_transactions":[{"amount":-1250,"fee":1500,"currency":"usd"}]}`
	post("evt_1", "charge.dispute.created", withFee)
	post("evt_2", "charge.dispute.funds_withdrawn", withFee)
	if feeRows() != 1 {
		t.Fatalf("dispute_fee rows = %d, want one row of 1500 USD for the case, however many notifications carry it", feeRows())
	}
	post("evt_3", "charge.dispute.closed", `{"id":"du_1","amount":1250,"currency":"eur","payment_intent":"pi_1","status":"won"}`)
	d := one()
	if d.FeeMinor == nil || *d.FeeMinor != 1500 || d.FeeCurrency != "USD" || feeRows() != 1 {
		t.Fatalf("the case = %+v (fee rows %d), want its fee 1500 USD kept after a notification that does not name one", d, feeRows())
	}
	if n := count(`SELECT count(*) FROM payment_ledger WHERE tenant_id = $1 AND kind = 'charge'`, tenant); n != 1 {
		t.Errorf("the fee row was taken for a charge: %d charge rows", n)
	}

	// What the seller does, through the operator route the gateway calls, as the gateway says who.
	call := func(method, path, who, body string) (int, map[string]any) {
		t.Helper()
		req, _ := http.NewRequest(method, api.URL+path, strings.NewReader(body))
		req.Header.Set("X-Internal-Token", "internal")
		audit.SetHeaders(audit.NewContext(ctx, audit.Actor{Type: audit.ActorUser, ID: "user-1", Email: who}), req.Header)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var m map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&m)
		return resp.StatusCode, m
	}
	base := "/admin/tenants/" + tenant.String() + "/disputes/" + strconv.FormatInt(d.ID, 10)
	key := func(m map[string]any) string {
		e, _ := m["error"].(map[string]any)
		k, _ := e["key"].(string)
		return k
	}
	const ada = "ada@example.com"

	if code, m := call("POST", base+"/notes", ada, `{"note":"  The customer signed for the delivery on the 3rd.  "}`); code != http.StatusCreated || m["action"] != "note" || m["note"] != "The customer signed for the delivery on the 3rd." || m["actor"] != ada {
		t.Fatalf("a note: %d %v", code, m)
	}
	for _, tc := range []struct {
		name, path, body, key string
		status                int
	}{
		{"a blank note", base + "/notes", `{"note":"   "}`, "payments.dispute.note_required", 400},
		{"a note past 2000 characters", base + "/notes", `{"note":"` + strings.Repeat("é", 2001) + `"}`, "payments.dispute.note_too_long", 400},
		{"a case that does not exist", "/admin/tenants/" + tenant.String() + "/disputes/999999/notes", `{"note":"x"}`, "payments.dispute.not_found", 404},
		{"another tenant's case", "/admin/tenants/" + other.String() + "/disputes/" + strconv.FormatInt(d.ID, 10) + "/notes", `{"note":"x"}`, "payments.dispute.not_found", 404},
		{"a bad id", "/admin/tenants/" + tenant.String() + "/disputes/abc/notes", `{"note":"x"}`, "payments.dispute.bad_id", 400},
	} {
		if code, m := call("POST", tc.path, ada, tc.body); code != tc.status || key(m) != tc.key {
			t.Errorf("%s: %d %v; want %d %s", tc.name, code, m, tc.status, tc.key)
		}
	}
	if code, _ := call("PUT", base+"/handled", ada, `{}`); code != http.StatusBadRequest {
		t.Errorf("handled without a value: %d, want 400", code)
	}
	if n := count(`SELECT count(*) FROM payment_dispute_events WHERE tenant_id = $1`, tenant); n != 1 {
		t.Fatalf("a refused call wrote a history row: %d rows, want the one note", n)
	}

	// Handled, then handled again (nothing), then lifted by somebody else.
	if code, m := call("PUT", base+"/handled", ada, `{"handled":true}`); code != 200 || m["handled_by"] != ada || m["handled_time"] == nil || m["note_count"] != float64(1) {
		t.Fatalf("handled: %d %v", code, m)
	}
	call("PUT", base+"/handled", "bob@example.com", `{"handled":true}`)
	if h := one(); h.HandledAt == nil || h.HandledBy != ada || h.NoteCount != 1 {
		t.Fatalf("after a second mark: %+v; want Ada's mark kept", h)
	}
	if code, m := call("PUT", base+"/handled", "bob@example.com", `{"handled":false}`); code != 200 || m["handled_time"] != nil || m["handled_by"] != nil {
		t.Fatalf("lifted: %d %v", code, m)
	}
	call("PUT", base+"/handled", "bob@example.com", `{"handled":false}`)

	code, hist := call("GET", base+"/history", ada, "")
	events, _ := hist["events"].([]any)
	if code != 200 || len(events) != 3 {
		t.Fatalf("history: %d %v; want the note, the mark and its lifting (the repeats wrote nothing)", code, hist)
	}
	var actions []string
	for _, e := range events {
		actions = append(actions, e.(map[string]any)["action"].(string)+":"+e.(map[string]any)["actor"].(string))
	}
	if strings.Join(actions, " ") != "unhandled:bob@example.com handled:ada@example.com note:ada@example.com" {
		t.Errorf("history (newest first) = %v", actions)
	}
	if code, m := call("GET", "/admin/tenants/"+other.String()+"/disputes/"+strconv.FormatInt(d.ID, 10)+"/history", ada, ""); code != 404 || key(m) != "payments.dispute.not_found" {
		t.Errorf("another tenant reads the history: %d %v", code, m)
	}

	// One audit entry per change, in the log of the case's tenant, with the note's text left out.
	for action, want := range map[string]int{"dispute.note_added": 1, "dispute.handled": 1, "dispute.unhandled": 1} {
		if n := count(`SELECT count(*) FROM audit_events WHERE tenant_id = $1 AND action = $2 AND target_type = 'dispute' AND target_id = $3`, tenant, action, strconv.FormatInt(d.ID, 10)); n != want {
			t.Errorf("audit entries %s = %d, want %d", action, n, want)
		}
	}
	if n := count(`SELECT count(*) FROM audit_events WHERE tenant_id = $1 AND (summary || detail::text) LIKE '%signed for the delivery%'`, tenant); n != 0 {
		t.Error("the text of a note is in the audit log")
	}
	if n := count(`SELECT count(*) FROM audit_events WHERE tenant_id = $1 AND action = 'dispute.note_added' AND actor_email = $2 AND (detail->>'note_length')::int = 48`, tenant, ada); n != 1 {
		t.Error("the audit entry of the note does not name who wrote it and how long it is")
	}

	// The history is append-only: nobody deletes a note.
	if _, err := cdb.At(tenant).Exec(ctx, `DELETE FROM payment_dispute_events WHERE tenant_id = $1`, tenant); err == nil {
		t.Error("a note was deleted")
	}
}
