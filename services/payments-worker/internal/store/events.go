package store

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Events is the inbox of provider notifications.
type Events struct{ DB *pgxpool.Pool }

// Receive records a notification. It reports false when the provider's event id was seen before (the provider retries).
func (e Events) Receive(ctx context.Context, provider, eventID string, body []byte) (bool, error) {
	tag, err := e.DB.Exec(ctx, `INSERT INTO provider_events (provider, event_id, body, status) VALUES ($1, $2, $3, 'received') ON CONFLICT DO NOTHING`, provider, eventID, body)
	return tag.RowsAffected() == 1, err
}

// Finish marks a notification processed, ignored or failed.
func (e Events) Finish(ctx context.Context, provider, eventID string, tenant uuid.UUID, status, errMsg string) error {
	var t, m any
	if tenant != uuid.Nil {
		t = tenant
	}
	if errMsg != "" {
		m = errMsg
	}
	_, err := e.DB.Exec(ctx, `UPDATE provider_events SET status = $3, tenant_id = $4, error = $5, processed_at = now() WHERE provider = $1 AND event_id = $2`,
		provider, eventID, status, t, m)
	return err
}

// Forget removes a notification that was received or failed (never a processed one) so the provider's retry is handled afresh instead of being taken for a duplicate.
func (e Events) Forget(ctx context.Context, provider, eventID string) error {
	_, err := e.DB.Exec(ctx, `DELETE FROM provider_events WHERE provider = $1 AND event_id = $2 AND status IN ('received', 'failed')`, provider, eventID)
	return err
}

// Attempt is the charge attempt a notification refers to.
type Attempt struct {
	TenantID    uuid.UUID
	InvoiceID   uuid.UUID
	Environment string
	Provider    string
	AmountMinor int64
	Currency    string
}

// ChargeByKey finds the most recent charge attempt made under an idempotency key (the provider's conversation id).
func (a Attempts) ChargeByKey(ctx context.Context, key string) (Attempt, bool, error) {
	var at Attempt
	err := a.DB.QueryRow(ctx, `SELECT tenant_id, invoice_id, environment, provider, COALESCE((request->>'amount_minor')::bigint, (request->>'amountMinor')::bigint, 0), COALESCE(request->>'currency', '')
		FROM payment_attempts WHERE kind = 'charge' AND idempotency_key = $1 ORDER BY created_at DESC LIMIT 1`, key).
		Scan(&at.TenantID, &at.InvoiceID, &at.Environment, &at.Provider, &at.AmountMinor, &at.Currency)
	if errors.Is(err, pgx.ErrNoRows) {
		return Attempt{}, false, nil
	}
	return at, err == nil, err
}

// ChargeProvider returns the provider the invoice was last charged through: refunds and status queries go to the same one.
func (a Attempts) ChargeProvider(ctx context.Context, tenant, invoice uuid.UUID) (string, bool, error) {
	var p string
	err := a.DB.QueryRow(ctx, `SELECT provider FROM payment_attempts WHERE tenant_id = $1 AND invoice_id = $2 AND kind = 'charge' ORDER BY created_at DESC LIMIT 1`, tenant, invoice).Scan(&p)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	return p, err == nil, err
}
