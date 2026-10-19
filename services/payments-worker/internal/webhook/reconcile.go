package webhook

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"sort"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	billingv1 "github.com/0furkancolak/quotify/gen/go/quotify/billing/v1"
	"github.com/0furkancolak/quotify/pkg/tenant"
)

// BillingAPI is what reconciliation asks of billing-svc, as the tenant: its invoices.
type BillingAPI interface {
	ListInvoices(ctx context.Context, tenant uuid.UUID, req *billingv1.ListInvoicesRequest) (*billingv1.ListInvoicesResponse, error)
	GetInvoice(ctx context.Context, tenant uuid.UUID, id string) (*billingv1.Invoice, error)
}

// BillingClient talks to billing-svc's tenant API.
type BillingClient struct {
	Client billingv1.BillingServiceClient
}

func (b BillingClient) ListInvoices(ctx context.Context, id uuid.UUID, req *billingv1.ListInvoicesRequest) (*billingv1.ListInvoicesResponse, error) {
	return b.Client.ListInvoices(tenant.OutgoingContext(tenant.NewContext(ctx, tenant.Info{ID: id})), req)
}

func (b BillingClient) GetInvoice(ctx context.Context, id uuid.UUID, invoice string) (*billingv1.Invoice, error) {
	r, err := b.Client.GetInvoice(tenant.OutgoingContext(tenant.NewContext(ctx, tenant.Info{ID: id})), &billingv1.GetInvoiceRequest{InvoiceId: invoice})
	return r.GetInvoice(), err
}

// Discrepancy kinds.
const (
	PaidWithoutCharge   = "paid_without_charge"  // billing says paid; the ledger has no successful charge
	AmountMismatch      = "amount_mismatch"      // the ledger's charge is for another amount or currency than the invoice
	DuplicateCharge     = "duplicate_charge"     // more than one distinct successful charge for one invoice
	ChargeWithoutPaid   = "charge_without_paid"  // the ledger has a successful charge for an invoice billing does not call paid
	ConflictingOutcomes = "conflicting_outcomes" // the ledger holds a success and a failure for the same provider reference
)

// Discrepancy is one thing that does not agree.
type Discrepancy struct {
	Kind        string `json:"kind"`
	InvoiceID   string `json:"invoice_id"`
	Number      string `json:"number,omitempty"`
	Provider    string `json:"provider,omitempty"`
	ProviderRef string `json:"provider_ref,omitempty"`
	Currency    string `json:"currency,omitempty"`
	BilledMinor int64  `json:"billed_minor,omitempty"`
	LedgerMinor int64  `json:"ledger_minor,omitempty"`
	Detail      string `json:"detail,omitempty"`
}

// Reconciliation compares the invoices billing calls paid in a period with the successful charges in the ledger.
type Reconciliation struct {
	Period        string         `json:"period"`
	PaidInvoices  int            `json:"paid_invoices"`
	Charges       int            `json:"successful_charges"`
	Counts        map[string]int `json:"discrepancy_counts"`
	Discrepancies []Discrepancy  `json:"discrepancies"`
	// Truncated is set when there were more discrepancies than are listed (the counts are complete).
	Truncated bool `json:"truncated,omitempty"`
}

const (
	maxListed     = 200
	maxInvoicePgs = 100
)

var reconcilePeriod = regexp.MustCompile(`^[0-9]{4}-(0[1-9]|1[0-2])$`)

