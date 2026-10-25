//go:build integration

package itest

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/api"
)

type seedInvoice struct {
	period, currency, provider, country, status string
	total                                       int64
	saga                                        string // invoice_sagas.state, empty for none
	dunning                                     int
	refunded                                    bool
}

func seedInvoices(t *testing.T, tid uuid.UUID, rows []seedInvoice) {
	t.Helper()
	ctx := context.Background()
	for i, r := range rows {
		id := uuid.New()
		var country any
		if r.country != "" {
			country = r.country
		}
		if _, err := shared.system.Exec(ctx, `INSERT INTO invoices (tenant_id, id, customer_id, period_id, kind, plan_version_id, currency, subtotal_minor, total_minor, status, snapshot, provider, country)
			VALUES ($1,$2,$3,$4,'REGULAR',$5,$6,$7,$7,$8,$9::jsonb,$10,$11)`,
			tid, id, uuid.New(), r.period, uuid.New(), r.currency, r.total, r.status,
			`{"customer_external_id":"cus_`+string(rune('a'+i))+`","environment":"live"}`, r.provider, country); err != nil {
			t.Fatal(err)
		}
		if r.saga == "" {
			continue
		}
		saga := uuid.New()
		if _, err := shared.system.Exec(ctx, `INSERT INTO invoice_sagas (tenant_id, id, invoice_id, state, version, dunning_attempt, next_attempt_at, last_error)
			VALUES ($1,$2,$3,$4,1,$5, CASE WHEN $4 = 'PAYMENT_DECLINED' THEN now() + interval '1 hour' END, CASE WHEN $4 = 'UNCOLLECTIBLE' THEN 'card_declined' END)`,
			tid, saga, id, r.saga, r.dunning); err != nil {
			t.Fatal(err)
		}
		if r.refunded {
			if _, err := shared.system.Exec(ctx, `INSERT INTO saga_events (tenant_id, saga_id, seq, from_state, to_state, cause) VALUES ($1,$2,1,'REFUND_REQUESTED','REFUNDED','reply:PaymentRefunded')`, tid, saga); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func testReports(t *testing.T) {
	e := newEnv(t, envOpts{})
	other := uuid.New()
	seedInvoices(t, e.tid, []seedInvoice{
		{period: "2026-09", currency: "TRY", provider: "iyzico", country: "TR", status: "paid", total: 1000, saga: "COMPLETED"},
		{period: "2026-09", currency: "TRY", provider: "iyzico", country: "TR", status: "open", total: 500, saga: "PAYMENT_DECLINED", dunning: 1},
		{period: "2026-10", currency: "USD", provider: "paddle", country: "DE", status: "void", total: 2000, saga: "VOIDED", refunded: true},
		{period: "2026-10", currency: "USD", provider: "paddle", country: "DE", status: "uncollectible", total: 300, saga: "UNCOLLECTIBLE", dunning: 3},
		{period: "2026-10", currency: "USD", provider: "paddle", country: "US", status: "paid", total: 700, saga: "COMPLETED"},
		{period: "2026-10", currency: "USD", provider: "", country: "", status: "paid", total: 50, saga: "COMPLETED"},
		{period: "2026-10", currency: "USD", provider: "paddle", country: "US", status: "draft", total: 99999},
	})
	seedInvoices(t, other, []seedInvoice{{period: "2026-10", currency: "USD", provider: "paddle", country: "US", status: "paid", total: 123456, saga: "COMPLETED"}})
	svc := api.New(shared.app, shared.system, e.mgr, e.eng)
	ctx := tenant.NewContext(context.Background(), tenant.Info{ID: e.tid})

	rows := func(from, to string, group ...string) map[string]*billingv1.RevenueRow {
		t.Helper()
		r, err := svc.GetRevenueReport(ctx, &billingv1.GetRevenueReportRequest{FromPeriod: from, ToPeriod: to, GroupBy: group})
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]*billingv1.RevenueRow{}
		for _, row := range r.GetRows() {
			out[row.GetPeriod()+"|"+row.GetProvider()+"|"+row.GetCountry()+"|"+row.GetCurrency()] = row
		}
		return out
	}

	t.Run("by period and currency, never across currencies", func(t *testing.T) {
		got := rows("", "")
		if len(got) != 2 {
			t.Fatalf("rows = %v; two periods, one currency each", got)
		}
		sep, oct := got["2026-09|||TRY"], got["2026-10|||USD"]
		if sep == nil || oct == nil {
			t.Fatalf("rows = %v", got)
		}
		if sep.GetInvoices() != 2 || sep.GetBilledMinor() != 1500 || sep.GetCollectedMinor() != 1000 || sep.GetOutstandingMinor() != 500 || sep.GetRefundedMinor() != 0 {
			t.Errorf("september = %v", sep)
		}
		// October: void 2000 (refunded), uncollectible 300, paid 700 + 50. The draft and the other tenant's invoice are not here.
		if oct.GetInvoices() != 4 || oct.GetBilledMinor() != 1050 || oct.GetCollectedMinor() != 750 || oct.GetRefundedMinor() != 2000 ||
			oct.GetUncollectibleMinor() != 300 || oct.GetVoidMinor() != 2000 || oct.GetOutstandingMinor() != 0 {
			t.Errorf("october = %v", oct)
		}
	})
	t.Run("by provider and country", func(t *testing.T) {
		got := rows("", "", "provider", "country")
		if r := got["|iyzico|TR|TRY"]; r == nil || r.GetBilledMinor() != 1500 {
			t.Errorf("iyzico/TR = %v", r)
		}
		if r := got["|paddle|DE|USD"]; r == nil || r.GetBilledMinor() != 300 || r.GetRefundedMinor() != 2000 {
			t.Errorf("paddle/DE = %v", r)
		}
		if r := got["|paddle|US|USD"]; r == nil || r.GetCollectedMinor() != 700 || r.GetInvoices() != 1 {
			t.Errorf("paddle/US = %v; the draft is not counted", r)
		}
		if r := got["|||USD"]; r == nil || r.GetCollectedMinor() != 50 {
			t.Errorf("an invoice with no provider and no country = %v", r)
		}
	})
	t.Run("a range of periods", func(t *testing.T) {
		if got := rows("2026-10", "2026-10"); len(got) != 1 || got["2026-10|||USD"] == nil {
			t.Errorf("rows = %v", got)
		}
		if got := rows("2026-11", ""); len(got) != 0 {
			t.Errorf("nothing after October: %v", got)
		}
	})
	t.Run("bad requests", func(t *testing.T) {
		for name, req := range map[string]*billingv1.GetRevenueReportRequest{
			"bad period":    {FromPeriod: "2026-13"},
			"reversed":      {FromPeriod: "2026-10", ToPeriod: "2026-09"},
			"unknown split": {GroupBy: []string{"customer"}},
		} {
			if _, err := svc.GetRevenueReport(ctx, req); status.Code(err) != codes.InvalidArgument {
				t.Errorf("%s: %v", name, err)
			}
		}
		if _, err := svc.GetRevenueReport(context.Background(), &billingv1.GetRevenueReportRequest{}); err == nil {
			t.Error("a report without a tenant must be refused")
		}
	})
	t.Run("dunning", func(t *testing.T) {
		d, err := svc.GetDunningReport(ctx, &billingv1.GetDunningReportRequest{})
		if err != nil {
			t.Fatal(err)
		}
		if len(d.GetInvoices()) != 2 {
			t.Fatalf("invoices = %v; one declined and retrying, one given up", d.GetInvoices())
		}
		states := map[string]*billingv1.DunningInvoice{}
		for _, inv := range d.GetInvoices() {
			states[inv.GetState()] = inv
		}
		if r := states["PAYMENT_DECLINED"]; r == nil || r.GetDunningAttempt() != 1 || r.GetNextAttemptTime() == nil || r.GetCurrency() != "TRY" || r.GetTotalMinor() != 500 || r.GetProvider() != "iyzico" {
			t.Errorf("declined = %v", r)
		}
		if r := states["UNCOLLECTIBLE"]; r == nil || r.GetDunningAttempt() != 3 || r.GetLastError() != "card_declined" || r.GetProvider() != "paddle" {
			t.Errorf("uncollectible = %v", r)
		}
		if len(d.GetSummary()) != 2 {
			t.Errorf("summary = %v", d.GetSummary())
		}
		if _, err := svc.GetDunningReport(ctx, &billingv1.GetDunningReportRequest{Limit: 1000}); status.Code(err) != codes.InvalidArgument {
			t.Errorf("limit 1000: %v", err)
		}
		one, _ := svc.GetDunningReport(ctx, &billingv1.GetDunningReportRequest{Limit: 1})
		if len(one.GetInvoices()) != 1 || len(one.GetSummary()) != 2 {
			t.Errorf("limit 1: %d invoices, %d summary rows; the summary is never limited", len(one.GetInvoices()), len(one.GetSummary()))
		}
	})
}
