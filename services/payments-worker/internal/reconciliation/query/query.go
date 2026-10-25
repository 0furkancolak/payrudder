// Package query compares the invoices billing calls paid with the successful charges in the payment ledger. It never writes.
package query

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"sort"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
	"github.com/0furkancolak/payrudder/pkg/payments/banktransfer"
	"github.com/0furkancolak/payrudder/pkg/period"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/refuse"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store"
)

// Billing is what reconciliation asks of billing-svc, as the tenant: its invoices.
type Billing interface {
	ListInvoices(ctx context.Context, tenant uuid.UUID, req *billingv1.ListInvoicesRequest) (*billingv1.ListInvoicesResponse, error)
	GetInvoice(ctx context.Context, tenant uuid.UUID, id string) (*billingv1.Invoice, error)
}

// Deps is what the query reads. Billing nil means billing-svc is not configured.
type Deps struct {
	Ledger  store.Ledger
	Billing Billing
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
	// maxUnpaidChecked is how many ledger charges of invoices billing does not list as paid are looked up one by one.
	maxUnpaidChecked = 500
)

// Get runs the comparison for one billing period (YYYY-MM).
func Get(ctx context.Context, d Deps, t uuid.UUID, pid string) (*Reconciliation, error) {
	if !period.Valid(pid) {
		return nil, refuse.New(http.StatusBadRequest, "payments.reconcile.bad_period", "period must look like 2026-10")
	}
	if d.Billing == nil {
		return nil, refuse.New(http.StatusServiceUnavailable, "payments.billing.not_configured", "billing-svc is not configured")
	}
	rec := &Reconciliation{Period: pid, Counts: map[string]int{}, Discrepancies: []Discrepancy{}}
	paid, err := paidInvoices(ctx, d, t, pid)
	if err != nil {
		return nil, billingFailure("list the paid invoices", err)
	}
	rec.PaidInvoices = len(paid)

	charges, err := d.Ledger.SuccessfulCharges(ctx, t)
	if err != nil {
		return nil, fmt.Errorf("list successful charges: %w", err)
	}
	byInvoice := map[string][]ledgerCharge{}
	for _, c := range mergeTransfers(charges) {
		byInvoice[c.InvoiceID] = append(byInvoice[c.InvoiceID], ledgerCharge(c))
	}
	rec.Charges = len(charges)

	rec.compare(paid, byInvoice, paidByHand(ctx, d, t))
	if err := unpaidCharges(ctx, d, rec, t, paid, byInvoice); err != nil {
		return nil, billingFailure("look up an invoice", err)
	}
	conflicts, err := d.Ledger.ConflictingCharges(ctx, t)
	if err != nil {
		return nil, fmt.Errorf("list conflicting charges: %w", err)
	}
	for _, c := range conflicts {
		rec.add(Discrepancy{Kind: ConflictingOutcomes, InvoiceID: c.InvoiceID, Provider: c.Provider, ProviderRef: c.ProviderRef, Detail: "a success and a failure are both recorded"})
	}
	return rec, nil
}

// billingFailure is a failure of billing-svc: the caller gets billing's refusal, or a bad gateway. An error that is no gRPC status is ours and stays internal.
func billingFailure(what string, err error) error {
	if status.Code(err) == codes.Unknown {
		return fmt.Errorf("reconciliation: %s: %w", what, err)
	}
	return &refuse.Billing{Err: err}
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
func paidInvoices(ctx context.Context, d Deps, t uuid.UUID, pid string) (map[string]*billingv1.Invoice, error) {
	paid := map[string]*billingv1.Invoice{}
	token := ""
	for pages := 0; pages < maxInvoicePgs; pages++ {
		resp, err := d.Billing.ListInvoices(ctx, t, &billingv1.ListInvoicesRequest{PeriodId: pid, Status: "paid", PageSize: 500, PageToken: token})
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
	for _, id := range slices.Sorted(maps.Keys(paid)) {
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
func unpaidCharges(ctx context.Context, d Deps, rec *Reconciliation, t uuid.UUID, paid map[string]*billingv1.Invoice, byInvoice map[string][]ledgerCharge) error {
	var unknown []string
	for id := range byInvoice {
		if _, isPaid := paid[id]; !isPaid {
			unknown = append(unknown, id)
		}
	}
	sort.Strings(unknown)
	for checked, id := range unknown {
		if checked >= maxUnpaidChecked {
			break
		}
		inv, err := d.Billing.GetInvoice(ctx, t, id)
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
func paidByHand(ctx context.Context, d Deps, t uuid.UUID) func(id string) bool {
	return func(id string) bool {
		inv, err := d.Billing.GetInvoice(ctx, t, id)
		return err == nil && len(inv.GetPayments()) > 0
	}
}
