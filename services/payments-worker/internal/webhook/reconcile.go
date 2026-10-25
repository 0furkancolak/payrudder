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
	"github.com/0furkancolak/quotify/pkg/payments/banktransfer"
	"github.com/0furkancolak/quotify/pkg/tenant"
	"github.com/0furkancolak/quotify/services/payments-worker/internal/store"
)

// BillingAPI is what reconciliation asks of billing-svc, as the tenant: its invoices.
type BillingAPI interface {
	ListInvoices(ctx context.Context, tenant uuid.UUID, req *billingv1.ListInvoicesRequest) (*billingv1.ListInvoicesResponse, error)
	GetInvoice(ctx context.Context, tenant uuid.UUID, id string) (*billingv1.Invoice, error)
	// SettleInvoicePayment tells billing that a provider confirmed a payment made on a hosted page.
	SettleInvoicePayment(ctx context.Context, tenant uuid.UUID, req *billingv1.SettleInvoicePaymentRequest) (*billingv1.SettleInvoicePaymentResponse, error)
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

func (b BillingClient) SettleInvoicePayment(ctx context.Context, id uuid.UUID, req *billingv1.SettleInvoicePaymentRequest) (*billingv1.SettleInvoicePaymentResponse, error) {
	return b.Client.SettleInvoicePayment(tenant.OutgoingContext(tenant.NewContext(ctx, tenant.Info{ID: id})), req)
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
		fail(w, http.StatusBadRequest, "payments.reconcile.bad_period", "period must look like 2026-10")
		return
	}
	if s.Billing == nil {
		fail(w, http.StatusServiceUnavailable, "payments.billing.not_configured", "billing-svc is not configured")
		return
	}
	rec, err := s.Reconcile(r.Context(), t, period)
	if err != nil {
		if status.Code(err) == codes.Unknown {
			s.Log.ErrorContext(r.Context(), "reconciliation failed", "err", err)
			fail(w, http.StatusInternalServerError, "common.internal", "internal error")
			return
		}
		s.billingFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

// Reconcile runs the comparison for one billing period.
func (s *Server) Reconcile(ctx context.Context, t uuid.UUID, period string) (*Reconciliation, error) {
	rec := &Reconciliation{Period: period, Counts: map[string]int{}, Discrepancies: []Discrepancy{}}
	paid, err := s.paidInvoices(ctx, t, period)
	if err != nil {
		return nil, err
	}
	rec.PaidInvoices = len(paid)

	charges, err := s.Ledger.SuccessfulCharges(ctx, t)
	if err != nil {
		return nil, err
	}
	byInvoice := map[string][]ledgerCharge{}
	for _, c := range mergeTransfers(charges) {
		byInvoice[c.InvoiceID] = append(byInvoice[c.InvoiceID], ledgerCharge(c))
	}
	rec.Charges = len(charges)

	rec.compare(paid, byInvoice, s.paidByHand(ctx, t))
	if err := s.unpaidCharges(ctx, rec, t, paid, byInvoice); err != nil {
		return nil, err
	}
	conflicts, err := s.Ledger.ConflictingCharges(ctx, t)
	if err != nil {
		return nil, err
	}
	for _, c := range conflicts {
		rec.add(Discrepancy{Kind: ConflictingOutcomes, InvoiceID: c.InvoiceID, Provider: c.Provider, ProviderRef: c.ProviderRef, Detail: "a success and a failure are both recorded"})
	}
	return rec, nil
}

func (rec *Reconciliation) add(d Discrepancy) {
	rec.Counts[d.Kind]++
	if len(rec.Discrepancies) < maxListed {
		rec.Discrepancies = append(rec.Discrepancies, d)
	} else {
		rec.Truncated = true
	}
}

// paidInvoices lists the invoices billing calls paid in the period, by id.
func (s *Server) paidInvoices(ctx context.Context, t uuid.UUID, period string) (map[string]*billingv1.Invoice, error) {
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
	return paid, nil
}

// compare checks every paid invoice against the successful charges the ledger holds for it.
func (rec *Reconciliation) compare(paid map[string]*billingv1.Invoice, byInvoice map[string][]ledgerCharge, byHand func(id string) bool) {
	for _, id := range sortedKeys(paid) {
		inv := paid[id]
		if inv.GetTotalMinor() == 0 {
			continue // nothing is charged for an invoice of nothing
		}
		cs := byInvoice[id]
		if len(cs) == 0 {
			if byHand(id) {
				continue // settled with RecordInvoicePayment: money received outside a provider, which has no charge in the ledger
			}
			rec.add(Discrepancy{Kind: PaidWithoutCharge, InvoiceID: id, Number: inv.GetNumber(), Currency: inv.GetCurrency(), BilledMinor: inv.GetTotalMinor()})
			continue
		}
		distinct := map[string]ledgerCharge{}
		for _, c := range cs {
			distinct[c.Provider+"|"+c.ProviderRef] = c
		}
		if len(distinct) > 1 {
			rec.add(Discrepancy{Kind: DuplicateCharge, InvoiceID: id, Number: inv.GetNumber(), Currency: inv.GetCurrency(), BilledMinor: inv.GetTotalMinor(),
				Detail: fmt.Sprintf("%d distinct successful charges", len(distinct))})
			continue
		}
		c := cs[0]
		if c.AmountMinor != inv.GetTotalMinor() || (c.Currency != "" && c.Currency != "XXX" && c.Currency != inv.GetCurrency()) {
			rec.add(Discrepancy{Kind: AmountMismatch, InvoiceID: id, Number: inv.GetNumber(), Provider: c.Provider, ProviderRef: c.ProviderRef, Currency: inv.GetCurrency(),
				BilledMinor: inv.GetTotalMinor(), LedgerMinor: c.AmountMinor, Detail: "ledger currency " + c.Currency})
		}
	}
}

// unpaidCharges finds charges in the ledger for invoices of this period that billing does not call paid.
func (s *Server) unpaidCharges(ctx context.Context, rec *Reconciliation, t uuid.UUID, paid map[string]*billingv1.Invoice, byInvoice map[string][]ledgerCharge) error {
	var unknown []string
	for id := range byInvoice {
		if _, isPaid := paid[id]; !isPaid {
			unknown = append(unknown, id)
		}
	}
	sort.Strings(unknown)
	for checked, id := range unknown {
		if checked >= 500 {
			break
		}
		inv, err := s.Billing.GetInvoice(ctx, t, id)
		if err != nil {
			if status.Code(err) == codes.NotFound {
				continue // not one of this tenant's invoices (or not billed through Payrudder)
			}
			return err
		}
		if inv.GetPeriodId() != rec.Period || inv.GetStatus() == "paid" {
			continue
		}
		c := byInvoice[id][0]
		rec.add(Discrepancy{Kind: ChargeWithoutPaid, InvoiceID: id, Number: inv.GetNumber(), Provider: c.Provider, ProviderRef: c.ProviderRef, Currency: c.Currency,
			BilledMinor: inv.GetTotalMinor(), LedgerMinor: c.AmountMinor, Detail: "invoice status " + inv.GetStatus()})
	}
	return nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

type ledgerCharge struct {
	InvoiceID, Provider, ProviderRef, Currency string
	AmountMinor                                int64
}

// mergeTransfers adds up the receipts of a bank transfer: an invoice paid by several transfers has one ledger row each, which together are one charge of the
// invoice's total, not several charges.
func mergeTransfers(charges []store.Charge) []store.Charge {
	var out []store.Charge
	at := map[string]int{}
	for _, c := range charges {
		if c.Provider != banktransfer.Name {
			out = append(out, c)
			continue
		}
		if i, ok := at[c.InvoiceID]; ok {
			out[i].AmountMinor += c.AmountMinor
			continue
		}
		at[c.InvoiceID] = len(out)
		c.ProviderRef = "bank transfer"
		out = append(out, c)
	}
	return out
}

// paidByHand reports whether billing-svc recorded a payment by hand for an invoice. A lookup that fails counts as no: the discrepancy is then listed, and an
// operator looks.
func (s *Server) paidByHand(ctx context.Context, t uuid.UUID) func(id string) bool {
	return func(id string) bool {
		inv, err := s.Billing.GetInvoice(ctx, t, id)
		return err == nil && len(inv.GetPayments()) > 0
	}
}
