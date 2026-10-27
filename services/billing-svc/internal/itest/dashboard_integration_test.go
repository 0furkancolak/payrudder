//go:build integration

package itest

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/accounting"
)

// seedInvoiceOf inserts an invoice of a named customer; a paid one also gets its saga history, an open one a finalize time `age` days ago.
func seedInvoiceOf(t *testing.T, tid, customer uuid.UUID, ref, name, currency string, total int64, status string, paidAt time.Time, age int) {
	t.Helper()
	ctx := context.Background()
	id, saga := uuid.New(), uuid.New()
	var finalized any
	if status == "open" {
		finalized = time.Now().AddDate(0, 0, -age)
	}
	if _, err := shared.sql.Exec(ctx, `INSERT INTO invoices (tenant_id, id, customer_id, period_id, kind, plan_version_id, currency, subtotal_minor, total_minor, status, snapshot, provider, finalized_at)
		VALUES ($1,$2,$3,$4,'REGULAR',$5,$6,$7,$7,$8,jsonb_build_object('customer_external_id', $9::text, 'customer_name', $10::text, 'environment', 'live'),'',$11)`,
		tid, id, customer, uuid.NewString()[:7], uuid.New(), currency, total, status, ref, name, finalized); err != nil {
		t.Fatal(err)
	}
	if status != "paid" {
		return
	}
	if _, err := shared.sql.Exec(ctx, `INSERT INTO invoice_sagas (tenant_id, id, invoice_id, state, version) VALUES ($1,$2,$3,'PAID',1)`, tid, saga, id); err != nil {
		t.Fatal(err)
	}
	if _, err := shared.sql.Exec(ctx, `INSERT INTO saga_events (tenant_id, saga_id, seq, from_state, to_state, cause, at) VALUES ($1,$2,1,'PAYMENT_PENDING','PAID','reply:PaymentSucceeded',$3)`, tid, saga, paidAt); err != nil {
		t.Fatal(err)
	}
}

// testDashboardStats: the customers who paid most in a period, per currency and never summed across currencies; the open invoices by age; another tenant's
// invoices are not seen; a bad period is refused with its key.
func testDashboardStats(t *testing.T) {
	infra(t)
	svc := accounting.New(shared.app, shared.system)
	tid, other := uuid.New(), uuid.New()
	ctx := tenant.NewContext(context.Background(), tenant.Info{ID: tid})
	ada, bob, cy := uuid.New(), uuid.New(), uuid.New()
	now := time.Now().UTC()
	in := now.Add(-48 * time.Hour)

	seedInvoiceOf(t, tid, ada, "cus_ada", "Ada", "USD", 5000, "paid", in, 0)
	seedInvoiceOf(t, tid, ada, "cus_ada", "Ada", "USD", 2500, "paid", in, 0)
	seedInvoiceOf(t, tid, bob, "cus_bob", "Bob", "USD", 9000, "paid", in, 0)
	seedInvoiceOf(t, tid, cy, "cus_cy", "Cy", "USD", 100, "paid", in, 0)
	seedInvoiceOf(t, tid, bob, "cus_bob", "Bob", "EUR", 700, "paid", in, 0)
	seedInvoiceOf(t, tid, ada, "cus_ada", "Ada", "USD", 4000, "paid", now.AddDate(0, -3, 0), 0) // before the period
	for _, age := range []int{0, 5, 8, 20, 45, 90} {
		seedInvoiceOf(t, tid, ada, "cus_ada", "Ada", "TRY", 1000, "open", time.Time{}, age)
	}
	seedInvoiceOf(t, other, ada, "cus_ada", "Ada", "USD", 777777, "paid", in, 0) // somebody else's

	from, to := now.AddDate(0, 0, -29).Format("2006-01-02"), now.Format("2006-01-02")
	r, err := svc.GetDashboardStats(ctx, &billingv1.GetDashboardStatsRequest{FromDate: from, ToDate: to, Top: 2})
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]*billingv1.DashboardCurrency{}
	for _, c := range r.GetCurrencies() {
		by[c.GetCurrency()] = c
	}
	if len(by) != 3 || by["USD"] == nil || by["EUR"] == nil || by["TRY"] == nil {
		t.Fatalf("currencies = %v", r.GetCurrencies())
	}
	usd := by["USD"].GetTopCustomers()
	if len(usd) != 2 || usd[0].GetCustomerExternalId() != "cus_bob" || usd[0].GetIncomeMinor() != 9000 || usd[1].GetCustomerExternalId() != "cus_ada" || usd[1].GetIncomeMinor() != 7500 || usd[1].GetInvoices() != 2 || usd[1].GetCustomerName() != "Ada" {
		t.Errorf("USD top customers (top 2, the old invoice and the other tenant's left out) = %v", usd)
	}
	if eur := by["EUR"].GetTopCustomers(); len(eur) != 1 || eur[0].GetIncomeMinor() != 700 {
		t.Errorf("EUR top customers = %v", eur)
	}
	// A currency's block never carries another currency's money, and the other tenant's 777777 is nowhere.
	for _, c := range r.GetCurrencies() {
		for _, tc := range c.GetTopCustomers() {
			if tc.GetIncomeMinor() == 777777 {
				t.Errorf("another tenant's invoice is in %s", c.GetCurrency())
			}
		}
	}
	// Open invoices by age, in the order of the ages.
	var buckets []string
	counts := map[string]int64{}
	for _, a := range by["TRY"].GetAging() {
		buckets = append(buckets, a.GetBucket())
		counts[a.GetBucket()] = a.GetCount()
		if a.GetAmountMinor() != a.GetCount()*1000 {
			t.Errorf("bucket %s amount = %d for %d invoices", a.GetBucket(), a.GetAmountMinor(), a.GetCount())
		}
	}
	if len(buckets) != 4 || buckets[0] != "0-7" || buckets[1] != "8-30" || buckets[2] != "31-60" || buckets[3] != "61+" || counts["0-7"] != 2 || counts["8-30"] != 2 || counts["31-60"] != 1 || counts["61+"] != 1 {
		t.Errorf("aging = %v %v", buckets, counts)
	}
	// The newest invoices come newest first, and only this tenant's.
	if len(r.GetRecentInvoices()) != 8 {
		t.Fatalf("recent invoices = %d, want 8", len(r.GetRecentInvoices()))
	}
	for i := 1; i < len(r.GetRecentInvoices()); i++ {
		if r.GetRecentInvoices()[i].GetId() >= r.GetRecentInvoices()[i-1].GetId() {
			t.Errorf("recent invoices are not newest first at %d", i)
		}
	}
	for _, i := range r.GetRecentInvoices() {
		if i.GetTotalMinor() == 777777 {
			t.Error("another tenant's invoice is in the feed")
		}
	}
	// An empty tenant has no blocks, not an error; the default of top is 5.
	if e, err := svc.GetDashboardStats(tenant.NewContext(context.Background(), tenant.Info{ID: uuid.New()}), &billingv1.GetDashboardStatsRequest{FromDate: from, ToDate: to}); err != nil || len(e.GetCurrencies()) != 0 {
		t.Errorf("empty tenant = %v, %v", e, err)
	}
	for _, c := range []struct{ from, to, key string }{
		{"nope", to, "billing.accounting.bad_period"},
		{to, from, "billing.accounting.bad_period"},
		{"2015-01-01", to, "billing.accounting.range_too_long"},
	} {
		_, err := svc.GetDashboardStats(ctx, &billingv1.GetDashboardStatsRequest{FromDate: c.from, ToDate: c.to})
		wantKey(t, err, codes.InvalidArgument, c.key)
	}
}
