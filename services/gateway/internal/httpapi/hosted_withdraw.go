package httpapi

import (
	"cmp"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/env"
	"github.com/0furkancolak/payrudder/pkg/tenant"
)

// The online withdrawal function of the hosted pages (docs/plans/21-contracts.md). Directive 2011/83/EU art. 11a, inserted by Directive (EU) 2023/2673 and applied from 19
// June 2026, asks a trader whose consumer contracted through an online interface for a function labelled "withdraw from contract here" that is available throughout the
// withdrawal period, prominently displayed and easily accessible (1); a statement in which the consumer provides or confirms their name, details identifying the contract
// and the electronic means the confirmation will be sent by (2); a confirmation function labelled only "confirm withdrawal" (3); and an acknowledgement of receipt on a
// durable medium, with the content of the statement and the date and time of its submission, without undue delay (4). The pages are the function: a link on the paid
// invoice page and on the portal, a form, a page that shows what will be sent, and the confirmation. The mail is the notifier's (contract.withdrawal-received.v1).
//
// What the function does not do: it decides nothing. It records the statement for the seller, who accepts or rejects it, and the refund follows from that decision. It is
// offered only to a consumer, only while the seller requires agreements (a seller that never asked for any has no contract Payrudder knows of) and has not switched it off, and
// only for an invoice that is paid. The seller's own period (14 days unless its contract says otherwise) is the seller's to apply when it decides.

// withdrawSubject is who is withdrawing and from what.
type withdrawSubject struct {
	tenantID, customerID, externalID, name, email, locale string
	seller, logo                                          string
	test                                                  bool
	// invoices are the paid invoices that can be withdrawn from.
	invoices []withdrawInvoice
	// base is the page the function belongs to, without the language suffix: /i/{token} or /c/{token}.
	base string
}

type withdrawInvoice struct {
	ID, Number, Currency string
	Issued               *time.Time
	Total                int64
	Exponent             int
}

// choice is the invoice as the person reads it in the page's language: its number, when it was issued and what it cost.
func (i withdrawInvoice) choice(b pageBase) withdrawChoice {
	label := b.L["withdraw_invoice"] + " " + i.Number
	if i.Issued != nil {
		label += " · " + b.F.Date(i.Issued.UTC())
	}
	return withdrawChoice{ID: i.ID, Label: label + " · " + b.F.Amount(i.Total, i.Exponent) + " " + i.Currency}
}

type withdrawChoice struct{ ID, Label string }

func (s *withdrawSubject) choices(b pageBase) []withdrawChoice {
	out := make([]withdrawChoice, len(s.invoices))
	for i, inv := range s.invoices {
		out[i] = inv.choice(b)
	}
	return out
}

type withdrawView struct {
	pageBase
	Action, Back, Name, Email, Words, Contract, ContractLabel string
	Invoices                                                  []withdrawChoice
	Error                                                     string
	// Review is the second step: what will be sent, with the button that sends it.
	Review bool
}

func (v *withdrawView) Title() string {
	if v.Review {
		return v.L["withdraw_review"]
	}
	return v.L["withdraw_title"]
}

func (v *withdrawView) formTargets() string { return formsSelf }

func (s *Server) withdrawRoutes() []Route {
	return []Route{
		{"GET", "/i/{token}/withdraw", AccessPublic, "", s.withdrawPage(false)},
		{"POST", "/i/{token}/withdraw", AccessPublic, "", s.withdrawReview(false)},
		{"POST", "/i/{token}/withdraw/confirm", AccessPublic, "", s.withdrawConfirm(false)},
		{"GET", "/c/{token}/withdraw", AccessPublic, "", s.withdrawPage(true)},
		{"POST", "/c/{token}/withdraw", AccessPublic, "", s.withdrawReview(true)},
		{"POST", "/c/{token}/withdraw/confirm", AccessPublic, "", s.withdrawConfirm(true)},
	}
}

// pageWait bounds the lookups a link to the function waits for on another page: a slow plans-svc must not hold the invoice or the portal back.
const withdrawLinkWait = 2 * time.Second

