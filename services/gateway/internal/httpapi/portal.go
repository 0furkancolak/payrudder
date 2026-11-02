package httpapi

import (
	"cmp"
	"context"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
	"github.com/0furkancolak/payrudder/pkg/env"
	"github.com/0furkancolak/payrudder/pkg/errkey"
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
	// Subscriptions is the read-only summary of the customer's subscriptions; empty hides the section.
	Subscriptions []portalSubRow
	Invoices      []portalInvoiceRow
	Payments      []portalPaymentRow
	// Credits (phase 36). ShowCredits is true when the customer has credits or the tenant has packs on sale.
	ShowCredits bool
	Balance     string
	Lots        []portalLotRow
	Packs       []portalPackRow
	CanBuy      bool
	Buy         string
	// The seller's agreements (docs/plans/21-contracts.md): what the customer accepted (with the page they accepted), the texts to accept again when a material version was
	// published since, and the link to the withdrawal function (Directive 2011/83/EU art. 11a). Each is empty when there is nothing to show.
	Agreements  []portalAgreementRow
	Ask         *contractView
	AgreeAction string
	Withdraw    string
}

// portalSubRow is one subscription as the summary shows it: the plan, its state as a badge, what one period costs, and a few facts (dates, quantity, who bills it).
type portalSubRow struct {
	Plan, State, StateLabel, Price string
	Facts                          []string
}

type portalLotRow struct{ Granted, Remaining, Expires string }

type portalPackRow struct{ ID, Name, Credits, Price, Currency, Valid string }

func (v *portalView) Title() string { return v.L["portal_title"] }

func (s *Server) portalPage(w http.ResponseWriter, r *http.Request) {
	s.portalPageWith(w, r, http.StatusOK, "")
}

// portalPageWith writes the portal; problem is the key of the text to show above the agreements to accept again (the form was not complete), with the 422 that goes with it.
func (s *Server) portalPageWith(w http.ResponseWriter, r *http.Request, code int, problem string) {
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
	v.Invoices, ids, shown = portalInvoices(resp, v.pageBase, token, q)
	rows, err := s.ledgerLookup(r.Context(), resp.GetTenantId(), ids)
	if err != nil {
		s.d.Log.WarnContext(r.Context(), "portal ledger lookup failed", "error", err)
	}
	v.Payments = portalPayments(rows, shown, v.pageBase)
	v.Subscriptions = s.portalSubscriptions(r.Context(), token, v.pageBase)
	portalCredits(v, resp, token, q)
	s.portalAgreements(r.Context(), v, resp, token, lang, q, problem)
	v.Withdraw = s.withdrawalLink(r.Context(), subjectOfPortal(resp, token), q)
	s.render(w, r, code, "portal", v)
}

// subscriptionsWait is how long the portal waits for the subscription summary: a slow plans-svc must not hold the whole page back.
const subscriptionsWait = 2 * time.Second

// portalSubscriptions asks billing for the customer's subscriptions and words them. The summary is read-only and optional: when billing cannot give it, the page is shown
// without the section.
func (s *Server) portalSubscriptions(ctx context.Context, token string, b pageBase) []portalSubRow {
	ctx, cancel := context.WithTimeout(ctx, subscriptionsWait)
	defer cancel()
	resp, err := s.d.Hosted.GetPortalSubscriptions(ctx, &billingv1.GetPortalSubscriptionsRequest{PortalToken: token})
	if err != nil {
		if status.Code(err) != codes.Unimplemented {
			s.d.Log.WarnContext(ctx, "portal subscriptions unavailable", "error", err)
		}
		return nil
	}
	return portalSubRows(resp.GetSubscriptions(), b)
}

