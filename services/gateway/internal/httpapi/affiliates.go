package httpapi

import (
	"net/http"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/services/gateway/internal/ratelimit"
)

// The affiliate program (phase 47), owner side: the rules, the partners, who they brought. Key-scoped like the rest of /v1, so a sandbox key runs the sandbox
// program and a live key the live one. A partner's own view is under /app/v1/affiliate (affiliate_portal.go); the public link and the cookie are in
// affiliate_links.go.

func (s *Server) affiliateRoutes() []Route {
	return []Route{
		{"GET", "/v1/affiliates/program", AccessTenant, ratelimit.ClassRead, s.getAffiliateProgram},
		{"PUT", "/v1/affiliates/program", AccessTenant, ratelimit.ClassWrite, s.setAffiliateProgram},
		{"POST", "/v1/affiliates", AccessTenant, ratelimit.ClassWrite, s.createAffiliate},
		{"GET", "/v1/affiliates", AccessTenant, ratelimit.ClassRead, s.listAffiliates},
		{"GET", "/v1/affiliates/referrals", AccessTenant, ratelimit.ClassRead, s.listAffiliateReferrals},
		{"GET", "/v1/affiliates/commissions", AccessTenant, ratelimit.ClassRead, s.listAffiliateCommissions},
		{"POST", "/v1/affiliates/commissions/reverse", AccessTenant, ratelimit.ClassWrite, s.reverseAffiliateCommission},
		{"GET", "/v1/affiliates/balances", AccessTenant, ratelimit.ClassRead, s.getAffiliateBalances},
		{"GET", "/v1/affiliates/funnel", AccessTenant, ratelimit.ClassRead, s.getAffiliateFunnel},
		{"GET", "/v1/affiliates/{id}", AccessTenant, ratelimit.ClassRead, s.getAffiliate},
		{"PATCH", "/v1/affiliates/{id}", AccessTenant, ratelimit.ClassWrite, s.updateAffiliate},
		{"PUT", "/v1/affiliates/{id}/promotion-code", AccessTenant, ratelimit.ClassWrite, s.linkAffiliatePromotionCode},
		{"POST", "/v1/customers/{external_id}/referral", AccessTenant, ratelimit.ClassWrite, s.attributeCustomer},
	}
}

func (s *Server) getAffiliateProgram(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Affiliates.GetAffiliateProgram(r.Context(), &plansv1.GetAffiliateProgramRequest{})
	respond(w, r, http.StatusOK, resp.GetProgram(), err)
}

func (s *Server) setAffiliateProgram(w http.ResponseWriter, r *http.Request) {
	p := &plansv1.AffiliateProgram{}
	if err := decode(r, p); err != nil {
		writeError(w, r, err)
		return
	}
	resp, err := s.d.Affiliates.SetAffiliateProgram(r.Context(), &plansv1.SetAffiliateProgramRequest{Program: p})
	respond(w, r, http.StatusOK, resp.GetProgram(), err)
}

func (s *Server) createAffiliate(w http.ResponseWriter, r *http.Request) {
	req := &plansv1.CreateAffiliateRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	resp, err := s.d.Affiliates.CreateAffiliate(r.Context(), req)
	respond(w, r, http.StatusCreated, s.withLink(resp.GetAffiliate()), err)
}

func (s *Server) listAffiliates(w http.ResponseWriter, r *http.Request) {
	size, token, ok := pageParams(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	resp, err := s.d.Affiliates.ListAffiliates(r.Context(), &plansv1.ListAffiliatesRequest{Status: q.Get("status"), Search: q.Get("search"), PageSize: size, PageToken: token})
	for _, a := range resp.GetAffiliates() {
		s.withLink(a)
	}
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) listAffiliateReferrals(w http.ResponseWriter, r *http.Request) {
	size, token, ok := pageParams(w, r)
	if !ok {
		return
	}
	resp, err := s.d.Affiliates.ListAffiliateReferrals(r.Context(), &plansv1.ListAffiliateReferralsRequest{AffiliateId: r.URL.Query().Get("affiliate_id"), PageSize: size, PageToken: token})
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) getAffiliate(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Affiliates.GetAffiliate(r.Context(), &plansv1.GetAffiliateRequest{Id: r.PathValue("id")})
	respond(w, r, http.StatusOK, s.withLink(resp.GetAffiliate()), err)
}

func (s *Server) updateAffiliate(w http.ResponseWriter, r *http.Request) {
	req := &plansv1.UpdateAffiliateRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	req.Id = r.PathValue("id")
	resp, err := s.d.Affiliates.UpdateAffiliate(r.Context(), req)
	respond(w, r, http.StatusOK, s.withLink(resp.GetAffiliate()), err)
}

func (s *Server) linkAffiliatePromotionCode(w http.ResponseWriter, r *http.Request) {
	req := &plansv1.LinkAffiliatePromotionCodeRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	req.AffiliateId = r.PathValue("id")
	resp, err := s.d.Affiliates.LinkAffiliatePromotionCode(r.Context(), req)
	respond(w, r, http.StatusOK, s.withLink(resp.GetAffiliate()), err)
}

// attributeCustomer is POST /v1/customers/{external_id}/referral: the tenant's own sign-up says which affiliate brought a customer who already exists.
func (s *Server) attributeCustomer(w http.ResponseWriter, r *http.Request) {
	req := &plansv1.AttributeCustomerRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	req.CustomerExternalId, req.CustomerId = r.PathValue("external_id"), ""
	resp, err := s.d.Affiliates.AttributeCustomer(r.Context(), req)
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) listAffiliateCommissions(w http.ResponseWriter, r *http.Request) {
	size, token, ok := pageParams(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	req := &plansv1.ListAffiliateCommissionsRequest{AffiliateId: q.Get("affiliate_id"), Status: q.Get("status"), Kind: q.Get("kind"), InvoiceId: q.Get("invoice_id"),
		PayoutId: q.Get("payout_id"), PageSize: size, PageToken: token}
	for name, dst := range map[string]**timestamppb.Timestamp{"from": &req.FromTime, "to": &req.ToTime} {
		if v := q.Get(name); v != "" {
			t, err := time.Parse(time.RFC3339, v)
			if err != nil {
				writeError(w, r, invalid(name, "must be an RFC 3339 time"))
				return
			}
			*dst = timestamppb.New(t)
		}
	}
	resp, err := s.d.Affiliates.ListAffiliateCommissions(r.Context(), req)
	respond(w, r, http.StatusOK, resp, err)
}

// reverseAffiliateCommission is POST /v1/affiliates/commissions/reverse: take a commission back for a chargeback or by hand. Refunds, voids and credit notes
// reverse on their own.
func (s *Server) reverseAffiliateCommission(w http.ResponseWriter, r *http.Request) {
	req := &plansv1.ReverseAffiliateCommissionRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	resp, err := s.d.Affiliates.ReverseAffiliateCommission(r.Context(), req)
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) getAffiliateBalances(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Affiliates.GetAffiliateBalances(r.Context(), &plansv1.GetAffiliateBalancesRequest{AffiliateId: r.URL.Query().Get("affiliate_id")})
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) getAffiliateFunnel(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	resp, err := s.d.Affiliates.GetAffiliateFunnel(r.Context(), &plansv1.GetAffiliateFunnelRequest{FromDate: q.Get("from"), ToDate: q.Get("to"), AffiliateId: q.Get("affiliate_id")})
	respond(w, r, http.StatusOK, resp, err)
}
