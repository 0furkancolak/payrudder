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
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commandsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/commands/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/payments"
	"github.com/0furkancolak/payrudder/pkg/payments/banktransfer"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/secretbox"
	"github.com/0furkancolak/payrudder/pkg/testkit"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/handler"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/transfers"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/transfers/command"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/transfers/query"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/webhook"
	"github.com/0furkancolak/payrudder/services/payments-worker/migrations"
)

// transferBilling answers for billing-svc: the status of an invoice, and what the saga nudges count.
type transferBilling struct {
	mu      sync.Mutex
	status  string
	nudged  []uuid.UUID
	nudgeFn func() error
}

func (b *transferBilling) InvoiceStatus(context.Context, uuid.UUID, uuid.UUID) (string, bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.status, true, nil
}

func (b *transferBilling) Nudge(_ context.Context, _, invoice uuid.UUID) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.nudged = append(b.nudged, invoice)
	if b.nudgeFn != nil {
		return b.nudgeFn()
	}
	return nil
}

func (b *transferBilling) nudges() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.nudged)
}

// tclock is a clock the test moves.
type tclock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *tclock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *tclock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

const bankSettings = `{"bank_name":"Example Bank","account_holder":"Acme Ltd","iban":"tr33 0006 1005 1978 6457 8413 26","bic":"tczb tr2a","reference_template":"{invoice_number}","currencies":["TRY"],"payment_window_days":7}`

