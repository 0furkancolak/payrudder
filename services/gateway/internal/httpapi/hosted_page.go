package httpapi

import (
	"bytes"
	"cmp"
	"context"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
	"github.com/0furkancolak/payrudder/pkg/brand"
	"github.com/0furkancolak/payrudder/pkg/env"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/localefmt"
	"github.com/0furkancolak/payrudder/pkg/money"
	"github.com/0furkancolak/payrudder/services/gateway/internal/ratelimit"
)

// The public pages of payment links (/i/...) and of the customer portal (/c/...). Both show an invoice the same way; a scope says which credential opens it.
// Paying never marks anything paid here: the page asks billing-svc to take a payment on (mock) or payments-worker to start one at a provider, and shows what billing
// and the ledger say afterwards.

func (s *Server) hostedPublicRoutes() []Route {
	return []Route{
		{"GET", "/i/{token}", AccessPublic, ratelimit.Class(""), s.hostedInvoicePage},
		{"GET", "/i/{token}/pdf", AccessPublic, "", s.hostedInvoicePDF},
		{"GET", "/i/{token}/receipt", AccessPublic, "", s.hostedInvoiceReceipt},
		{"GET", "/i/{token}/logo", AccessPublic, "", s.hostedInvoiceLogo},
		{"POST", "/i/{token}/start", AccessPublic, "", s.hostedLinkStart},
		{"POST", "/i/{token}/promo", AccessPublic, "", s.hostedLinkPromo},
		{"POST", "/i/{token}/pay", AccessPublic, "", s.hostedInvoicePay},
		{"POST", "/i/{token}/return/{key}", AccessPublic, "", s.hostedInvoiceReturn},
		{"GET", "/c/{token}", AccessPublic, "", s.portalPage},
		{"GET", "/c/{token}/logo", AccessPublic, "", s.portalLogo},
		{"POST", "/c/{token}/credits/buy", AccessPublic, "", s.portalCreditsBuy},
		{"GET", "/c/{token}/invoices/{invoice_id}", AccessPublic, "", s.portalInvoicePage},
		{"GET", "/c/{token}/invoices/{invoice_id}/pdf", AccessPublic, "", s.portalInvoicePDF},
		{"GET", "/c/{token}/invoices/{invoice_id}/receipt", AccessPublic, "", s.portalInvoiceReceipt},
		{"POST", "/c/{token}/invoices/{invoice_id}/pay", AccessPublic, "", s.portalInvoicePay},
		{"POST", "/c/{token}/invoices/{invoice_id}/return/{key}", AccessPublic, "", s.portalInvoiceReturn},
	}
}

// scope is what opens an invoice: a payment link's token, or a portal token together with the invoice of the portal that is wanted.
type scope struct {
	token, portal, invoice string
}

func (sc scope) page() string {
	if sc.portal != "" {
		return "/c/" + sc.portal + "/invoices/" + sc.invoice
	}
	return "/i/" + sc.token
}

func (sc scope) logo() string {
	if sc.portal != "" {
		return "/c/" + sc.portal + "/logo"
	}
	return "/i/" + sc.token + "/logo"
}

func (sc scope) back() string {
	if sc.portal != "" {
		return "/c/" + sc.portal
	}
	return ""
}

func (sc scope) get() *billingv1.GetHostedInvoiceRequest {
	return &billingv1.GetHostedInvoiceRequest{Token: sc.token, PortalToken: sc.portal, InvoiceId: sc.invoice}
}

// goneError is every way a visitor's credential fails: it is the same 404 page for all of them.
func goneError(err error) bool {
	switch status.Code(err) {
	case codes.NotFound, codes.InvalidArgument, codes.PermissionDenied:
		return true
	}
	return false
}

// langSuffix keeps an explicit ?lang= on the links of a page.
func langSuffix(r *http.Request) string {
	if v := r.URL.Query().Get("lang"); slices.Contains(hostedLangs, v) {
		return "?lang=" + v
	}
	return ""
}

// withWait marks a page address "wait for the state to settle": a page in "processing" holds the request on the server (up to startWait) instead of answering at once.
// The pages have no script and no meta refresh, so this is how the payer lands on "Paid" after paying and "Check again" does not need a second click.
func withWait(u string) string {
	if strings.Contains(u, "?") {
		return u + "&wait=1"
	}
	return u + "?wait=1"
}

func waitRequested(r *http.Request) bool { return r.URL.Query().Get("wait") == "1" }

func htmlPage(w http.ResponseWriter, code int, logo bool, forms string) {
	hostedHeadersCSP(w, pageCSP(logo, forms))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
}

// render writes a page. The template runs into a buffer first, so a template error is a 500 and not half a page.
func (s *Server) render(w http.ResponseWriter, r *http.Request, code int, name string, data any) {
	var buf bytes.Buffer
	if err := pageTemplates.ExecuteTemplate(&buf, name, data); err != nil {
		s.d.Log.ErrorContext(r.Context(), "hosted page not rendered", "page", name, "error", err)
		hostedHeaders(w)
		http.Error(w, "the page cannot be shown right now", http.StatusInternalServerError)
		return
	}
	p, _ := data.(interface {
		hasLogo() bool
		formTargets() string
	})
	if p == nil {
		p = pageBase{}
	}
	htmlPage(w, code, p.hasLogo(), p.formTargets())
	_, _ = w.Write(buf.Bytes())
}