// reconcile is GET /admin/tenants/{tenant}/reconciliation?period=YYYY-MM.
func (s *Server) reconcile(w http.ResponseWriter, r *http.Request) {
	t, ok := s.tenant(w, r)
	if !ok {
		return
	}
	period := r.URL.Query().Get("period")
	if !reconcilePeriod.MatchString(period) {
		fail(w, http.StatusBadRequest, "period must look like 2026-10")
		return
	}
	if s.Billing == nil {
		fail(w, http.StatusServiceUnavailable, "billing-svc is not configured")
		return
	}
	rec, err := s.Reconcile(r.Context(), t, period)
	if err != nil {
		if status.Code(err) == codes.Unknown {
			s.Log.ErrorContext(r.Context(), "reconciliation failed", "err", err)
			fail(w, http.StatusInternalServerError, "internal error")
			return
		}
		planFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

// Reconcile runs the comparison for one billing period.
func (s *Server) Reconcile(ctx context.Context, t uuid.UUID, period string) (*Reconciliation, error) {
	rec := &Reconciliation{Period: period, Counts: map[string]int{}, Discrepancies: []Discrepancy{}}
	add := func(d Discrepancy) {
		rec.Counts[d.Kind]++
		if len(rec.Discrepancies) < maxListed {
			rec.Discrepancies = append(rec.Discrepancies, d)
		} else {
			rec.Truncated = true
		}
	}

	paid := map[string]*billingv1.Invoice{}
	token := ""
	for pages := 0; pages < maxInvoicePgs; pages++ {
		resp, err := s.Billing.ListInvoices(ctx, t, &billingv1.ListInvoicesRequest{PeriodId: period, Status: "paid", PageSize: 500, PageToken: token})
		if err != nil {
			return nil, err
		}
		for _, inv := range resp.GetInvoices() {
			paid[inv.GetId()] = inv
		}
		if token = resp.GetNextPageToken(); token == "" {
			break
		}
	}
	rec.PaidInvoices = len(paid)

	// Successful charges for those invoices, and for any invoice whose charge settled in the period's window.
	charges, err := s.Ledger.SuccessfulCharges(ctx, t)
	if err != nil {
		return nil, err
	}
	byInvoice := map[string][]ledgerCharge{}
	for _, c := range charges {
		byInvoice[c.InvoiceID] = append(byInvoice[c.InvoiceID], ledgerCharge(c))
	}
	rec.Charges = len(charges)

	ids := make([]string, 0, len(paid))
	for id := range paid {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		inv := paid[id]
		if inv.GetTotalMinor() == 0 {
			continue // nothing is charged for an invoice of nothing
		}
		cs := byInvoice[id]
		switch {
		case len(cs) == 0:
			add(Discrepancy{Kind: PaidWithoutCharge, InvoiceID: id, Number: inv.GetNumber(), Currency: inv.GetCurrency(), BilledMinor: inv.GetTotalMinor()})
			continue
		}
		distinct := map[string]ledgerCharge{}
		for _, c := range cs {
			distinct[c.Provider+"|"+c.ProviderRef] = c
		}
		if len(distinct) > 1 {
			add(Discrepancy{Kind: DuplicateCharge, InvoiceID: id, Number: inv.GetNumber(), Currency: inv.GetCurrency(), BilledMinor: inv.GetTotalMinor(),
				Detail: fmt.Sprintf("%d distinct successful charges", len(distinct))})
			continue
		}
		c := cs[0]
		if c.AmountMinor != inv.GetTotalMinor() || (c.Currency != "" && c.Currency != "XXX" && c.Currency != inv.GetCurrency()) {
			add(Discrepancy{Kind: AmountMismatch, InvoiceID: id, Number: inv.GetNumber(), Provider: c.Provider, ProviderRef: c.ProviderRef, Currency: inv.GetCurrency(),
				BilledMinor: inv.GetTotalMinor(), LedgerMinor: c.AmountMinor, Detail: "ledger currency " + c.Currency})
		}
	}

	// Charges in the ledger for invoices of this period that billing does not call paid.
	checked := 0
	var unknown []string
	for id := range byInvoice {
		if _, isPaid := paid[id]; !isPaid {
			unknown = append(unknown, id)
		}
	}
	sort.Strings(unknown)
	for _, id := range unknown {
		if checked >= 500 {
			break
		}
		checked++
		inv, err := s.Billing.GetInvoice(ctx, t, id)
		if err != nil {
			if status.Code(err) == codes.NotFound {
				continue // not one of this tenant's invoices (or not billed through Quotify)
			}
			return nil, err
		}
		if inv.GetPeriodId() != period || inv.GetStatus() == "paid" {
			continue
		}
		c := byInvoice[id][0]
		add(Discrepancy{Kind: ChargeWithoutPaid, InvoiceID: id, Number: inv.GetNumber(), Provider: c.Provider, ProviderRef: c.ProviderRef, Currency: c.Currency,
			BilledMinor: inv.GetTotalMinor(), LedgerMinor: c.AmountMinor, Detail: "invoice status " + inv.GetStatus()})
	}

	conflicts, err := s.Ledger.ConflictingCharges(ctx, t)
	if err != nil {
		return nil, err
	}
	for _, c := range conflicts {
		add(Discrepancy{Kind: ConflictingOutcomes, InvoiceID: c.InvoiceID, Provider: c.Provider, ProviderRef: c.ProviderRef, Detail: "a success and a failure are both recorded"})
	}
	return rec, nil
}

type ledgerCharge struct {
	InvoiceID, Provider, ProviderRef, Currency string
	AmountMinor                                int64
}
