package httpapi

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
)

func (fakeBilling) CreateExpense(_ context.Context, req *billingv1.CreateExpenseRequest) (*billingv1.CreateExpenseResponse, error) {
	if req.GetAmountMinor() < 1 {
		return nil, errkey.New(codes.InvalidArgument, "billing.accounting.bad_expense", "the amount must be above zero")
	}
	return &billingv1.CreateExpenseResponse{Expense: &billingv1.Expense{Id: "exp-1", Vendor: req.GetVendor(), Category: req.GetCategory(), AmountMinor: req.GetAmountMinor(),
		Currency: req.GetCurrency(), IncurredOn: req.GetIncurredOn(), Recurring: "none"}}, nil
}

func (fakeBilling) UpdateExpense(_ context.Context, req *billingv1.UpdateExpenseRequest) (*billingv1.UpdateExpenseResponse, error) {
	if req.GetId() == "voided" {
		return nil, errkey.New(codes.FailedPrecondition, "billing.accounting.expense_voided", "the expense was voided")
	}
	return &billingv1.UpdateExpenseResponse{Expense: &billingv1.Expense{Id: req.GetId(), Vendor: req.GetVendor(), AmountMinor: req.GetAmountMinor()}}, nil
}

func (fakeBilling) VoidExpense(_ context.Context, req *billingv1.VoidExpenseRequest) (*billingv1.VoidExpenseResponse, error) {
	if req.GetReason() == "" {
		return nil, errkey.New(codes.InvalidArgument, "billing.accounting.reason_required", "a reason is required")
	}
	return &billingv1.VoidExpenseResponse{Expense: &billingv1.Expense{Id: req.GetId(), VoidReason: req.GetReason(), VoidTime: timestamppb.Now()}}, nil
}

func (fakeBilling) ListExpenses(_ context.Context, req *billingv1.ListExpensesRequest) (*billingv1.ListExpensesResponse, error) {
	return &billingv1.ListExpensesResponse{Expenses: []*billingv1.Expense{{Id: "exp-1", Vendor: "AWS", Category: req.GetCategory(), Currency: req.GetCurrency()}}, NextPageToken: req.GetPageToken() + req.GetFromDate()}, nil
}

func (fakeBilling) GetAccountingSummary(_ context.Context, req *billingv1.GetAccountingSummaryRequest) (*billingv1.GetAccountingSummaryResponse, error) {
	if req.GetFromDate() == "" {
		return nil, errkey.New(codes.InvalidArgument, "billing.accounting.bad_period", "from_date and to_date are required")
	}
	return &billingv1.GetAccountingSummaryResponse{FromDate: req.GetFromDate(), ToDate: req.GetToDate(), Granularity: "day", AppId: req.GetAppId(), Currencies: []*billingv1.AccountingCurrency{
		{Currency: "EUR", ExpensesMinor: 40, ProfitMinor: -40, Series: []*billingv1.AccountingPoint{{Bucket: req.GetFromDate(), ExpensesMinor: 40, ProfitMinor: -40}}},
		{Currency: "TRY", GrossIncomeMinor: 10000, NetIncomeMinor: 10000, ExpensesMinor: 4000, ProfitMinor: 6000, ProviderFeesMinor: 250, ProviderFeesRecorded: true,
			ExpenseByCategory: []*billingv1.AccountingCategoryTotal{{Category: "infrastructure", AmountMinor: 4000}},
			DiscountsMinor:    1000, SalesMinor: 11000, Coupons: []*billingv1.AccountingCoupon{{CouponCode: "-WELCOME", Invoices: 2, GrossMinor: 11000, DiscountMinor: 1000, NetMinor: 10000}},
			Series: []*billingv1.AccountingPoint{{Bucket: req.GetFromDate(), GrossIncomeMinor: 10000, NetIncomeMinor: 10000, ExpensesMinor: 4000, ProfitMinor: 6000, DiscountsMinor: 1000}}},
	}}, nil
}

func TestExpensesCRUD(t *testing.T) {
	ts, _ := newTestServer(t)
	resp, m := do(t, ts, "POST", "/v1/expenses", "live-key", `{"vendor":"AWS","category":"infrastructure","amount_minor":4000,"currency":"TRY","incurred_on":"2026-10-10"}`)
	if resp.StatusCode != http.StatusCreated || m["id"] != "exp-1" || m["amount_minor"] != "4000" {
		t.Fatalf("create: %d %v", resp.StatusCode, m)
	}
	if resp, m := do(t, ts, "POST", "/v1/expenses", "live-key", `{"vendor":"AWS"}`); resp.StatusCode != 400 || errKey(m) != "billing.accounting.bad_expense" {
		t.Fatalf("bad expense: %d %v", resp.StatusCode, m)
	}
	// The id comes from the path, whatever the body says.
	resp, m = do(t, ts, "PUT", "/v1/expenses/exp-7", "live-key", `{"id":"other","vendor":"GCP","amount_minor":5}`)
	if resp.StatusCode != 200 || m["id"] != "exp-7" || m["vendor"] != "GCP" {
		t.Fatalf("update: %d %v", resp.StatusCode, m)
	}
	if resp, m := do(t, ts, "PUT", "/v1/expenses/voided", "live-key", `{}`); resp.StatusCode != 412 && resp.StatusCode != 409 || errKey(m) != "billing.accounting.expense_voided" {
		t.Fatalf("update voided: %d %v", resp.StatusCode, m)
	}
	if resp, m := do(t, ts, "DELETE", "/v1/expenses/exp-1", "live-key", ""); resp.StatusCode != 400 || errKey(m) != "billing.accounting.reason_required" {
		t.Fatalf("void without reason: %d %v", resp.StatusCode, m)
	}
	resp, m = do(t, ts, "DELETE", "/v1/expenses/exp-1", "live-key", `{"reason":"duplicate"}`)
	if resp.StatusCode != 200 || m["void_reason"] != "duplicate" || m["void_time"] == nil {
		t.Fatalf("void by body: %d %v", resp.StatusCode, m)
	}
	if resp, m = do(t, ts, "DELETE", "/v1/expenses/exp-1?reason=typo", "live-key", ""); resp.StatusCode != 200 || m["void_reason"] != "typo" {
		t.Fatalf("void by query: %d %v", resp.StatusCode, m)
	}
	if resp, _ := do(t, ts, "POST", "/v1/expenses", "", `{}`); resp.StatusCode != 401 {
		t.Fatalf("no key: %d", resp.StatusCode)
	}
}