// message is a small page for a refusal or a notice.
func (s *Server) message(w http.ResponseWriter, r *http.Request, code int, lang, title, text, back string) {
	s.render(w, r, code, "message", &messageView{pageBase: newBase(lang), Heading: title, Text: text, Back: back})
}

type messageView struct {
	pageBase
	Heading, Text, Back string
}

func (v *messageView) Title() string { return v.Heading }

type hostedLine struct {
	Description, Period, Amount string
	Negative                    bool
}

type methodView struct{ ID, Label string }

// pageBase is what every page needs.
type pageBase struct {
	L                map[string]string
	Lang, Dir, Brand string
	// F writes the numbers and dates of the page in its language; Zone is the calendar its days are of (the seller's time zone, UTC when the invoice froze none).
	F    *localefmt.Format
	Zone *time.Location
	Logo string
	Test bool
	// What the coloured side of the page says: who the page is from, the amount it is about and a line under it (the state of the invoice, the customer).
	SideSeller, SideAmount, SideNote string
}

func newBase(lang string) pageBase {
	return pageBase{L: hostedText(lang), Lang: lang, Dir: hostedDir(lang), Brand: brand.Name(), F: localefmt.For(lang), Zone: time.UTC}
}

// zoneOf loads an IANA time zone; a name that is empty or does not load is UTC, which is how every page was dated.
func zoneOf(name string) *time.Location {
	if loc, err := time.LoadLocation(name); err == nil {
		return loc
	}
	return time.UTC
}

// Day writes a day in words, as the calendar of the page's zone has it: the same moment is the first of January in Istanbul while it is still the 31st of December in UTC.
func (b pageBase) Day(t time.Time) string { return b.F.Date(t.In(b.Zone)) }

// hostedBank is where to send the money for an invoice paid by bank transfer.
type hostedBank struct{ Name, Holder, IBAN, BIC, Reference string }

// groupIBAN writes an IBAN in blocks of four, the way it is printed on bank documents.
func groupIBAN(iban string) string {
	var b strings.Builder
	for i, c := range iban {
		if i > 0 && i%4 == 0 {
			b.WriteByte(' ')
		}
		b.WriteRune(c)
	}
	return b.String()
}

// invoiceHeader is what the invoice page and the receipt both say about the invoice: who it is from and to, its number, date, currency, total and lines.
type invoiceHeader struct {
	Seller, Customer, Number, Date, Currency, Total string
	SellerAddress, CustomerAddress                  []string
	Lines                                           []hostedLine
}

// newInvoiceHeader reads the header from billing's answer. The lines carry their service period when withPeriod is set (the invoice page shows it, the
// receipt does not).
func newInvoiceHeader(resp *billingv1.GetHostedInvoiceResponse, b pageBase, withPeriod bool) invoiceHeader {
	f := b.F
	inv := resp.GetInvoice()
	exp := int(resp.GetCurrencyExponent())
	h := invoiceHeader{Seller: resp.GetSellerName(), Customer: resp.GetCustomerName(), Number: inv.GetNumber(), Currency: inv.GetCurrency(),
		Total:         f.Amount(inv.GetTotalMinor(), exp),
		SellerAddress: resp.GetSellerAddressLines(), CustomerAddress: resp.GetCustomerAddressLines()}
	if t := inv.GetFinalizeTime(); t != nil {
		h.Date = b.Day(t.AsTime())
	}
	for _, l := range inv.GetLines() {
		line := hostedLine{Description: l.GetDescription(), Amount: f.Amount(l.GetAmountMinor(), exp), Negative: l.GetAmountMinor() < 0}
		if from, to := l.GetServiceFrom(), l.GetServiceTo(); withPeriod && from != nil && to != nil {
			line.Period = b.Day(from.AsTime()) + " – " + b.Day(to.AsTime())
		}
		h.Lines = append(h.Lines, line)
	}
	return h
}

// hostedBase is the page base of a page about an invoice of the scope: language, brand, the seller's logo when it has one, and the test-mode banner.
func hostedBase(resp *billingv1.GetHostedInvoiceResponse, sc scope, lang string) pageBase {
	b := newBase(lang)
	b.Zone = zoneOf(resp.GetTimeZone())
	b.Test = resp.GetEnvironment() == env.Sandbox.String()
	b.SideSeller = resp.GetSellerName()
	if resp.GetLogoAvailable() {
		b.Logo = sc.logo()
	}
	return b
}

type invoiceView struct {
	pageBase
	invoiceHeader
	Status, State, StateLabel, Subtotal string
	HasPDF                              bool
	PDF, Receipt, Back, Pay, Self       string
	Payable, NoMethod, CheckAgain       bool
	Bank                                *hostedBank
	Methods                             []methodView
	Hint                                string
	// Contracts is what the seller asks the customer to accept before the payment starts; nil when nothing is.
	Contracts *contractView
	// Withdraw is the link to the withdrawal function (Directive 2011/83/EU art. 11a) on the page of a paid invoice; empty when it is not offered.
	Withdraw string
}