// subjectOfPortal reads who the portal opens and what they paid, from billing's answer.
func subjectOfPortal(resp *billingv1.GetPortalResponse, token string) *withdrawSubject {
	subj := &withdrawSubject{tenantID: resp.GetTenantId(), customerID: resp.GetCustomerId(), name: resp.GetCustomerName(), seller: resp.GetSellerName(), locale: resp.GetCustomerLocale(),
		test: resp.GetEnvironment() == env.Sandbox.String(), base: "/c/" + token}
	if resp.GetLogoAvailable() {
		subj.logo = scope{portal: token}.logo()
	}
	for _, inv := range resp.GetInvoices() {
		if inv.GetStatus() != "paid" {
			continue
		}
		var at *time.Time
		if t := inv.GetIssueTime(); t != nil {
			v := t.AsTime()
			at = &v
		}
		subj.invoices = append(subj.invoices, withdrawInvoice{ID: inv.GetId(), Number: inv.GetNumber(), Currency: inv.GetCurrency(), Issued: at, Total: inv.GetTotalMinor(), Exponent: int(inv.GetCurrencyExponent())})
	}
	return subj
}

// subjectOfInvoice reads who an invoice link opens: the invoice, when it is paid. ok is false for an invoice that is not a customer's (a guest's payment link).
func subjectOfInvoice(resp *billingv1.GetHostedInvoiceResponse, token string) (subj *withdrawSubject, ok bool) {
	inv := resp.GetInvoice()
	id := inv.GetCustomerId()
	if inv == nil || id == "" || id == uuid.Nil.String() {
		return nil, false
	}
	sc := scope{token: token}
	subj = &withdrawSubject{tenantID: resp.GetPayment().GetTenantId(), customerID: id, name: resp.GetCustomerName(), seller: resp.GetSellerName(), locale: resp.GetCustomerLocale(),
		test: resp.GetEnvironment() == env.Sandbox.String(), base: "/i/" + token}
	if resp.GetLogoAvailable() {
		subj.logo = sc.logo()
	}
	if inv.GetStatus() == "paid" {
		var at *time.Time
		if t := inv.GetFinalizeTime(); t != nil {
			v := t.AsTime()
			at = &v
		}
		subj.invoices = []withdrawInvoice{{ID: inv.GetId(), Number: inv.GetNumber(), Currency: inv.GetCurrency(), Issued: at, Total: inv.GetTotalMinor(), Exponent: int(resp.GetCurrencyExponent())}}
	}
	return subj, true
}

// withdrawSubjectOf reads the credential from billing. A credential that opens nothing, or an invoice that is not a customer's, has no function: ok is false.
func (s *Server) withdrawSubjectOf(ctx context.Context, token string, portal bool) (*withdrawSubject, bool, error) {
	if portal {
		resp, err := s.d.Hosted.GetPortal(ctx, &billingv1.GetPortalRequest{PortalToken: token})
		if err != nil {
			return nil, false, err
		}
		return subjectOfPortal(resp, token), true, nil
	}
	resp, err := s.d.Hosted.GetHostedInvoice(ctx, scope{token: token}.get())
	if err != nil {
		return nil, false, err
	}
	subj, ok := subjectOfInvoice(resp, token)
	return subj, ok, nil
}

// withdrawalOffered says whether the function is offered to this person: the seller requires agreements and has not switched it off, and the customer is a consumer who has
// something paid to withdraw from. It also fills the customer's own details (the name to confirm, the address the acknowledgement goes to).
func (s *Server) withdrawalOffered(ctx context.Context, subj *withdrawSubject) bool {
	if s.d.Contracts == nil || s.d.Plans == nil || len(subj.invoices) == 0 {
		return false
	}
	tid, err := uuid.Parse(subj.tenantID)
	if err != nil {
		return false
	}
	octx := tenant.OutgoingFor(ctx, tid)
	st, err := s.d.Contracts.GetContractSettings(octx, &plansv1.GetContractSettingsRequest{})
	if err != nil || !st.GetSettings().GetWithdrawalFunction() || len(st.GetSettings().GetRequiredKinds()) == 0 {
		return false
	}
	c, err := s.d.Plans.GetCustomer(octx, &plansv1.GetCustomerRequest{Id: subj.customerID})
	if err != nil || c.GetCustomer().GetCustomerType() == "business" {
		return false // a business customer has no right of withdrawal from the consumer rules; unknown counts as a consumer
	}
	subj.externalID, subj.email = c.GetCustomer().GetExternalId(), c.GetCustomer().GetEmail()
	if c.GetCustomer().GetName() != "" {
		subj.name = c.GetCustomer().GetName()
	}
	return true
}