// portalSubRows words the subscriptions of the summary in the page's language.
func portalSubRows(subs []*billingv1.PortalSubscription, b pageBase) []portalSubRow {
	var out []portalSubRow
	date := func(t *timestamppb.Timestamp) string { return b.F.Date(t.AsTime().UTC()) }
	for _, p := range subs {
		row := portalSubRow{Plan: cmp.Or(p.GetPlanName(), p.GetPlanCode()), State: p.GetState(), StateLabel: b.L["sub_state_"+p.GetState()]}
		if p.GetPriceKnown() {
			row.Price = b.F.Amount(p.GetPriceMinor(), int(p.GetCurrencyExponent())) + " " + p.GetCurrency() + " / " + b.L["sub_"+p.GetInterval()]
		}
		if p.GetQuantity() > 1 {
			row.Facts = append(row.Facts, b.L["sub_quantity"]+": "+b.F.Amount(int64(p.GetQuantity()), 0))
		}
		switch {
		case p.GetTrialEndTime() != nil:
			row.Facts = append(row.Facts, fmt.Sprintf(b.L["sub_trial_ends"], date(p.GetTrialEndTime())))
		case p.GetScheduledTime() != nil:
			row.Facts = append(row.Facts, fmt.Sprintf(b.L["sub_changes"], cmp.Or(p.GetScheduledPlanName(), p.GetPlanName()), date(p.GetScheduledTime())))
		case p.GetNextRenewalTime() != nil && !p.GetCancelAtPeriodEnd():
			row.Facts = append(row.Facts, fmt.Sprintf(b.L["sub_next"], date(p.GetNextRenewalTime())))
		}
		if p.GetCancelAtPeriodEnd() && p.GetEndTime() != nil {
			row.Facts = append(row.Facts, fmt.Sprintf(b.L["sub_ends"], date(p.GetEndTime())))
		}
		if p.GetProviderManaged() {
			row.Facts = append(row.Facts, b.L["sub_provider"])
		}
		out = append(out, row)
	}
	return out
}

// knownInvoice is an invoice of the portal's customer: what a ledger row of it is shown with.
type knownInvoice struct {
	number   string
	exponent int
}

// portalInvoices lists the customer's invoices as the page shows them, with their ids (the ledger is read for exactly these) and what to show a ledger
// row of each with.
func portalInvoices(resp *billingv1.GetPortalResponse, b pageBase, token, q string) (rows []portalInvoiceRow, ids []string, shown map[string]knownInvoice) {
	shown = map[string]knownInvoice{}
	for _, inv := range resp.GetInvoices() {
		state := paymentStateWord(inv.GetPaymentState(), inv.GetStatus())
		exp := int(inv.GetCurrencyExponent())
		row := portalInvoiceRow{Number: inv.GetNumber(), State: state, StateLabel: b.L["st_"+state], Total: b.F.Amount(inv.GetTotalMinor(), exp), Currency: inv.GetCurrency(),
			Payable: inv.GetPayable(), Href: "/c/" + token + "/invoices/" + inv.GetId() + q}
		if t := inv.GetIssueTime(); t != nil {
			row.Date = b.F.Date(t.AsTime().UTC())
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
func portalPayments(rows []ledgerRow, shown map[string]knownInvoice, b pageBase) []portalPaymentRow {
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].OccurredAt.After(rows[j].OccurredAt) })
	var out []portalPaymentRow
	for _, row := range rows {
		k, ok := shown[row.InvoiceID]
		if !ok {
			continue
		}
		p := portalPaymentRow{Date: b.F.Date(row.OccurredAt.UTC()), Kind: b.L["kind_"+row.Kind], Number: k.number, Status: b.L[portalPaymentStatusKey[row.Status]],
			Provider: row.Provider, Currency: row.Currency, Amount: b.F.Amount(row.amount(), k.exponent), Negative: row.Kind == "refund"}
		if p.Negative {
			p.Amount = b.F.Minus + p.Amount
		}
		out = append(out, p)
	}
	return out
}

// portalCredits fills the credit balance, lots and packs of the page.
func portalCredits(v *portalView, resp *billingv1.GetPortalResponse, token, q string) {
	v.Balance, v.CanBuy, v.Buy = v.F.Amount(resp.GetCreditBalance(), 0), resp.GetCanBuyCredits(), "/c/"+token+"/credits/buy"+q
	for _, l := range resp.GetCreditLots() {
		row := portalLotRow{Granted: v.F.Amount(l.GetGranted(), 0), Remaining: v.F.Amount(l.GetRemaining(), 0)}
		if t := l.GetExpireTime(); t != nil {
			row.Expires = v.F.Date(t.AsTime().UTC())
		}
		v.Lots = append(v.Lots, row)
	}
	for _, p := range resp.GetCreditPacks() {
		v.Packs = append(v.Packs, portalPackRow{ID: p.GetId(), Name: p.GetName(), Credits: v.F.Amount(p.GetCredits(), 0), Price: v.F.Amount(p.GetPriceMinor(), int(p.GetCurrencyExponent())),
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
	case gateRefused(err):
		s.message(w, r, http.StatusConflict, lang, l["cr_buy_title"], l["pay_blocked"], back)
	case status.Code(err) == codes.NotFound || status.Code(err) == codes.FailedPrecondition:
		s.message(w, r, http.StatusConflict, lang, l["cr_buy_title"], l["cr_unavailable"], back)
	default:
		s.message(w, r, http.StatusServiceUnavailable, lang, l["cr_buy_title"], l["pay_unavailable"], back)
	}
}