func (v *invoiceView) Title() string { return v.L["invoice"] + " " + v.Number }

// paymentStateWord is the state of an invoice as a person reads it: what the payment state says, else paid or void, else open.
func paymentStateWord(paymentState, invoiceStatus string) string {
	if paymentState != "" {
		return paymentState
	}
	switch invoiceStatus {
	case "paid":
		return "paid"
	case "void":
		return "void"
	}
	return "open"
}

func stateOf(resp *billingv1.GetHostedInvoiceResponse) string {
	return paymentStateWord(resp.GetPaymentState(), resp.GetInvoice().GetStatus())
}

// hostedInvoicePage is GET /i/{token}: the invoice (or the ad-hoc request) as a page, no sign-in.
func (s *Server) hostedInvoicePage(w http.ResponseWriter, r *http.Request) {
	s.invoiceOrAdhoc(w, r, scope{token: r.PathValue("token")})
}

func (s *Server) portalInvoicePage(w http.ResponseWriter, r *http.Request) {
	s.invoiceOrAdhoc(w, r, scope{portal: r.PathValue("token"), invoice: r.PathValue("invoice_id")})
}

func (s *Server) invoiceOrAdhoc(w http.ResponseWriter, r *http.Request, sc scope) {
	lang := pickLang(r)
	if !s.hostedAllowed(w, r) {
		return
	}
	s.captureRef(w, r)
	resp, err := s.d.Hosted.GetHostedInvoice(r.Context(), sc.get())
	if goneError(err) {
		hostedNotFound(w, lang)
		return
	}
	if err != nil {
		hostedTrouble(w, lang)
		return
	}
	lang = customerLang(r, resp.GetCustomerLocale())
	if resp.GetInvoice() == nil && resp.GetAdhoc() != nil {
		s.adhocPage(w, r, sc, lang, resp)
		return
	}
	if resp.GetInvoice() == nil {
		hostedNotFound(w, lang)
		return
	}
	v := s.invoiceView(r.Context(), r, sc, lang, resp)
	if waitRequested(r) && v.State == "processing" {
		v = s.waitForSettled(r, sc, lang, v)
	}
	s.render(w, r, http.StatusOK, "invoice", v)
}

// waitForSettled looks at an invoice that is "processing" again every startPoll, for at most startWait, and returns the view once it is not (or the last view).
func (s *Server) waitForSettled(r *http.Request, sc scope, lang string, v *invoiceView) *invoiceView {
	ctx, cancel := context.WithTimeout(r.Context(), startWait)
	defer cancel()
	for v.State == "processing" {
		select {
		case <-ctx.Done():
			return v
		case <-time.After(startPoll):
		}
		resp, err := s.d.Hosted.GetHostedInvoice(ctx, sc.get())
		if err != nil || resp.GetInvoice() == nil {
			return v
		}
		v = s.invoiceView(ctx, r, sc, lang, resp)
	}
	return v
}

// Again is the address of the "Check again" link: the same page, held on the server while the payment is still processing.
func (v *invoiceView) Again() string { return withWait(v.Self) }

func (s *Server) invoiceView(ctx context.Context, r *http.Request, sc scope, lang string, resp *billingv1.GetHostedInvoiceResponse) *invoiceView {
	inv := resp.GetInvoice()
	q := langSuffix(r)
	base := hostedBase(resp, sc, lang)
	v := &invoiceView{
		pageBase: base, invoiceHeader: newInvoiceHeader(resp, base, true), Status: inv.GetStatus(),
		Subtotal: base.F.Amount(inv.GetSubtotalMinor(), int(resp.GetCurrencyExponent())), HasPDF: resp.GetPdfAvailable(),
		PDF: sc.page() + "/pdf", Pay: sc.page() + "/pay" + q, Self: sc.page() + q, Back: sc.back(), State: stateOf(resp),
	}
	if v.Back != "" {
		v.Back += q
	}
	if in := resp.GetPaymentInstructions(); in.GetIban() != "" {
		v.Bank = &hostedBank{Name: in.GetBankName(), Holder: in.GetAccountHolder(), IBAN: groupIBAN(in.GetIban()), BIC: in.GetBic(), Reference: in.GetReference()}
	}
	rows := s.ledgerRowsOf(ctx, resp)
	v.State = refineState(v.State, rows, inv.GetId(), s.now())
	v.StateLabel = v.L["st_"+v.State]
	v.SideAmount, v.SideNote = v.Total+" "+v.Currency, v.StateLabel
	switch v.State {
	case "processing":
		v.CheckAgain, v.Hint = true, v.L["hint_processing"]
	case "failed":
		// An invoice the provider could not collect and the seller wrote off has no way to pay; saying "you can try again" with no button would be a lie.
		if inv.GetStatus() == "uncollectible" {
			v.Hint = v.L["hint_uncollectible"]
		} else {
			v.Hint = strings.TrimSpace(declineText(v.L, lastDecline(rows, inv.GetId())) + " " + v.L["hint_failed"])
		}
	case "void":
		v.Hint = v.L["hint_void"]
	case "paid":
		v.Receipt = sc.page() + "/receipt" + q
		if subj, ok := subjectOfInvoice(resp, sc.token); ok && sc.portal == "" {
			v.Withdraw = s.withdrawalLink(ctx, subj, q)
		}
	}
	if v.State == "open" || v.State == "failed" {
		v.Payable = resp.GetPayable()
	}
	if v.Payable {
		v.Methods = s.methodViews(ctx, v.L, resp)
		v.NoMethod = len(v.Methods) == 0
		// The seller's agreements are asked for next to the pay button. When they cannot be shown, nothing can be paid: a payment never starts without them.
		var unavailable bool
		if v.Contracts, unavailable = s.contractsView(r, resp, lang, v.L); unavailable {
			v.Payable, v.Hint = false, v.L["contract_unavailable"]
		}
	}
	return v
}

