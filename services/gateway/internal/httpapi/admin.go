package httpapi

import (
	"context"
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	billingv1 "github.com/0furkancolak/quotify/gen/go/quotify/billing/v1"
	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/tenant"
)

// The operator console's reads (web/admin): tenants, a tenant's invoices with their saga history, and the dead-letter queues.
// An operator acts on behalf of the tenant named in the path, so these routes put it into the call's tenant context.

func operatorAs(r *http.Request) (context.Context, error) {
	id, err := uuid.Parse(r.PathValue("tenant_id"))
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "tenant_id must be a UUID")
	}
	return tenant.NewContext(r.Context(), tenant.Info{ID: id}), nil
}

func (s *Server) adminListTenants(w http.ResponseWriter, r *http.Request) {
	req := &plansv1.ListTenantsRequest{PageToken: r.URL.Query().Get("page_token")}
	if v := r.URL.Query().Get("page_size"); v != "" {
		n, err := strconv.ParseInt(v, 10, 32)
		if err != nil {
			writeError(w, r, invalid("page_size", "must be an integer"))
			return
		}
		req.PageSize = int32(n)
	}
	resp, err := s.d.Admin.ListTenants(r.Context(), req)
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
	if v := q.Get("page_size"); v != "" {
		n, err := strconv.ParseInt(v, 10, 32)
		if err != nil {
			writeError(w, r, invalid("page_size", "must be an integer"))
			return
		}
		req.PageSize = int32(n)
	}
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
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSONValue(w, http.StatusOK, map[string]any{"url": resp.GetUrl(), "expires_at": resp.GetExpireTime().AsTime().UTC().Format("2006-01-02T15:04:05Z07:00")})
}

func (s *Server) adminInvoiceSaga(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.BillingAdmin.GetInvoiceSaga(r.Context(), &billingv1.GetInvoiceSagaRequest{TenantId: r.PathValue("tenant_id"), InvoiceId: r.PathValue("invoice_id")})
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) adminListDeadLetters(w http.ResponseWriter, r *http.Request) {
	req := &billingv1.ListDeadLettersRequest{Queue: r.URL.Query().Get("queue")}
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.ParseInt(v, 10, 32)
		if err != nil {
			writeError(w, r, invalid("limit", "must be an integer"))
			return
		}
		req.Limit = int32(n)
	}
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
