package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	billingv1 "github.com/0furkancolak/quotify/gen/go/quotify/billing/v1"
	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
)

// listInvoices is GET /v1/invoices?customer=&period=&status=&page_size=&page_token=. customer is the external id.
func (s *Server) listInvoices(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	req := &billingv1.ListInvoicesRequest{PeriodId: q.Get("period"), Status: q.Get("status"), PageToken: q.Get("page_token")}
	if v := q.Get("page_size"); v != "" {
		n, err := strconv.ParseInt(v, 10, 32)
		if err != nil {
			writeError(w, r, invalid("page_size", "must be an integer"))
			return
		}
		req.PageSize = int32(n)
	}
	if c := q.Get("customer"); c != "" {
		cust, err := s.d.Plans.ResolveCustomer(r.Context(), &plansv1.ResolveCustomerRequest{ExternalId: c})
		if err != nil {
			writeError(w, r, err)
			return
		}
		req.CustomerId = cust.GetCustomer().GetId()
	}
	resp, err := s.d.Billing.ListInvoices(r.Context(), req)
	respond(w, r, http.StatusOK, resp, err)
}

// getInvoice is GET /v1/invoices/{id}, with its lines.
func (s *Server) getInvoice(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Billing.GetInvoice(r.Context(), &billingv1.GetInvoiceRequest{InvoiceId: r.PathValue("id")})
	respond(w, r, http.StatusOK, resp.GetInvoice(), err)
}

// getInvoicePDF is GET /v1/invoices/{id}/pdf: a presigned download link, valid for 5 minutes.
func (s *Server) getInvoicePDF(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Billing.GetInvoicePdfUrl(r.Context(), &billingv1.GetInvoicePdfUrlRequest{InvoiceId: r.PathValue("id")})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSONValue(w, http.StatusOK, map[string]any{"url": resp.GetUrl(), "expires_at": resp.GetExpireTime().AsTime().UTC().Format("2006-01-02T15:04:05Z07:00")})
}

// sandboxClosePeriod is POST /v1/sandbox/close-period: close the current (or the given) period now and run the real
// billing flow, so a sandbox account sees invoices within a minute instead of at month end.
func (s *Server) sandboxClosePeriod(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Period string `json:"period"`
	}
	if r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, r, invalid("body", "must be JSON like {\"period\": \"2026-11\"}"))
			return
		}
	}
	if body.Period != "" && !periodParam.MatchString(body.Period) {
		writeError(w, r, invalid("period", "must look like 2026-11"))
		return
	}
	resp, err := s.d.Sandbox.ClosePeriodNow(r.Context(), &billingv1.ClosePeriodNowRequest{PeriodId: body.Period})
	respond(w, r, http.StatusAccepted, resp.GetRun(), err)
}

func (s *Server) adminClosePeriod(w http.ResponseWriter, r *http.Request) {
	req := &billingv1.ClosePeriodRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	resp, err := s.d.BillingAdmin.ClosePeriod(r.Context(), req)
	respond(w, r, http.StatusAccepted, resp.GetRun(), err)
}

func (s *Server) adminGetBillingRun(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.BillingAdmin.GetBillingRun(r.Context(), &billingv1.GetBillingRunRequest{TenantId: r.PathValue("tenant_id"), PeriodId: r.PathValue("period")})
	respond(w, r, http.StatusOK, resp.GetRun(), err)
}

func (s *Server) adminRetryInvoice(w http.ResponseWriter, r *http.Request) {
	req := &billingv1.RetryInvoiceSagaRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	req.InvoiceId = r.PathValue("id")
	resp, err := s.d.BillingAdmin.RetryInvoiceSaga(r.Context(), req)
	respond(w, r, http.StatusOK, resp.GetSaga(), err)
}

func (s *Server) adminVoidInvoice(w http.ResponseWriter, r *http.Request) {
	req := &billingv1.VoidInvoiceRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	req.InvoiceId = r.PathValue("id")
	resp, err := s.d.BillingAdmin.VoidInvoice(r.Context(), req)
	respond(w, r, http.StatusOK, resp.GetSaga(), err)
}

// adminStartRerating is POST /admin/v1/tenants/{tenant_id}/billing-runs/{period}/rerating. 201 when the job was created, 200
// when the same (tenant, period, plan version) had one already.
func (s *Server) adminStartRerating(w http.ResponseWriter, r *http.Request) {
	req := &billingv1.StartReratingRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	req.TenantId, req.PeriodId = r.PathValue("tenant_id"), r.PathValue("period")
	resp, err := s.d.BillingAdmin.StartRerating(r.Context(), req)
	code := http.StatusCreated
	if err == nil && !resp.GetCreated() {
		code = http.StatusOK
	}
	respond(w, r, code, resp.GetJob(), err)
}

func (s *Server) adminGetRerating(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.BillingAdmin.GetReratingJob(r.Context(), &billingv1.GetReratingJobRequest{TenantId: r.PathValue("tenant_id"), JobId: r.PathValue("job_id")})
	respond(w, r, http.StatusOK, resp.GetJob(), err)
}

// revenueReport is GET /v1/reports/revenue?from=&to=&group_by=provider,country: invoices summed by period, payment provider, country and currency.
func (s *Server) revenueReport(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	req := &billingv1.GetRevenueReportRequest{FromPeriod: q.Get("from"), ToPeriod: q.Get("to")}
	if g := q.Get("group_by"); g != "" {
		req.GroupBy = strings.Split(g, ",")
	}
	resp, err := s.d.Billing.GetRevenueReport(r.Context(), req)
	respond(w, r, http.StatusOK, resp, err)
}

// dunningReport is GET /v1/reports/dunning?limit=: invoices whose payment has not gone through.
func (s *Server) dunningReport(w http.ResponseWriter, r *http.Request) {
	req := &billingv1.GetDunningReportRequest{}
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.ParseInt(v, 10, 32)
		if err != nil {
			writeError(w, r, invalid("limit", "must be an integer"))
			return
		}
		req.Limit = int32(n)
	}
	resp, err := s.d.Billing.GetDunningReport(r.Context(), req)
	respond(w, r, http.StatusOK, resp, err)
}

// subscriptionReport is GET /v1/reports/subscriptions?month=&group_by=provider,country: subscriptions and monthly recurring revenue.
func (s *Server) subscriptionReport(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	req := &plansv1.GetSubscriptionReportRequest{Month: q.Get("month")}
	if g := q.Get("group_by"); g != "" {
		req.GroupBy = strings.Split(g, ",")
	}
	resp, err := s.d.Plans.GetSubscriptionReport(r.Context(), req)
	respond(w, r, http.StatusOK, resp, err)
}