// withdrawalLink is the href of the function for a page that shows a link to it, or "" when it is not offered (or cannot be told within a moment).
func (s *Server) withdrawalLink(ctx context.Context, subj *withdrawSubject, suffix string) string {
	if subj == nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, withdrawLinkWait)
	defer cancel()
	if !s.withdrawalOffered(ctx, subj) {
		return ""
	}
	return subj.base + "/withdraw" + suffix
}

// maskEmail shows enough of an address for its owner to recognise it and no more: the first letter, then the domain.
func maskEmail(e string) string {
	at := strings.LastIndex(e, "@")
	if at < 1 {
		return ""
	}
	return e[:1] + "***" + e[at:]
}

func (s *Server) withdrawBase(subj *withdrawSubject, lang string) pageBase {
	b := newBase(lang)
	b.Test, b.SideSeller, b.Logo = subj.test, subj.seller, subj.logo
	b.SideNote = b.L["withdraw_title"]
	return b
}

// withdrawLoad is the start of all three handlers: rate limit, the language, the person, and whether the function is offered. When it answers, it returns false.
func (s *Server) withdrawLoad(w http.ResponseWriter, r *http.Request, portal, post bool) (*withdrawSubject, string, bool) {
	lang := pickLang(r)
	allowed := s.hostedAllowed
	if post {
		allowed = s.hostedPayAllowed
	}
	if !allowed(w, r) {
		return nil, lang, false
	}
	subj, ok, err := s.withdrawSubjectOf(r.Context(), r.PathValue("token"), portal)
	switch {
	case goneError(err) || (err == nil && !ok):
		hostedNotFound(w, lang)
		return nil, lang, false
	case err != nil:
		hostedTrouble(w, lang)
		return nil, lang, false
	}
	lang = customerLang(r, subj.locale)
	if !s.withdrawalOffered(r.Context(), subj) {
		l := hostedText(lang)
		text := l["withdraw_unavailable"]
		if len(subj.invoices) == 0 {
			text = l["withdraw_none"]
		}
		s.message(w, r, http.StatusNotFound, lang, l["withdraw_title"], text, subj.base+langSuffix(r))
		return nil, lang, false
	}
	return subj, lang, true
}

func (s *Server) withdrawPage(portal bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		subj, lang, ok := s.withdrawLoad(w, r, portal, false)
		if !ok {
			return
		}
		s.withdrawForm(w, r, http.StatusOK, subj, lang, url.Values{}, "")
	}
}

func (s *Server) withdrawForm(w http.ResponseWriter, r *http.Request, code int, subj *withdrawSubject, lang string, form url.Values, problem string) {
	v := &withdrawView{pageBase: s.withdrawBase(subj, lang), Action: subj.base + "/withdraw" + langSuffix(r), Back: subj.base + langSuffix(r), Invoices: subj.choices(newBase(lang)),
		Name: cmp.Or(form.Get("name"), subj.name), Words: form.Get("words"), Contract: cmp.Or(form.Get("contract"), subj.invoices[0].ID), Error: problem}
	v.Email = withdrawEmailLine(v.L, subj.email)
	s.render(w, r, code, "withdraw", v)
}

func withdrawEmailLine(l map[string]string, email string) string {
	if email == "" {
		return l["withdraw_no_email"]
	}
	return l["withdraw_email"] + " " + maskEmail(email)
}

// chosen reads the form: the name, the contract (one of the paid invoices of the person, never an id the form made up) and their own words.
func (subj *withdrawSubject) chosen(form url.Values, b pageBase) (name string, inv withdrawChoice, words string, ok bool) {
	name, words = strings.TrimSpace(form.Get("name")), strings.TrimSpace(form.Get("words"))
	if len([]rune(name)) > 200 {
		name = string([]rune(name)[:200])
	}
	if len([]rune(words)) > 1000 {
		words = string([]rune(words)[:1000])
	}
	for _, i := range subj.invoices {
		if i.ID == form.Get("contract") {
			inv = i.choice(b)
		}
	}
	return name, inv, words, name != "" && inv.ID != ""
}