func TestExpensesList(t *testing.T) {
	ts, _ := newTestServer(t)
	_, m := do(t, ts, "GET", "/v1/expenses?category=marketing&currency=USD&from=2026-10-01&page_token=a", "live-key", "")
	list, _ := m["expenses"].([]any)
	first, _ := list[0].(map[string]any)
	if len(list) != 1 || first["category"] != "marketing" || first["currency"] != "USD" || m["next_page_token"] != "a2026-10-01" {
		t.Fatalf("list = %v", m)
	}
	if resp, m := do(t, ts, "GET", "/v1/expenses?page_size=500", "live-key", ""); resp.StatusCode != 400 {
		t.Fatalf("page size: %d %v", resp.StatusCode, m)
	}
}

func TestAccountingSummaryKeepsCurrenciesApart(t *testing.T) {
	ts, _ := newTestServer(t)
	resp, m := do(t, ts, "GET", "/v1/accounting/summary?from=2026-10-01&to=2026-10-31&app_id=a1&granularity=day", "live-key", "")
	cs, _ := m["currencies"].([]any)
	if resp.StatusCode != 200 || m["app_id"] != "a1" || len(cs) != 2 {
		t.Fatalf("summary: %d %v", resp.StatusCode, m)
	}
	try, _ := cs[1].(map[string]any)
	if try["currency"] != "TRY" || try["profit_minor"] != "6000" || try["provider_fees_recorded"] != true {
		t.Fatalf("TRY = %v", try)
	}
	if resp, m := do(t, ts, "GET", "/v1/accounting/summary", "live-key", ""); resp.StatusCode != 400 || errKey(m) != "billing.accounting.bad_period" {
		t.Fatalf("no period: %d %v", resp.StatusCode, m)
	}
}

func TestCouponsExportCSV(t *testing.T) {
	ts, _ := newTestServer(t)
	req, _ := http.NewRequest("GET", ts.URL+"/v1/accounting/coupons.csv?from=2026-10-01&to=2026-10-31", nil)
	req.Header.Set("Authorization", "Bearer live-key")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Content-Disposition"), "coupons-2026-10-01-2026-10-31.csv") {
		t.Fatalf("headers: %d %v", resp.StatusCode, resp.Header)
	}
	// One row per currency and coupon; a code that starts like a formula is quoted so that a spreadsheet does not run it.
	want := "currency,coupon,invoices,gross_minor,discount_minor,net_minor\nTRY,'-WELCOME,2,11000,1000,10000\n"
	if string(b) != want {
		t.Errorf("csv = %q, want %q", string(b), want)
	}
	if resp, m := do(t, ts, "GET", "/v1/accounting/coupons.csv", "live-key", ""); resp.StatusCode != 400 || errKey(m) != "billing.accounting.bad_period" {
		t.Errorf("no period: %d %v", resp.StatusCode, m)
	}
}

func TestAccountingExportCSV(t *testing.T) {
	ts, _ := newTestServer(t)
	req, _ := http.NewRequest("GET", ts.URL+"/v1/accounting/export.csv?from=2026-10-01&to=2026-10-31", nil)
	req.Header.Set("Authorization", "Bearer live-key")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/csv") || !strings.Contains(resp.Header.Get("Content-Disposition"), "accounting-2026-10-01-2026-10-31.csv") {
		t.Fatalf("headers: %d %v", resp.StatusCode, resp.Header)
	}
	want := []string{
		"currency,bucket,gross_income_minor,tax_minor,refunds_minor,net_income_minor,expenses_minor,provider_fees_minor,profit_minor,discounts_minor,sales_before_discounts_minor",
		"EUR,2026-10-01,0,0,0,0,40,,-40,0,0",
		"EUR,total,0,0,0,0,40,not_recorded,-40,0,0",
		"TRY,2026-10-01,10000,0,0,10000,4000,,6000,1000,11000",
		"TRY,total,10000,0,0,10000,4000,250,6000,1000,11000",
	}
	if len(lines) != len(want) {
		t.Fatalf("csv = %q", string(b))
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, lines[i], want[i])
		}
	}
	if resp, m := do(t, ts, "GET", "/v1/accounting/export.csv", "live-key", ""); resp.StatusCode != 400 || errKey(m) != "billing.accounting.bad_period" {
		t.Fatalf("no period: %d %v", resp.StatusCode, m)
	}
}