// A tenant pays by bank transfer: the charge only announces it, the status waits, an operator confirms what arrived, and the saga's status query finds the payment.
func TestBankTransferThroughTheWorker(t *testing.T) {
	pg := testkit.StartPostgres(t)
	db := testkit.MigratedDB(t, pg, "payments", migrations.FS)
	pool, err := pgxx.NewPool(context.Background(), db.System)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	ctx := context.Background()

	box, _ := secretbox.New(bytes.Repeat([]byte{7}, 32))
	registry := payments.NewRegistry()
	banktransfer.Register(registry)
	accounts := store.Accounts{DB: pool, Box: box}
	resolver := &handler.Resolver{Registry: registry, Accounts: accounts}
	clk := &tclock{t: time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := &handler.Handler{Sandbox: payments.NewMockPSP("http://unused", 0), Attempts: store.Attempts{DB: pool}, Ledger: store.Ledger{DB: pool}, Resolver: resolver, Log: log,
		Transfers: store.Transfers{DB: pool}, TransferWindow: 14 * 24 * time.Hour, Now: clk.now}
	billing := &transferBilling{status: "open"}
	plans := &recPlans{}
	api := httptest.NewServer((&webhook.Server{Registry: registry, Resolver: resolver, Accounts: accounts, Events: store.Events{DB: pool}, Attempts: h.Attempts, Ledger: h.Ledger,
		Plans: plans, Log: log, Token: "internal", Modules: []webhook.RouteRegistrar{&transfers.Handler{Log: log, Deps: command.Deps{Billing: billing, Deps: query.Deps{
			Transfers: h.Transfers, Accounts: resolver, DefaultWindow: 14 * 24 * time.Hour, Now: clk.now}}}}}).Handler())
	defer api.Close()

	tenant := uuid.New()
	do := func(method, path, body string, headers ...string) (int, map[string]any) {
		t.Helper()
		req, _ := http.NewRequest(method, api.URL+path, strings.NewReader(body))
		req.Header.Set("X-Internal-Token", "internal")
		for i := 0; i+1 < len(headers); i += 2 {
			req.Header.Set(headers[i], headers[i+1])
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		var out map[string]any
		_ = json.Unmarshal(raw, &out)
		return resp.StatusCode, out
	}
	errKey := func(out map[string]any) string {
		e, _ := out["error"].(map[string]any)
		k, _ := e["key"].(string)
		return k
	}

	// The account is validated, stored, and its public part is handed to plans-svc for the invoices.
	acct := "/admin/tenants/" + tenant.String() + "/provider-accounts/banktransfer?environment=live&default=true"
	if code, _ := do("PUT", acct, `{"bank_name":"B","account_holder":"H","iban":"TR00 0000"}`); code != 400 {
		t.Fatalf("a bad IBAN: %d, want 400", code)
	}
	if code, out := do("PUT", acct, bankSettings); code != 200 {
		t.Fatalf("put account: %d %v", code, out)
	}
	if len(plans.bank) != 1 || plans.bank[0].GetIban() != "TR330006100519786457841326" || plans.bank[0].GetBic() != "TCZBTR2A" || !plans.bank[0].GetIsDefault() ||
		plans.bank[0].GetBankName() != "Example Bank" || len(plans.bank[0].GetCurrencies()) != 1 {
		t.Fatalf("plans-svc was told %v; want the normalised public details, as the default account", plans.bank)
	}

	invoice := uuid.New()
	charge := func(inv uuid.UUID, dunning int32, amount int64, currency string) *commandsv1.ReplyEnvelope {
		t.Helper()
		r, err := h.Handle(ctx, &commandsv1.CommandEnvelope{CommandId: uuid.NewString(), SagaId: uuid.NewString(), TenantId: tenant.String(), Environment: plansv1.Environment_ENVIRONMENT_LIVE,
			Command: &commandsv1.CommandEnvelope_ChargePayment{ChargePayment: &commandsv1.ChargePayment{InvoiceId: inv.String(), InvoiceNumber: "INV-2026-000007", AmountMinor: amount,
				Currency: currency, Provider: "banktransfer", DunningAttempt: dunning, IdempotencyKey: inv.String() + ":charge:" + string(rune('0'+dunning))}}})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	statusOf := func(inv uuid.UUID, dunning int32) *commandsv1.PaymentStatus {
		t.Helper()
		r, err := h.Handle(ctx, &commandsv1.CommandEnvelope{CommandId: uuid.NewString(), SagaId: uuid.NewString(), TenantId: tenant.String(), Environment: plansv1.Environment_ENVIRONMENT_LIVE,
			Command: &commandsv1.CommandEnvelope_QueryPaymentStatus{QueryPaymentStatus: &commandsv1.QueryPaymentStatus{InvoiceId: inv.String(),
				IdempotencyKey: inv.String() + ":charge:" + string(rune('0'+dunning))}}})
		if err != nil {
			t.Fatal(err)
		}
		return r.GetPaymentStatus()
	}
	transferPath := func(inv uuid.UUID, tail string) string {
		return "/admin/tenants/" + tenant.String() + "/invoices/" + inv.String() + "/transfer" + tail
	}
	receipt := func(inv uuid.UUID, amount int64, ref string) (int, map[string]any) {
		return do("POST", transferPath(inv, "/receipts"), `{"amount_minor":`+itoa(amount)+`,"currency":"TRY","bank_reference":"`+ref+`","note":"statement line","received_at":"2026-11-05T10:00:00Z"}`,
			"X-Payrudder-Actor", "alice@example.com")
	}

	// 1. The charge calls nothing: it is accepted as pending, with the reference in the ledger, and the status waits.
	if code, out := do("GET", transferPath(invoice, ""), ""); code != 200 || out["state"] != "none" {
		t.Fatalf("before the charge: %d %v; want state none", code, out)
	}
	if code, out := receipt(invoice, 1000, "EARLY"); code != 409 || errKey(out) != "payments.transfer.not_a_transfer" {
		t.Fatalf("a receipt before any charge: %d %v; want 409 not_a_transfer", code, out)
	}
	r := charge(invoice, 0, 150000, "TRY")
	if r.GetPaymentErrored().GetCode() != handler.CodePending || r.GetPaymentErrored().GetRetryable() {
		t.Fatalf("charge reply = %v; want a non-retryable PaymentErrored PENDING (accepted, not settled)", r)
	}
	if r := charge(invoice, 0, 150000, "TRY"); r.GetPaymentErrored().GetCode() != handler.CodePending {
		t.Fatalf("a redelivered charge = %v; want the same answer, with no call to make", r)
	}
	if s := statusOf(invoice, 0); s.GetState() != commandsv1.PaymentStatus_STATE_PENDING {
		t.Fatalf("status = %v, want PENDING", s)
	}
	rows, _ := h.Ledger.List(ctx, tenant, 10)
	if len(rows) != 1 || rows[0].Status != "pending" || rows[0].Provider != "banktransfer" || rows[0].Kind != "charge" || rows[0].AmountMinor != 150000 ||
		rows[0].Detail == nil || *rows[0].Detail != "INV-2026-000007" {
		t.Fatalf("ledger = %+v; want one pending charge row carrying the reference", rows)
	}
	// A currency the account does not take is refused before anything is recorded.
	if r := charge(uuid.New(), 0, 100, "USD"); r.GetPaymentErrored().GetCode() != handler.CodeCurrency {
		t.Fatalf("USD charge = %v; want CURRENCY_NOT_SUPPORTED", r)
	}

	// 2. The state an operator sees: the reference and the bank details to quote, when the window ends.
	code, st := do("GET", transferPath(invoice, ""), "")
	instr, _ := st["instructions"].(map[string]any)
	if code != 200 || st["state"] != "pending" || st["reference"] != "INV-2026-000007" || instr["iban"] != "TR330006100519786457841326" || st["total_minor"] != float64(150000) ||
		st["outstanding_minor"] != float64(150000) || st["expires_at"] != "2026-11-12T12:00:00Z" {
		t.Fatalf("state = %d %v", code, st)
	}

	// 3. Validation: no bank reference, a non-positive amount, the wrong currency, more than the invoice, a date in the future, a closed invoice.
	for name, c := range map[string]struct {
		body string
		code int
		key  string
	}{
		"no reference":   {`{"amount_minor":100,"currency":"TRY"}`, 400, "payments.transfer.bad_reference"},
		"zero amount":    {`{"amount_minor":0,"currency":"TRY","bank_reference":"Z"}`, 400, "payments.transfer.bad_amount"},
		"wrong currency": {`{"amount_minor":100,"currency":"USD","bank_reference":"Z"}`, 400, "payments.transfer.bad_currency"},
		"future date":    {`{"amount_minor":100,"currency":"TRY","bank_reference":"Z","received_at":"2026-12-01T00:00:00Z"}`, 400, "payments.transfer.bad_date"},
		"too much":       {`{"amount_minor":150001,"currency":"TRY","bank_reference":"Z"}`, 409, "payments.transfer.overpayment"},
		"garbage amount": {`{"amount_minor":"abc","currency":"TRY","bank_reference":"Z"}`, 400, "payments.transfer.bad_body"},
	} {
		if got, out := do("POST", transferPath(invoice, "/receipts"), c.body); got != c.code || errKey(out) != c.key {
			t.Errorf("%s: %d %v; want %d %s", name, got, out, c.code, c.key)
		}
	}
	billing.mu.Lock()
	billing.status = "paid"
	billing.mu.Unlock()
	if got, out := receipt(invoice, 100, "CLOSED"); got != 409 || errKey(out) != "payments.transfer.invoice_closed" {
		t.Errorf("a receipt for a paid invoice: %d %v", got, out)
	}
	billing.mu.Lock()
	billing.status = "open"
	billing.mu.Unlock()

	// 4. A part payment is recorded and the invoice stays pending; the same bank reference again is a no-op, with another amount it is refused.
	code, out := receipt(invoice, 50000, "TRX-1")
	if code != 201 || out["paid"] != false || out["duplicate"] != false {
		t.Fatalf("part receipt: %d %v", code, out)
	}
	if tr, _ := out["transfer"].(map[string]any); tr["state"] != "pending" || tr["received_minor"] != float64(50000) || tr["outstanding_minor"] != float64(100000) {
		t.Fatalf("after a part payment the transfer = %v", tr)
	}
	if code, out := receipt(invoice, 50000, "TRX-1"); code != 200 || out["duplicate"] != true {
		t.Fatalf("the same receipt again: %d %v; want 200 duplicate", code, out)
	}
	if code, out := receipt(invoice, 49999, "TRX-1"); code != 409 || errKey(out) != "payments.transfer.reference_reused" {
		t.Fatalf("the same reference with another amount: %d %v", code, out)
	}
	if s := statusOf(invoice, 0); s.GetState() != commandsv1.PaymentStatus_STATE_PENDING {
		t.Fatalf("status after a part payment = %v, want still PENDING", s)
	}
	if billing.nudges() != 0 {
		t.Fatalf("the saga was nudged %d times for a part payment", billing.nudges())
	}

	// 5. Overpayment is refused with the outstanding amount in the message.
	if code, out := receipt(invoice, 100001, "TRX-2"); code != 409 || errKey(out) != "payments.transfer.overpayment" {
		t.Fatalf("overpayment: %d %v", code, out)
	} else if msg, _ := out["error"].(map[string]any)["message"].(string); !strings.Contains(msg, "100000") {
		t.Errorf("the refusal does not say what is outstanding: %q", msg)
	}

	// 6. The rest arrives: the invoice is paid, the saga is nudged, and the status query finds the payment in the ledger.
	code, out = receipt(invoice, 100000, "TRX-2")
	if code != 201 || out["paid"] != true || out["saga_notified"] != true || billing.nudges() != 1 {
		t.Fatalf("settling receipt: %d %v (nudges %d)", code, out, billing.nudges())
	}
	if tr, _ := out["transfer"].(map[string]any); tr["state"] != "paid" || tr["outstanding_minor"] != nil && tr["outstanding_minor"] != float64(0) {
		t.Fatalf("settled transfer = %v", tr)
	}
	s := statusOf(invoice, 0)
	if s.GetState() != commandsv1.PaymentStatus_STATE_SUCCEEDED || s.GetPspChargeId() == "" {
		t.Fatalf("status after the money arrived = %v, want SUCCEEDED", s)
	}
	rows, _ = h.Ledger.List(ctx, tenant, 20)
	manual := 0
	for _, r := range rows {
		if r.Source == "manual" {
			manual++
			if r.Kind != "charge" || r.Status != "succeeded" || r.Provider != "banktransfer" || r.InvoiceID == nil || *r.InvoiceID != invoice.String() {
				t.Errorf("manual ledger row = %+v", r)
			}
		}
	}
	if manual != 2 {
		t.Fatalf("manual ledger rows = %d, want 2 (one per transfer)", manual)
	}
	code, st = do("GET", transferPath(invoice, ""), "")
	receipts, _ := st["receipts"].([]any)
	if code != 200 || st["state"] != "paid" || len(receipts) != 2 {
		t.Fatalf("final state = %d %v", code, st)
	}
	first, _ := receipts[0].(map[string]any)
	if first["bank_reference"] != "TRX-1" || first["recorded_by"] != "alice@example.com" || first["received_at"] != "2026-11-05T10:00:00Z" || first["note"] != "statement line" {
		t.Errorf("receipt audit = %v; want who, when and the bank reference", first)
	}
	// The saga could not be told (billing-svc down): the receipt stands, the response says so, and the poll finds the payment.
	billing.mu.Lock()
	billing.nudgeFn = func() error { return status.Error(codes.Unavailable, "down") }
	billing.mu.Unlock()
	invoice2 := uuid.New()
	charge(invoice2, 0, 1000, "TRY")
	if code, out := receipt(invoice2, 1000, "T-1"); code != 201 || out["paid"] != true || out["saga_notified"] != false {
		t.Fatalf("receipt while billing is down: %d %v", code, out)
	}
	billing.mu.Lock()
	billing.nudgeFn = nil
	billing.mu.Unlock()
	if s := statusOf(invoice2, 0); s.GetState() != commandsv1.PaymentStatus_STATE_SUCCEEDED {
		t.Fatalf("status = %v; the poll must find the receipt", s)
	}
}

// A transfer that never arrives expires after the window, a rejected one is declined at once; the decline starts dunning, whose retry opens a new window, and a late
// transfer still pays the invoice.
func TestBankTransferExpiryRejectionAndLatePayment(t *testing.T) {
	pg := testkit.StartPostgres(t)
	db := testkit.MigratedDB(t, pg, "payments", migrations.FS)
	pool, err := pgxx.NewPool(context.Background(), db.System)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	ctx := context.Background()
	box, _ := secretbox.New(bytes.Repeat([]byte{7}, 32))
	registry := payments.NewRegistry()
	banktransfer.Register(registry)
	accounts := store.Accounts{DB: pool, Box: box}
	resolver := &handler.Resolver{Registry: registry, Accounts: accounts}
	clk := &tclock{t: time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := &handler.Handler{Sandbox: payments.NewMockPSP("http://unused", 0), Attempts: store.Attempts{DB: pool}, Ledger: store.Ledger{DB: pool}, Resolver: resolver, Log: log,
		Transfers: store.Transfers{DB: pool}, TransferWindow: 14 * 24 * time.Hour, Now: clk.now}
	billing := &transferBilling{status: "open"}
	tdeps := command.Deps{Billing: billing, Deps: query.Deps{Transfers: h.Transfers, Accounts: resolver, DefaultWindow: 14 * 24 * time.Hour, Now: clk.now}}
	tenant := uuid.New()
	// The account sets no window of its own: the deployment's 14 days apply.
	if err := accounts.Put(ctx, tenant, "banktransfer", "live", []byte(`{"bank_name":"B","account_holder":"H","iban":"DE89370400440532013000"}`), true); err != nil {
		t.Fatal(err)
	}

	run := func(kind string, inv uuid.UUID, dunning int32) *commandsv1.ReplyEnvelope {
		t.Helper()
		env := &commandsv1.CommandEnvelope{CommandId: uuid.NewString(), SagaId: uuid.NewString(), TenantId: tenant.String(), Environment: plansv1.Environment_ENVIRONMENT_LIVE}
		key := inv.String() + ":charge:" + string(rune('0'+dunning))
		switch kind {
		case "charge":
			env.Command = &commandsv1.CommandEnvelope_ChargePayment{ChargePayment: &commandsv1.ChargePayment{InvoiceId: inv.String(), InvoiceNumber: "INV-9", AmountMinor: 5000, Currency: "EUR",
				Provider: "banktransfer", DunningAttempt: dunning, IdempotencyKey: key}}
		case "status":
			env.Command = &commandsv1.CommandEnvelope_QueryPaymentStatus{QueryPaymentStatus: &commandsv1.QueryPaymentStatus{InvoiceId: inv.String(), IdempotencyKey: key}}
		case "refund":
			env.Command = &commandsv1.CommandEnvelope_RefundPayment{RefundPayment: &commandsv1.RefundPayment{InvoiceId: inv.String(), AmountMinor: 100, Currency: "EUR", IdempotencyKey: inv.String() + ":refund"}}
		}
		r, err := h.Handle(ctx, env)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}

	// Expiry: pending for the window, then declined, once; asking again says the same.
	inv := uuid.New()
	run("charge", inv, 0)
	clk.advance(14*24*time.Hour - time.Minute)
	if s := run("status", inv, 0).GetPaymentStatus(); s.GetState() != commandsv1.PaymentStatus_STATE_PENDING {
		t.Fatalf("a minute before the window ends: %v", s)
	}
	clk.advance(2 * time.Minute)
	for range 2 {
		if s := run("status", inv, 0).GetPaymentStatus(); s.GetState() != commandsv1.PaymentStatus_STATE_DECLINED || s.GetDeclineCode() != handler.TransferExpired {
			t.Fatalf("after the window: %v, want DECLINED transfer_expired", s)
		}
	}
	rows, _ := h.Ledger.List(ctx, tenant, 20)
	declined := 0
	for _, r := range rows {
		if r.Status == "declined" {
			declined++
		}
	}
	if declined != 1 {
		t.Fatalf("declined ledger rows = %d, want exactly 1", declined)
	}
	// The dunning retry is a new charge: a new pending row, a fresh window.
	run("charge", inv, 1)
	if s := run("status", inv, 1).GetPaymentStatus(); s.GetState() != commandsv1.PaymentStatus_STATE_PENDING {
		t.Fatalf("the retry's status = %v, want PENDING (a new window)", s)
	}

	// A late transfer pays the invoice though the first window is gone.
	srv := httptest.NewServer(func() http.Handler {
		mux := http.NewServeMux()
		(&transfers.Handler{Log: log, Deps: tdeps}).Register(mux, func(next http.HandlerFunc) http.HandlerFunc { return next })
		return mux
	}())
	defer srv.Close()
	post := func(path, body string) (int, map[string]any) {
		resp, err := http.Post(srv.URL+path, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		var out map[string]any
		_ = json.Unmarshal(raw, &out)
		return resp.StatusCode, out
	}
	base := "/admin/tenants/" + tenant.String() + "/invoices/" + inv.String() + "/transfer"
	if code, out := post(base+"/receipts", `{"amount_minor":5000,"currency":"EUR","bank_reference":"LATE"}`); code != 201 || out["paid"] != true {
		t.Fatalf("late receipt: %d %v", code, out)
	}
	if s := run("status", inv, 1).GetPaymentStatus(); s.GetState() != commandsv1.PaymentStatus_STATE_SUCCEEDED {
		t.Fatalf("status after a late payment = %v, want SUCCEEDED", s)
	}

	// Rejection: the operator gives up at once; it is idempotent and nudges the saga; a paid or unknown transfer cannot be rejected.
	inv2 := uuid.New()
	run("charge", inv2, 0)
	before := billing.nudges()
	base2 := "/admin/tenants/" + tenant.String() + "/invoices/" + inv2.String() + "/transfer"
	if code, out := post(base2+"/reject", `{"outcome":"sideways"}`); code != 400 || out["error"].(map[string]any)["key"] != "payments.transfer.bad_outcome" {
		t.Fatalf("bad outcome: %d %v", code, out)
	}
	code, out := post(base2+"/reject", `{"outcome":"rejected","reason":"the customer's bank returned it"}`)
	if code != 200 || out["state"] != "rejected" || !strings.Contains(out["closed_reason"].(string), "returned it") || billing.nudges() != before+1 {
		t.Fatalf("reject: %d %v (nudges %d→%d)", code, out, before, billing.nudges())
	}
	if code, out := post(base2+"/reject", `{}`); code != 200 || out["state"] != "rejected" || billing.nudges() != before+1 {
		t.Fatalf("rejecting again: %d %v; want the same state and no second nudge", code, out)
	}
	if s := run("status", inv2, 0).GetPaymentStatus(); s.GetState() != commandsv1.PaymentStatus_STATE_DECLINED || s.GetDeclineCode() != handler.TransferRejected {
		t.Fatalf("status of a rejected transfer = %v", s)
	}
	if code, out := post(base+"/reject", `{}`); code != 409 || out["error"].(map[string]any)["key"] != "payments.transfer.nothing_pending" {
		t.Fatalf("rejecting a paid transfer: %d %v", code, out)
	}
	if code, out := post("/admin/tenants/"+tenant.String()+"/invoices/"+uuid.NewString()+"/transfer/reject", `{}`); code != 409 || out["error"].(map[string]any)["key"] != "payments.transfer.not_a_transfer" {
		t.Fatalf("rejecting where nothing was charged: %d %v", code, out)
	}
	// "Expired" by the operator is recorded as such.
	inv3 := uuid.New()
	run("charge", inv3, 0)
	code, out = post("/admin/tenants/"+tenant.String()+"/invoices/"+inv3.String()+"/transfer/reject", `{"outcome":"expired"}`)
	if code != 200 || out["state"] != "expired" {
		t.Fatalf("mark expired: %d %v", code, out)
	}

	// An uncollectible invoice takes no transfer receipt: it is settled with a payment recorded on the invoice.
	billing.mu.Lock()
	billing.status = "uncollectible"
	billing.mu.Unlock()
	inv4 := uuid.New()
	run("charge", inv4, 0)
	if code, out := post("/admin/tenants/"+tenant.String()+"/invoices/"+inv4.String()+"/transfer/receipts", `{"amount_minor":1,"currency":"EUR","bank_reference":"U"}`); code != 409 ||
		out["error"].(map[string]any)["key"] != "payments.transfer.invoice_closed" {
		t.Fatalf("receipt for an uncollectible invoice: %d %v", code, out)
	}

	// There is no refund API: the refund fails for good with a reason that says what to do.
	r := run("refund", inv, 0)
	if r.GetPaymentErrored() == nil || r.GetPaymentErrored().GetRetryable() || !strings.Contains(r.GetPaymentErrored().GetMessage(), "outside Payrudder") {
		t.Fatalf("refund reply = %v; want a permanent PaymentErrored that points to a refund outside Payrudder", r)
	}
}

func itoa(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}
