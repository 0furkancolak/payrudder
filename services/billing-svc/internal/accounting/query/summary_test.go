package query

import (
	"testing"
	"time"

	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/accounting/model"
)

func day(s string) time.Time {
	d, err := model.ParseDay(s)
	if err != nil {
		panic(err)
	}
	return d
}

func find(t *testing.T, cs []CurrencySummary, cur string) CurrencySummary {
	t.Helper()
	for _, c := range cs {
		if c.Currency == cur {
			return c
		}
	}
	t.Fatalf("no %s block in %+v", cur, cs)
	return CurrencySummary{}
}

// The owner's example: earned 100 by selling, 40 of it went to AWS.
func TestEarnedHundredSpentFortyOnInfrastructure(t *testing.T) {
	income := []IncomeRow{{Bucket: day("2026-10-05"), Currency: "TRY", Invoices: 2, Gross: 10000}}
	exp := []model.Expense{{ID: [16]byte{1}, Category: "infrastructure", AmountMinor: 4000, Currency: "TRY", IncurredOn: day("2026-10-10"), Recurring: "none"}}
	got := Build(day("2026-10-01"), day("2026-10-31"), GranMonth, income, nil, exp)
	if len(got) != 1 {
		t.Fatalf("currencies = %d", len(got))
	}
	c := got[0]
	if c.Gross != 10000 || c.Net != 10000 || c.Expenses != 4000 || c.Profit != 6000 || c.PaidInvoices != 2 {
		t.Fatalf("summary = %+v", c)
	}
	if len(c.ByCategory) != 1 || c.ByCategory[0] != (CategoryTotal{"infrastructure", 4000}) {
		t.Fatalf("categories = %+v", c.ByCategory)
	}
	if c.ProviderFeesRecorded {
		t.Error("provider fees are recorded without a payment_fees expense")
	}
}

func TestCurrenciesAreNeverAddedTogether(t *testing.T) {
	income := []IncomeRow{
		{Bucket: day("2026-10-01"), Currency: "TRY", Invoices: 1, Gross: 12000, Tax: 2000},
		{Bucket: day("2026-10-01"), Currency: "USD", Invoices: 1, Gross: 500},
	}
	refunds := []RefundRow{{Bucket: day("2026-10-02"), Currency: "USD", Refunded: 100}}
	exp := []model.Expense{
		{Category: "software", AmountMinor: 900, Currency: "EUR", IncurredOn: day("2026-10-03"), Recurring: "none"},
		{Category: "software", AmountMinor: 300, Currency: "USD", IncurredOn: day("2026-10-03"), Recurring: "none"},
	}
	got := Build(day("2026-10-01"), day("2026-10-31"), GranMonth, income, refunds, exp)
	if len(got) != 3 || got[0].Currency != "EUR" || got[1].Currency != "TRY" || got[2].Currency != "USD" {
		t.Fatalf("blocks = %+v", got)
	}
	try, usd, eur := find(t, got, "TRY"), find(t, got, "USD"), find(t, got, "EUR")
	if try.Net != 10000 || try.Expenses != 0 || try.Profit != 10000 || try.Tax != 2000 {
		t.Fatalf("TRY = %+v", try)
	}
	if usd.Gross != 500 || usd.Refunds != 100 || usd.Net != 400 || usd.Expenses != 300 || usd.Profit != 100 {
		t.Fatalf("USD = %+v", usd)
	}
	// Only an expense: a currency with no income still has its block, with a negative profit.
	if eur.Gross != 0 || eur.Expenses != 900 || eur.Profit != -900 {
		t.Fatalf("EUR = %+v", eur)
	}
}

func TestRefundsAndTaxAreSeparateLines(t *testing.T) {
	income := []IncomeRow{{Bucket: day("2026-10-01"), Currency: "EUR", Invoices: 3, Gross: 36000, Tax: 6000}}
	refunds := []RefundRow{{Bucket: day("2026-10-09"), Currency: "EUR", Refunded: 1200}}
	c := Build(day("2026-10-01"), day("2026-10-31"), GranMonth, income, refunds, nil)[0]
	if c.Gross != 36000 || c.Tax != 6000 || c.Refunds != 1200 || c.Net != 28800 || c.Profit != 28800 {
		t.Fatalf("lines = %+v", c)
	}
}

func TestProviderFeesAreTheManualPaymentFeesCategory(t *testing.T) {
	exp := []model.Expense{
		{Category: "payment_fees", AmountMinor: 250, Currency: "TRY", IncurredOn: day("2026-10-04"), Recurring: "none"},
		{Category: "payment_fees", AmountMinor: 150, Currency: "TRY", IncurredOn: day("2026-10-20"), Recurring: "none"},
		{Category: "marketing", AmountMinor: 1000, Currency: "TRY", IncurredOn: day("2026-10-20"), Recurring: "none"},
	}
	c := Build(day("2026-10-01"), day("2026-10-31"), GranMonth, nil, nil, exp)[0]
	if !c.ProviderFeesRecorded || c.ProviderFees != 400 || c.Expenses != 1400 {
		t.Fatalf("fees = %+v", c)
	}
	// A fee entered for another month is not "recorded" for this one.
	other := []model.Expense{{Category: "payment_fees", AmountMinor: 250, Currency: "TRY", IncurredOn: day("2026-09-30"), Recurring: "none"}}
	if got := Build(day("2026-10-01"), day("2026-10-31"), GranMonth, nil, nil, other); len(got) != 0 {
		t.Fatalf("an expense outside the window made a block: %+v", got)
	}
}