// methodViews lists the ways to pay: in a sandbox or demo the mock, else what payments-worker offers for the invoice.
func (s *Server) methodViews(ctx context.Context, l map[string]string, resp *billingv1.GetHostedInvoiceResponse) []methodView {
	if resp.GetEnvironment() == env.Sandbox.String() || s.d.Demo {
		return []methodView{{ID: "mock", Label: l["pay_mock"]}}
	}
	p := resp.GetPayment()
	methods, err := s.liveMethods(ctx, p.GetTenantId(), p.GetCurrency(), p.GetCountry(), p.GetProvider(), p.GetCustomerExternalId())
	if err != nil {
		s.d.Log.WarnContext(ctx, "payment methods unavailable", "error", err)
		return nil
	}
	var out []methodView
	for _, m := range methods {
		label := l["pay_checkout"]
		switch m.Kind {
		case "instructions":
			label = l["pay_instructions"]
		case "stored_card":
			label = l["pay_stored_card"]
		}
		out = append(out, methodView{ID: m.ID, Label: label})
	}
	return out
}

type adhocView struct {
	pageBase
	Seller, Description string
	Amount, Currency    string
	Consumed            bool
	Start               string
	// Pack is set for a credit-pack link: what the payer gets.
	Pack *packView
	// PromoAction is where the promotion code field posts to. Code is what was typed, Quote what billing says the code takes off, PromoError why it was refused.
	PromoAction string
	Code        string
	Quote       *quoteView
	PromoError  string
}

// quoteView is what a promotion code takes off the purchase, as the payer reads it.
type quoteView struct {
	Discount, Total, Bonus string
	HasDiscount            bool
}

// promoInput is what a page is shown with after a code was typed or the link carries one.
type promoInput struct {
	Code   string
	Quote  *billingv1.QuoteLinkPromotionResponse
	ErrKey string
	Status int
}

// packView is what a credit-pack link sells, as the payer reads it.
type packView struct {
	Name, Credits, Valid string
	Quantity             int32
}

func (v *adhocView) Title() string { return v.Description }

func (s *Server) adhocPage(w http.ResponseWriter, r *http.Request, sc scope, lang string, resp *billingv1.GetHostedInvoiceResponse) {
	in := promoInput{Status: http.StatusOK}
	if a := resp.GetAdhoc(); a.GetPromotionCode() != "" && !a.GetConsumed() {
		// The seller chose a code for this link: the page shows what it takes off. A code that stopped working shows nothing, and the payer sees the plain price.
		if q, err := s.d.Hosted.QuoteLinkPromotion(r.Context(), &billingv1.QuoteLinkPromotionRequest{Token: sc.token}); err == nil && q.GetApplied() {
			in.Quote = q
		}
	}
	s.adhocPageWith(w, r, sc, lang, resp, in)
}

func (s *Server) adhocPageWith(w http.ResponseWriter, r *http.Request, sc scope, lang string, resp *billingv1.GetHostedInvoiceResponse, in promoInput) {
	a := resp.GetAdhoc()
	v := &adhocView{pageBase: newBase(lang), Seller: cmp.Or(a.GetSellerName(), resp.GetSellerName()), Description: a.GetDescription(),
		Amount: localefmt.For(lang).Amount(a.GetAmountMinor(), int(a.GetCurrencyExponent())), Currency: a.GetCurrency(), Consumed: a.GetConsumed(), Start: sc.page() + "/start" + langSuffix(r),
		PromoAction: sc.page() + "/promo" + langSuffix(r), Code: in.Code}
	v.Test = cmp.Or(a.GetEnvironment(), resp.GetEnvironment()) == env.Sandbox.String()
	if k := a.GetPack(); k != nil {
		v.Pack = &packView{Name: k.GetName(), Credits: v.F.Amount(k.GetCredits(), 0), Valid: validText(v.L, k.GetValidityDays()), Quantity: k.GetQuantity()}
	}
	if q := in.Quote; q != nil {
		exp := int(a.GetCurrencyExponent())
		v.Quote = &quoteView{Discount: v.F.Amount(q.GetDiscountMinor(), exp), Total: v.F.Amount(q.GetTotalMinor(), exp), HasDiscount: q.GetDiscountMinor() > 0}
		if q.GetBonusCredits() > 0 {
			v.Quote.Bonus = v.F.Amount(q.GetBonusCredits(), 0)
		}
	}
	if in.ErrKey != "" {
		v.PromoError = promoErrorText(v.L, in.ErrKey)
	}
	v.SideSeller, v.SideAmount = v.Seller, v.Amount+" "+v.Currency
	if v.Quote != nil && v.Quote.HasDiscount {
		v.SideAmount = v.Quote.Total + " " + v.Currency
	}
	if a.GetLogoAvailable() || resp.GetLogoAvailable() {
		v.Logo = sc.logo()
	}
	s.render(w, r, orStatus(in.Status), "adhoc", v)
}

