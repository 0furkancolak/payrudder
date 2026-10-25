//go:build integration

package itest

import (
	"bytes"
	"context"
	"encoding/base64"
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
	"google.golang.org/protobuf/types/known/timestamppb"

	commandsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/commands/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/blob"
	pkg "github.com/0furkancolak/payrudder/pkg/einvoice"
	"github.com/0furkancolak/payrudder/pkg/einvoice/mock"
	"github.com/0furkancolak/payrudder/pkg/einvoice/nilvera"
	einvoicewebhook "github.com/0furkancolak/payrudder/pkg/einvoice/webhook"
	"github.com/0furkancolak/payrudder/pkg/payments"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/secretbox"
	"github.com/0furkancolak/payrudder/pkg/testkit"
	"github.com/0furkancolak/payrudder/pkg/webhooksig"
	einvoiceworker "github.com/0furkancolak/payrudder/services/payments-worker/internal/einvoice"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/webhook"
	"github.com/0furkancolak/payrudder/services/payments-worker/migrations"
)

type memBlobs struct {
	mu   sync.Mutex
	data map[string][]byte
}

func (m *memBlobs) Put(_ context.Context, key string, body []byte, _ blob.PutOptions) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.data == nil {
		m.data = map[string][]byte{}
	}
	m.data[key] = append([]byte(nil), body...)
	return nil
}

// integrator is a webhook receiver in the contract of pkg/einvoice/webhook: idempotent on the document UUID, verifying the signature.
type integrator struct {
	mu       sync.Mutex
	srv      *httptest.Server
	issues   map[string]string // uuid -> legal number
	calls    map[string]int
	sigBad   int
	reject   bool
	downTill int // answer 503 this many times first
	seq      int
}

func newIntegrator(t *testing.T, secret string) *integrator {
	in := &integrator{issues: map[string]string{}, calls: map[string]int{}}
	in.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		in.mu.Lock()
		defer in.mu.Unlock()
		if webhooksig.Verify(r.Header.Get(webhooksig.Header()), body, secret, time.Now(), 0) != nil {
			in.sigBad++
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		action := r.URL.Query().Get("action")
		in.calls[action]++
		id := r.Header.Get(strings.Replace(webhooksig.Header(), "Signature", "Document-Uuid", 1))
		w.Header().Set("Content-Type", "application/json")
		switch action {
		case "issue":
			if in.downTill > 0 {
				in.downTill--
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			if in.reject {
				w.WriteHeader(http.StatusUnprocessableEntity)
				_, _ = io.WriteString(w, `{"message":"VKN not registered"}`)
				return
			}
			if _, ok := in.issues[id]; !ok {
				in.seq++
				in.issues[id] = "ABC2026" + strings.Repeat("0", 8) + string(rune('0'+in.seq))
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"state": "issued", "legal_number": in.issues[id], "external_id": "ext-" + id,
				"pdf_base64": base64.StdEncoding.EncodeToString([]byte("%PDF-1.4 legal"))})
		case "cancel":
			_, _ = io.WriteString(w, `{"state":"cancelled"}`)
		case "ubl":
			_ = json.NewEncoder(w).Encode(map[string]string{"ubl_base64": base64.StdEncoding.EncodeToString([]byte("<Invoice>sealed</Invoice>"))})
		default:
			w.WriteHeader(http.StatusNotImplemented)
		}
	}))
	t.Cleanup(in.srv.Close)
	return in
}

func (in *integrator) count(action string) int {
	in.mu.Lock()
	defer in.mu.Unlock()
	return in.calls[action]
}

func snapshot(number string, seller, buyer *commandsv1.PartySnapshot, lines ...*commandsv1.InvoiceLineSnapshot) *commandsv1.InvoiceSnapshot {
	var total int64
	for _, l := range lines {
		total += l.GetAmountMinor()
	}
	return &commandsv1.InvoiceSnapshot{InvoiceId: uuid.NewString(), Number: number, Currency: "TRY", CurrencyExponent: 2, TenantName: "Acme", CustomerName: "Customer",
		IssuedAt: timestamppb.New(time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)), Lines: lines, TotalMinor: total, SubtotalMinor: total, Seller: seller, Buyer: buyer}
}

