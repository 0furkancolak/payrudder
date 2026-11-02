package store

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/0furkancolak/payrudder/pkg/payments"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store/sqlc"
)

// Subscriptions is the table of subscriptions a provider runs.
type Subscriptions struct{ DB DB }

// ProviderSubscription is a subscription at a provider.
type ProviderSubscription struct {
	Provider                string     `json:"provider"`
	Environment             string     `json:"environment"`
	ProviderRef             string     `json:"provider_ref"`
	PayrudderSubscriptionID *string    `json:"quotify_subscription_id,omitempty"`
	Status                  string     `json:"status"`
	NextBilledAt            *time.Time `json:"next_billed_at,omitempty"`
	// ScheduledChange is what the provider has announced it will do to the subscription later (a cancellation at the end of the period leaves Status "active" until then);
	// absent when nothing is announced. Read-only: it is the provider's word as its last notification gave it.
	ScheduledChange *ScheduledChange `json:"scheduled_change,omitempty"`
	UpdatedAt       time.Time        `json:"updated_at"`
}

// ScheduledChange is a pending change to a subscription at its provider, in the provider's words.
type ScheduledChange struct {
	Action      string     `json:"action"`
	EffectiveAt time.Time  `json:"effective_at"`
	ResumeAt    *time.Time `json:"resume_at,omitempty"`
}

// Upsert records what the provider says about a subscription. Events can arrive out of order, so an older event never overwrites a
// newer one: occurred is the provider's timestamp of the event, and the row keeps the latest. sched is the change the event announces, nil for none: every event
// carries the whole subscription, so the row's announcement is replaced, and an event without one clears it.
func (s Subscriptions) Upsert(ctx context.Context, tenant uuid.UUID, provider, env, ref string, payrudderID *uuid.UUID, status string, next *time.Time, sched *payments.ScheduledChange, occurred time.Time) error {
	p := sqlc.UpsertProviderSubscriptionParams{
		TenantID: tenant, Provider: provider, Environment: env, ProviderRef: ref, PayrudderSubscriptionID: payrudderID,
		Status: status, NextBilledAt: next, UpdatedAt: occurred,
	}
	if sched != nil {
		p.ScheduledAction, p.ScheduledEffectiveAt, p.ScheduledResumeAt = &sched.Action, &sched.EffectiveAt, sched.ResumeAt
	}
	return at(ctx, s.DB, tenant, func(q *sqlc.Queries) error { return q.UpsertProviderSubscription(ctx, p) })
}

// List returns a tenant's provider subscriptions, most recently changed first.
func (s Subscriptions) List(ctx context.Context, tenant uuid.UUID) ([]ProviderSubscription, error) {
	rows, err := get(ctx, s.DB, tenant, func(q *sqlc.Queries) ([]sqlc.ListProviderSubscriptionsRow, error) {
		return q.ListProviderSubscriptions(ctx, tenant)
	})
	if err != nil {
		return nil, err
	}
	out := make([]ProviderSubscription, len(rows))
	for i, r := range rows {
		out[i] = ProviderSubscription{
			Provider: r.Provider, Environment: r.Environment, ProviderRef: r.ProviderRef,
			Status: r.Status, NextBilledAt: r.NextBilledAt, UpdatedAt: r.UpdatedAt,
		}
		if r.ScheduledAction != nil && r.ScheduledEffectiveAt != nil {
			out[i].ScheduledChange = &ScheduledChange{Action: *r.ScheduledAction, EffectiveAt: *r.ScheduledEffectiveAt, ResumeAt: r.ScheduledResumeAt}
		}
		if r.PayrudderSubscriptionID != nil {
			id := r.PayrudderSubscriptionID.String()
			out[i].PayrudderSubscriptionID = &id
		}
	}
	return out, nil
}

// PayrudderID returns our subscription id remembered for a provider subscription.
func (s Subscriptions) PayrudderID(ctx context.Context, tenant uuid.UUID, provider, env, ref string) (uuid.UUID, bool, error) {
	id, ok, err := lookup(ctx, s.DB, tenant, func(q *sqlc.Queries) (*uuid.UUID, error) {
		return q.GetProviderSubscriptionPayrudderID(ctx, sqlc.GetProviderSubscriptionPayrudderIDParams{TenantID: tenant, Provider: provider, Environment: env, ProviderRef: ref})
	})
	if !ok || id == nil {
		return uuid.Nil, false, err
	}
	return *id, true, nil
}

// NextBilledAt returns when the provider will next bill a subscription: the end of the period it has been paid for.
func (s Subscriptions) NextBilledAt(ctx context.Context, tenant uuid.UUID, provider, env, ref string) (time.Time, bool, error) {
	t, ok, err := lookup(ctx, s.DB, tenant, func(q *sqlc.Queries) (*time.Time, error) {
		return q.GetProviderSubscriptionNextBilledAt(ctx, sqlc.GetProviderSubscriptionNextBilledAtParams{TenantID: tenant, Provider: provider, Environment: env, ProviderRef: ref})
	})
	if !ok || t == nil {
		return time.Time{}, false, err
	}
	return *t, true, nil
}
