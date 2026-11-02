//go:build integration

package itest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
	"github.com/0furkancolak/payrudder/pkg/payments"
	"github.com/0furkancolak/payrudder/pkg/payments/stripe"
	"github.com/0furkancolak/payrudder/pkg/secretbox"
	"github.com/0furkancolak/payrudder/pkg/testkit"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/handler"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/webhook"
)

// announced is a webhook.DisputesAPI that remembers what billing was told.
type announced struct {
	mu   sync.Mutex
	got  []*billingv1.RecordPaymentDisputeRequest
	fail error
}

func (a *announced) RecordPaymentDispute(_ context.Context, _ uuid.UUID, req *billingv1.RecordPaymentDisputeRequest) (*billingv1.RecordPaymentDisputeResponse, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.fail != nil {
		return nil, a.fail
	}
	a.got = append(a.got, req)
	return &billingv1.RecordPaymentDisputeResponse{Announced: true}, nil
}

func (a *announced) steps() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []string
	for _, r := range a.got {
		out = append(out, r.GetDisputeRef()+" "+r.GetOutcome())
	}
	return out
}

// A dispute notification becomes a step of the case: billing is told once per step (a second event for the same step, such as Stripe's funds_withdrawn after created, is
// not told again), the ledger holds each step as a row of kind dispute, the table holds the case as it stands (an ended case is not reopened by a late event), a payment
// Payrudder did not make is kept without an invoice and announced to nobody, and a failure to tell billing is a 503 that leaves nothing behind, so the provider's retry is
// handled from the top.
func TestDisputeNotificationsAreAnnouncedOncePerStep(t *testing.T) {
	cdb := openDB(t, testkit.StartPostgres(t))
	ctx := context.Background()
	box, _ := secretbox.New(bytes.Repeat([]byte{9}, 32))
	registry := payments.NewRegistry()
	stripe.Register(registry)
	accounts := store.Accounts{DB: cdb.DB, Box: box}
	resolver := &handler.Resolver{Registry: registry, Accounts: accounts}
	told := &announced{}
	ledger := store.Ledger{DB: cdb.DB}
	srv := &webhook.Server{Registry: registry, Resolver: resolver, Accounts: accounts, Events: store.Events{DB: cdb.DB}, Attempts: store.Attempts{DB: cdb.DB}, Ledger: ledger,
		Subscriptions: store.Subscriptions{DB: cdb.DB}, Disputes: told, DisputeCases: store.Disputes{DB: cdb.DB}, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Token: "internal"}
	api := httptest.NewServer(srv.Handler())
	defer api.Close()

	tenant, invoice := uuid.New(), uuid.New()
	const secret = "whsec_disputes"
	if err := accounts.Put(ctx, tenant, "stripe", "live", []byte(`{"api_key":"sk_live_abc","webhook_secret":"`+secret+`"}`), true); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Append(ctx, store.LedgerEntry{TenantID: tenant, InvoiceID: invoice, Provider: "stripe", Environment: "live", Kind: "charge", Status: "succeeded", AmountMinor: 1250,
		Currency: "EUR", ProviderRef: "pi_1", IdempotencyKey: invoice.String() + ":charge:0", Source: "command"}); err != nil {
		t.Fatal(err)
	}
	post := func(id, typ, object string) int {
		t.Helper()
		body := `{"id":"` + id + `","type":"` + typ + `","created":1790000000,"data":{"object":` + object + `}}`
		req, _ := http.NewRequest("POST", api.URL+"/webhooks/stripe/"+tenant.String()+"/live", strings.NewReader(body))
		req.Header.Set("Stripe-Signature", stripe.SignatureHeader(secret, time.Now(), []byte(body)))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	dispute := func(id, status, pi string) string {
		return `{"id":"` + id + `","amount":1250,"currency":"eur","charge":"ch_1","payment_intent":"` + pi + `","reason":"fraudulent","status":"` + status + `","evidence_details":{"due_by":1790864000}}`
	}
	rows := func(kind, status string) int {
		t.Helper()
		var c int
		if err := cdb.At(tenant).QueryRow(ctx, `SELECT count(*) FROM payment_ledger WHERE tenant_id = $1 AND kind = $2 AND status = $3`, tenant, kind, status).Scan(&c); err != nil {
			t.Fatal(err)
		}
		return c
	}
	cases := store.Disputes{DB: cdb.DB}

	if code := post("evt_1", "charge.dispute.created", dispute("du_1", "needs_response", "pi_1")); code != http.StatusOK {
		t.Fatalf("created: %d", code)
	}
	if got := told.steps(); len(got) != 1 || got[0] != "du_1 open" {
		t.Fatalf("billing was told %v, want du_1 open", got)
	}
	first := told.got[0]
	if first.GetInvoiceId() != invoice.String() || first.GetProvider() != "stripe" || first.GetPaymentRef() != "pi_1" || first.GetAmountMinor() != 1250 || first.GetCurrency() != "EUR" ||
		!first.GetFormal() || first.GetReason() != "fraudulent" || first.GetEvidenceDueTime() == nil || first.GetProviderStatus() != "needs_response" {
		t.Errorf("the announcement = %v", first)
	}
	if rows("dispute", "pending") != 1 {
		t.Errorf("pending dispute rows = %d, want 1", rows("dispute", "pending"))
	}
	if open, err := cases.Open(ctx, tenant); err != nil || len(open) != 1 || open[0].InvoiceID != invoice || open[0].Ref != "du_1" {
		t.Errorf("open cases = %+v, %v", open, err)
	}

	// The same step again (funds withdrawn, an update with evidence): nothing new is announced or written.
	post("evt_2", "charge.dispute.funds_withdrawn", dispute("du_1", "needs_response", "pi_1"))
	post("evt_3", "charge.dispute.updated", dispute("du_1", "under_review", "pi_1"))
	if got := told.steps(); len(got) != 1 || rows("dispute", "pending") != 1 {
		t.Fatalf("a repeated step: told %v, %d pending rows; want one of each", got, rows("dispute", "pending"))
	}

	// It ends in the seller's favour: announced once more, a row, the case closed, nothing open.
	if code := post("evt_4", "charge.dispute.closed", dispute("du_1", "won", "pi_1")); code != http.StatusOK {
		t.Fatalf("closed: %d", code)
	}
	post("evt_5", "charge.dispute.funds_reinstated", dispute("du_1", "won", "pi_1"))
	if got := told.steps(); len(got) != 2 || got[1] != "du_1 won" || rows("dispute", "succeeded") != 1 {
		t.Fatalf("after the win: told %v, %d succeeded rows", got, rows("dispute", "succeeded"))
	}
	// A late notification of an earlier step does not reopen it.
	post("evt_6", "charge.dispute.updated", dispute("du_1", "under_review", "pi_1"))
	list, err := cases.List(ctx, tenant, invoice, "", 10)
	if err != nil || len(list) != 1 || list[0].Outcome != "won" || list[0].ClosedAt == nil || list[0].InvoiceID == nil || *list[0].InvoiceID != invoice.String() || list[0].Reason != "fraudulent" {
		t.Fatalf("the case = %+v, %v", list, err)
	}
	if open, _ := cases.Open(ctx, tenant); len(open) != 0 {
		t.Errorf("a won case is still open: %+v", open)
	}

	// A payment Payrudder never made: kept, with no invoice, and nobody is told.
	post("evt_7", "charge.dispute.created", dispute("du_other", "needs_response", "pi_unknown"))
	if got := told.steps(); len(got) != 2 {
		t.Errorf("a dispute of a payment that is not ours was announced: %v", got)
	}
	if all, _ := cases.List(ctx, tenant, uuid.Nil, "open", 10); len(all) != 1 || all[0].InvoiceID != nil || all[0].DisputeRef != "du_other" {
		t.Errorf("open cases = %+v", all)
	}

	// Billing cannot be told: 503, nothing is written, and the retry of the same event is handled from the top.
	told.fail = errors.New("billing-svc is down")
	if code := post("evt_8", "charge.dispute.created", dispute("du_2", "needs_response", "pi_1")); code != http.StatusServiceUnavailable {
		t.Fatalf("billing down: %d, want 503", code)
	}
	if rows("dispute", "pending") != 2 { // du_1's and du_other's, not du_2's
		t.Errorf("a failed announcement left a ledger row: %d pending rows", rows("dispute", "pending"))
	}
	told.mu.Lock()
	told.fail = nil
	told.mu.Unlock()
	if code := post("evt_8", "charge.dispute.created", dispute("du_2", "needs_response", "pi_1")); code != http.StatusOK {
		t.Fatalf("the retry: %d", code)
	}
	if got := told.steps(); len(got) != 3 || got[2] != "du_2 open" || rows("dispute", "pending") != 3 {
		t.Errorf("after the retry: told %v, %d pending rows", got, rows("dispute", "pending"))
	}

	// An inquiry is not a chargeback.
	post("evt_9", "charge.dispute.created", dispute("du_3", "warning_needs_response", "pi_1"))
	if told.got[len(told.got)-1].GetFormal() {
		t.Error("an inquiry was announced as a chargeback")
	}

	// The operator lists them, of one invoice and of one outcome.
	req, _ := http.NewRequest("GET", api.URL+"/admin/tenants/"+tenant.String()+"/disputes?invoice_id="+invoice.String()+"&outcome=open", nil)
	req.Header.Set("X-Internal-Token", "internal")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct {
		Disputes []store.Dispute `json:"disputes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || resp.StatusCode != http.StatusOK || len(out.Disputes) != 2 {
		t.Fatalf("the list: %d %+v %v; want the two open cases of the invoice (du_2 and the inquiry du_3)", resp.StatusCode, out, err)
	}
}
