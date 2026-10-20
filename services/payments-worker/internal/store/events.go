package store

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/0furkancolak/quotify/services/payments-worker/internal/store/sqlc"
)

// Events is the inbox of provider notifications.
type Events struct{ DB *pgxpool.Pool }

// Receive records a notification. It reports false when the provider's event id was seen before (the provider retries).
func (e Events) Receive(ctx context.Context, provider, eventID string, body []byte) (bool, error) {
	n, err := sqlc.New(e.DB).ReceiveProviderEvent(ctx, sqlc.ReceiveProviderEventParams{Provider: provider, EventID: eventID, Body: body})
	return n == 1, err
}

// Finish marks a notification processed, ignored or failed.
func (e Events) Finish(ctx context.Context, provider, eventID string, tenant uuid.UUID, status, errMsg string) error {
	p := sqlc.FinishProviderEventParams{Provider: provider, EventID: eventID, Status: status}
	if tenant != uuid.Nil {
		p.TenantID = &tenant
	}
	if errMsg != "" {
		p.Error = &errMsg
	}
	return sqlc.New(e.DB).FinishProviderEvent(ctx, p)
}

// Forget removes a notification that was received or failed (never a processed one) so the provider's retry is handled afresh instead of being taken for a duplicate.
func (e Events) Forget(ctx context.Context, provider, eventID string) error {
	return sqlc.New(e.DB).ForgetProviderEvent(ctx, sqlc.ForgetProviderEventParams{Provider: provider, EventID: eventID})
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
	r, err := sqlc.New(a.DB).GetChargeAttemptByKey(ctx, key)
	if err != nil {
		return Attempt{}, false, notFound(err)
	}
	return Attempt{TenantID: r.TenantID, InvoiceID: r.InvoiceID, Environment: r.Environment, Provider: r.Provider, AmountMinor: r.AmountMinor, Currency: r.Currency}, true, nil
}

// ChargeProvider returns the provider the invoice was last charged through: refunds and status queries go to the same one.
func (a Attempts) ChargeProvider(ctx context.Context, tenant, invoice uuid.UUID) (string, bool, error) {
	p, err := sqlc.New(a.DB).GetChargeProvider(ctx, sqlc.GetChargeProviderParams{TenantID: tenant, InvoiceID: invoice})
	return found(p, err)
}