// hostedLinkStart is POST /i/{token}/start: the first use of an ad-hoc link. Billing makes the invoice and an invoice link; the payer continues on that.
func (s *Server) hostedLinkStart(w http.ResponseWriter, r *http.Request) {
	lang := pickLang(r)
	if !s.hostedPayAllowed(w, r) {
		return
	}
	code := formCode(r)
	refCode, refAt := referral(r)
	resp, err := s.d.Hosted.StartLinkPayment(r.Context(), &billingv1.StartLinkPaymentRequest{Token: r.PathValue("token"), PromotionCode: code, ReferralCode: refCode, ReferralTouchTime: refAt})
	switch {
	case isPromoError(errkey.Of(err)):
		// The code did not work: no invoice was made. The payer sees why, with the code still in the field.
		s.adhocAgain(w, r, lang, promoInput{Code: code, ErrKey: errkey.Of(err), Status: http.StatusUnprocessableEntity})
	case goneError(err):
		hostedNotFound(w, lang)
	case gateRefused(err):
		// The seller has blocked this customer, or the operator has taken the seller out of service: not "already used", and not a reason to say more than that the
		// payment cannot be made.
		s.message(w, r, http.StatusConflict, lang, hostedText(lang)["adhoc_title"], hostedText(lang)["pay_blocked"], "")
	case status.Code(err) == codes.FailedPrecondition:
		s.message(w, r, http.StatusConflict, lang, hostedText(lang)["adhoc_title"], hostedText(lang)["adhoc_used"], "")
	case err != nil:
		s.message(w, r, http.StatusServiceUnavailable, lang, hostedText(lang)["adhoc_title"], hostedText(lang)["pay_unavailable"], "")
	default:
		s.waitUntilPayable(r.Context(), resp.GetInvoiceToken())
		hostedHeaders(w)
		http.Redirect(w, r, "/i/"+resp.GetInvoiceToken()+langSuffix(r), http.StatusSeeOther)
	}
}

// startWait and startPoll bound how long Continue waits for the new invoice to leave "processing" (its saga makes the PDF and opens it, usually within seconds).
// The pages carry no script and no meta refresh (a refresh fails WCAG 2.2.1), so waiting here is what lets a payer land on the pay buttons directly; after the
// wait the page shows "processing" with its "Check again" link.
var startWait, startPoll = 20 * time.Second, 500 * time.Millisecond

// waitUntilPayable polls the invoice of a just-started ad-hoc link until it is no longer "processing", the request ends, or startWait has passed.
func (s *Server) waitUntilPayable(ctx context.Context, token string) {
	ctx, cancel := context.WithTimeout(ctx, startWait)
	defer cancel()
	for {
		resp, err := s.d.Hosted.GetHostedInvoice(ctx, scope{token: token}.get())
		if err != nil || stateOf(resp) != "processing" {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(startPoll):
		}
	}
}

// gateRefused says whether billing's payment gate refused the payment: the customer is blocked, or the seller is on hold. The page says only that the payment
// cannot be made.
func gateRefused(err error) bool {
	k := errkey.Of(err)
	return k == "billing.customer.blocked" || k == "billing.tenant.on_hold"
}

// formCode is the promotion code a payer typed (one line, at most 40 characters), or "".
func formCode(r *http.Request) string {
	if err := r.ParseForm(); err != nil {
		return ""
	}
	c := strings.TrimSpace(r.PostForm.Get("code"))
	if len(c) > 40 {
		return c[:40]
	}
	return c
}

func orStatus(code int) int {
	if code == 0 {
		return http.StatusOK
	}
	return code
}

// adhocAgain shows the page of an ad-hoc link again, after a code was typed.
func (s *Server) adhocAgain(w http.ResponseWriter, r *http.Request, lang string, in promoInput) {
	sc := scope{token: r.PathValue("token")}
	resp, err := s.d.Hosted.GetHostedInvoice(r.Context(), sc.get())
	switch {
	case goneError(err):
		hostedNotFound(w, lang)
	case err != nil:
		s.message(w, r, http.StatusServiceUnavailable, lang, hostedText(lang)["adhoc_title"], hostedText(lang)["pay_unavailable"], "")
	case resp.GetAdhoc() == nil || resp.GetInvoice() != nil:
		hostedHeaders(w)
		http.Redirect(w, r, sc.page()+langSuffix(r), http.StatusSeeOther)
	default:
		s.adhocPageWith(w, r, sc, lang, resp, in)
	}
}

