//go:build integration

package itest

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/accounting"
)

// seedPaidInvoice inserts an invoice, its saga and the saga events of its history: paidAt is when it reached PAID, refundedAt (zero for none) when it was refunded.
func seedPaidInvoice(t *testing.T, tid uuid.UUID, currency string, total, tax int64, paidAt, refundedAt time.Time) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	id, saga := uuid.New(), uuid.New()
	status := "paid"
	if !refundedAt.IsZero() {
		status = "void"
	}
	if _, err := shared.sql.Exec(ctx, `INSERT INTO invoices (tenant_id, id, customer_id, period_id, kind, plan_version_id, currency, subtotal_minor, total_minor, status, snapshot, provider)
		VALUES ($1,$2,$3,'2026-09','REGULAR',$4,$5,$6,$7,$8,'{"customer_external_id":"cus_acc","environment":"live"}'::jsonb,'')`, tid, id, uuid.New(), uuid.New(), currency, total-tax, total, status); err != nil {
		t.Fatal(err)
	}
	if tax > 0 {
		if _, err := shared.sql.Exec(ctx, `INSERT INTO invoice_lines (tenant_id, invoice_id, line_no, kind, amount_minor, description) VALUES ($1,$2,1,'BASE_FEE',$3,'fee'), ($1,$2,2,'TAX',$4,'VAT')`,
			tid, id, total-tax, tax); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := shared.sql.Exec(ctx, `INSERT INTO invoice_sagas (tenant_id, id, invoice_id, state, version) VALUES ($1,$2,$3,'PAID',1)`, tid, saga, id); err != nil {
		t.Fatal(err)
	}
	if _, err := shared.sql.Exec(ctx, `INSERT INTO saga_events (tenant_id, saga_id, seq, from_state, to_state, cause, at) VALUES ($1,$2,1,'PAYMENT_PENDING','PAID','reply:PaymentSucceeded',$3)`, tid, saga, paidAt); err != nil {
		t.Fatal(err)
	}
	if !refundedAt.IsZero() {
		if _, err := shared.sql.Exec(ctx, `INSERT INTO saga_events (tenant_id, saga_id, seq, from_state, to_state, cause, at) VALUES ($1,$2,2,'REFUND_REQUESTED','REFUNDED','reply:PaymentRefunded',$3)`, tid, saga, refundedAt); err != nil {
			t.Fatal(err)
		}
	}
	return id
}

