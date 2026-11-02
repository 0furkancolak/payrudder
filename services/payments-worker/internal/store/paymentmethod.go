package store

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store/sqlc"
)

// PaymentMethods remembers when the newest saved-payment-method notification of a customer happened, so that an older one that arrives later is not applied.
type PaymentMethods struct{ DB DB }

// AppliedAt returns the provider's time of the newest notification applied to the customer, and whether there is one.
func (p PaymentMethods) AppliedAt(ctx context.Context, tenant uuid.UUID, provider, env, customer string) (time.Time, bool, error) {
	return lookup(ctx, p.DB, tenant, func(q *sqlc.Queries) (time.Time, error) {
		return q.GetPaymentMethodApplied(ctx, sqlc.GetPaymentMethodAppliedParams{TenantID: tenant, Provider: provider, Environment: env, CustomerRef: customer})
	})
}

// MarkApplied records the time of a notification that was applied; the stored time only moves forward.
func (p PaymentMethods) MarkApplied(ctx context.Context, tenant uuid.UUID, provider, env, customer string, when time.Time) error {
	return at(ctx, p.DB, tenant, func(q *sqlc.Queries) error {
		return q.PutPaymentMethodApplied(ctx, sqlc.PutPaymentMethodAppliedParams{TenantID: tenant, Provider: provider, Environment: env, CustomerRef: customer, OccurredAt: when})
	})
}
