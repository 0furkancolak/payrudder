package httpapi

import (
	"net/http"
	"strings"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/services/gateway/internal/ratelimit"
)

// The partner's own pages (phase 47): a signed-in person sees their affiliations, their link, clicks, referrals (customers as masked e-mail addresses), earnings by
// status and payouts, and applies to a program. Like the rest of /app/v1 the caller is the session's user; a body never names the person, and plans-svc finds every row
// only through that person's own partner record. These routes need no account and no API key: a partner is a person, not a tenant.

func (s *Server) affiliatePortalRoutes() []Route {
	return []Route{
		{"GET", "/app/v1/affiliate/affiliations", AccessSession, ratelimit.ClassRead, s.myAffiliations},
		{"POST", "/app/v1/affiliate/claim", AccessSession, ratelimit.ClassWrite, s.claimMyAffiliations},
		{"GET", "/app/v1/affiliate/programs/{tenant_id}", AccessSession, ratelimit.ClassRead, s.lookupAffiliateProgram},
		{"POST", "/app/v1/affiliate/applications", AccessSession, ratelimit.ClassWrite, s.applyAsAffiliate},
		{"GET", "/app/v1/affiliate/affiliations/{id}", AccessSession, ratelimit.ClassRead, s.myAffiliation},
		{"PATCH", "/app/v1/affiliate/affiliations/{id}", AccessSession, ratelimit.ClassWrite, s.updateMyAffiliation},
		{"GET", "/app/v1/affiliate/affiliations/{id}/referrals", AccessSession, ratelimit.ClassRead, s.myReferrals},
		{"GET", "/app/v1/affiliate/affiliations/{id}/commissions", AccessSession, ratelimit.ClassRead, s.myCommissions},
		{"GET", "/app/v1/affiliate/affiliations/{id}/payouts", AccessSession, ratelimit.ClassRead, s.myPayouts},
	}
}

// myLink gives an affiliation the public address of its link, which only the gateway knows.
func (s *Server) myLink(a *plansv1.MyAffiliation) *plansv1.MyAffiliation {
	if a != nil && a.GetCode() != "" {
		a.Link = strings.TrimRight(s.d.PublicURL, "/") + "/r/" + a.GetCode()
	}
	return a
}

func (s *Server) myAffiliations(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.AffiliatePortal.ListMyAffiliations(r.Context(), &plansv1.ListMyAffiliationsRequest{UserId: sessionFrom(r.Context()).UserID})
	for _, a := range resp.GetAffiliations() {
		s.myLink(a)
	}
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) claimMyAffiliations(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.AffiliatePortal.ClaimMyAffiliations(r.Context(), &plansv1.ClaimMyAffiliationsRequest{UserId: sessionFrom(r.Context()).UserID})
	for _, a := range resp.GetAffiliations() {
		s.myLink(a)
	}
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) lookupAffiliateProgram(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.AffiliatePortal.LookupAffiliateProgram(r.Context(), &plansv1.LookupAffiliateProgramRequest{UserId: sessionFrom(r.Context()).UserID, TenantId: r.PathValue("tenant_id")})
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) applyAsAffiliate(w http.ResponseWriter, r *http.Request) {
	req := &plansv1.ApplyAsAffiliateRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	req.UserId = sessionFrom(r.Context()).UserID
	resp, err := s.d.AffiliatePortal.ApplyAsAffiliate(r.Context(), req)
	respond(w, r, http.StatusCreated, s.myLink(resp.GetAffiliation()), err)
}

func (s *Server) myAffiliation(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	resp, err := s.d.AffiliatePortal.GetMyAffiliation(r.Context(), &plansv1.GetMyAffiliationRequest{UserId: sessionFrom(r.Context()).UserID, AffiliateId: r.PathValue("id"),
		FromDate: q.Get("from"), ToDate: q.Get("to")})
	s.myLink(resp.GetAffiliation())
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) updateMyAffiliation(w http.ResponseWriter, r *http.Request) {
	req := &plansv1.UpdateMyAffiliationRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	req.UserId, req.AffiliateId = sessionFrom(r.Context()).UserID, r.PathValue("id")
	resp, err := s.d.AffiliatePortal.UpdateMyAffiliation(r.Context(), req)
	respond(w, r, http.StatusOK, s.myLink(resp.GetAffiliation()), err)
}

func (s *Server) myReferrals(w http.ResponseWriter, r *http.Request) {
	size, token, ok := pageParams(w, r)
	if !ok {
		return
	}
	resp, err := s.d.AffiliatePortal.ListMyReferrals(r.Context(), &plansv1.ListMyReferralsRequest{UserId: sessionFrom(r.Context()).UserID, AffiliateId: r.PathValue("id"), PageSize: size, PageToken: token})
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) myCommissions(w http.ResponseWriter, r *http.Request) {
	size, token, ok := pageParams(w, r)
	if !ok {
		return
	}
	resp, err := s.d.AffiliatePortal.ListMyCommissions(r.Context(), &plansv1.ListMyCommissionsRequest{UserId: sessionFrom(r.Context()).UserID, AffiliateId: r.PathValue("id"),
		Status: r.URL.Query().Get("status"), PageSize: size, PageToken: token})
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) myPayouts(w http.ResponseWriter, r *http.Request) {
	size, token, ok := pageParams(w, r)
	if !ok {
		return
	}
	resp, err := s.d.AffiliatePortal.ListMyPayouts(r.Context(), &plansv1.ListMyPayoutsRequest{UserId: sessionFrom(r.Context()).UserID, AffiliateId: r.PathValue("id"), PageSize: size, PageToken: token})
	respond(w, r, http.StatusOK, resp, err)
}
