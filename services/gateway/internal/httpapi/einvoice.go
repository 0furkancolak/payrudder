package httpapi

import (
	"net/http"
	"net/url"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
)

// E-invoice routes (docs/phases/phase-30-e-invoice.md): the tenant reads its e-documents with its API key; the operator reads the same through
// /admin/v1/tenants/{tenant_id}, retries and cancels them, and configures the tenant's integrator account at payments-worker.

func einvoiceListRequest(r *http.Request) (*billingv1.ListEInvoicesRequest, error) {
	q := r.URL.Query()
	req := &billingv1.ListEInvoicesRequest{InvoiceId: q.Get("invoice_id"), CreditNoteId: q.Get("credit_note_id"), Status: q.Get("status"), PageToken: q.Get("page_token")}
	size, err := intParam(q, "page_size")
	if err != nil {
		return nil, err
	}
	req.PageSize = size
	return req, nil
}

func (s *Server) listEInvoices(w http.ResponseWriter, r *http.Request) {
	req, err := einvoiceListRequest(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	resp, err := s.d.Billing.ListEInvoices(r.Context(), req)
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) getEInvoice(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Billing.GetEInvoice(r.Context(), &billingv1.GetEInvoiceRequest{Id: r.PathValue("id")})
	respond(w, r, http.StatusOK, resp.GetDocument(), err)
}

func (s *Server) getEInvoiceFile(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Billing.GetEInvoiceFileUrl(r.Context(), &billingv1.GetEInvoiceFileUrlRequest{Id: r.PathValue("id"), Kind: r.PathValue("kind")})
	respondSignedURL(w, r, resp.GetUrl(), resp.GetExpiresAt(), err)
}

func (s *Server) adminListEInvoices(w http.ResponseWriter, r *http.Request) {
	ctx, err := operatorAs(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	req, err := einvoiceListRequest(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	resp, err := s.d.Billing.ListEInvoices(ctx, req)
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) adminGetEInvoice(w http.ResponseWriter, r *http.Request) {
	ctx, err := operatorAs(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	resp, err := s.d.Billing.GetEInvoice(ctx, &billingv1.GetEInvoiceRequest{Id: r.PathValue("id")})
	respond(w, r, http.StatusOK, resp.GetDocument(), err)
}

func (s *Server) adminGetEInvoiceFile(w http.ResponseWriter, r *http.Request) {
	ctx, err := operatorAs(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	resp, err := s.d.Billing.GetEInvoiceFileUrl(ctx, &billingv1.GetEInvoiceFileUrlRequest{Id: r.PathValue("id"), Kind: r.PathValue("kind")})
	respondSignedURL(w, r, resp.GetUrl(), resp.GetExpiresAt(), err)
}

func (s *Server) adminRetryEInvoice(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.BillingAdmin.RetryEInvoice(r.Context(), &billingv1.RetryEInvoiceRequest{TenantId: r.PathValue("tenant_id"), Id: r.PathValue("id")})
	respond(w, r, http.StatusOK, resp.GetDocument(), err)
}

func (s *Server) adminCancelEInvoice(w http.ResponseWriter, r *http.Request) {
	req := &billingv1.CancelEInvoiceRequest{}
	if r.ContentLength != 0 {
		if err := decode(r, req); err != nil {
			writeError(w, r, err)
			return
		}
	}
	req.TenantId, req.Id = r.PathValue("tenant_id"), r.PathValue("id")
	resp, err := s.d.BillingAdmin.CancelEInvoice(r.Context(), req)
	respond(w, r, http.StatusOK, resp.GetDocument(), err)
}

// setCustomerTax is PUT /v1/customers/{external_id}/tax: the body is {tax_id, tax_office, e_invoice_registered}.
func (s *Server) setCustomerTax(w http.ResponseWriter, r *http.Request) {
	req := &plansv1.SetCustomerTaxRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	req.ExternalId = r.PathValue("external_id")
	resp, err := s.d.Plans.SetCustomerTax(r.Context(), req)
	respond(w, r, http.StatusOK, resp.GetCustomer(), err)
}

func (s *Server) einvoiceAccounts(w http.ResponseWriter, r *http.Request) {
	s.paymentsProxy(func(r *http.Request) string {
		return "/admin/tenants/" + url.PathEscape(r.PathValue("tenant_id")) + "/e-invoice-accounts"
	})(w, r)
}

func (s *Server) einvoiceAccount(w http.ResponseWriter, r *http.Request) {
	s.paymentsProxy(func(r *http.Request) string {
		return "/admin/tenants/" + url.PathEscape(r.PathValue("tenant_id")) + "/e-invoice-accounts/" + url.PathEscape(r.PathValue("environment"))
	})(w, r)
}