// hostedLinkPromo is POST /i/{token}/promo: the payer typed a promotion code. Billing says what it would take off, or why it cannot be used; nothing is claimed until
// the payer presses Continue.
func (s *Server) hostedLinkPromo(w http.ResponseWriter, r *http.Request) {
	lang := pickLang(r)
	if !s.hostedPayAllowed(w, r) {
		return
	}
	code := formCode(r)
	if code == "" {
		s.adhocAgain(w, r, lang, promoInput{Status: http.StatusOK})
		return
	}
	q, err := s.d.Hosted.QuoteLinkPromotion(r.Context(), &billingv1.QuoteLinkPromotionRequest{Token: r.PathValue("token"), PromotionCode: code})
	switch {
	case err == nil && q.GetApplied():
		s.adhocAgain(w, r, lang, promoInput{Code: code, Quote: q, Status: http.StatusOK})
	case err == nil:
		s.adhocAgain(w, r, lang, promoInput{Code: code, ErrKey: "coupon.not_applicable", Status: http.StatusUnprocessableEntity})
	case isPromoError(errkey.Of(err)):
		s.adhocAgain(w, r, lang, promoInput{Code: code, ErrKey: errkey.Of(err), Status: http.StatusUnprocessableEntity})
	case goneError(err):
		hostedNotFound(w, lang)
	default:
		s.message(w, r, http.StatusServiceUnavailable, lang, hostedText(lang)["adhoc_title"], hostedText(lang)["pay_unavailable"], "")
	}
}

func (s *Server) hostedInvoicePDF(w http.ResponseWriter, r *http.Request) {
	s.invoicePDF(w, r, scope{token: r.PathValue("token")})
}

func (s *Server) portalInvoicePDF(w http.ResponseWriter, r *http.Request) {
	s.invoicePDF(w, r, scope{portal: r.PathValue("token"), invoice: r.PathValue("invoice_id")})
}

// invoicePDF redirects to a short-lived download link.
func (s *Server) invoicePDF(w http.ResponseWriter, r *http.Request, sc scope) {
	lang := pickLang(r)
	if !s.hostedAllowed(w, r) {
		return
	}
	resp, err := s.d.Hosted.GetHostedInvoicePdfUrl(r.Context(), &billingv1.GetHostedInvoicePdfUrlRequest{Token: sc.token, PortalToken: sc.portal, InvoiceId: sc.invoice})
	switch {
	case err == nil:
	case goneError(err):
		hostedNotFound(w, lang)
		return
	case status.Code(err) == codes.FailedPrecondition:
		hostedHeaders(w)
		http.Error(w, "the PDF is not ready yet; try again in a minute", http.StatusConflict)
		return
	default:
		hostedHeaders(w)
		http.Error(w, "the PDF cannot be fetched right now", http.StatusServiceUnavailable)
		return
	}
	hostedHeaders(w)
	http.Redirect(w, r, resp.GetUrl(), http.StatusFound)
}

func (s *Server) hostedInvoiceLogo(w http.ResponseWriter, r *http.Request) {
	s.logo(w, r, &billingv1.GetHostedLogoUrlRequest{Token: r.PathValue("token")})
}

func (s *Server) portalLogo(w http.ResponseWriter, r *http.Request) {
	s.logo(w, r, &billingv1.GetHostedLogoUrlRequest{PortalToken: r.PathValue("token")})
}

const maxLogoBytes = 1 << 20

var logoHTTP = &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

// logo serves the seller's logo from a presigned link the gateway fetches itself, so the page can load images from its own origin only. Only a PNG or a JPEG
// of at most 1 MiB is passed on.
func (s *Server) logo(w http.ResponseWriter, r *http.Request, req *billingv1.GetHostedLogoUrlRequest) {
	if !s.hostedAllowed(w, r) {
		return
	}
	resp, err := s.d.Hosted.GetHostedLogoUrl(r.Context(), req)
	if err != nil || resp.GetUrl() == "" {
		http.NotFound(w, r)
		return
	}
	u, err := url.Parse(resp.GetUrl())
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		http.NotFound(w, r)
		return
	}
	get, err := http.NewRequestWithContext(r.Context(), http.MethodGet, u.String(), nil)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	up, err := logoHTTP.Do(get)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer func() { _ = up.Body.Close() }()
	if up.StatusCode != http.StatusOK {
		http.NotFound(w, r)
		return
	}
	body, err := io.ReadAll(io.LimitReader(up.Body, maxLogoBytes+1))
	if err != nil || len(body) > maxLogoBytes {
		http.NotFound(w, r)
		return
	}
	// The bytes decide the type, not what the storage claims.
	ct := http.DetectContentType(body)
	if ct != "image/png" && ct != "image/jpeg" {
		http.NotFound(w, r)
		return
	}
	h := w.Header()
	h.Set("Content-Type", ct)
	h.Set("Cache-Control", "private, max-age=300")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'")
	h.Set("X-Robots-Tag", "noindex, nofollow")
	h.Set("Referrer-Policy", "no-referrer")
	_, _ = w.Write(body)
}

type receiptView struct {
	pageBase
	invoiceHeader
	PaidOn, PaidWith string
	Back             string
}

func (v *receiptView) Title() string { return v.L["receipt"] + " " + v.Number }

