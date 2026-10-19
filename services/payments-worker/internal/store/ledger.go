package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
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
	Source         string // command, webhook, reconciliation
	Detail         string
}

// Ledger is the append-only payment_ledger.
type Ledger struct{ DB *pgxpool.Pool }

// Append writes a fact. The same fact (provider, kind, reference, status) reported again is not written twice; Append then returns false.
func (l Ledger) Append(ctx context.Context, e LedgerEntry) (bool, error) {
	var inv, ref, key, detail any
	if e.InvoiceID != uuid.Nil {
		inv = e.InvoiceID
	}
	if e.ProviderRef != "" {
		ref = e.ProviderRef
	}
	if e.IdempotencyKey != "" {
		key = e.IdempotencyKey
	}
	if e.Detail != "" {
		detail = e.Detail
	}
	tag, err := l.DB.Exec(ctx, `INSERT INTO payment_ledger (tenant_id, invoice_id, provider, environment, kind, status, amount_minor, currency, provider_ref, idempotency_key, source, detail)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) ON CONFLICT DO NOTHING`,
		e.TenantID, inv, e.Provider, e.Environment, e.Kind, e.Status, e.AmountMinor, e.Currency, ref, key, e.Source, detail)
	return tag.RowsAffected() == 1, err
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
	rows, err := l.DB.Query(ctx, `SELECT id, invoice_id::text, provider, environment, kind, status, amount_minor, currency, provider_ref, source, detail, occurred_at
		FROM payment_ledger WHERE tenant_id = $1 ORDER BY id DESC LIMIT $2`, tenant, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Row{}
	for rows.Next() {
		var r Row
		if err := rows.Scan(&r.ID, &r.InvoiceID, &r.Provider, &r.Environment, &r.Kind, &r.Status, &r.AmountMinor, &r.Currency, &r.ProviderRef, &r.Source, &r.Detail, &r.OccurredAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Outcomes counts the distinct statuses recorded for one provider reference. More than one means the command's answer and the
// provider's confirmation disagree.
func (l Ledger) Outcomes(ctx context.Context, provider, kind, ref string) (int, error) {
	var n int
	err := l.DB.QueryRow(ctx, `SELECT count(DISTINCT status) FROM payment_ledger WHERE provider = $1 AND kind = $2 AND provider_ref = $3`, provider, kind, ref).Scan(&n)
	return n, err
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
	var f Fact
	var ref, detail *string
	err := l.DB.QueryRow(ctx, `SELECT status, provider_ref, detail FROM payment_ledger
		WHERE kind = 'charge' AND idempotency_key = $1 AND status IN ('succeeded','declined')
		ORDER BY (status = 'succeeded') DESC, id DESC LIMIT 1`, key).Scan(&f.Status, &ref, &detail)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Fact{}, false, nil
		}
		return Fact{}, false, err
	}
	if ref != nil {
		f.ProviderRef = *ref
	}
	if detail != nil {
		f.Detail = *detail
	}
	return f, true, nil
}
