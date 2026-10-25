package query

import "testing"

// A coupon is contra-revenue: gross income is already after it, sales before it are gross - tax + discounts, and net income does not move.
func TestDiscountsAreContraRevenueAndTheCouponReportAddsUp(t *testing.T) {
	income := []IncomeRow{
		{Bucket: day("2026-10-02"), Currency: "EUR", Invoices: 2, Gross: 11900, Tax: 1900, Discount: 1000},
		{Bucket: day("2026-10-09"), Currency: "EUR", Invoices: 1, Gross: 5000, Discount: 500},
		{Bucket: day("2026-10-09"), Currency: "USD", Invoices: 1, Gross: 800, Discount: 200},
	}
	got := Build(day("2026-10-01"), day("2026-10-31"), GranMonth, income, nil, nil)
	AddCoupons(got, []CouponRow{
		{Currency: "EUR", CouponCode: "SPRING", Invoices: 2, Sales: 11000, Discount: 1000},
		{Currency: "EUR", CouponCode: "BIG", Invoices: 1, Sales: 5500, Discount: 500},
		{Currency: "EUR", CouponCode: "AAA", Invoices: 1, Sales: 100, Discount: 500},
		{Currency: "CHF", CouponCode: "LOST", Invoices: 1, Sales: 1, Discount: 1}, // a currency without a block is left out
	})
	eur := find(t, got, "EUR")
	if eur.Discounts != 1500 || eur.Gross != 16900 || eur.Tax != 1900 || eur.Net != 15000 || eur.Sales != 16900-1900+1500 {
		t.Fatalf("EUR = %+v", eur)
	}
	if eur.Series[0].Discounts != 1500 {
		t.Errorf("series discounts = %+v", eur.Series)
	}
	// Most discount first, equal discounts by code.
	want := []CouponTotal{{"SPRING", 2, 11000, 1000, 10000}, {"AAA", 1, 100, 500, -400}, {"BIG", 1, 5500, 500, 5000}}
	if len(eur.Coupons) != 3 || eur.Coupons[0] != want[0] || eur.Coupons[1] != want[1] || eur.Coupons[2] != want[2] {
		t.Errorf("coupons = %+v, want %+v", eur.Coupons, want)
	}
	usd := find(t, got, "USD")
	if usd.Discounts != 200 || usd.Sales != 1000 || len(usd.Coupons) != 0 {
		t.Errorf("USD = %+v; currencies are never added together", usd)
	}
}