func (s *Server) hostedInvoiceReceipt(w http.ResponseWriter, r *http.Request) {
	s.receipt(w, r, scope{token: r.PathValue("token")})
}

func (s *Server) portalInvoiceReceipt(w http.ResponseWriter, r *http.Request) {
	s.receipt(w, r, scope{portal: r.PathValue("token"), invoice: r.PathValue("invoice_id")})
}

// receipt is a printable page for a paid invoice. Anything that is not paid is the not-found page.
func (s *Server) receipt(w http.ResponseWriter, r *http.Request, sc scope) {
	lang := pickLang(r)
	if !s.hostedAllowed(w, r) {
		return
	}
	resp, err := s.d.Hosted.GetHostedInvoice(r.Context(), sc.get())
	if goneError(err) || (err == nil && (resp.GetInvoice() == nil || stateOf(resp) != "paid")) {
		hostedNotFound(w, lang)
		return
	}
	if err != nil {
		hostedTrouble(w, lang)
		return
	}
	inv := resp.GetInvoice()
	lang = customerLang(r, resp.GetCustomerLocale())
	base := hostedBase(resp, sc, lang)
	v := &receiptView{pageBase: base, invoiceHeader: newInvoiceHeader(resp, base, false), Back: sc.page() + langSuffix(r)}
	v.SideAmount = v.Total + " " + v.Currency
	for _, row := range s.ledgerRowsOf(r.Context(), resp) {
		if row.InvoiceID == inv.GetId() && row.Kind == "charge" && row.Status == "succeeded" {
			v.PaidOn, v.PaidWith = v.Day(row.OccurredAt), row.Provider
		}
	}
	s.render(w, r, http.StatusOK, "receipt", v)
}

func (s *Server) hostedInvoicePay(w http.ResponseWriter, r *http.Request) {
	s.invoicePay(w, r, scope{token: r.PathValue("token")})
}

func (s *Server) portalInvoicePay(w http.ResponseWriter, r *http.Request) {
	s.invoicePay(w, r, scope{portal: r.PathValue("token"), invoice: r.PathValue("invoice_id")})
}

type instructionsView struct {
	pageBase
	Seller, Number, Total     string
	Currency, Reference, Back string
	Lines                     []struct{ Label, Value string }
}

func (v *instructionsView) Title() string { return v.L["instr_title"] }

// invoicePay is the POST of a pay button. Method "mock" is billing's (a sandbox or demo payment through the saga); any other method continues at payments-worker with
// the context billing returned, and ends in a redirect to the provider's page or a page with the payment instructions.
func (s *Server) invoicePay(w http.ResponseWriter, r *http.Request, sc scope) {
	lang := pickLang(r)
	if !s.hostedPayAllowed(w, r) {
		return
	}
	l := hostedText(lang)
	back := sc.page() + langSuffix(r)
	// refuse answers with a page the person can go back from: the payment cannot be made (409), or cannot be started now (503).
	refuse := func(code int, key string) { s.message(w, r, code, lang, l["pay_title"], l[key], back) }
	method := ""
	if err := r.ParseForm(); err == nil {
		method = r.PostForm.Get("method")
	}
	if method == "" || len(method) > maxMethodLen {
		refuse(http.StatusConflict, "pay_cannot")
		return
	}
	if !s.requireContracts(w, r, sc, lang) {
		return
	}
	refCode, refAt := referral(r)
	resp, err := s.d.Hosted.RequestHostedPayment(r.Context(), &billingv1.RequestHostedPaymentRequest{Token: sc.token, PortalToken: sc.portal, InvoiceId: sc.invoice, Method: method,
		ReferralCode: refCode, ReferralTouchTime: refAt})
	switch {
	case err == nil:
	case goneError(err):
		hostedNotFound(w, lang)
		return
	case gateRefused(err):
		refuse(http.StatusConflict, "pay_blocked")
		return
	case status.Code(err) == codes.FailedPrecondition:
		refuse(http.StatusConflict, "pay_cannot")
		return
	default:
		refuse(http.StatusServiceUnavailable, "pay_unavailable")
		return
	}
	if resp.GetAction() != "provider" {
		hostedHeaders(w)
		// A payment through the saga (sandbox, demo) settles in seconds: the page waits for it.
		http.Redirect(w, r, withWait(back), http.StatusSeeOther)
		return
	}
	p := resp.GetPayment()
	// The provider posts the result of a payment on its own page (the checkout form, the bank's 3-D Secure page) to this route, through the payer's browser; the route
	// asks the provider what the payment is and sends the payer back to the invoice page, {key} being the attempt payments-worker opens.
	callback := s.d.PublicURL + sc.page() + "/return/{key}" + langSuffix(r)
	out, code, err := s.startProviderPayment(r.Context(), p, method, s.d.PublicURL+back, callback, s.clientAddr(r))
	switch {
	case err != nil || code >= 500:
		if err != nil {
			s.d.Log.WarnContext(r.Context(), "payment not started", "error", err)
		}
		refuse(http.StatusServiceUnavailable, "pay_unavailable")
		return
	case code == http.StatusUnprocessableEntity:
		// The provider needs more about the customer than the seller has on file: the payer can do nothing but tell the seller.
		refuse(http.StatusUnprocessableEntity, "pay_buyer")
		return
	case code/100 != 2:
		refuse(http.StatusConflict, "pay_cannot")
		return
	}
	if out.Kind == "html" {
		threeDSPage(w, out.HTML)
		return
	}
	if out.Kind == "redirect" {
		u, perr := url.Parse(out.URL)
		if perr != nil || u.Scheme != "https" || u.Host == "" {
			refuse(http.StatusServiceUnavailable, "pay_unavailable")
			return
		}
		hostedHeaders(w)
		http.Redirect(w, r, u.String(), http.StatusSeeOther)
		return
	}
	base := newBase(lang)
	v := &instructionsView{pageBase: base, Number: p.GetNumber(), Total: base.F.Amount(p.GetAmountMinor(), exponentOf(p.GetCurrency())),
		Currency: p.GetCurrency(), Reference: out.Reference, Back: back}
	v.Test = p.GetEnvironment() == env.Sandbox.String()
	for _, ln := range out.Lines {
		v.Lines = append(v.Lines, struct{ Label, Value string }{ln.Label, ln.Value})
	}
	s.render(w, r, http.StatusOK, "instructions", v)
}

