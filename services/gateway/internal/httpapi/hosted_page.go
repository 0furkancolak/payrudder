package httpapi

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
	"github.com/0furkancolak/payrudder/pkg/brand"
	"github.com/0furkancolak/payrudder/pkg/env"
	"github.com/0furkancolak/payrudder/pkg/errkey"
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
		{"GET", "/c/{token}", AccessPublic, "", s.portalPage},
		{"GET", "/c/{token}/logo", AccessPublic, "", s.portalLogo},
		{"POST", "/c/{token}/credits/buy", AccessPublic, "", s.portalCreditsBuy},
		{"GET", "/c/{token}/invoices/{invoice_id}", AccessPublic, "", s.portalInvoicePage},
		{"GET", "/c/{token}/invoices/{invoice_id}/pdf", AccessPublic, "", s.portalInvoicePDF},
		{"GET", "/c/{token}/invoices/{invoice_id}/receipt", AccessPublic, "", s.portalInvoiceReceipt},
		{"POST", "/c/{token}/invoices/{invoice_id}/pay", AccessPublic, "", s.portalInvoicePay},
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
	if v := r.URL.Query().Get("lang"); v != "" {
		for _, l := range hostedLangs {
			if v == l {
				return "?lang=" + l
			}
		}
	}
	return ""
}

func htmlPage(w http.ResponseWriter, code int, formAction string) {
	hostedHeadersCSP(w, formAction)
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
	htmlPage(w, code, "form-action 'self' https:")
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
	Logo             string
	Test, Refresh    bool
}

func newBase(lang string) pageBase {
	return pageBase{L: hostedText(lang), Lang: lang, Dir: hostedDir(lang), Brand: brand.Name()}
}

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

type invoiceView struct {
	pageBase
	Seller, Customer, Number, Status string
	State, StateLabel                string
	SellerAddress, CustomerAddress   []string
	Date, Currency, Subtotal, Total  string
	Lines                            []hostedLine
	HasPDF                           bool
	PDF, Receipt, Back, Pay          string
	Payable, NoMethod                bool
	Bank                             *hostedBank
	Methods                          []methodView
	Hint                             string
}

func (v *invoiceView) Title() string { return v.L["invoice"] + " " + v.Number }

func stateOf(resp *billingv1.GetHostedInvoiceResponse) string {
	if s := resp.GetPaymentState(); s != "" {
		return s
	}
	switch resp.GetInvoice().GetStatus() {
	case "paid":
		return "paid"
	case "void":
		return "void"
	}
	return "open"
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
		hostedHeaders(w)
		http.Error(w, "the invoice cannot be shown right now", http.StatusServiceUnavailable)
		return
	}
	if resp.GetInvoice() == nil && resp.GetAdhoc() != nil {
		s.adhocPage(w, r, sc, lang, resp)
		return
	}
	if resp.GetInvoice() == nil {
		hostedNotFound(w, lang)
		return
	}
	v := s.invoiceView(r.Context(), r, sc, lang, resp)
	s.render(w, r, http.StatusOK, "invoice", v)
}

func (s *Server) invoiceView(ctx context.Context, r *http.Request, sc scope, lang string, resp *billingv1.GetHostedInvoiceResponse) *invoiceView {
	inv := resp.GetInvoice()
	exp := int(resp.GetCurrencyExponent())
	q := langSuffix(r)
	v := &invoiceView{
		pageBase: newBase(lang), Seller: resp.GetSellerName(), Customer: resp.GetCustomerName(),
		SellerAddress: resp.GetSellerAddressLines(), CustomerAddress: resp.GetCustomerAddressLines(), Number: inv.GetNumber(), Status: inv.GetStatus(),
		Currency: inv.GetCurrency(), Subtotal: money.FormatMinorGrouped(inv.GetSubtotalMinor(), exp), Total: money.FormatMinorGrouped(inv.GetTotalMinor(), exp), HasPDF: resp.GetPdfAvailable(),
		PDF: sc.page() + "/pdf", Pay: sc.page() + "/pay" + q, Back: sc.back(), State: stateOf(resp),
	}
	v.Test = resp.GetEnvironment() == env.Sandbox.String()
	if v.Back != "" {
		v.Back += q
	}
	if resp.GetLogoAvailable() {
		v.Logo = sc.logo()
	}
	if t := inv.GetFinalizeTime(); t != nil {
		v.Date = t.AsTime().UTC().Format("2006-01-02")
	}
	for _, l := range inv.GetLines() {
		line := hostedLine{Description: l.GetDescription(), Amount: money.FormatMinorGrouped(l.GetAmountMinor(), exp), Negative: l.GetAmountMinor() < 0}
		if f, t := l.GetServiceFrom(), l.GetServiceTo(); f != nil && t != nil {
			line.Period = f.AsTime().UTC().Format("2006-01-02") + " – " + t.AsTime().UTC().Format("2006-01-02")
		}
		v.Lines = append(v.Lines, line)
	}
	if in := resp.GetPaymentInstructions(); in.GetIban() != "" {
		v.Bank = &hostedBank{Name: in.GetBankName(), Holder: in.GetAccountHolder(), IBAN: groupIBAN(in.GetIban()), BIC: in.GetBic(), Reference: in.GetReference()}
	}
	rows, _ := s.ledgerLookup(ctx, resp.GetPayment().GetTenantId(), []string{inv.GetId()})
	v.State = refineState(v.State, rows, inv.GetId(), time.Now())
	v.StateLabel = v.L["st_"+v.State]
	switch v.State {
	case "processing":
		v.Refresh, v.Hint = true, v.L["hint_processing"]
	case "failed":
		v.Hint = v.L["hint_failed"]
	case "paid":
		v.Receipt = sc.page() + "/receipt" + q
	}
	if v.State == "open" || v.State == "failed" {
		v.Payable = resp.GetPayable()
	}
	if v.Payable {
		v.Methods = s.methodViews(ctx, v.L, resp)
		v.NoMethod = len(v.Methods) == 0
	}
	return v
}

