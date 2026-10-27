package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store/sqlc"
)

// Events is the inbox of provider notifications. A notification is kept where its tenant's data is; one whose tenant is not known (yet) is kept in the pooled
// database, which is also where the inbox of a deployment without cells is.
type Events struct{ DB DB }

// ByTenant reports whether the inbox is kept per tenant, in the tenant's place: true when cells are on. Then the tenant of a notification has to be known before
// it is written, and the pooled database keeps only the notifications that belong to no tenant.
func (e Events) ByTenant() bool { return hasCells(e.DB) }

// Receive records a notification of the tenant (uuid.Nil when it is not known). It reports false when the provider's event id was seen before (the provider
// retries).
func (e Events) Receive(ctx context.Context, tenant uuid.UUID, provider, eventID string, body []byte) (bool, error) {
	n, err := get(ctx, e.DB, tenant, func(q *sqlc.Queries) (int64, error) {
		return q.ReceiveProviderEvent(ctx, sqlc.ReceiveProviderEventParams{Provider: provider, EventID: eventID, Body: body})
	})
	return n == 1, err
}

// Finish marks a notification processed, ignored or failed. tenant is where the notification was received, and the tenant it is recorded for.
func (e Events) Finish(ctx context.Context, provider, eventID string, tenant uuid.UUID, status, errMsg string) error {
	p := sqlc.FinishProviderEventParams{Provider: provider, EventID: eventID, Status: status}
	if tenant != uuid.Nil {
		p.TenantID = &tenant
	}
	if errMsg != "" {
		p.Error = &errMsg
	}
	return at(ctx, e.DB, tenant, func(q *sqlc.Queries) error { return q.FinishProviderEvent(ctx, p) })
}

// Forget removes a notification that was received or failed (never a processed one) so the provider's retry is handled afresh instead of being taken for a
// duplicate. tenant is where it was received.
func (e Events) Forget(ctx context.Context, tenant uuid.UUID, provider, eventID string) error {
	return at(ctx, e.DB, tenant, func(q *sqlc.Queries) error {
		return q.ForgetProviderEvent(ctx, sqlc.ForgetProviderEventParams{Provider: provider, EventID: eventID})
	})
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

func attemptOf(r sqlc.GetChargeAttemptByKeyRow) Attempt {
	return Attempt{TenantID: r.TenantID, InvoiceID: r.InvoiceID, Environment: r.Environment, Provider: r.Provider, AmountMinor: r.AmountMinor, Currency: r.Currency}
}

// ChargeByKey finds the most recent charge attempt made under an idempotency key (the provider's conversation id), whoever's it is: the way to the tenant of a
// notification whose URL names none. With cells off it is one query. With cells on it asks for the key's tenant where the keys of all tenants are indexed (the
// pooled table, and shared.charge_keys of each cell: one query per place, never one per tenant) and then reads the attempt in that tenant's place, so what it
// returns is the tenant's own row. A tenant that is moving makes it fail (tenantdir.ErrNotServing): the notification is retried, never dropped.
func (a Attempts) ChargeByKey(ctx context.Context, key string) (Attempt, bool, error) {
	r, ok := a.DB.(*pgxx.Router)
	if !ok || !hasCells(a.DB) {
		return a.ChargeOf(ctx, uuid.Nil, key)
	}
	tenant, ok, err := a.tenantOfKey(ctx, r, key)
	if !ok {
		return Attempt{}, false, err
	}
	return a.ChargeOf(ctx, tenant, key)
}

// tenantOfKey is the tenant that made the newest charge under key, from the pooled table or from the index of a cell.
func (a Attempts) tenantOfKey(ctx context.Context, r *pgxx.Router, key string) (uuid.UUID, bool, error) {
	row, ok, err := found(sqlc.New(r.Pooled()).GetChargeAttemptByKey(ctx, key))
	if err != nil {
		return uuid.Nil, false, err
	}
	if ok {
		return row.TenantID, true, nil
	}
	for _, cell := range r.CellNames() {
		pool, err := r.Cell(cell)
		if err != nil {
			return uuid.Nil, false, err
		}
		var tenant uuid.UUID
		err = pool.QueryRow(ctx, `SELECT tenant_id FROM shared.charge_keys WHERE idempotency_key = $1`, key).Scan(&tenant)
		switch code := pgxx.ErrCode(err); {
		case err == nil:
			return tenant, true, nil
		case errors.Is(err, pgx.ErrNoRows):
		case code == "42P01" || code == "3F000":
			// No tenant of the cell has been provisioned yet: there is no index, so no key in it.
		default:
			return uuid.Nil, false, fmt.Errorf("store: charge key index of cell %s: %w", cell, err)
		}
	}
	return uuid.Nil, false, nil
}

// ChargeOf finds the most recent charge attempt of the tenant made under an idempotency key. With a nil tenant it is the pooled database's, whoever's it is.
func (a Attempts) ChargeOf(ctx context.Context, tenant uuid.UUID, key string) (Attempt, bool, error) {
	if err := requireTenant(a.DB, tenant); err != nil {
		return Attempt{}, false, err
	}
	row, ok, err := lookup(ctx, a.DB, tenant, func(q *sqlc.Queries) (sqlc.GetChargeAttemptByKeyRow, error) { return q.GetChargeAttemptByKey(ctx, key) })
	if !ok {
		return Attempt{}, false, err
	}
	return attemptOf(row), true, nil
}

// ChargeProvider returns the provider the invoice was last charged through: refunds and status queries go to the same one.
func (a Attempts) ChargeProvider(ctx context.Context, tenant, invoice uuid.UUID) (string, bool, error) {
	return lookup(ctx, a.DB, tenant, func(q *sqlc.Queries) (string, error) {
		return q.GetChargeProvider(ctx, sqlc.GetChargeProviderParams{TenantID: tenant, InvoiceID: invoice})
	})
}