// maxMethodLen bounds the payment method a pay form names.
const maxMethodLen = 64

// startProviderPayment asks payments-worker to start paying the invoice billing prepared (p) by the method, returning to returnURL. It returns the worker's
// answer and its HTTP status; the error is a worker that could not be reached.
func (s *Server) startProviderPayment(ctx context.Context, p *billingv1.HostedPaymentContext, method, returnURL, callbackURL, payerIP string) (paymentStart, int, error) {
	var out paymentStart
	code, err := s.paymentsDo(ctx, http.MethodPost, "/admin/tenants/"+url.PathEscape(p.GetTenantId())+"/invoice-payments", nil, map[string]any{
		"invoice_id": p.GetInvoiceId(), "saga_id": p.GetSagaId(), "number": p.GetNumber(), "amount_minor": p.GetAmountMinor(), "currency": p.GetCurrency(), "method": method,
		"provider": p.GetProvider(), "country": p.GetCountry(), "customer_email": p.GetCustomerEmail(), "customer_name": p.GetCustomerName(), "description": p.GetDescription(),
		"customer_external_id": p.GetCustomerExternalId(), "payer_ip": payerIP, "return_url": returnURL, "callback_url": callbackURL, "environment": p.GetEnvironment(),
	}, &out)
	return out, code, err
}

// threeDSPage serves the page of the payer's bank that starts a 3-D Secure payment: an auto-submitting form, as the provider hands it over. It is not our page, so it is
// served as a sandboxed document with no origin of its own: its script and its form may run, and nothing it does can reach this site, its cookies or its pages.
func threeDSPage(w http.ResponseWriter, html string) {
	hostedHeadersCSP(w, "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src https: data:; form-action https:; base-uri 'none'; frame-ancestors 'none'; sandbox allow-forms allow-scripts")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, html)
}

func (s *Server) hostedInvoiceReturn(w http.ResponseWriter, r *http.Request) {
	s.invoiceReturn(w, r, scope{token: r.PathValue("token")})
}

func (s *Server) portalInvoiceReturn(w http.ResponseWriter, r *http.Request) {
	s.invoiceReturn(w, r, scope{portal: r.PathValue("token"), invoice: r.PathValue("invoice_id")})
}

// invoiceReturn is where a provider posts the result of a payment on its own page, through the payer's browser. The form is not believed: payments-worker asks the provider
// what the payment is and records it, and the payer is sent on to the invoice page, which shows the state from the ledger (paid, processing, or failed with the reason).
// Whatever happens here, the payer lands on the page: the worker being down is a page that says the payment is processing, not an error screen after a card was charged.
func (s *Server) invoiceReturn(w http.ResponseWriter, r *http.Request, sc scope) {
	if !s.hostedAllowed(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, returnFormLimit)
	_ = r.ParseForm() // the provider's fields are in the body; a provider that redirects with a GET puts them in the query
	form := r.Form
	form.Del("lang")
	var out struct {
		State string `json:"state"`
	}
	code, err := s.paymentsDo(r.Context(), http.MethodPost, "/admin/invoice-payments/return", nil, map[string]any{"key": r.PathValue("key"), "form": form}, &out)
	if err != nil || code/100 != 2 {
		s.d.Log.WarnContext(r.Context(), "a payer's return was not confirmed", "status", code, "error", err)
	}
	hostedHeaders(w)
	http.Redirect(w, r, sc.page()+langSuffix(r), http.StatusSeeOther)
}

// returnFormLimit bounds what a provider's return may post: a token, a payment id and a few fields.
const returnFormLimit = 32 << 10

// exponentOf is the number of decimal places of a currency, for the one page that is rendered from payments-worker's answer; the others use the exponent
// billing sends.
func exponentOf(currency string) int {
	if c, ok := money.LookupCurrency(strings.ToUpper(currency)); ok {
		return c.Exponent
	}
	return 2
}
