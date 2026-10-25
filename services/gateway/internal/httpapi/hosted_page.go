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

	billingv1 "github.com/0furkancolak/quotify/gen/go/quotify/billing/v1"
	"github.com/0furkancolak/quotify/pkg/brand"
	"github.com/0furkancolak/quotify/pkg/money"
	"github.com/0furkancolak/quotify/services/gateway/internal/ratelimit"
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
		Currency: inv.GetCurrency(), Subtotal: formatMinor(inv.GetSubtotalMinor(), exp), Total: formatMinor(inv.GetTotalMinor(), exp), HasPDF: resp.GetPdfAvailable(),
		PDF: sc.page() + "/pdf", Pay: sc.page() + "/pay" + q, Back: sc.back(), State: stateOf(resp),
	}
	v.Test = resp.GetEnvironment() == "sandbox"
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
		line := hostedLine{Description: l.GetDescription(), Amount: formatMinor(l.GetAmountMinor(), exp), Negative: l.GetAmountMinor() < 0}
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
	if resp.GetEnvironment() == "sandbox" || s.d.Demo {
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
}

// packView is what a credit-pack link sells, as the payer reads it.
type packView struct {
	Name, Credits, Valid string
	Quantity             int32
}

func (v *adhocView) Title() string { return v.Description }

func (s *Server) adhocPage(w http.ResponseWriter, r *http.Request, sc scope, lang string, resp *billingv1.GetHostedInvoiceResponse) {
	a := resp.GetAdhoc()
	v := &adhocView{pageBase: newBase(lang), Seller: orDefault(a.GetSellerName(), resp.GetSellerName()), Description: a.GetDescription(),
		Amount: formatMinor(a.GetAmountMinor(), int(a.GetCurrencyExponent())), Currency: a.GetCurrency(), Consumed: a.GetConsumed(), Start: sc.page() + "/start" + langSuffix(r)}
	v.Test = orDefault(a.GetEnvironment(), resp.GetEnvironment()) == "sandbox"
	if k := a.GetPack(); k != nil {
		v.Pack = &packView{Name: k.GetName(), Credits: formatMinor(k.GetCredits(), 0), Valid: validText(v.L, k.GetValidityDays()), Quantity: k.GetQuantity()}
	}
	if a.GetLogoAvailable() || resp.GetLogoAvailable() {
		v.Logo = sc.logo()
	}
	s.render(w, r, http.StatusOK, "adhoc", v)
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
	resp, err := s.d.Hosted.StartLinkPayment(r.Context(), &billingv1.StartLinkPaymentRequest{Token: r.PathValue("token")})
	switch {
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
		SellerAddress: resp.GetSellerAddressLines(), CustomerAddress: resp.GetCustomerAddressLines(), Currency: inv.GetCurrency(), Total: formatMinor(inv.GetTotalMinor(), exp),
		Back: sc.page() + langSuffix(r)}
	v.Test = resp.GetEnvironment() == "sandbox"
	if resp.GetLogoAvailable() {
		v.Logo = sc.logo()
	}
	if t := inv.GetFinalizeTime(); t != nil {
		v.Date = t.AsTime().UTC().Format("2006-01-02")
	}
	for _, l := range inv.GetLines() {
		v.Lines = append(v.Lines, hostedLine{Description: l.GetDescription(), Amount: formatMinor(l.GetAmountMinor(), exp), Negative: l.GetAmountMinor() < 0})
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
	resp, err := s.d.Hosted.RequestHostedPayment(r.Context(), &billingv1.RequestHostedPaymentRequest{Token: sc.token, PortalToken: sc.portal, InvoiceId: sc.invoice, Method: method})
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
	v := &instructionsView{pageBase: newBase(lang), Number: p.GetNumber(), Total: formatMinor(p.GetAmountMinor(), exponentOf(p.GetCurrency())),
		Currency: p.GetCurrency(), Reference: out.Reference, Back: back}
	v.Test = p.GetEnvironment() == "sandbox"
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
