package httpapi

import (
	"context"
	"net/http"
	"slices"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/tenant"
)

// The operator console's reads (web/admin): tenants, a tenant's invoices with their saga history, and the dead-letter queues.
// An operator acts on behalf of the tenant named in the path, so these routes put it into the call's tenant context.

func operatorAs(r *http.Request) (context.Context, error) {
	id, err := errkey.ParseUUID("gateway.request.bad_tenant_id", "tenant_id", r.PathValue("tenant_id"))
	if err != nil {
		return nil, err
	}
	return tenant.NewContext(r.Context(), tenant.Info{ID: id}), nil
}

func (s *Server) adminListTenants(w http.ResponseWriter, r *http.Request) {
	req := &plansv1.ListTenantsRequest{PageToken: r.URL.Query().Get("page_token")}
	n, err := intParam(r.URL.Query(), "page_size")
	if err != nil {
		writeError(w, r, err)
		return
	}
	req.PageSize = n
	resp, err := s.d.Admin.ListTenants(r.Context(), req)
	if hide := s.hiddenPlatformTenant(r.Context()); err == nil && hide != "" {
		resp.Tenants = slices.DeleteFunc(resp.Tenants, func(t *plansv1.Tenant) bool { return t.GetId() == hide })
	}
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) adminListInvoices(w http.ResponseWriter, r *http.Request) {
	ctx, err := operatorAs(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	q := r.URL.Query()
	req := &billingv1.ListInvoicesRequest{PeriodId: q.Get("period"), Status: q.Get("status"), PageToken: q.Get("page_token")}
	n, err := intParam(q, "page_size")
	if err != nil {
		writeError(w, r, err)
		return
	}
	req.PageSize = n
	resp, err := s.d.Billing.ListInvoices(ctx, req)
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) adminGetInvoice(w http.ResponseWriter, r *http.Request) {
	ctx, err := operatorAs(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	resp, err := s.d.Billing.GetInvoice(ctx, &billingv1.GetInvoiceRequest{InvoiceId: r.PathValue("invoice_id")})
	respond(w, r, http.StatusOK, resp.GetInvoice(), err)
}

func (s *Server) adminInvoicePDF(w http.ResponseWriter, r *http.Request) {
	ctx, err := operatorAs(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	resp, err := s.d.Billing.GetInvoicePdfUrl(ctx, &billingv1.GetInvoicePdfUrlRequest{InvoiceId: r.PathValue("invoice_id")})
	respondSignedURL(w, r, resp.GetUrl(), resp.GetExpireTime(), err)
}

func (s *Server) adminInvoiceSaga(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.BillingAdmin.GetInvoiceSaga(r.Context(), &billingv1.GetInvoiceSagaRequest{TenantId: r.PathValue("tenant_id"), InvoiceId: r.PathValue("invoice_id")})
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) adminListDeadLetters(w http.ResponseWriter, r *http.Request) {
	req := &billingv1.ListDeadLettersRequest{Queue: r.URL.Query().Get("queue")}
	n, err := intParam(r.URL.Query(), "limit")
	if err != nil {
		writeError(w, r, err)
		return
	}
	req.Limit = n
	resp, err := s.d.BillingAdmin.ListDeadLetters(r.Context(), req)
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) adminRedriveDeadLetters(w http.ResponseWriter, r *http.Request) {
	req := &billingv1.RedriveDeadLettersRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	resp, err := s.d.BillingAdmin.RedriveDeadLetters(r.Context(), req)
	respond(w, r, http.StatusOK, resp, err)
}