// methodViews lists the ways to pay: in a sandbox or demo the mock, else what payments-worker offers for the invoice.
func (s *Server) methodViews(ctx context.Context, l map[string]string, resp *billingv1.GetHostedInvoiceResponse) []methodView {
	if resp.GetEnvironment() == env.Sandbox.String() || s.d.Demo {
		return []methodView{{ID: "mock", Label: l["pay_mock"]}}
	}
	p := resp.GetPayment()
	methods, err := s.liveMethods(ctx, p.GetTenantId(), p.GetCurrency(), p.GetCountry(), p.GetProvider())
	if err != nil {
		s.d.Log.WarnContext(ctx, "payment methods unavailable", "error", err)
		return nil
	}
	var out []methodView
	for _, m := range methods {
		label := l["pay_checkout"]
		if m.Kind == "instructions" {
			label = l["pay_instructions"]
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
	v := &adhocView{pageBase: newBase(lang), Seller: orDefault(a.GetSellerName(), resp.GetSellerName()), Description: a.GetDescription(),
		Amount: money.FormatMinorGrouped(a.GetAmountMinor(), int(a.GetCurrencyExponent())), Currency: a.GetCurrency(), Consumed: a.GetConsumed(), Start: sc.page() + "/start" + langSuffix(r),
		PromoAction: sc.page() + "/promo" + langSuffix(r), Code: in.Code}
	v.Test = orDefault(a.GetEnvironment(), resp.GetEnvironment()) == env.Sandbox.String()
	if k := a.GetPack(); k != nil {
		v.Pack = &packView{Name: k.GetName(), Credits: money.FormatMinorGrouped(k.GetCredits(), 0), Valid: validText(v.L, k.GetValidityDays()), Quantity: k.GetQuantity()}
	}
	if q := in.Quote; q != nil {
		exp := int(a.GetCurrencyExponent())
		v.Quote = &quoteView{Discount: money.FormatMinorGrouped(q.GetDiscountMinor(), exp), Total: money.FormatMinorGrouped(q.GetTotalMinor(), exp), HasDiscount: q.GetDiscountMinor() > 0}
		if q.GetBonusCredits() > 0 {
			v.Quote.Bonus = money.FormatMinorGrouped(q.GetBonusCredits(), 0)
		}
	}
	if in.ErrKey != "" {
		v.PromoError = promoErrorText(v.L, in.ErrKey)
	}
	if a.GetLogoAvailable() || resp.GetLogoAvailable() {
		v.Logo = sc.logo()
	}
	s.render(w, r, orStatus(in.Status), "adhoc", v)
}

func orDefault(a, b string) string {
	if a != "" {
		return a
	}
	return b
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
	case status.Code(err) == codes.FailedPrecondition:
		s.message(w, r, http.StatusConflict, lang, hostedText(lang)["adhoc_title"], hostedText(lang)["adhoc_used"], "")
	case err != nil:
		s.message(w, r, http.StatusServiceUnavailable, lang, hostedText(lang)["adhoc_title"], hostedText(lang)["pay_unavailable"], "")
	default:
		hostedHeaders(w)
		http.Redirect(w, r, "/i/"+resp.GetInvoiceToken()+langSuffix(r), http.StatusSeeOther)
	}
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
	Seller, Customer, Number, Date    string
	Currency, Total, PaidOn, PaidWith string
	SellerAddress, CustomerAddress    []string
	Lines                             []hostedLine
	Back                              string
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
		hostedHeaders(w)
		http.Error(w, "the receipt cannot be shown right now", http.StatusServiceUnavailable)
		return
	}
	inv := resp.GetInvoice()
	exp := int(resp.GetCurrencyExponent())
	v := &receiptView{pageBase: newBase(lang), Seller: resp.GetSellerName(), Customer: resp.GetCustomerName(), Number: inv.GetNumber(),
		SellerAddress: resp.GetSellerAddressLines(), CustomerAddress: resp.GetCustomerAddressLines(), Currency: inv.GetCurrency(), Total: money.FormatMinorGrouped(inv.GetTotalMinor(), exp),
		Back: sc.page() + langSuffix(r)}
	v.Test = resp.GetEnvironment() == env.Sandbox.String()
	if resp.GetLogoAvailable() {
		v.Logo = sc.logo()
	}
	if t := inv.GetFinalizeTime(); t != nil {
		v.Date = t.AsTime().UTC().Format("2006-01-02")
	}
	for _, l := range inv.GetLines() {
		v.Lines = append(v.Lines, hostedLine{Description: l.GetDescription(), Amount: money.FormatMinorGrouped(l.GetAmountMinor(), exp), Negative: l.GetAmountMinor() < 0})
	}
	rows, _ := s.ledgerLookup(r.Context(), resp.GetPayment().GetTenantId(), []string{inv.GetId()})
	for _, row := range rows {
		if row.InvoiceID == inv.GetId() && row.Kind == "charge" && row.Status == "succeeded" {
			v.PaidOn, v.PaidWith = row.OccurredAt.UTC().Format("2006-01-02"), row.Provider
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
	method := ""
	if err := r.ParseForm(); err == nil {
		method = r.PostForm.Get("method")
	}
	back := sc.page() + langSuffix(r)
	if method == "" || len(method) > 64 {
		s.message(w, r, http.StatusConflict, lang, l["pay_title"], l["pay_cannot"], back)
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
	case status.Code(err) == codes.FailedPrecondition:
		s.message(w, r, http.StatusConflict, lang, l["pay_title"], l["pay_cannot"], back)
		return
	default:
		s.message(w, r, http.StatusServiceUnavailable, lang, l["pay_title"], l["pay_unavailable"], back)
		return
	}
	if resp.GetAction() != "provider" {
		hostedHeaders(w)
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	p := resp.GetPayment()
	var out paymentStart
	code, err := s.paymentsDo(r.Context(), http.MethodPost, "/admin/tenants/"+url.PathEscape(p.GetTenantId())+"/invoice-payments", nil, map[string]any{
		"invoice_id": p.GetInvoiceId(), "saga_id": p.GetSagaId(), "number": p.GetNumber(), "amount_minor": p.GetAmountMinor(), "currency": p.GetCurrency(), "method": method,
		"provider": p.GetProvider(), "country": p.GetCountry(), "customer_email": p.GetCustomerEmail(), "customer_name": p.GetCustomerName(), "description": p.GetDescription(),
		"return_url": s.d.PublicURL + back, "environment": p.GetEnvironment(),
	}, &out)
	switch {
	case err != nil || code >= 500:
		if err != nil {
			s.d.Log.WarnContext(r.Context(), "payment not started", "error", err)
		}
		s.message(w, r, http.StatusServiceUnavailable, lang, l["pay_title"], l["pay_unavailable"], back)
		return
	case code/100 != 2:
		s.message(w, r, http.StatusConflict, lang, l["pay_title"], l["pay_cannot"], back)
		return
	}
	if out.Kind == "redirect" {
		u, perr := url.Parse(out.URL)
		if perr != nil || u.Scheme != "https" || u.Host == "" {
			s.message(w, r, http.StatusServiceUnavailable, lang, l["pay_title"], l["pay_unavailable"], back)
			return
		}
		hostedHeaders(w)
		http.Redirect(w, r, u.String(), http.StatusSeeOther)
		return
	}
	v := &instructionsView{pageBase: newBase(lang), Number: p.GetNumber(), Total: money.FormatMinorGrouped(p.GetAmountMinor(), exponentOf(p.GetCurrency())),
		Currency: p.GetCurrency(), Reference: out.Reference, Back: back}
	v.Test = p.GetEnvironment() == env.Sandbox.String()
	for _, ln := range out.Lines {
		v.Lines = append(v.Lines, struct{ Label, Value string }{ln.Label, ln.Value})
	}
	s.render(w, r, http.StatusOK, "instructions", v)
}

// exponentOf is the number of decimal places of a currency, for the one page that is rendered from payments-worker's answer; the others use the exponent
// billing sends.
func exponentOf(currency string) int {
	if c, ok := money.LookupCurrency(strings.ToUpper(currency)); ok {
		return c.Exponent
	}
	return 2
}
