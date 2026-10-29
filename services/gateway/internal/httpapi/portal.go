package httpapi

import (
	"net/http"
	"sort"
	"strconv"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
	"github.com/0furkancolak/payrudder/pkg/env"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/money"
)

// The customer portal: GET /c/{token}. Read-only, for one customer of one tenant. billing-svc resolves the token to the tenant and the customer and returns only
// that customer's finalized invoices; the ledger rows shown are those of exactly those invoices, so nothing else can reach the page.

type portalInvoiceRow struct {
	Number, Date, State, StateLabel, Total, Currency, Href string
	Payable                                                bool
}

type portalPaymentRow struct {
	Date, Kind, Number, Status, Provider, Amount, Currency string
	Negative                                               bool
}

type portalView struct {
	pageBase
	Seller, Customer string
	Invoices         []portalInvoiceRow
	Payments         []portalPaymentRow
	// Credits (phase 36). ShowCredits is true when the customer has credits or the tenant has packs on sale.
	ShowCredits bool
	Balance     string
	Lots        []portalLotRow
	Packs       []portalPackRow
	CanBuy      bool
	Buy         string
}

type portalLotRow struct{ Granted, Remaining, Expires string }

type portalPackRow struct{ ID, Name, Credits, Price, Currency, Valid string }

func (v *portalView) Title() string { return v.L["portal_title"] }

func (s *Server) portalPage(w http.ResponseWriter, r *http.Request) {
	lang := pickLang(r)
	if !s.hostedAllowed(w, r) {
		return
	}
	s.captureRef(w, r)
	token := r.PathValue("token")
	resp, err := s.d.Hosted.GetPortal(r.Context(), &billingv1.GetPortalRequest{PortalToken: token})
	if goneError(err) {
		hostedNotFound(w, lang)
		return
	}
	if err != nil {
		hostedTrouble(w, lang)
		return
	}
	lang = customerLang(r, resp.GetCustomerLocale())
	q := langSuffix(r)
	v := &portalView{pageBase: newBase(lang), Seller: resp.GetSellerName(), Customer: resp.GetCustomerName()}
	v.SideSeller, v.SideNote = v.Seller, v.Customer
	v.Test = resp.GetEnvironment() == env.Sandbox.String()
	if resp.GetLogoAvailable() {
		v.Logo = scope{portal: token}.logo()
	}
	var ids []string
	var shown map[string]knownInvoice
	v.Invoices, ids, shown = portalInvoices(resp, v.L, token, q)
	rows, err := s.ledgerLookup(r.Context(), resp.GetTenantId(), ids)
	if err != nil {
		s.d.Log.WarnContext(r.Context(), "portal ledger lookup failed", "error", err)
	}
	v.Payments = portalPayments(rows, shown, v.L)
	portalCredits(v, resp, token, q)
	s.render(w, r, http.StatusOK, "portal", v)
}

// knownInvoice is an invoice of the portal's customer: what a ledger row of it is shown with.
type knownInvoice struct {
	number   string
	exponent int
}

// portalInvoices lists the customer's invoices as the page shows them, with their ids (the ledger is read for exactly these) and what to show a ledger
// row of each with.
func portalInvoices(resp *billingv1.GetPortalResponse, l map[string]string, token, q string) (rows []portalInvoiceRow, ids []string, shown map[string]knownInvoice) {
	shown = map[string]knownInvoice{}
	for _, inv := range resp.GetInvoices() {
		state := paymentStateWord(inv.GetPaymentState(), inv.GetStatus())
		exp := int(inv.GetCurrencyExponent())
		row := portalInvoiceRow{Number: inv.GetNumber(), State: state, StateLabel: l["st_"+state], Total: money.FormatMinorGrouped(inv.GetTotalMinor(), exp), Currency: inv.GetCurrency(),
			Payable: inv.GetPayable(), Href: "/c/" + token + "/invoices/" + inv.GetId() + q}
		if t := inv.GetIssueTime(); t != nil {
			row.Date = t.AsTime().UTC().Format(time.DateOnly)
		}
		rows = append(rows, row)
		shown[inv.GetId()] = knownInvoice{inv.GetNumber(), exp}
		ids = append(ids, inv.GetId())
	}
	return rows, ids, shown
}

// portalPaymentStatusKey is the text key of a ledger row's status.
var portalPaymentStatusKey = map[string]string{"succeeded": "pay_succeeded", "declined": "pay_declined", "failed": "pay_failed", "pending": "pay_pending"}

