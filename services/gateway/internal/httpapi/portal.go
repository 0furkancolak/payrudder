package httpapi

import (
	"net/http"
	"sort"

	billingv1 "github.com/0furkancolak/quotify/gen/go/quotify/billing/v1"
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
}

func (v *portalView) Title() string { return v.L["portal_title"] }

func (s *Server) portalPage(w http.ResponseWriter, r *http.Request) {
	lang := pickLang(r)
	if !s.hostedAllowed(w, r) {
		return
	}
	token := r.PathValue("token")
	resp, err := s.d.Hosted.GetPortal(r.Context(), &billingv1.GetPortalRequest{PortalToken: token})
	if goneError(err) {
		hostedNotFound(w, lang)
		return
	}
	if err != nil {
		hostedHeaders(w)
		http.Error(w, "the page cannot be shown right now", http.StatusServiceUnavailable)
		return
	}
	q := langSuffix(r)
	v := &portalView{pageBase: newBase(lang), Seller: resp.GetSellerName(), Customer: resp.GetCustomerName()}
	v.Test = resp.GetEnvironment() == "sandbox"
	if resp.GetLogoAvailable() {
		v.Logo = scope{portal: token}.logo()
	}
	type known struct {
		number   string
		exponent int
	}
	byID := map[string]known{}
	var ids []string
	for _, inv := range resp.GetInvoices() {
		state := inv.GetPaymentState()
		if state == "" {
			state = map[string]string{"paid": "paid", "void": "void"}[inv.GetStatus()]
			if state == "" {
				state = "open"
			}
		}
		exp := int(inv.GetCurrencyExponent())
		row := portalInvoiceRow{Number: inv.GetNumber(), State: state, StateLabel: v.L["st_"+state], Total: formatMinor(inv.GetTotalMinor(), exp), Currency: inv.GetCurrency(),
			Payable: inv.GetPayable(), Href: "/c/" + token + "/invoices/" + inv.GetId() + q}
		if t := inv.GetIssueTime(); t != nil {
			row.Date = t.AsTime().UTC().Format("2006-01-02")
		}
		v.Invoices = append(v.Invoices, row)
		byID[inv.GetId()] = known{inv.GetNumber(), exp}
		ids = append(ids, inv.GetId())
	}
	rows, err := s.ledgerLookup(r.Context(), resp.GetTenantId(), ids)
	if err != nil {
		s.d.Log.WarnContext(r.Context(), "portal ledger lookup failed", "error", err)
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].OccurredAt.After(rows[j].OccurredAt) })
	statusKey := map[string]string{"succeeded": "pay_succeeded", "declined": "pay_declined", "failed": "pay_failed", "pending": "pay_pending"}
	for _, row := range rows {
		k, ok := byID[row.InvoiceID]
		if !ok {
			continue // a row of an invoice that is not this customer's is never shown
		}
		p := portalPaymentRow{Date: row.OccurredAt.UTC().Format("2006-01-02"), Kind: v.L["kind_"+row.Kind], Number: k.number, Status: v.L[statusKey[row.Status]],
			Provider: row.Provider, Currency: row.Currency, Amount: formatMinor(row.amount(), k.exponent), Negative: row.Kind == "refund"}
		if p.Negative {
			p.Amount = "-" + p.Amount
		}
		v.Payments = append(v.Payments, p)
	}
	s.render(w, r, http.StatusOK, "portal", v)
}
