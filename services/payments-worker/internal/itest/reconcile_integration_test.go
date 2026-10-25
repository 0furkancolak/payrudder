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
	"testing"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
	"github.com/0furkancolak/payrudder/pkg/payments"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/secretbox"
	"github.com/0furkancolak/payrudder/pkg/testkit"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/handler"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/webhook"
	"github.com/0furkancolak/payrudder/services/payments-worker/migrations"
)

type fakeBilling struct{ invoices map[string]*billingv1.Invoice }

func (f fakeBilling) ListInvoices(_ context.Context, _ uuid.UUID, req *billingv1.ListInvoicesRequest) (*billingv1.ListInvoicesResponse, error) {
	out := &billingv1.ListInvoicesResponse{}
	for _, inv := range f.invoices {
		if inv.GetPeriodId() == req.GetPeriodId() && inv.GetStatus() == req.GetStatus() {
			out.Invoices = append(out.Invoices, inv)
		}
	}
	return out, nil
}

func (f fakeBilling) GetInvoice(_ context.Context, _ uuid.UUID, id string) (*billingv1.Invoice, error) {
	if inv, ok := f.invoices[id]; ok {
		return inv, nil
	}
	return nil, status.Error(codes.NotFound, "no such invoice")
}

func TestReconciliation(t *testing.T) {
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
	tenant := uuid.New()
	ledger := store.Ledger{DB: pool}

	mkInv := func(status string, total int64, period string) *billingv1.Invoice {
		return &billingv1.Invoice{Id: uuid.NewString(), Number: "INV-" + uuid.NewString()[:6], PeriodId: period, Currency: "USD", TotalMinor: total, Status: status}
	}
	a, b, c, d, e, f := mkInv("paid", 1000, "2026-10"), mkInv("paid", 500, "2026-10"), mkInv("paid", 700, "2026-10"), mkInv("paid", 900, "2026-10"),
		mkInv("open", 400, "2026-10"), mkInv("paid", 0, "2026-10")
	other := mkInv("open", 300, "2026-09") // another period: its charge is not this report's business
	billing := fakeBilling{invoices: map[string]*billingv1.Invoice{}}
	for _, inv := range []*billingv1.Invoice{a, b, c, d, e, f, other} {
		billing.invoices[inv.Id] = inv
	}
	charge := func(inv *billingv1.Invoice, ref string, amount int64, status string) {
		t.Helper()
		if _, err := ledger.Append(ctx, store.LedgerEntry{TenantID: tenant, InvoiceID: uuid.MustParse(inv.Id), Provider: "paddle", Environment: "live", Kind: "charge",
			Status: status, AmountMinor: amount, Currency: "USD", ProviderRef: ref, Source: "webhook"}); err != nil {
			t.Fatal(err)
		}
	}
	charge(a, "txn_a", 1000, "succeeded")
	// b: no charge at all
	charge(c, "txn_c", 650, "succeeded")
	charge(d, "txn_d1", 900, "succeeded")
	charge(d, "txn_d2", 900, "succeeded")
	charge(e, "txn_e", 400, "succeeded")
	charge(other, "txn_o", 300, "succeeded")
	charge(f, "txn_x", 100, "succeeded")
	charge(f, "txn_x", 100, "declined") // the same reference both succeeded and declined

	box, _ := secretbox.New(bytes.Repeat([]byte{1}, 32))
	registry := payments.NewRegistry()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := &webhook.Server{Registry: registry, Resolver: &handler.Resolver{Registry: registry, Accounts: store.Accounts{DB: pool, Box: box}}, Ledger: ledger, Billing: billing, Log: log, Token: "internal"}
	api := httptest.NewServer(srv.Handler())
	defer api.Close()
	get := func(path string) (int, []byte) {
		req, _ := http.NewRequest("GET", api.URL+path, nil)
		req.Header.Set("X-Internal-Token", "internal")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, b
	}

	code, body := get("/admin/tenants/" + tenant.String() + "/reconciliation?period=2026-10")
	if code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	var rec webhook.Reconciliation
	if err := json.Unmarshal(body, &rec); err != nil {
		t.Fatal(err)
	}
	if rec.PaidInvoices != 5 {
		t.Errorf("paid invoices = %d", rec.PaidInvoices)
	}
	want := map[string]int{webhook.PaidWithoutCharge: 1, webhook.AmountMismatch: 1, webhook.DuplicateCharge: 1, webhook.ChargeWithoutPaid: 1, webhook.ConflictingOutcomes: 1}
	for k, n := range want {
		if rec.Counts[k] != n {
			t.Errorf("%s = %d, want %d (all: %v)", k, rec.Counts[k], n, rec.Counts)
		}
	}
	byKind := map[string]webhook.Discrepancy{}
	for _, x := range rec.Discrepancies {
		byKind[x.Kind] = x
	}
	if x := byKind[webhook.PaidWithoutCharge]; x.InvoiceID != b.Id {
		t.Errorf("paid without a charge = %+v; want invoice b", x)
	}
	if x := byKind[webhook.AmountMismatch]; x.InvoiceID != c.Id || x.BilledMinor != 700 || x.LedgerMinor != 650 {
		t.Errorf("amount mismatch = %+v", x)
	}
	if x := byKind[webhook.DuplicateCharge]; x.InvoiceID != d.Id {
		t.Errorf("duplicate = %+v", x)
	}
	if x := byKind[webhook.ChargeWithoutPaid]; x.InvoiceID != e.Id || x.Detail != "invoice status open" {
		t.Errorf("charge without paid = %+v", x)
	}
	if x := byKind[webhook.ConflictingOutcomes]; x.ProviderRef != "txn_x" {
		t.Errorf("conflict = %+v", x)
	}
	for _, x := range rec.Discrepancies {
		if (x.InvoiceID == f.Id || x.InvoiceID == a.Id) && x.Kind != webhook.ConflictingOutcomes {
			t.Errorf("an agreeing invoice was reported: %+v", x)
		}
		if x.InvoiceID == other.Id {
			t.Errorf("another period's invoice was reported: %+v", x)
		}
	}

	if code, _ := get("/admin/tenants/" + tenant.String() + "/reconciliation?period=2026-9"); code != 400 {
		t.Errorf("bad period: %d", code)
	}
	if code, _ := get("/admin/tenants/" + tenant.String() + "/reconciliation"); code != 400 {
		t.Errorf("no period: %d", code)
	}
}
