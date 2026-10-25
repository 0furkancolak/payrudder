package httpapi

import (
	"encoding/csv"
	"net/http"
	"strconv"
	"strings"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
	"github.com/0furkancolak/payrudder/services/gateway/internal/ratelimit"
)

// accountingRoutes is the table for expenses and the accounting summary.
func (s *Server) accountingRoutes() []Route {
	return []Route{
		{"GET", "/v1/expenses", AccessTenant, ratelimit.ClassRead, s.listExpenses},
		{"POST", "/v1/expenses", AccessTenant, ratelimit.ClassWrite, s.createExpense},
		{"PUT", "/v1/expenses/{id}", AccessTenant, ratelimit.ClassWrite, s.updateExpense},
		{"DELETE", "/v1/expenses/{id}", AccessTenant, ratelimit.ClassWrite, s.voidExpense},
		{"GET", "/v1/accounting/summary", AccessTenant, ratelimit.ClassRead, s.accountingSummary},
		{"GET", "/v1/accounting/export.csv", AccessTenant, ratelimit.ClassRead, s.accountingExport},
		{"GET", "/v1/accounting/coupons.csv", AccessTenant, ratelimit.ClassRead, s.couponsExport},
	}
}

// listExpenses is GET /v1/expenses?category=&app_id=&currency=&from=&to=&include_voided=&page_size=&page_token=.
func (s *Server) listExpenses(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	req := &billingv1.ListExpensesRequest{Category: q.Get("category"), AppId: q.Get("app_id"), Currency: q.Get("currency"), FromDate: q.Get("from"), ToDate: q.Get("to"),
		IncludeVoided: q.Get("include_voided") == "true", PageToken: q.Get("page_token")}
	if v := q.Get("page_size"); v != "" {
		n, err := strconv.ParseInt(v, 10, 32)
		if err != nil || n < 1 || n > 200 {
			writeError(w, r, invalid("page_size", "must be 1 to 200"))
			return
		}
		req.PageSize = int32(n)
	}
	resp, err := s.d.Billing.ListExpenses(r.Context(), req)
	respond(w, r, http.StatusOK, resp, err)
}

// createExpense is POST /v1/expenses.
func (s *Server) createExpense(w http.ResponseWriter, r *http.Request) {
	req := &billingv1.CreateExpenseRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	resp, err := s.d.Billing.CreateExpense(r.Context(), req)
	respond(w, r, http.StatusCreated, resp.GetExpense(), err)
}

// updateExpense is PUT /v1/expenses/{id}: the body replaces the whole record.
func (s *Server) updateExpense(w http.ResponseWriter, r *http.Request) {
	req := &billingv1.UpdateExpenseRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	req.Id = r.PathValue("id")
	resp, err := s.d.Billing.UpdateExpense(r.Context(), req)
	respond(w, r, http.StatusOK, resp.GetExpense(), err)
}

// voidExpense is DELETE /v1/expenses/{id}. The reason is in the body ({"reason": ...}) or in ?reason=. The expense is voided, not deleted.
func (s *Server) voidExpense(w http.ResponseWriter, r *http.Request) {
	req := &billingv1.VoidExpenseRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	req.Id = r.PathValue("id")
	if req.Reason == "" {
		req.Reason = r.URL.Query().Get("reason")
	}
	resp, err := s.d.Billing.VoidExpense(r.Context(), req)
	respond(w, r, http.StatusOK, resp.GetExpense(), err)
}

func summaryRequest(r *http.Request) *billingv1.GetAccountingSummaryRequest {
	q := r.URL.Query()
	return &billingv1.GetAccountingSummaryRequest{FromDate: q.Get("from"), ToDate: q.Get("to"), AppId: q.Get("app_id"), Granularity: q.Get("granularity")}
}

// accountingSummary is GET /v1/accounting/summary?from=&to=&app_id=&granularity=.
func (s *Server) accountingSummary(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Billing.GetAccountingSummary(r.Context(), summaryRequest(r))
	respond(w, r, http.StatusOK, resp, err)
}

// accountingExport is GET /v1/accounting/export.csv with the query of the summary: one row per currency and bucket, then a total row per currency. Amounts are
// minor units; currencies are never added together.
func (s *Server) accountingExport(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Billing.GetAccountingSummary(r.Context(), summaryRequest(r))
	if err != nil {
		writeError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="accounting-`+resp.GetFromDate()+`-`+resp.GetToDate()+`.csv"`)
	cw := csv.NewWriter(w)
	n := func(v int64) string { return strconv.FormatInt(v, 10) }
	_ = cw.Write([]string{"currency", "bucket", "gross_income_minor", "tax_minor", "refunds_minor", "net_income_minor", "expenses_minor", "provider_fees_minor", "profit_minor", "discounts_minor",
		"sales_before_discounts_minor"})
	for _, c := range resp.GetCurrencies() {
		for _, p := range c.GetSeries() {
			_ = cw.Write([]string{c.GetCurrency(), p.GetBucket(), n(p.GetGrossIncomeMinor()), n(p.GetTaxMinor()), n(p.GetRefundsMinor()), n(p.GetNetIncomeMinor()), n(p.GetExpensesMinor()), "", n(p.GetProfitMinor()),
				n(p.GetDiscountsMinor()), n(p.GetGrossIncomeMinor() - p.GetTaxMinor() + p.GetDiscountsMinor())})
		}
		_ = cw.Write([]string{c.GetCurrency(), "total", n(c.GetGrossIncomeMinor()), n(c.GetTaxMinor()), n(c.GetRefundsMinor()), n(c.GetNetIncomeMinor()), n(c.GetExpensesMinor()), feesCell(c), n(c.GetProfitMinor()),
			n(c.GetDiscountsMinor()), n(c.GetSalesMinor())})
	}
	cw.Flush()
}

// couponsExport is GET /v1/accounting/coupons.csv with the query of the summary: one row per currency and coupon, with the invoices paid in the period that used it, their
// gross sales (before the coupon, tax excluded), the discount and the net. Currencies are never added together.
func (s *Server) couponsExport(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Billing.GetAccountingSummary(r.Context(), summaryRequest(r))
	if err != nil {
		writeError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="coupons-`+resp.GetFromDate()+`-`+resp.GetToDate()+`.csv"`)
	cw := csv.NewWriter(w)
	n := func(v int64) string { return strconv.FormatInt(v, 10) }
	_ = cw.Write([]string{"currency", "coupon", "invoices", "gross_minor", "discount_minor", "net_minor"})
	for _, c := range resp.GetCurrencies() {
		for _, k := range c.GetCoupons() {
			_ = cw.Write([]string{c.GetCurrency(), csvSafe(k.GetCouponCode()), n(k.GetInvoices()), n(k.GetGrossMinor()), n(k.GetDiscountMinor()), n(k.GetNetMinor())})
		}
	}
	cw.Flush()
}

// csvSafe keeps a cell that starts like a formula from being run by a spreadsheet. A coupon code is 2 to 40 letters, digits, - and _, so only a leading - can matter.
func csvSafe(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}

// feesCell is the provider fees of the total row; "not_recorded" when no payment_fees expense exists, which is not the same as zero.
func feesCell(c *billingv1.AccountingCurrency) string {
	if !c.GetProviderFeesRecorded() {
		return "not_recorded"
	}
	return strconv.FormatInt(c.GetProviderFeesMinor(), 10)
}
