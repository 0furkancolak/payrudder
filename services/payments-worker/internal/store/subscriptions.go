package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
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
	var q any
	if quotifyID != nil {
		q = *quotifyID
	}
	_, err := s.DB.Exec(ctx, `INSERT INTO provider_subscriptions (tenant_id, provider, environment, provider_ref, quotify_subscription_id, status, next_billed_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT (tenant_id, provider, environment, provider_ref) DO UPDATE
		SET status = EXCLUDED.status, next_billed_at = EXCLUDED.next_billed_at,
		    quotify_subscription_id = COALESCE(EXCLUDED.quotify_subscription_id, provider_subscriptions.quotify_subscription_id), updated_at = EXCLUDED.updated_at
		WHERE provider_subscriptions.updated_at <= EXCLUDED.updated_at`,
		tenant, provider, env, ref, q, status, next, occurred)
	return err
}

// List returns a tenant's provider subscriptions, most recently changed first.
func (s Subscriptions) List(ctx context.Context, tenant uuid.UUID) ([]ProviderSubscription, error) {
	rows, err := s.DB.Query(ctx, `SELECT provider, environment, provider_ref, quotify_subscription_id::text, status, next_billed_at, updated_at
		FROM provider_subscriptions WHERE tenant_id = $1 ORDER BY updated_at DESC LIMIT 500`, tenant)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ProviderSubscription{}
	for rows.Next() {
		var p ProviderSubscription
		if err := rows.Scan(&p.Provider, &p.Environment, &p.ProviderRef, &p.QuotifySubscriptionID, &p.Status, &p.NextBilledAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// QuotifyID returns our subscription id remembered for a provider subscription.
func (s Subscriptions) QuotifyID(ctx context.Context, tenant uuid.UUID, provider, env, ref string) (uuid.UUID, bool, error) {
	var id *uuid.UUID
	err := s.DB.QueryRow(ctx, `SELECT quotify_subscription_id FROM provider_subscriptions WHERE tenant_id = $1 AND provider = $2 AND environment = $3 AND provider_ref = $4`,
		tenant, provider, env, ref).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && id == nil) {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, err
	}
	return *id, true, nil
}

// NextBilledAt returns when the provider will next bill a subscription: the end of the period it has been paid for.
func (s Subscriptions) NextBilledAt(ctx context.Context, tenant uuid.UUID, provider, env, ref string) (time.Time, bool, error) {
	var t *time.Time
	err := s.DB.QueryRow(ctx, `SELECT next_billed_at FROM provider_subscriptions WHERE tenant_id = $1 AND provider = $2 AND environment = $3 AND provider_ref = $4`,
		tenant, provider, env, ref).Scan(&t)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && t == nil) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	return *t, true, nil
}
