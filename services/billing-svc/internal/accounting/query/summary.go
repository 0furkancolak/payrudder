package query

import (
	"cmp"
	"slices"
	"strings"
	"time"

	"github.com/0furkancolak/payrudder/services/billing-svc/internal/accounting/model"
)

// Granularity of a time series.
const (
	GranDay   = "day"
	GranMonth = "month"
)

// IncomeRow is the paid invoices of one currency in one bucket.
type IncomeRow struct {
	Bucket   time.Time
	Currency string
	Invoices int64
	Gross    int64
	Tax      int64
	// Discount is what coupons took off those invoices (contra-revenue); Gross is already net of it.
	Discount int64
}

// CouponRow is the paid invoices of one currency that used one coupon.
type CouponRow struct {
	Currency   string
	CouponCode string
	Invoices   int64
	// Sales is what those invoices came to before the coupon (their positive lines except tax), Discount what the coupon took off.
	Sales    int64
	Discount int64
}

// CouponTotal is one coupon's line in the report: gross sales, discounts, and net.
type CouponTotal struct {
	CouponCode string
	Invoices   int64
	Gross      int64
	Discount   int64
	Net        int64
}

// RefundRow is the refunds and credit notes of one currency in one bucket.
type RefundRow struct {
	Bucket   time.Time
	Currency string
	Refunded int64
}

// Point is one bucket of the series of a currency.
type Point struct {
	Bucket                                     time.Time
	Gross, Tax, Refunds, Net, Expenses, Profit int64
	Discounts                                  int64
}

// CategoryTotal is what was spent in one category.
type CategoryTotal struct {
	Category string
	Amount   int64
}

// CurrencySummary is the period of one currency. Amounts of different currencies are never added: each currency has its own block.
type CurrencySummary struct {
	Currency                                   string
	Gross, Tax, Refunds, Net, Expenses, Profit int64
	ProviderFees                               int64
	ProviderFeesRecorded                       bool
	PaidInvoices                               int64
	ByCategory                                 []CategoryTotal
	Series                                     []Point
	// Discounts is what coupons took off the paid invoices (contra-revenue, a positive number); Sales is revenue before it: gross - tax + discounts. Net income
	// is unchanged by them: gross is already after the discount.
	Discounts, Sales int64
	// Coupons is the report per coupon, most discount first.
	Coupons []CouponTotal
}

// AddCoupons puts the per-coupon rows into the summary of their currency (a currency the summary does not have is left out: nothing was paid in it that period).
func AddCoupons(cs []CurrencySummary, rows []CouponRow) {
	for _, r := range rows {
		for i := range cs {
			if cs[i].Currency == r.Currency {
				cs[i].Coupons = append(cs[i].Coupons, CouponTotal{CouponCode: r.CouponCode, Invoices: r.Invoices, Gross: r.Sales, Discount: r.Discount, Net: r.Sales - r.Discount})
			}
		}
	}
	for i := range cs {
		slices.SortFunc(cs[i].Coupons, func(a, b CouponTotal) int {
			return cmp.Or(cmp.Compare(b.Discount, a.Discount), strings.Compare(a.CouponCode, b.CouponCode)) // the most given away first
		})
	}
}

// Buckets returns the first day of every bucket from `from` to `to`, both inside.
func Buckets(from, to time.Time, gran string) []time.Time {
	var out []time.Time
	if gran == GranMonth {
		for d := model.MonthStart(from); !d.After(to); d = d.AddDate(0, 1, 0) {
			out = append(out, d)
		}
		return out
	}
	for d := model.Day(from); !d.After(to); d = d.AddDate(0, 0, 1) {
		out = append(out, d)
	}
	return out
}

// BucketOf is the bucket a day falls into.
func BucketOf(d time.Time, gran string) time.Time {
	if gran == GranMonth {
		return model.MonthStart(d)
	}
	return model.Day(d)
}

// Build adds up the income and refund rows and the occurrences of the expenses into one summary per currency, ordered by currency. The expenses are the rows the
// database found for the window; a recurring one is expanded here, so nothing is stored per occurrence.
func Build(from, to time.Time, gran string, income []IncomeRow, refunds []RefundRow, expenses []model.Expense) []CurrencySummary {
	buckets := Buckets(from, to, gran)
	index := make(map[time.Time]int, len(buckets))
	for i, b := range buckets {
		index[b] = i
	}
	byCur := map[string]*CurrencySummary{}
	cat := map[string]map[string]int64{}
	of := func(cur string) *CurrencySummary {
		c := byCur[cur]
		if c == nil {
			c = &CurrencySummary{Currency: cur, Series: make([]Point, len(buckets))}
			for i, b := range buckets {
				c.Series[i].Bucket = b
			}
			byCur[cur] = c
			cat[cur] = map[string]int64{}
		}
		return c
	}
	point := func(c *CurrencySummary, day time.Time) *Point {
		i, ok := index[BucketOf(day, gran)]
		if !ok {
			return nil
		}
		return &c.Series[i]
	}
	for _, r := range income {
		c := of(r.Currency)
		if p := point(c, r.Bucket); p != nil {
			p.Gross += r.Gross
			p.Tax += r.Tax
			p.Discounts += r.Discount
			c.Gross += r.Gross
			c.Tax += r.Tax
			c.Discounts += r.Discount
			c.PaidInvoices += r.Invoices
		}
	}
	for _, r := range refunds {
		c := of(r.Currency)
		if p := point(c, r.Bucket); p != nil {
			p.Refunds += r.Refunded
			c.Refunds += r.Refunded
		}
	}
	for i := range expenses {
		e := &expenses[i]
		for _, day := range e.Occurrences(from, to) {
			c := of(e.Currency)
			p := point(c, day)
			if p == nil {
				continue
			}
			p.Expenses += e.AmountMinor
			c.Expenses += e.AmountMinor
			cat[e.Currency][e.Category] += e.AmountMinor
			if e.Category == model.CategoryPaymentFees {
				c.ProviderFees += e.AmountMinor
				c.ProviderFeesRecorded = true
			}
		}
	}
	out := make([]CurrencySummary, 0, len(byCur))
	for cur, c := range byCur {
		for i := range c.Series {
			p := &c.Series[i]
			p.Net = p.Gross - p.Tax - p.Refunds
			p.Profit = p.Net - p.Expenses
		}
		c.Net = c.Gross - c.Tax - c.Refunds
		c.Profit = c.Net - c.Expenses
		c.Sales = c.Gross - c.Tax + c.Discounts
		for k, v := range cat[cur] {
			c.ByCategory = append(c.ByCategory, CategoryTotal{Category: k, Amount: v})
		}
		slices.SortFunc(c.ByCategory, func(a, b CategoryTotal) int {
			return cmp.Or(cmp.Compare(b.Amount, a.Amount), strings.Compare(a.Category, b.Category)) // the biggest first
		})
		out = append(out, *c)
	}
	slices.SortFunc(out, func(a, b CurrencySummary) int { return strings.Compare(a.Currency, b.Currency) })
	return out
}