func TestRecurringExpensesAreExpandedPerBucket(t *testing.T) {
	until := day("2026-04-15")
	exp := []model.Expense{
		{Category: "infrastructure", AmountMinor: 4000, Currency: "USD", IncurredOn: day("2026-01-15"), Recurring: "monthly", RecurringUntil: &until},
		{Category: "software", AmountMinor: 12000, Currency: "USD", IncurredOn: day("2025-03-01"), Recurring: "yearly"},
	}
	c := Build(day("2026-01-01"), day("2026-06-30"), GranMonth, nil, nil, exp)[0]
	if c.Expenses != 4*4000+12000 {
		t.Fatalf("total = %d", c.Expenses)
	}
	want := map[string]int64{"2026-01-01": 4000, "2026-02-01": 4000, "2026-03-01": 4000 + 12000, "2026-04-01": 4000, "2026-05-01": 0, "2026-06-01": 0}
	if len(c.Series) != 6 {
		t.Fatalf("buckets = %d", len(c.Series))
	}
	for _, p := range c.Series {
		if p.Expenses != want[model.FormatDay(p.Bucket)] {
			t.Errorf("%s expenses = %d, want %d", model.FormatDay(p.Bucket), p.Expenses, want[model.FormatDay(p.Bucket)])
		}
	}
	if len(c.ByCategory) != 2 || c.ByCategory[0] != (CategoryTotal{"infrastructure", 16000}) || c.ByCategory[1] != (CategoryTotal{"software", 12000}) {
		t.Fatalf("categories = %+v", c.ByCategory)
	}
}

func TestSeriesIsDenseAndSumsToTheTotals(t *testing.T) {
	income := []IncomeRow{
		{Bucket: day("2026-10-02"), Currency: "TRY", Invoices: 1, Gross: 1000, Tax: 100},
		{Bucket: day("2026-10-30"), Currency: "TRY", Invoices: 1, Gross: 2000, Tax: 200},
	}
	exp := []model.Expense{{Category: "other", AmountMinor: 77, Currency: "TRY", IncurredOn: day("2026-10-15"), Recurring: "none"}}
	c := Build(day("2026-10-01"), day("2026-10-31"), GranDay, income, nil, exp)[0]
	if len(c.Series) != 31 {
		t.Fatalf("days = %d", len(c.Series))
	}
	var gross, net, expenses, profit int64
	for _, p := range c.Series {
		gross, net, expenses, profit = gross+p.Gross, net+p.Net, expenses+p.Expenses, profit+p.Profit
	}
	if gross != c.Gross || net != c.Net || expenses != c.Expenses || profit != c.Profit || c.Profit != 2700-77 {
		t.Fatalf("series %d/%d/%d/%d vs totals %+v", gross, net, expenses, profit, c)
	}
}

func TestIncomeOutsideThePeriodIsIgnored(t *testing.T) {
	income := []IncomeRow{{Bucket: day("2026-11-01"), Currency: "TRY", Invoices: 1, Gross: 999}}
	c := Build(day("2026-10-01"), day("2026-10-31"), GranMonth, income, nil, nil)
	if len(c) != 1 || c[0].Gross != 0 || c[0].PaidInvoices != 0 {
		t.Fatalf("stray bucket counted: %+v", c)
	}
}

func TestZeroDecimalAndLargeAmountsAreExact(t *testing.T) {
	// Sums are integers of minor units: nothing is rounded and nothing goes through a float.
	big := int64(9_000_000_000_000_000)
	exp := []model.Expense{
		{Category: "payroll", AmountMinor: big, Currency: "JPY", IncurredOn: day("2026-10-01"), Recurring: "none"},
		{Category: "payroll", AmountMinor: 1, Currency: "JPY", IncurredOn: day("2026-10-02"), Recurring: "none"},
	}
	c := Build(day("2026-10-01"), day("2026-10-31"), GranMonth, nil, nil, exp)[0]
	if c.Expenses != big+1 || c.Profit != -(big+1) {
		t.Fatalf("expenses = %d", c.Expenses)
	}
}

func TestPeriodValidation(t *testing.T) {
	for _, tc := range []struct {
		from, to, gran, wantGran, wantKey string
	}{
		{"2026-10-01", "2026-10-31", "", "day", ""},
		{"2026-01-01", "2026-12-31", "", "month", ""},
		{"2026-01-01", "2027-01-02", "day", "", "billing.accounting.range_too_long"},
		{"2026-01-01", "2026-12-31", "day", "day", ""},
		{"2026-01-01", "2030-12-31", "month", "month", ""},
		{"2026-01-01", "2031-01-02", "month", "", "billing.accounting.range_too_long"},
		{"2026-10-31", "2026-10-01", "", "", "billing.accounting.bad_period"},
		{"", "2026-10-01", "", "", "billing.accounting.bad_period"},
		{"2026-10-01", "2026-10-31", "week", "", "billing.accounting.bad_granularity"},
	} {
		_, _, gran, err := period(SummaryParams{FromDate: tc.from, ToDate: tc.to, Granularity: tc.gran})
		if tc.wantKey == "" {
			if err != nil || gran != tc.wantGran {
				t.Errorf("%+v: gran %q err %v", tc, gran, err)
			}
			continue
		}
		if errkey.Of(err) != tc.wantKey {
			t.Errorf("%+v: err %v", tc, err)
		}
	}
}
