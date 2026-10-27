package store

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store/sqlc"
)

// LedgerLine is a ledger row as a customer-facing page may see it: no provider reference, no detail.
type LedgerLine struct {
	ID          int64     `json:"id"`
	InvoiceID   string    `json:"invoice_id"`
	Provider    string    `json:"provider"`
	Environment string    `json:"environment"`
	Kind        string    `json:"kind"`
	Status      string    `json:"status"`
	AmountMinor int64     `json:"amount_minor"`
	Currency    string    `json:"currency"`
	OccurredAt  time.Time `json:"occurred_at"`
}

// LookupInvoices returns the tenant's ledger rows of the given invoices, newest first. Rows of other tenants are never returned, whatever ids are asked for.
func (l Ledger) LookupInvoices(ctx context.Context, tenant uuid.UUID, invoices []uuid.UUID) ([]LedgerLine, error) {
	rows, err := get(ctx, l.DB, tenant, func(q *sqlc.Queries) ([]sqlc.LookupLedgerByInvoicesRow, error) {
		return q.LookupLedgerByInvoices(ctx, sqlc.LookupLedgerByInvoicesParams{TenantID: tenant, InvoiceIds: invoices})
	})
	if err != nil {
		return nil, err
	}
	out := make([]LedgerLine, len(rows))
	for i, r := range rows {
		out[i] = LedgerLine{ID: r.ID, InvoiceID: r.InvoiceID, Provider: r.Provider, Environment: r.Environment, Kind: r.Kind, Status: r.Status,
			AmountMinor: r.AmountMinor, Currency: r.Currency, OccurredAt: r.OccurredAt}
	}
	return out, nil
}

// OpenCheckout is a hosted checkout that was opened for an invoice and has not been settled either way.
type OpenCheckout struct {
	URL, Ref, Key string
}

// OpenCheckoutOf returns the newest checkout opened at provider for the invoice since the given time that no notification has answered, if any.
func (l Ledger) OpenCheckoutOf(ctx context.Context, tenant, invoice uuid.UUID, provider string, since time.Time) (OpenCheckout, bool, error) {
	r, ok, err := lookup(ctx, l.DB, tenant, func(q *sqlc.Queries) (sqlc.FindOpenCheckoutRow, error) {
		return q.FindOpenCheckout(ctx, sqlc.FindOpenCheckoutParams{TenantID: tenant, InvoiceID: &invoice, Provider: provider, Since: since})
	})
	if !ok {
		return OpenCheckout{}, false, err
	}
	out := OpenCheckout{}
	if r.Detail != nil {
		out.URL = strings.TrimPrefix(*r.Detail, "checkout:")
	}
	if r.ProviderRef != nil {
		out.Ref = *r.ProviderRef
	}
	if r.IdempotencyKey != nil {
		out.Key = *r.IdempotencyKey
	}
	return out, true, nil
}

// CountLinkAttempts is how many hosted-payment charges were started for the invoice, so the next key is link:{invoice}:{n+1}.
func (a Attempts) CountLinkAttempts(ctx context.Context, tenant, invoice uuid.UUID) (int, error) {
	n, err := get(ctx, a.DB, tenant, func(q *sqlc.Queries) (int64, error) {
		return q.CountLinkAttempts(ctx, sqlc.CountLinkAttemptsParams{TenantID: tenant, InvoiceID: invoice})
	})
	return int(n), err
}
