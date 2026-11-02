package httpapi

import (
	"context"
	"net/http"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/tenant"
)

// The seller's agreements on the customer portal (docs/plans/21-contracts.md): what the customer accepted, each with the page they accepted exactly as it was stored; and,
// when the seller published a material version since, the texts to accept again before their next payment. A renewal is never held back by this (nobody is present to
// accept, and the payment gate of the pay pages already asks again at the next purchase): the prompt is how a customer who is here finds out and gets it done.

type portalAgreementRow struct{ Label, Number, Date, Href string }

func (s *Server) portalRoutes() []Route {
	return []Route{
		{"POST", "/c/{token}/agreements", AccessPublic, "", s.portalAgree},
		{"GET", "/c/{token}/agreements/{id}", AccessPublic, "", s.portalAgreementPage},
	}
}

// portalAgreements fills the agreements of the portal. The section is optional: when plans-svc cannot answer, the page is shown without it.
func (s *Server) portalAgreements(ctx context.Context, v *portalView, resp *billingv1.GetPortalResponse, token, lang, q, problem string) {
	tid, err := uuid.Parse(resp.GetTenantId())
	if s.d.Contracts == nil || err != nil || resp.GetCustomerId() == "" {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, subscriptionsWait)
	defer cancel()
	octx := tenant.OutgoingFor(ctx, tid)
	list, err := s.d.Contracts.ListContractAcceptances(octx, &plansv1.ListContractAcceptancesRequest{CustomerId: resp.GetCustomerId()})
	if err != nil {
		s.d.Log.WarnContext(ctx, "portal agreements unavailable", "error", err)
		return
	}
	seen := map[string]bool{}
	for _, a := range list.GetAcceptances() { // newest first: the version a kind stands at now
		if seen[a.GetKind()] {
			continue
		}
		seen[a.GetKind()] = true
		v.Agreements = append(v.Agreements, portalAgreementRow{Label: kindLabel(lang, a.GetKind()), Number: v.F.Amount(int64(a.GetVersionNumber()), 0),
			Date: v.F.Date(a.GetAcceptTime().AsTime().UTC()), Href: "/c/" + token + "/agreements/" + a.GetId() + q})
	}
	if len(list.GetAcceptances()) == 0 {
		return // nobody who never accepted is asked to accept "again"; a first acceptance happens where they pay
	}
	req, err := s.d.Contracts.GetContractRequirements(octx, &plansv1.GetContractRequirementsRequest{CustomerId: resp.GetCustomerId(), Locale: lang})
	if err != nil || !req.GetEnforced() || req.GetSatisfied() || len(req.GetMissingKinds()) > 0 {
		return
	}
	v.Ask, v.AgreeAction = contractViewOf(req, lang, v.L), "/c/"+token+"/agreements"+q
	if problem != "" {
		v.Ask.Error = v.L[problem]
	}
}

// portalAgree is POST /c/{token}/agreements: the boxes of the texts to accept again. It records the acceptance (source hosted, context portal) and goes back to the portal.
func (s *Server) portalAgree(w http.ResponseWriter, r *http.Request) {
	lang := pickLang(r)
	if !s.hostedPayAllowed(w, r) {
		return
	}
	token := r.PathValue("token")
	resp, err := s.d.Hosted.GetPortal(r.Context(), &billingv1.GetPortalRequest{PortalToken: token})
	if goneError(err) {
		hostedNotFound(w, lang)
		return
	}
	tid, perr := uuid.Parse(resp.GetTenantId())
	if err != nil || perr != nil || s.d.Contracts == nil {
		hostedTrouble(w, lang)
		return
	}
	lang = customerLang(r, resp.GetCustomerLocale())
	l, back := hostedText(lang), "/c/"+token+langSuffix(r)
	octx := tenant.OutgoingFor(r.Context(), tid)
	req, err := s.d.Contracts.GetContractRequirements(octx, &plansv1.GetContractRequirementsRequest{CustomerId: resp.GetCustomerId(), Locale: lang})
	if err != nil || len(req.GetMissingKinds()) > 0 {
		s.message(w, r, http.StatusServiceUnavailable, lang, l["agreements_title"], l["contract_unavailable"], back)
		return
	}
	if !req.GetEnforced() || req.GetSatisfied() {
		hostedHeaders(w)
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	_ = r.ParseForm()
	items, problem := agreementItems(req, r.PostForm, l)
	if problem != "" {
		s.portalPageWith(w, r, http.StatusUnprocessableEntity, problem)
		return
	}
	rec := &plansv1.RecordContractAcceptanceRequest{CustomerId: resp.GetCustomerId(), Source: "hosted", ContextKind: "portal", Locale: lang, Items: items, Ip: s.clientAddr(r), UserAgent: r.UserAgent()}
	if req.GetImmediateStart() {
		rec.ImmediateStartConsent, rec.ImmediateStartText = true, l["consent_start"]
		rec.WithdrawalWaiverAcknowledged, rec.WithdrawalWaiverText = true, l["consent_waiver"]
	}
	if _, err := s.d.Contracts.RecordContractAcceptance(octx, rec); err != nil {
		switch {
		case errkey.Of(err) == "plans.contract.version_stale":
			s.portalPageWith(w, r, http.StatusUnprocessableEntity, "contract_stale")
		case status.Code(err) == codes.FailedPrecondition || status.Code(err) == codes.InvalidArgument:
			s.d.Log.WarnContext(r.Context(), "portal acceptance refused", "error", err)
			s.portalPageWith(w, r, http.StatusUnprocessableEntity, "contract_missing")
		default:
			s.d.Log.ErrorContext(r.Context(), "portal acceptance not recorded", "error", err)
			s.message(w, r, http.StatusServiceUnavailable, lang, l["agreements_title"], l["pay_unavailable"], back)
		}
		return
	}
	hostedHeaders(w)
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// portalAgreementPage is GET /c/{token}/agreements/{id}: the page the customer accepted, exactly as it was stored (the evidence, not a page written again), under a policy
// that lets it run nothing. Only an acceptance of this portal's customer opens: any other id is the same not-found as a link that does not exist.
func (s *Server) portalAgreementPage(w http.ResponseWriter, r *http.Request) {
	lang := pickLang(r)
	if !s.hostedAllowed(w, r) {
		return
	}
	resp, err := s.d.Hosted.GetPortal(r.Context(), &billingv1.GetPortalRequest{PortalToken: r.PathValue("token")})
	if goneError(err) {
		hostedNotFound(w, lang)
		return
	}
	tid, perr := uuid.Parse(resp.GetTenantId())
	if err != nil || perr != nil || s.d.Contracts == nil {
		hostedTrouble(w, lang)
		return
	}
	octx := tenant.OutgoingFor(r.Context(), tid)
	list, err := s.d.Contracts.ListContractAcceptances(octx, &plansv1.ListContractAcceptancesRequest{CustomerId: resp.GetCustomerId()})
	if err != nil {
		hostedTrouble(w, lang)
		return
	}
	found := false
	for _, a := range list.GetAcceptances() {
		found = found || a.GetId() == r.PathValue("id")
	}
	if !found {
		hostedNotFound(w, lang)
		return
	}
	snap, err := s.d.Contracts.GetContractSnapshot(octx, &plansv1.GetContractSnapshotRequest{AcceptanceId: r.PathValue("id")})
	if err != nil {
		hostedTrouble(w, lang)
		return
	}
	hostedHeadersCSP(w, pageCSP(false, formsNone))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(snap.GetHtml()))
}
