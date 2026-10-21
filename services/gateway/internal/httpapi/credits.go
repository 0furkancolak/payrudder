package httpapi

import (
	"net/http"
	"strconv"

	billingv1 "github.com/0furkancolak/quotify/gen/go/quotify/billing/v1"
	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/services/gateway/internal/ratelimit"
)

// creditRoutes is the table for credit packs and a customer's credits.
func (s *Server) creditRoutes() []Route {
	return []Route{
		{"GET", "/v1/credit-packs", AccessTenant, ratelimit.ClassRead, s.listCreditPacks},
		{"POST", "/v1/credit-packs", AccessTenant, ratelimit.ClassWrite, s.createCreditPack},
		{"POST", "/v1/credit-packs/{id}/activate", AccessTenant, ratelimit.ClassWrite, s.setCreditPackActive(true)},
		{"POST", "/v1/credit-packs/{id}/deactivate", AccessTenant, ratelimit.ClassWrite, s.setCreditPackActive(false)},
		{"GET", "/v1/customers/{external_id}/credits", AccessTenant, ratelimit.ClassRead, s.getCreditBalance},
		{"POST", "/v1/customers/{external_id}/credits/grant", AccessTenant, ratelimit.ClassWrite, s.grantCredits},
		{"POST", "/v1/customers/{external_id}/credits/consume", AccessTenant, ratelimit.ClassWrite, s.consumeCredits},
		{"GET", "/v1/customers/{external_id}/credits/ledger", AccessTenant, ratelimit.ClassRead, s.listCreditLedger},
	}
}

// listCreditPacks is GET /v1/credit-packs?active=true.
func (s *Server) listCreditPacks(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Billing.ListCreditPacks(r.Context(), &billingv1.ListCreditPacksRequest{ActiveOnly: r.URL.Query().Get("active") == "true"})
	respond(w, r, http.StatusOK, resp, err)
}

// createCreditPack is POST /v1/credit-packs.
func (s *Server) createCreditPack(w http.ResponseWriter, r *http.Request) {
	req := &billingv1.CreateCreditPackRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	resp, err := s.d.Billing.CreateCreditPack(r.Context(), req)
	respond(w, r, http.StatusCreated, resp.GetPack(), err)
}

// setCreditPackActive is POST /v1/credit-packs/{id}/activate and /deactivate.
func (s *Server) setCreditPackActive(active bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		resp, err := s.d.Billing.SetCreditPackActive(r.Context(), &billingv1.SetCreditPackActiveRequest{Id: r.PathValue("id"), Active: active})
		respond(w, r, http.StatusOK, resp, err)
	}
}

// customerID resolves the external id in the path to Quotify's id, or writes the error.
func (s *Server) customerID(w http.ResponseWriter, r *http.Request) (string, bool) {
	cust, err := s.d.Plans.ResolveCustomer(r.Context(), &plansv1.ResolveCustomerRequest{ExternalId: r.PathValue("external_id")})
	if err != nil {
		writeError(w, r, err)
		return "", false
	}
	return cust.GetCustomer().GetId(), true
}

// getCreditBalance is GET /v1/customers/{external_id}/credits.
func (s *Server) getCreditBalance(w http.ResponseWriter, r *http.Request) {
	id, ok := s.customerID(w, r)
	if !ok {
		return
	}
	resp, err := s.d.Billing.GetCreditBalance(r.Context(), &billingv1.GetCreditBalanceRequest{CustomerId: id})
	respond(w, r, http.StatusOK, resp, err)
}

// grantCredits is POST /v1/customers/{external_id}/credits/grant. The key may come in the Idempotency-Key header.
func (s *Server) grantCredits(w http.ResponseWriter, r *http.Request) {
	req := &billingv1.GrantCreditsRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	id, ok := s.customerID(w, r)
	if !ok {
		return
	}
	req.CustomerId = id
	if req.IdempotencyKey == "" {
		req.IdempotencyKey = r.Header.Get("Idempotency-Key")
	}
	resp, err := s.d.Billing.GrantCredits(r.Context(), req)
	respond(w, r, http.StatusOK, resp, err)
}

// consumeCredits is POST /v1/customers/{external_id}/credits/consume. The key may come in the Idempotency-Key header.
func (s *Server) consumeCredits(w http.ResponseWriter, r *http.Request) {
	req := &billingv1.ConsumeCreditsRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	id, ok := s.customerID(w, r)
	if !ok {
		return
	}
	req.CustomerId = id
	if req.IdempotencyKey == "" {
		req.IdempotencyKey = r.Header.Get("Idempotency-Key")
	}
	resp, err := s.d.Billing.ConsumeCredits(r.Context(), req)
	respond(w, r, http.StatusOK, resp, err)
}

// listCreditLedger is GET /v1/customers/{external_id}/credits/ledger?page_size=&page_token=.
func (s *Server) listCreditLedger(w http.ResponseWriter, r *http.Request) {
	id, ok := s.customerID(w, r)
	if !ok {
		return
	}
	req := &billingv1.ListCreditLedgerRequest{CustomerId: id, PageToken: r.URL.Query().Get("page_token")}
	if v := r.URL.Query().Get("page_size"); v != "" {
		n, err := strconv.ParseInt(v, 10, 32)
		if err != nil || n < 1 || n > 200 {
			writeError(w, r, invalid("page_size", "must be 1 to 200"))
			return
		}
		req.PageSize = int32(n)
	}
	resp, err := s.d.Billing.ListCreditLedger(r.Context(), req)
	respond(w, r, http.StatusOK, resp, err)
}