// portalPayments lists the ledger rows of the customer's invoices, newest first. A row of an invoice that is not in shown is never listed.
func portalPayments(rows []ledgerRow, shown map[string]knownInvoice, l map[string]string) []portalPaymentRow {
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].OccurredAt.After(rows[j].OccurredAt) })
	var out []portalPaymentRow
	for _, row := range rows {
		k, ok := shown[row.InvoiceID]
		if !ok {
			continue
		}
		p := portalPaymentRow{Date: row.OccurredAt.UTC().Format(time.DateOnly), Kind: l["kind_"+row.Kind], Number: k.number, Status: l[portalPaymentStatusKey[row.Status]],
			Provider: row.Provider, Currency: row.Currency, Amount: money.FormatMinorGrouped(row.amount(), k.exponent), Negative: row.Kind == "refund"}
		if p.Negative {
			p.Amount = "-" + p.Amount
		}
		out = append(out, p)
	}
	return out
}

// portalCredits fills the credit balance, lots and packs of the page.
func portalCredits(v *portalView, resp *billingv1.GetPortalResponse, token, q string) {
	v.Balance, v.CanBuy, v.Buy = money.FormatMinorGrouped(resp.GetCreditBalance(), 0), resp.GetCanBuyCredits(), "/c/"+token+"/credits/buy"+q
	for _, l := range resp.GetCreditLots() {
		row := portalLotRow{Granted: money.FormatMinorGrouped(l.GetGranted(), 0), Remaining: money.FormatMinorGrouped(l.GetRemaining(), 0)}
		if t := l.GetExpireTime(); t != nil {
			row.Expires = t.AsTime().UTC().Format(time.DateOnly)
		}
		v.Lots = append(v.Lots, row)
	}
	for _, p := range resp.GetCreditPacks() {
		v.Packs = append(v.Packs, portalPackRow{ID: p.GetId(), Name: p.GetName(), Credits: money.FormatMinorGrouped(p.GetCredits(), 0), Price: money.FormatMinorGrouped(p.GetPriceMinor(), int(p.GetCurrencyExponent())),
			Currency: p.GetCurrency(), Valid: validText(v.L, p.GetValidityDays())})
	}
	v.ShowCredits = len(v.Lots) > 0 || len(v.Packs) > 0 || resp.GetCreditBalance() > 0
}

// portalCreditsBuy is POST /c/{token}/credits/buy: the "Buy" button of a credit pack. Billing makes the credit-pack link and its invoice (the price is the pack's, not the
// form's) and the customer continues on the invoice page. The credits arrive when the payment is confirmed, never from this request.
func (s *Server) portalCreditsBuy(w http.ResponseWriter, r *http.Request) {
	lang := pickLang(r)
	if !s.hostedPayAllowed(w, r) {
		return
	}
	token := r.PathValue("token")
	var packID, code string
	var quantity int32
	if err := r.ParseForm(); err == nil {
		packID = r.PostForm.Get("pack_id")
		code = formCode(r)
		if v := r.PostForm.Get("quantity"); v != "" {
			n, perr := strconv.ParseInt(v, 10, 32)
			if perr != nil {
				n = -1
			}
			quantity = int32(n)
		}
	}
	back := "/c/" + token + langSuffix(r)
	l := hostedText(lang)
	refCode, refAt := referral(r)
	resp, err := s.d.Hosted.StartPortalPurchase(r.Context(), &billingv1.StartPortalPurchaseRequest{PortalToken: token, PackId: packID, Quantity: quantity, PromotionCode: code,
		ReferralCode: refCode, ReferralTouchTime: refAt})
	switch {
	case err == nil:
		hostedHeaders(w)
		http.Redirect(w, r, "/i/"+resp.GetInvoiceToken()+langSuffix(r), http.StatusSeeOther)
	case isPromoError(errkey.Of(err)):
		s.message(w, r, http.StatusUnprocessableEntity, lang, l["cr_buy_title"], promoErrorText(l, errkey.Of(err)), back)
	case status.Code(err) == codes.PermissionDenied || status.Code(err) == codes.InvalidArgument || errkey.Of(err) == "billing.invoice_link.not_found":
		hostedNotFound(w, lang)
	case errkey.Of(err) == "billing.customer.blocked":
		s.message(w, r, http.StatusConflict, lang, l["cr_buy_title"], l["pay_blocked"], back)
	case status.Code(err) == codes.NotFound || status.Code(err) == codes.FailedPrecondition:
		s.message(w, r, http.StatusConflict, lang, l["cr_buy_title"], l["cr_unavailable"], back)
	default:
		s.message(w, r, http.StatusServiceUnavailable, lang, l["cr_buy_title"], l["pay_unavailable"], back)
	}
}