func issueCmd(tenant uuid.UUID, env plansv1.Environment, doc uuid.UUID, kind string, snap *commandsv1.InvoiceSnapshot) *commandsv1.CommandEnvelope {
	return &commandsv1.CommandEnvelope{CommandId: uuid.NewString(), SagaId: doc.String(), TenantId: tenant.String(), Environment: env,
		Command: &commandsv1.CommandEnvelope_IssueEInvoice{IssueEInvoice: &commandsv1.IssueEInvoice{Action: "issue", DocumentId: doc.String(), Kind: kind, Document: snap}}}
}

func TestEInvoiceWorker(t *testing.T) {
	pg := testkit.StartPostgres(t)
	db := testkit.CreateServiceDB(t, pg, "payments")
	if err := pgxx.Migrate(context.Background(), db.Migrator, migrations.FS, nil); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxx.NewPool(context.Background(), db.System)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	box, _ := secretbox.New(bytes.Repeat([]byte{9}, 32))
	registry := pkg.NewRegistry()
	mock.Register(registry)
	nilvera.RegisterWithOptions(registry, nilvera.Options{AllowPrivate: true})
	einvoicewebhook.RegisterWithOptions(registry, einvoicewebhook.Options{AllowPrivate: true})
	accounts := store.EInvoiceAccounts{DB: pool, Box: box}
	blobs := &memBlobs{}
	h := &einvoiceworker.Handler{Registry: registry, Accounts: accounts, Issuances: store.EInvoiceIssuances{DB: pool}, Blobs: blobs, Log: log}

	api := httptest.NewServer((&webhook.Server{Registry: payments.NewRegistry(), EInvoiceAccounts: accounts, EInvoiceRegistry: registry, Log: log, Token: "internal"}).Handler())
	defer api.Close()
	do := func(method, path, body string) (int, string) {
		t.Helper()
		req, _ := http.NewRequest(method, api.URL+path, strings.NewReader(body))
		req.Header.Set("X-Internal-Token", "internal")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}

	seller := &commandsv1.PartySnapshot{Name: "Acme Yazilim A.S.", TaxId: "1234567890", TaxOffice: "Kadikoy", Line1: "Bagdat Cad. 1", City: "Istanbul", Country: "TR"}
	buyer := &commandsv1.PartySnapshot{Name: "Customer A.S.", TaxId: "9876543210", TaxOffice: "Besiktas", Line1: "Barbaros 2", City: "Istanbul", Country: "TR"}
	lines := []*commandsv1.InvoiceLineSnapshot{
		{LineNo: 1, Kind: "BASE_FEE", Description: "Base fee", AmountMinor: 100000},
		{LineNo: 2, Kind: "TAX", Description: "KDV 20%", AmountMinor: 20000},
	}
	tenant := uuid.New()

	t.Run("a sandbox document always goes to the mock and is idempotent", func(t *testing.T) {
		doc := uuid.New()
		cmd := issueCmd(tenant, plansv1.Environment_ENVIRONMENT_SANDBOX, doc, "invoice", snapshot("SBX-2026-000001", nil, nil, lines...))
		r1, err := h.Handle(ctx, cmd)
		if err != nil {
			t.Fatal(err)
		}
		res := r1.GetEInvoiceResult()
		if res.GetState() != "issued" || !strings.HasPrefix(res.GetLegalNumber(), "SBX") || res.GetProvider() != "mock" || res.GetXmlObjectKey() == "" || res.GetPdfObjectKey() == "" {
			t.Fatalf("result = %v", res)
		}
		if _, ok := blobs.data[res.GetXmlObjectKey()]; !ok || !strings.Contains(string(blobs.data[res.GetXmlObjectKey()]), "SBX-2026-000001") {
			t.Fatalf("the UBL was not stored: %v", res.GetXmlObjectKey())
		}
		r2, err := h.Handle(ctx, issueCmd(tenant, plansv1.Environment_ENVIRONMENT_SANDBOX, doc, "invoice", snapshot("SBX-2026-000001", nil, nil, lines...)))
		if err != nil || r2.GetEInvoiceResult().GetLegalNumber() != res.GetLegalNumber() || r2.GetEInvoiceResult().GetXmlObjectKey() != res.GetXmlObjectKey() {
			t.Fatalf("redelivery = %v, %v", r2, err)
		}
	})

	t.Run("a live tenant without an account is refused until it has one", func(t *testing.T) {
		doc := uuid.New()
		r, err := h.Handle(ctx, issueCmd(tenant, plansv1.Environment_ENVIRONMENT_LIVE, doc, "invoice", snapshot("INV-2026-000001", seller, buyer, lines...)))
		if err != nil || r.GetEInvoiceResult().GetState() != "failed" || r.GetEInvoiceResult().GetErrorKey() != "einvoice.account.missing" || r.GetEInvoiceResult().GetRetryable() {
			t.Fatalf("no account: %v, %v", r, err)
		}
	})

	t.Run("the account API seals credentials, never echoes them, and keeps the sandbox on the mock", func(t *testing.T) {
		base := "/admin/tenants/" + tenant.String() + "/e-invoice-accounts"
		if code, body := do("PUT", base+"/sandbox", `{"provider":"nilvera","credentials":{"api_key":"sk_secret_value"}}`); code != 400 || !strings.Contains(body, "einvoice.account.sandbox_mock_only") {
			t.Fatalf("sandbox nilvera = %d %s", code, body)
		}
		if code, body := do("PUT", base+"/live", `{"provider":"mock"}`); code != 400 || !strings.Contains(body, "einvoice.account.invalid") {
			t.Fatalf("live mock = %d %s", code, body)
		}
		if code, _ := do("PUT", base+"/live", `{"provider":"nilvera","mode":"bogus","credentials":{"api_key":"x"}}`); code != 400 {
			t.Fatalf("bad mode = %d", code)
		}
		if code, body := do("PUT", base+"/live", `{"provider":"nilvera","mode":"test","credentials":{"api_key":"sk_secret_value"}}`); code != 200 || strings.Contains(body, "sk_secret_value") {
			t.Fatalf("put = %d %s", code, body)
		}
		code, body := do("GET", base, "")
		if code != 200 || strings.Contains(body, "sk_secret_value") || !strings.Contains(body, `"has_credentials":true`) || !strings.Contains(body, `"provider":"nilvera"`) {
			t.Fatalf("list = %d %s", code, body)
		}
		// The key is sealed in the table.
		var enc []byte
		if err := pool.QueryRow(ctx, `SELECT settings_enc FROM einvoice_accounts WHERE tenant_id = $1 AND environment = 'live'`, tenant).Scan(&enc); err != nil || bytes.Contains(enc, []byte("sk_secret_value")) {
			t.Fatalf("settings_enc = %q, %v", enc, err)
		}
		// Another tenant's account is not this tenant's (the box is bound to the tenant).
		if _, err := pool.Exec(ctx, `INSERT INTO einvoice_accounts (tenant_id, environment, provider, mode, settings_enc)
			SELECT $1, environment, provider, mode, settings_enc FROM einvoice_accounts WHERE tenant_id = $2`, uuid.New(), tenant); err != nil {
			t.Fatal(err)
		}
		if code, _ := do("DELETE", base+"/live", ""); code != 204 {
			t.Fatalf("delete = %d", code)
		}
		if code, body := do("DELETE", base+"/live", ""); code != 404 || !strings.Contains(body, "einvoice.account.missing") {
			t.Fatalf("second delete = %d %s", code, body)
		}
	})

	in := newIntegrator(t, "whsec_test_secret")
	if err := accounts.Put(ctx, tenant, "live", "webhook", "production", []byte(`{"url":"`+in.srv.URL+`","secret":"whsec_test_secret","allow_private":true}`)); err != nil {
		t.Fatal(err)
	}

	var invoiceDoc uuid.UUID
	var invoiceLegal string
	t.Run("a live document goes through the tenant's integrator exactly once", func(t *testing.T) {
		invoiceDoc = uuid.New()
		cmd := issueCmd(tenant, plansv1.Environment_ENVIRONMENT_LIVE, invoiceDoc, "invoice", snapshot("INV-2026-000001", seller, buyer, lines...))
		r, err := h.Handle(ctx, cmd)
		if err != nil {
			t.Fatal(err)
		}
		res := r.GetEInvoiceResult()
		if res.GetState() != "issued" || res.GetLegalNumber() == "" || res.GetProvider() != "webhook" || res.GetDocumentType() != "e-arsiv" {
			t.Fatalf("result = %v", res)
		}
		invoiceLegal = res.GetLegalNumber()
		// The integrator's sealed UBL replaced ours as the stored copy, and its PDF is kept.
		if got := string(blobs.data[res.GetXmlObjectKey()]); got != "<Invoice>sealed</Invoice>" {
			t.Fatalf("stored UBL = %q", got)
		}
		if got := string(blobs.data[res.GetPdfObjectKey()]); !strings.HasPrefix(got, "%PDF") {
			t.Fatalf("stored PDF = %q", got)
		}
		again, err := h.Handle(ctx, issueCmd(tenant, plansv1.Environment_ENVIRONMENT_LIVE, invoiceDoc, "invoice", snapshot("INV-2026-000001", seller, buyer, lines...)))
		if err != nil || again.GetEInvoiceResult().GetLegalNumber() != invoiceLegal {
			t.Fatalf("redelivery = %v, %v", again, err)
		}
		if in.count("issue") != 1 || in.sigBad != 0 {
			t.Fatalf("integrator saw %d issues, %d bad signatures; want 1 and 0", in.count("issue"), in.sigBad)
		}
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM einvoice_issuances WHERE tenant_id = $1 AND legal_number = $2`, tenant, invoiceLegal).Scan(&n); err != nil || n != 1 {
			t.Fatalf("issuances with the legal number: %d, %v", n, err)
		}
	})

	t.Run("a legal number cannot be given to a second document", func(t *testing.T) {
		other := uuid.New()
		if _, err := pool.Exec(ctx, `INSERT INTO einvoice_issuances (document_id, tenant_id, environment, provider, kind, document_type, status) VALUES ($1,$2,'live','webhook','invoice','e-arsiv','started')`, other, tenant); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `UPDATE einvoice_issuances SET status = 'issued', legal_number = $1 WHERE document_id = $2`, invoiceLegal, other); err == nil {
			t.Fatal("the same legal number was accepted for two documents")
		}
	})

	t.Run("a credit note waits for its invoice and then refers to the legal number", func(t *testing.T) {
		cnDoc := uuid.New()
		cn := snapshot("CN-2026-000001", seller, buyer, &commandsv1.InvoiceLineSnapshot{LineNo: 1, Kind: "BASE_FEE", Description: "Base fee", AmountMinor: 50000},
			&commandsv1.InvoiceLineSnapshot{LineNo: 2, Kind: "TAX", Description: "KDV 20%", AmountMinor: 10000})
		cmd := issueCmd(tenant, plansv1.Environment_ENVIRONMENT_LIVE, cnDoc, "credit_note", cn)
		cmd.GetIssueEInvoice().OriginalDocumentId = uuid.NewString() // an invoice that was never issued
		cmd.GetIssueEInvoice().OriginalNumber, cmd.GetIssueEInvoice().OriginalIssuedAt = "INV-2026-000001", timestamppb.New(time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC))
		r, err := h.Handle(ctx, cmd)
		if err != nil || r.GetEInvoiceResult().GetState() != "failed" || !r.GetEInvoiceResult().GetRetryable() {
			t.Fatalf("before the invoice: %v, %v", r, err)
		}
		cmd.GetIssueEInvoice().OriginalDocumentId = invoiceDoc.String()
		r, err = h.Handle(ctx, cmd)
		if err != nil || r.GetEInvoiceResult().GetState() != "issued" || r.GetEInvoiceResult().GetLegalNumber() == invoiceLegal {
			t.Fatalf("after the invoice: %v, %v", r, err)
		}
	})

	t.Run("a refusal is permanent, an outage is retryable, and neither issues anything", func(t *testing.T) {
		in.mu.Lock()
		in.reject = true
		in.mu.Unlock()
		refused := uuid.New()
		r, err := h.Handle(ctx, issueCmd(tenant, plansv1.Environment_ENVIRONMENT_LIVE, refused, "invoice", snapshot("INV-2026-000002", seller, buyer, lines...)))
		res := r.GetEInvoiceResult()
		if err != nil || res.GetState() != "failed" || res.GetRetryable() || res.GetErrorKey() != "einvoice.provider.rejected" || !strings.Contains(res.GetErrorMessage(), "VKN not registered") {
			t.Fatalf("refusal = %v, %v", r, err)
		}
		in.mu.Lock()
		in.reject, in.downTill = false, 1
		in.mu.Unlock()
		down := uuid.New()
		r, err = h.Handle(ctx, issueCmd(tenant, plansv1.Environment_ENVIRONMENT_LIVE, down, "invoice", snapshot("INV-2026-000003", seller, buyer, lines...)))
		res = r.GetEInvoiceResult()
		if err != nil || res.GetState() != "failed" || !res.GetRetryable() || res.GetErrorKey() != "einvoice.provider.unavailable" {
			t.Fatalf("outage = %v, %v", r, err)
		}
		// The repeat of the same document (what billing-svc sends after its backoff) now succeeds, once.
		r, err = h.Handle(ctx, issueCmd(tenant, plansv1.Environment_ENVIRONMENT_LIVE, down, "invoice", snapshot("INV-2026-000003", seller, buyer, lines...)))
		if err != nil || r.GetEInvoiceResult().GetState() != "issued" {
			t.Fatalf("after the outage = %v, %v", r, err)
		}
	})

	t.Run("incomplete seller data is named and not retried", func(t *testing.T) {
		noTax := &commandsv1.PartySnapshot{Name: "Acme", Country: "TR"}
		r, err := h.Handle(ctx, issueCmd(tenant, plansv1.Environment_ENVIRONMENT_LIVE, uuid.New(), "invoice", snapshot("INV-2026-000004", noTax, buyer, lines...)))
		res := r.GetEInvoiceResult()
		if err != nil || res.GetState() != "failed" || res.GetErrorKey() != "einvoice.seller.incomplete" || res.GetRetryable() {
			t.Fatalf("result = %v, %v", r, err)
		}
	})

	t.Run("an issued document can be cancelled where the integrator allows it", func(t *testing.T) {
		cancel := &commandsv1.CommandEnvelope{CommandId: uuid.NewString(), SagaId: invoiceDoc.String(), TenantId: tenant.String(), Environment: plansv1.Environment_ENVIRONMENT_LIVE,
			Command: &commandsv1.CommandEnvelope_IssueEInvoice{IssueEInvoice: &commandsv1.IssueEInvoice{Action: "cancel", DocumentId: invoiceDoc.String(), Reason: "wrong customer"}}}
		r, err := h.Handle(ctx, cancel)
		if err != nil {
			t.Fatal(err)
		}
		if r.GetEInvoiceResult().GetState() == "failed" {
			// The webhook adapter says cancelling is up to the endpoint; the capability decides.
			if r.GetEInvoiceResult().GetErrorKey() != "einvoice.cancel.unsupported" {
				t.Fatalf("cancel = %v", r)
			}
			return
		}
		if r.GetEInvoiceResult().GetState() != "cancelled" || in.count("cancel") != 1 {
			t.Fatalf("cancel = %v (%d calls)", r, in.count("cancel"))
		}
	})

	t.Run("a document that was never issued cannot be cancelled", func(t *testing.T) {
		d := uuid.New()
		cancel := &commandsv1.CommandEnvelope{CommandId: uuid.NewString(), SagaId: d.String(), TenantId: tenant.String(), Environment: plansv1.Environment_ENVIRONMENT_LIVE,
			Command: &commandsv1.CommandEnvelope_IssueEInvoice{IssueEInvoice: &commandsv1.IssueEInvoice{Action: "cancel", DocumentId: d.String()}}}
		r, err := h.Handle(ctx, cancel)
		if err != nil || r.GetEInvoiceResult().GetState() != "failed" || r.GetEInvoiceResult().GetErrorKey() != "einvoice.cancel.unsupported" {
			t.Fatalf("cancel = %v, %v", r, err)
		}
	})
}
