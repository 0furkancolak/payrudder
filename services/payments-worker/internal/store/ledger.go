package store

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/0furkancolak/quotify/services/payments-worker/internal/store/sqlc"
)

// LedgerEntry is one fact about money at a provider.
type LedgerEntry struct {
	TenantID       uuid.UUID
	InvoiceID      uuid.UUID // zero when the fact is not about one of our invoices
	Provider       string
	Environment    string
	Kind           string // charge, refund, dispute, payout
	Status         string // succeeded, declined, failed, pending
	AmountMinor    int64
	Currency       string
	ProviderRef    string
	IdempotencyKey string
	Source         string // command, webhook, reconciliation, manual
	Detail         string
	// OccurredAt is when it happened; zero is now (the database's clock).
	OccurredAt time.Time
}

// Ledger is the append-only payment_ledger.
type Ledger struct{ DB *pgxpool.Pool }

// Append writes a fact. The same fact (provider, kind, reference, status) reported again is not written twice; Append then returns false.
func (l Ledger) Append(ctx context.Context, e LedgerEntry) (bool, error) {
	p := sqlc.AppendLedgerParams{
		TenantID: e.TenantID, Provider: e.Provider, Environment: e.Environment, Kind: e.Kind, Status: e.Status,
		AmountMinor: e.AmountMinor, Currency: e.Currency, Source: e.Source,
	}
	if e.InvoiceID != uuid.Nil {
		p.InvoiceID = &e.InvoiceID
	}
	if e.ProviderRef != "" {
		p.ProviderRef = &e.ProviderRef
	}
	if e.IdempotencyKey != "" {
		p.IdempotencyKey = &e.IdempotencyKey
	}
	if e.Detail != "" {
		p.Detail = &e.Detail
	}
	if !e.OccurredAt.IsZero() {
		p.OccurredAt = &e.OccurredAt
	}
	n, err := sqlc.New(l.DB).AppendLedger(ctx, p)
	return n == 1, err
}

// Row is a ledger row as read back.
type Row struct {
	ID          int64     `json:"id"`
	InvoiceID   *string   `json:"invoice_id,omitempty"`
	Provider    string    `json:"provider"`
	Environment string    `json:"environment"`
	Kind        string    `json:"kind"`
	Status      string    `json:"status"`
	AmountMinor int64     `json:"amount_minor"`
	Currency    string    `json:"currency"`
	ProviderRef *string   `json:"provider_ref,omitempty"`
	Source      string    `json:"source"`
	Detail      *string   `json:"detail,omitempty"`
	OccurredAt  time.Time `json:"occurred_at"`
}

// List returns a tenant's most recent rows, newest first.
func (l Ledger) List(ctx context.Context, tenant uuid.UUID, limit int) ([]Row, error) {
	rows, err := sqlc.New(l.DB).ListLedger(ctx, sqlc.ListLedgerParams{TenantID: tenant, RowLimit: int64(limit)})
	if err != nil {
		return nil, err
	}
	out := make([]Row, len(rows))
	for i, r := range rows {
		out[i] = Row{
			ID: r.ID, Provider: r.Provider, Environment: r.Environment, Kind: r.Kind, Status: r.Status, AmountMinor: r.AmountMinor,
			Currency: r.Currency, ProviderRef: r.ProviderRef, Source: r.Source, Detail: r.Detail, OccurredAt: r.OccurredAt,
		}
		if r.InvoiceID != nil {
			id := r.InvoiceID.String()
			out[i].InvoiceID = &id
		}
	}
	return out, nil
}

// Outcomes counts the distinct statuses one tenant has recorded for one provider reference. More than one means the command's answer and the
// provider's confirmation disagree.
func (l Ledger) Outcomes(ctx context.Context, tenant uuid.UUID, provider, kind, ref string) (int, error) {
	n, err := sqlc.New(l.DB).CountLedgerOutcomes(ctx, sqlc.CountLedgerOutcomesParams{TenantID: tenant, Provider: provider, Kind: kind, ProviderRef: &ref})
	return int(n), err
}

// Fact is a final outcome the ledger holds for a charge.
type Fact struct {
	Status      string // succeeded or declined
	ProviderRef string
	Detail      string
}

// ChargeFact returns the final outcome recorded for the charge made under an idempotency key, if any. A success wins over a decline when
// both are recorded (a notification that later overruled the first).
func (l Ledger) ChargeFact(ctx context.Context, key string) (Fact, bool, error) {
	r, ok, err := found(sqlc.New(l.DB).GetChargeFact(ctx, &key))
	if !ok {
		return Fact{}, false, err
	}
	f := Fact{Status: r.Status}
	if r.ProviderRef != nil {
		f.ProviderRef = *r.ProviderRef
	}
	if r.Detail != nil {
		f.Detail = *r.Detail
	}
	return f, true, nil
}

// Charge is a successful charge in the ledger.
type Charge struct {
	InvoiceID, Provider, ProviderRef, Currency string
	AmountMinor                                int64
}

// SuccessfulCharges returns the tenant's successful charges that belong to an invoice.
func (l Ledger) SuccessfulCharges(ctx context.Context, tenant uuid.UUID) ([]Charge, error) {
	rows, err := sqlc.New(l.DB).ListSuccessfulCharges(ctx, tenant)
	if err != nil {
		return nil, err
	}
	var out []Charge
	for _, r := range rows {
		out = append(out, Charge{InvoiceID: r.InvoiceID, Provider: r.Provider, ProviderRef: r.ProviderRef, Currency: r.Currency, AmountMinor: r.AmountMinor})
	}
	return out, nil
}

// ConflictingCharges returns the charges that have both a success and a failure recorded under one provider reference.
func (l Ledger) ConflictingCharges(ctx context.Context, tenant uuid.UUID) ([]Charge, error) {
	rows, err := sqlc.New(l.DB).ListConflictingCharges(ctx, tenant)
	if err != nil {
		return nil, err
	}
	var out []Charge
	for _, r := range rows {
		out = append(out, Charge{InvoiceID: r.InvoiceID, Provider: r.Provider, ProviderRef: r.ProviderRef})
	}
	return out, nil
}