func seedCreditNote(t *testing.T, tid, invoice uuid.UUID, currency string, amount int64, at time.Time) {
	t.Helper()
	if _, err := shared.sql.Exec(context.Background(), `INSERT INTO credit_notes (tenant_id, id, customer_id, invoice_id, number, currency, total_minor, created_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		tid, uuid.New(), uuid.New(), invoice, "CN-"+uuid.NewString()[:8], currency, amount, at); err != nil {
		t.Fatal(err)
	}
}

func testAccounting(t *testing.T) {
	infra(t)
	svc := accounting.New(shared.app, shared.system)
	tid, other := uuid.New(), uuid.New()
	ctx := tenant.NewContext(context.Background(), tenant.Info{ID: tid})
	otherCtx := tenant.NewContext(context.Background(), tenant.Info{ID: other})
	key := func(err error, code codes.Code, want string) {
		t.Helper()
		wantKey(t, err, code, want)
	}
	create := func(c context.Context, req *billingv1.CreateExpenseRequest) (*billingv1.Expense, error) {
		r, err := svc.CreateExpense(c, req)
		return r.GetExpense(), err
	}
	ok := func(e *billingv1.Expense, err error) *billingv1.Expense {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return e
	}

	// Validation: each mistake has its own key and writes nothing.
	for _, bad := range []struct {
		req *billingv1.CreateExpenseRequest
		key string
	}{
		{&billingv1.CreateExpenseRequest{Vendor: "", Category: "software", AmountMinor: 1, Currency: "USD", IncurredOn: "2026-10-01"}, "billing.accounting.bad_expense"},
		{&billingv1.CreateExpenseRequest{Vendor: "AWS", Category: "software", AmountMinor: 0, Currency: "USD", IncurredOn: "2026-10-01"}, "billing.accounting.bad_expense"},
		{&billingv1.CreateExpenseRequest{Vendor: "AWS", Category: "travel", AmountMinor: 1, Currency: "USD", IncurredOn: "2026-10-01"}, "billing.accounting.bad_category"},
		{&billingv1.CreateExpenseRequest{Vendor: "AWS", Category: "software", AmountMinor: 1, Currency: "XXX", IncurredOn: "2026-10-01"}, "billing.accounting.unknown_currency"},
		{&billingv1.CreateExpenseRequest{Vendor: "AWS", Category: "software", AmountMinor: 1, Currency: "USD", IncurredOn: "yesterday"}, "billing.accounting.bad_date"},
		{&billingv1.CreateExpenseRequest{Vendor: "AWS", Category: "software", AmountMinor: 1, Currency: "USD", IncurredOn: "2026-10-01", Recurring: "weekly"}, "billing.accounting.bad_recurring"},
		{&billingv1.CreateExpenseRequest{Vendor: "AWS", Category: "software", AmountMinor: 1, Currency: "USD", IncurredOn: "2026-10-01", RecurringUntil: "2026-12-01"}, "billing.accounting.bad_recurring"},
		{&billingv1.CreateExpenseRequest{Vendor: "AWS", Category: "software", AmountMinor: 1, Currency: "USD", IncurredOn: "2026-10-01", Recurring: "monthly", RecurringUntil: "2026-09-01"}, "billing.accounting.bad_recurring"},
		{&billingv1.CreateExpenseRequest{Vendor: "AWS", Category: "software", AmountMinor: 1, Currency: "USD", IncurredOn: "2026-10-01", AppId: "not-a-uuid"}, "billing.accounting.bad_app"},
	} {
		_, err := create(ctx, bad.req)
		key(err, codes.InvalidArgument, bad.key)
	}
	if list, err := svc.ListExpenses(ctx, &billingv1.ListExpensesRequest{IncludeVoided: true}); err != nil || len(list.GetExpenses()) != 0 {
		t.Fatalf("a refused expense was stored: %v %v", list, err)
	}

	// Income: October 2026 (UTC).
	oct := func(d, h int) time.Time { return time.Date(2026, 10, d, h, 0, 0, 0, time.UTC) }
	seedPaidInvoice(t, tid, "TRY", 12000, 2000, oct(5, 10), time.Time{})
	seedPaidInvoice(t, tid, "USD", 500, 0, oct(6, 23), time.Time{})
	seedPaidInvoice(t, tid, "TRY", 9999, 0, time.Date(2026, 9, 30, 23, 59, 0, 0, time.UTC), time.Time{}) // September: outside
	seedPaidInvoice(t, tid, "TRY", 8000, 0, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), time.Time{})   // the very start of November: outside
	seedPaidInvoice(t, tid, "TRY", 3000, 0, oct(7, 8), oct(8, 8))                                        // paid and refunded in full
	creditedPaid := seedPaidInvoice(t, tid, "TRY", 5000, 0, oct(1, 0), time.Time{})
	seedCreditNote(t, tid, creditedPaid, "TRY", 300, oct(9, 12))
	openInvoice := uuid.New()
	if _, err := shared.sql.Exec(context.Background(), `INSERT INTO invoices (tenant_id, id, customer_id, period_id, kind, plan_version_id, currency, subtotal_minor, total_minor, status, snapshot, provider)
		VALUES ($1,$2,$3,'2026-10','REGULAR',$4,'TRY',700,700,'open','{"environment":"live"}'::jsonb,'')`, tid, openInvoice, uuid.New(), uuid.New()); err != nil {
		t.Fatal(err)
	}
	seedCreditNote(t, tid, openInvoice, "TRY", 700, oct(9, 12)) // against an invoice nobody paid: not a refund of money
	// Another tenant's income must not leak in.
	seedPaidInvoice(t, other, "TRY", 77777, 0, oct(5, 10), time.Time{})

	// Expenses.
	aws := ok(create(ctx, &billingv1.CreateExpenseRequest{Vendor: "AWS", Category: "infrastructure", AmountMinor: 4000, Currency: "TRY", IncurredOn: "2026-10-10", Note: "compute"}))
	if aws.GetId() == "" || aws.GetRecurring() != "none" || aws.GetIncurredOn() != "2026-10-10" || aws.GetVoidTime() != nil {
		t.Fatalf("created = %v", aws)
	}
	sub := ok(create(ctx, &billingv1.CreateExpenseRequest{Vendor: "Linear", Category: "software", AmountMinor: 1000, Currency: "TRY", IncurredOn: "2026-08-31", Recurring: "monthly"}))
	fee := ok(create(ctx, &billingv1.CreateExpenseRequest{Vendor: "iyzico", Category: "payment_fees", AmountMinor: 250, Currency: "TRY", IncurredOn: "2026-10-12"}))
	app := uuid.NewString()
	ads := ok(create(ctx, &billingv1.CreateExpenseRequest{AppId: app, Vendor: "Ads", Category: "marketing", AmountMinor: 50, Currency: "USD", IncurredOn: "2026-10-15"}))
	mistake := ok(create(ctx, &billingv1.CreateExpenseRequest{Vendor: "Typo Ltd", Category: "other", AmountMinor: 999999, Currency: "TRY", IncurredOn: "2026-10-20"}))
	ended := ok(create(ctx, &billingv1.CreateExpenseRequest{Vendor: "Old tool", Category: "software", AmountMinor: 100, Currency: "EUR", IncurredOn: "2026-01-05", Recurring: "monthly", RecurringUntil: "2026-09-30"}))
	_ = ended
	ok(create(otherCtx, &billingv1.CreateExpenseRequest{Vendor: "Theirs", Category: "other", AmountMinor: 555, Currency: "TRY", IncurredOn: "2026-10-10"}))

	// Void with a reason; a voided expense is out of the summary, cannot be voided or edited again, and is still listed on request.
	_, err := svc.VoidExpense(ctx, &billingv1.VoidExpenseRequest{Id: mistake.GetId()})
	key(err, codes.InvalidArgument, "billing.accounting.reason_required")
	v, err := svc.VoidExpense(ctx, &billingv1.VoidExpenseRequest{Id: mistake.GetId(), Reason: "typed one zero too many"})
	if err != nil || v.GetExpense().GetVoidTime() == nil || v.GetExpense().GetVoidReason() != "typed one zero too many" {
		t.Fatalf("void = %v, %v", v, err)
	}
	_, err = svc.VoidExpense(ctx, &billingv1.VoidExpenseRequest{Id: mistake.GetId(), Reason: "again"})
	key(err, codes.FailedPrecondition, "billing.accounting.expense_voided")
	_, err = svc.UpdateExpense(ctx, &billingv1.UpdateExpenseRequest{Id: mistake.GetId(), Vendor: "x", Category: "other", AmountMinor: 1, Currency: "TRY", IncurredOn: "2026-10-20"})
	key(err, codes.FailedPrecondition, "billing.accounting.expense_voided")
	_, err = svc.VoidExpense(ctx, &billingv1.VoidExpenseRequest{Id: uuid.NewString(), Reason: "x"})
	key(err, codes.NotFound, "billing.accounting.expense_not_found")
	_, err = svc.VoidExpense(otherCtx, &billingv1.VoidExpenseRequest{Id: aws.GetId(), Reason: "not yours"})
	key(err, codes.NotFound, "billing.accounting.expense_not_found")

	// Edit: the whole record changes, updated_at moves, and the summary follows.
	upd, err := svc.UpdateExpense(ctx, &billingv1.UpdateExpenseRequest{Id: fee.GetId(), Vendor: "iyzico", Category: "payment_fees", AmountMinor: 400, Currency: "TRY", IncurredOn: "2026-10-12", Note: "commission, October"})
	if err != nil || upd.GetExpense().GetAmountMinor() != 400 || upd.GetExpense().GetNote() != "commission, October" {
		t.Fatalf("update = %v, %v", upd, err)
	}
	if !upd.GetExpense().GetUpdateTime().AsTime().After(fee.GetCreateTime().AsTime()) && !upd.GetExpense().GetUpdateTime().AsTime().Equal(fee.GetCreateTime().AsTime()) {
		t.Errorf("updated_at went backwards")
	}

	summary := func(c context.Context, req *billingv1.GetAccountingSummaryRequest) *billingv1.GetAccountingSummaryResponse {
		t.Helper()
		r, err := svc.GetAccountingSummary(c, req)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	cur := func(r *billingv1.GetAccountingSummaryResponse, code string) *billingv1.AccountingCurrency {
		t.Helper()
		for _, c := range r.GetCurrencies() {
			if c.GetCurrency() == code {
				return c
			}
		}
		t.Fatalf("no %s block in %v", code, r)
		return nil
	}

	r := summary(ctx, &billingv1.GetAccountingSummaryRequest{FromDate: "2026-10-01", ToDate: "2026-10-31"})
	if r.GetGranularity() != "month" && r.GetGranularity() != "day" {
		t.Fatalf("granularity = %q", r.GetGranularity())
	}
	if len(r.GetCurrencies()) != 2 {
		t.Fatalf("currencies = %v; EUR's monthly expense ended in September and must not appear", r.GetCurrencies())
	}
	try := cur(r, "TRY")
	// Paid in October: 12000 + 3000 (paid, then refunded) + 5000 = 20000; tax 2000; refunded 3000 whole + 300 credit note; the open invoice's note is not a refund.
	// Expenses: 4000 AWS + 1000 Linear (the 31st, from 31 August) + 400 fees.
	if try.GetGrossIncomeMinor() != 20000 || try.GetTaxMinor() != 2000 || try.GetRefundsMinor() != 3300 || try.GetNetIncomeMinor() != 14700 || try.GetPaidInvoices() != 3 {
		t.Errorf("TRY income = %v", try)
	}
	if try.GetExpensesMinor() != 5400 || try.GetProfitMinor() != 9300 || try.GetProviderFeesMinor() != 400 || !try.GetProviderFeesRecorded() {
		t.Errorf("TRY expenses = %v", try)
	}
	if len(try.GetExpenseByCategory()) != 3 || try.GetExpenseByCategory()[0].GetCategory() != "infrastructure" || try.GetExpenseByCategory()[0].GetAmountMinor() != 4000 {
		t.Errorf("TRY categories = %v", try.GetExpenseByCategory())
	}
	usd := cur(r, "USD")
	if usd.GetGrossIncomeMinor() != 500 || usd.GetExpensesMinor() != 50 || usd.GetProfitMinor() != 450 || usd.GetProviderFeesRecorded() {
		t.Errorf("USD = %v", usd)
	}

	// A day series covers every day, the sums of the points are the totals, and the UTC day boundary holds (paid at 23:00 on the 6th is the 6th).
	d := summary(ctx, &billingv1.GetAccountingSummaryRequest{FromDate: "2026-10-01", ToDate: "2026-10-31", Granularity: "day"})
	dt, du := cur(d, "TRY"), cur(d, "USD")
	if len(dt.GetSeries()) != 31 || dt.GetSeries()[0].GetBucket() != "2026-10-01" || dt.GetSeries()[30].GetBucket() != "2026-10-31" {
		t.Fatalf("series = %d points", len(dt.GetSeries()))
	}
	var gross, expenses int64
	for _, p := range dt.GetSeries() {
		gross += p.GetGrossIncomeMinor()
		expenses += p.GetExpensesMinor()
	}
	if gross != dt.GetGrossIncomeMinor() || expenses != dt.GetExpensesMinor() || dt.GetSeries()[30].GetExpensesMinor() != 1000 || dt.GetSeries()[4].GetGrossIncomeMinor() != 12000 {
		t.Errorf("series sums %d/%d vs %v", gross, expenses, dt)
	}
	if du.GetSeries()[5].GetGrossIncomeMinor() != 500 {
		t.Errorf("USD paid 23:00 UTC on the 6th: %v", du.GetSeries()[5])
	}

	// Recurring across a longer period, by month: Linear (monthly since August 31) is 1000 in September (the 30th) and October (the 31st).
	y := summary(ctx, &billingv1.GetAccountingSummaryRequest{FromDate: "2026-08-01", ToDate: "2026-12-31", Granularity: "month"})
	yt := cur(y, "TRY")
	var months []int64
	for _, p := range yt.GetSeries() {
		months = append(months, p.GetExpensesMinor())
	}
	if fmt.Sprint(months) != "[1000 1000 5400 1000 1000]" {
		t.Errorf("TRY expenses by month = %v", months)
	}
	if cur(y, "EUR").GetExpensesMinor() != 100*2 { // the old tool ran in August and September
		t.Errorf("EUR = %v", cur(y, "EUR"))
	}

	// The app filter limits expenses, not income.
	byApp := summary(ctx, &billingv1.GetAccountingSummaryRequest{FromDate: "2026-10-01", ToDate: "2026-10-31", AppId: app})
	if len(byApp.GetCurrencies()) != 2 || cur(byApp, "USD").GetExpensesMinor() != 50 || cur(byApp, "TRY").GetExpensesMinor() != 0 || cur(byApp, "TRY").GetGrossIncomeMinor() != 20000 {
		t.Errorf("app filter = %v", byApp)
	}

	// Tenants are isolated.
	or := summary(otherCtx, &billingv1.GetAccountingSummaryRequest{FromDate: "2026-10-01", ToDate: "2026-10-31"})
	if len(or.GetCurrencies()) != 1 || cur(or, "TRY").GetGrossIncomeMinor() != 77777 || cur(or, "TRY").GetExpensesMinor() != 555 {
		t.Errorf("other tenant = %v", or)
	}

	// An empty period is no blocks at all, not a block of zeros; a bad period is refused with its key.
	if empty := summary(ctx, &billingv1.GetAccountingSummaryRequest{FromDate: "2020-01-01", ToDate: "2020-01-31"}); len(empty.GetCurrencies()) != 0 {
		t.Errorf("empty period = %v", empty)
	}
	_, err = svc.GetAccountingSummary(ctx, &billingv1.GetAccountingSummaryRequest{FromDate: "2026-10-31", ToDate: "2026-10-01"})
	key(err, codes.InvalidArgument, "billing.accounting.bad_period")

	// The list: newest first, voided hidden unless asked, filters and paging.
	l, err := svc.ListExpenses(ctx, &billingv1.ListExpensesRequest{})
	if err != nil || len(l.GetExpenses()) != 5 || l.GetExpenses()[0].GetVendor() != "AWS" && l.GetExpenses()[0].GetVendor() != "Ads" {
		t.Fatalf("list = %v, %v", l, err)
	}
	if l, _ = svc.ListExpenses(ctx, &billingv1.ListExpensesRequest{IncludeVoided: true}); len(l.GetExpenses()) != 6 {
		t.Errorf("with voided = %d", len(l.GetExpenses()))
	}
	if l, _ = svc.ListExpenses(ctx, &billingv1.ListExpensesRequest{Category: "marketing"}); len(l.GetExpenses()) != 1 || l.GetExpenses()[0].GetId() != ads.GetId() || l.GetExpenses()[0].GetAppId() != app {
		t.Errorf("by category = %v", l)
	}
	if l, _ = svc.ListExpenses(ctx, &billingv1.ListExpensesRequest{Currency: "TRY", FromDate: "2026-10-01", ToDate: "2026-10-31"}); len(l.GetExpenses()) != 2 {
		t.Errorf("by currency and window = %v", l)
	}
	var seen []string
	token := ""
	for range 10 {
		page, err := svc.ListExpenses(ctx, &billingv1.ListExpensesRequest{IncludeVoided: true, PageSize: 2, PageToken: token})
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range page.GetExpenses() {
			seen = append(seen, e.GetId())
		}
		if token = page.GetNextPageToken(); token == "" {
			break
		}
	}
	if len(seen) != 6 {
		t.Errorf("paged %d entries, want 6 with no repeats", len(seen))
	}
	if _, err = svc.ListExpenses(ctx, &billingv1.ListExpensesRequest{PageToken: "garbage"}); err == nil {
		t.Error("a garbage page token was accepted")
	}
	// A page size above the maximum is refused like on every other list, not clamped.
	for _, size := range []int32{201, -1} {
		if _, err = svc.ListExpenses(ctx, &billingv1.ListExpensesRequest{PageSize: size}); err == nil || errkey.Of(err) != "billing.accounting.bad_page_size" {
			t.Errorf("page_size %d: err = %v, want billing.accounting.bad_page_size", size, err)
		}
	}
	if l, _ = svc.ListExpenses(otherCtx, &billingv1.ListExpensesRequest{}); len(l.GetExpenses()) != 1 || l.GetExpenses()[0].GetVendor() != "Theirs" {
		t.Errorf("other tenant list = %v", l)
	}
	_ = sub
}
