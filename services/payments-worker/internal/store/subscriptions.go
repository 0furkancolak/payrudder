package store

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/0furkancolak/quotify/services/payments-worker/internal/store/sqlc"
)

// Subscriptions is the table of subscriptions a provider runs.
type Subscriptions struct{ DB *pgxpool.Pool }

// ProviderSubscription is a subscription at a provider.
type ProviderSubscription struct {
	Provider              string     `json:"provider"`
	Environment           string     `json:"environment"`
	ProviderRef           string     `json:"provider_ref"`
	QuotifySubscriptionID *string    `json:"quotify_subscription_id,omitempty"`
	Status                string     `json:"status"`
	NextBilledAt          *time.Time `json:"next_billed_at,omitempty"`
	UpdatedAt             time.Time  `json:"updated_at"`
}

// Upsert records what the provider says about a subscription. Events can arrive out of order, so an older event never overwrites a
// newer one: occurred is the provider's timestamp of the event, and the row keeps the latest.
func (s Subscriptions) Upsert(ctx context.Context, tenant uuid.UUID, provider, env, ref string, quotifyID *uuid.UUID, status string, next *time.Time, occurred time.Time) error {
	return sqlc.New(s.DB).UpsertProviderSubscription(ctx, sqlc.UpsertProviderSubscriptionParams{
		TenantID: tenant, Provider: provider, Environment: env, ProviderRef: ref, QuotifySubscriptionID: quotifyID,
		Status: status, NextBilledAt: next, UpdatedAt: occurred,
	})
}

// List returns a tenant's provider subscriptions, most recently changed first.
func (s Subscriptions) List(ctx context.Context, tenant uuid.UUID) ([]ProviderSubscription, error) {
	rows, err := sqlc.New(s.DB).ListProviderSubscriptions(ctx, tenant)
	if err != nil {
		return nil, err
	}
	out := make([]ProviderSubscription, len(rows))
	for i, r := range rows {
		out[i] = ProviderSubscription{
			Provider: r.Provider, Environment: r.Environment, ProviderRef: r.ProviderRef,
			Status: r.Status, NextBilledAt: r.NextBilledAt, UpdatedAt: r.UpdatedAt,
		}
		if r.QuotifySubscriptionID != nil {
			id := r.QuotifySubscriptionID.String()
			out[i].QuotifySubscriptionID = &id
		}
	}
	return out, nil
}

// QuotifyID returns our subscription id remembered for a provider subscription.
func (s Subscriptions) QuotifyID(ctx context.Context, tenant uuid.UUID, provider, env, ref string) (uuid.UUID, bool, error) {
	id, ok, err := found(sqlc.New(s.DB).GetProviderSubscriptionQuotifyID(ctx, sqlc.GetProviderSubscriptionQuotifyIDParams{
		TenantID: tenant, Provider: provider, Environment: env, ProviderRef: ref,
	}))
	if !ok || id == nil {
		return uuid.Nil, false, err
	}
	return *id, true, nil
}

// NextBilledAt returns when the provider will next bill a subscription: the end of the period it has been paid for.
func (s Subscriptions) NextBilledAt(ctx context.Context, tenant uuid.UUID, provider, env, ref string) (time.Time, bool, error) {
	t, ok, err := found(sqlc.New(s.DB).GetProviderSubscriptionNextBilledAt(ctx, sqlc.GetProviderSubscriptionNextBilledAtParams{
		TenantID: tenant, Provider: provider, Environment: env, ProviderRef: ref,
	}))
	if !ok || t == nil {
		return time.Time{}, false, err
	}
	return *t, true, nil
}
