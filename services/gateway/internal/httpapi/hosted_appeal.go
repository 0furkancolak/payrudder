package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/env"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/tenant"
)

// The appeal of a blocked customer (docs/plans/13-customer-ban.md). The customer reaches it from the portal link the seller gave them (/c/{token}); the token is the only
// credential, as on every other portal page, and no new way of signing in exists. The page says nothing of why the customer is blocked (the category and the note are the
// seller's) and treats the statement as untrusted text: html/template escapes it wherever it is printed.

type appealSubject struct {
	externalID, seller, logo string
	test                     bool
}

type appealView struct {
	pageBase
	Action, Back, Statement, Error string
	// Open: an appeal is waiting for the seller, so the form is not offered. Answer is the seller's answer to the customer's latest appeal when it was rejected.
	Open   bool
	Answer string
}

func (v *appealView) Title() string { return v.L["appeal_title"] }
func (v *appealView) formTargets() string {
	if v.Open {
		return formsNone
	}
	return formsSelf
}

func (s *Server) appealRoutes() []Route {
	return []Route{
		{"GET", "/c/{token}/appeal", AccessPublic, "", s.appealPage},
		{"POST", "/c/{token}/appeal", AccessPublic, "", s.appealSubmit},
	}
}

// blockInForce says whether a customer as plans-svc returns it is blocked now: a block with an end in the past has ended, whatever block_time says.
func blockInForce(c *plansv1.Customer, now time.Time) bool {
	if c.GetBlockTime() == nil {
		return false
	}
	u := c.GetBlockedUntil()
	return u == nil || u.AsTime().After(now)
}

// appealOffered says whether the portal customer is blocked, for the link the portal shows. It fails closed (no link) when plans-svc cannot say within a moment.
func (s *Server) appealOffered(ctx context.Context, resp *billingv1.GetPortalResponse) bool {
	if s.d.Plans == nil {
		return false
	}
	tid, err := uuid.Parse(resp.GetTenantId())
	if err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(tenant.OutgoingFor(ctx, tid), withdrawLinkWait)
	defer cancel()
	c, err := s.d.Plans.GetCustomer(ctx, &plansv1.GetCustomerRequest{Id: resp.GetCustomerId()})
	return err == nil && blockInForce(c.GetCustomer(), time.Now())
}

func (s *Server) appealBase(sub *appealSubject, lang string) pageBase {
	b := newBase(lang)
	b.Test, b.SideSeller, b.Logo = sub.test, sub.seller, sub.logo
	b.SideNote = b.L["appeal_title"]
	return b
}

// appealLoad is the start of both handlers: rate limit, the person, and whether they are blocked. When it answers, it returns false. The context carries the tenant.
func (s *Server) appealLoad(w http.ResponseWriter, r *http.Request, post bool) (*appealSubject, context.Context, string, bool) {
	lang := pickLang(r)
	allowed := s.hostedAllowed
	if post {
		allowed = s.hostedPayAllowed
	}
	if !allowed(w, r) {
		return nil, nil, lang, false
	}
	token := r.PathValue("token")
	resp, err := s.d.Hosted.GetPortal(r.Context(), &billingv1.GetPortalRequest{PortalToken: token})
	switch {
	case goneError(err):
		hostedNotFound(w, lang)
		return nil, nil, lang, false
	case err != nil || s.d.Plans == nil:
		hostedTrouble(w, lang)
		return nil, nil, lang, false
	}
	lang = customerLang(r, resp.GetCustomerLocale())
	tid, perr := uuid.Parse(resp.GetTenantId())
	if perr != nil {
		hostedTrouble(w, lang)
		return nil, nil, lang, false
	}
	octx := tenant.OutgoingFor(r.Context(), tid)
	cust, err := s.d.Plans.GetCustomer(octx, &plansv1.GetCustomerRequest{Id: resp.GetCustomerId()})
	if err != nil {
		hostedTrouble(w, lang)
		return nil, nil, lang, false
	}
	sub := &appealSubject{externalID: cust.GetCustomer().GetExternalId(), seller: resp.GetSellerName(), test: resp.GetEnvironment() == env.Sandbox.String()}
	if resp.GetLogoAvailable() {
		sub.logo = scope{portal: token}.logo()
	}
	if !blockInForce(cust.GetCustomer(), time.Now()) {
		l := hostedText(lang)
		s.message(w, r, http.StatusNotFound, lang, l["appeal_title"], l["appeal_none"], "/c/"+token+langSuffix(r))
		return nil, nil, lang, false
	}
	return sub, octx, lang, true
}

func (s *Server) appealPage(w http.ResponseWriter, r *http.Request) {
	sub, octx, lang, ok := s.appealLoad(w, r, false)
	if !ok {
		return
	}
	v := &appealView{pageBase: s.appealBase(sub, lang), Action: "/c/" + r.PathValue("token") + "/appeal" + langSuffix(r), Back: "/c/" + r.PathValue("token") + langSuffix(r)}
	list, err := s.d.Plans.ListCustomerBlockAppeals(octx, &plansv1.ListCustomerBlockAppealsRequest{ExternalId: sub.externalID})
	if err != nil {
		s.d.Log.WarnContext(r.Context(), "appeals unavailable", "error", err)
		hostedTrouble(w, lang)
		return
	}
	if a := list.GetAppeals(); len(a) > 0 {
		v.Open = a[0].GetStatus() == "open"
		if a[0].GetStatus() == "rejected" {
			v.Answer = a[0].GetAnswer()
		}
	}
	s.render(w, r, http.StatusOK, "appeal", v)
}

func (s *Server) appealSubmit(w http.ResponseWriter, r *http.Request) {
	sub, octx, lang, ok := s.appealLoad(w, r, true)
	if !ok {
		return
	}
	l := hostedText(lang)
	back := "/c/" + r.PathValue("token") + langSuffix(r)
	_ = r.ParseForm()
	statement := r.PostForm.Get("statement")
	form := func(code int, problem string) {
		s.render(w, r, code, "appeal", &appealView{pageBase: s.appealBase(sub, lang), Action: "/c/" + r.PathValue("token") + "/appeal" + langSuffix(r), Back: back, Statement: statement, Error: problem})
	}
	_, err := s.d.Plans.SubmitCustomerBlockAppeal(octx, &plansv1.SubmitCustomerBlockAppealRequest{ExternalId: sub.externalID, Statement: statement})
	switch {
	case err == nil:
		s.message(w, r, http.StatusOK, lang, l["appeal_done"], l["appeal_done_text"], back)
	case status.Code(err) == codes.InvalidArgument:
		form(http.StatusUnprocessableEntity, l["appeal_invalid"])
	case errkey.Of(err) == "plans.customer_appeal.exists":
		s.message(w, r, http.StatusOK, lang, l["appeal_done"], l["appeal_open"], back)
	case errkey.Of(err) == "plans.customer_appeal.not_blocked":
		s.message(w, r, http.StatusNotFound, lang, l["appeal_title"], l["appeal_none"], back)
	default:
		s.d.Log.WarnContext(r.Context(), "appeal not recorded", "error", err)
		s.message(w, r, http.StatusServiceUnavailable, lang, l["appeal_title"], l["pay_unavailable"], back)
	}
}