func (s *Server) withdrawReview(portal bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		subj, lang, ok := s.withdrawLoad(w, r, portal, true)
		if !ok {
			return
		}
		_ = r.ParseForm()
		name, inv, words, valid := subj.chosen(r.PostForm, newBase(lang))
		if !valid {
			s.withdrawForm(w, r, http.StatusUnprocessableEntity, subj, lang, r.PostForm, hostedText(lang)["withdraw_missing"])
			return
		}
		v := &withdrawView{pageBase: s.withdrawBase(subj, lang), Action: subj.base + "/withdraw/confirm" + langSuffix(r), Back: subj.base + "/withdraw" + langSuffix(r), Name: name, Words: words,
			Contract: inv.ID, ContractLabel: inv.Label, Review: true}
		v.Email = withdrawEmailLine(v.L, subj.email)
		s.render(w, r, http.StatusOK, "withdraw", v)
	}
}

// maxStatement is what plans-svc keeps of a statement, in bytes (a character of Arabic or Turkish takes two).
const maxStatement = 2000

// statementOf is what is recorded as the customer's statement and mailed back as the acknowledgement: what they confirmed, in the words of the page they confirmed it on. Their
// own words are cut to what is left of the limit (at a character, never inside one) after the rest, so that a long statement in a language of wide characters is kept as far
// as it goes and never refused.
func statementOf(l map[string]string, name, contract, email, words string) string {
	lines := []string{l["withdraw_name"] + ": " + name, l["withdraw_contract"] + ": " + contract}
	if email != "" {
		lines = append(lines, l["withdraw_email_label"]+": "+email)
	}
	head := strings.Join(lines, "\n")
	if words == "" {
		return head
	}
	label := "\n" + l["withdraw_words"] + ": "
	room := maxStatement - len(head) - len(label)
	if room <= 0 {
		return head
	}
	for len(words) > room {
		_, size := utf8.DecodeLastRuneInString(words)
		words = words[:len(words)-size]
	}
	return head + label + words
}

func (s *Server) withdrawConfirm(portal bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		subj, lang, ok := s.withdrawLoad(w, r, portal, true)
		if !ok {
			return
		}
		l := hostedText(lang)
		_ = r.ParseForm()
		name, inv, words, valid := subj.chosen(r.PostForm, newBase(lang))
		if !valid {
			s.withdrawForm(w, r, http.StatusUnprocessableEntity, subj, lang, r.PostForm, l["withdraw_missing"])
			return
		}
		tid, _ := uuid.Parse(subj.tenantID)
		octx := tenant.OutgoingFor(r.Context(), tid)
		back := subj.base + langSuffix(r)
		// The same invoice is withdrawn from once: a second press of the button, or a second tab, finds the first withdrawal and says so.
		if list, err := s.d.Contracts.ListContractWithdrawals(octx, &plansv1.ListContractWithdrawalsRequest{CustomerExternalId: subj.externalID}); err == nil {
			for _, wd := range list.GetWithdrawals() {
				if wd.GetInvoiceId() == inv.ID && wd.GetStatus() != "rejected" {
					s.message(w, r, http.StatusOK, lang, l["withdraw_done"], l["withdraw_again"], back)
					return
				}
			}
		} else {
			s.d.Log.WarnContext(r.Context(), "withdrawals unavailable, no withdrawal recorded", "error", err)
			s.message(w, r, http.StatusServiceUnavailable, lang, l["withdraw_title"], l["pay_unavailable"], back)
			return
		}
		resp, err := s.d.Contracts.CreateContractWithdrawal(octx, &plansv1.CreateContractWithdrawalRequest{CustomerId: subj.customerID, InvoiceId: inv.ID, Source: "hosted",
			Statement: statementOf(l, name, inv.Label, subj.email, words)})
		if err != nil {
			code := http.StatusServiceUnavailable
			if status.Code(err) == codes.InvalidArgument {
				code = http.StatusUnprocessableEntity
			}
			s.d.Log.WarnContext(r.Context(), "withdrawal not recorded", "error", err)
			s.message(w, r, code, lang, l["withdraw_title"], l["pay_unavailable"], back)
			return
		}
		text := fmt.Sprintf(l["withdraw_done_text"], localeDateTime(lang, resp.GetWithdrawal().GetReceiveTime().AsTime()))
		if subj.email != "" {
			text += " " + l["withdraw_done_mail"]
		}
		s.message(w, r, http.StatusOK, lang, l["withdraw_done"], text, back)
	}
}

// localeDateTime writes a moment for a person in the page's language, in UTC (the pages know no time zone of the visitor), with the zone said.
func localeDateTime(lang string, t time.Time) string {
	b := newBase(lang)
	return b.F.Date(t.UTC()) + ", " + t.UTC().Format("15:04") + " UTC"
}
