// Package query reads the bank transfer state of an invoice from the ledger. It never writes.
package query

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/0furkancolak/payrudder/pkg/payments"
	"github.com/0furkancolak/payrudder/pkg/payments/banktransfer"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/handler"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store"
)

// States of a transfer.
const (
	None     = "none"     // the invoice was not charged by bank transfer
	Pending  = "pending"  // waiting for the money; partial receipts included
	Paid     = "paid"     // the receipts reach the total
	Expired  = "expired"  // the payment window passed
	Rejected = "rejected" // the operator gave up on it
)

// Accounts finds a tenant's bank transfer account.
type Accounts interface {
	Named(ctx context.Context, tenant uuid.UUID, provider, env string) (payments.Provider, error)
}

// Deps is what the queries and commands of the module read.
type Deps struct {
	Transfers store.Transfers
	Accounts  Accounts
	// DefaultWindow is the deployment's payment window, for an account that sets none.
	DefaultWindow time.Duration
	// Now is the clock; nil is time.Now.
	Now func() time.Time
}

// Clock returns the current time.
func (d Deps) Clock() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

// State is the bank transfer state of an invoice.
type State struct {
	InvoiceID   uuid.UUID `json:"invoice_id"`
	State       string    `json:"state"`
	Reference   string    `json:"reference,omitempty"`
	Currency    string    `json:"currency,omitempty"`
	TotalMinor  int64     `json:"total_minor,omitempty"`
	Received    int64     `json:"received_minor"`
	Outstanding int64     `json:"outstanding_minor,omitempty"`
	// Instructions are where the customer is told to pay; absent when the tenant has since removed the account.
	Instructions *banktransfer.Instructions `json:"instructions,omitempty"`
	PendingSince *time.Time                 `json:"pending_since,omitempty"`
	ExpiresAt    *time.Time                 `json:"expires_at,omitempty"`
	ClosedReason string                     `json:"closed_reason,omitempty"`
	Receipts     []store.Receipt            `json:"receipts"`

	// The charge the state is about; not part of the answer.
	Charge store.ChargeAttempt `json:"-"`
}

// Get returns the transfer state of an invoice of a tenant.
func Get(ctx context.Context, d Deps, tenant, invoice uuid.UUID) (State, error) {
	out := State{InvoiceID: invoice, State: None, Receipts: []store.Receipt{}}
	charge, ok, err := d.Transfers.LatestCharge(ctx, tenant, invoice)
	if err != nil || !ok || charge.Provider != banktransfer.Name {
		return out, err
	}
	out.Charge, out.Currency, out.TotalMinor = charge, charge.Currency, charge.AmountMinor
	if out.Receipts, err = d.Transfers.Receipts(ctx, tenant, invoice); err != nil {
		return out, err
	}
	got, err := d.Transfers.Received(ctx, tenant, invoice, banktransfer.Name)
	if err != nil {
		return out, err
	}
	out.Received, out.Outstanding = got.Minor, max(charge.AmountMinor-got.Minor, 0)

	window := d.DefaultWindow
	if prov, err := d.Accounts.Named(ctx, tenant, banktransfer.Name, charge.Environment); err == nil {
		if bt, ok := prov.(*banktransfer.Provider); ok {
			in := bt.Instructions(charge.InvoiceNumber, invoice.String())
			out.Instructions, out.Reference = &in, in.Reference
			if w := bt.PaymentWindow(); w > 0 {
				window = w
			}
		}
	}

	pending, hasPending, err := d.Transfers.Pending(ctx, tenant, banktransfer.Name, charge.IdempotencyKey)
	if err != nil {
		return out, err
	}
	if hasPending {
		since := pending.Since.UTC()
		exp := since.Add(window)
		out.PendingSince, out.ExpiresAt = &since, &exp
		if pending.Reference != "" {
			out.Reference = pending.Reference
		}
	}
	closure, closed, err := d.Transfers.Closure(ctx, tenant, banktransfer.Name, charge.IdempotencyKey)
	if err != nil {
		return out, err
	}
	switch {
	case charge.AmountMinor > 0 && got.Minor >= charge.AmountMinor:
		out.State = Paid
	case closed:
		out.State, out.ClosedReason = Rejected, closure.Detail
		if handler.DeclineCode(closure.Detail) == handler.TransferExpired {
			out.State = Expired
		}
	case hasPending && d.Clock().After(pending.Since.Add(window)):
		out.State = Expired
	default:
		out.State = Pending
	}
	return out, nil
}
