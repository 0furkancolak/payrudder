package webhook

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/tenant"
)

// PlansClient talks to plans-svc's tenant-scoped API as billing-svc does: the tenant travels in the call's metadata.
type PlansClient struct{ Client plansv1.PlansServiceClient }

func as(ctx context.Context, id uuid.UUID) context.Context {
	return tenant.OutgoingContext(tenant.NewContext(ctx, tenant.Info{ID: id}))
}

// SyncProviderSubscription implements PlansAPI.
func (p PlansClient) SyncProviderSubscription(ctx context.Context, id uuid.UUID, req *plansv1.SyncProviderSubscriptionRequest) error {
	if _, err := p.Client.SyncProviderSubscription(as(ctx, id), req); err != nil {
		return fmt.Errorf("sync with plans-svc: %w", err)
	}
	return nil
}

// GetSubscription implements PlansAPI. The gRPC status is kept so callers can relay it.
func (p PlansClient) GetSubscription(ctx context.Context, id uuid.UUID, sub string) (*plansv1.Subscription, error) {
	r, err := p.Client.GetSubscription(as(ctx, id), &plansv1.GetSubscriptionRequest{SubscriptionId: sub})
	return r.GetSubscription(), err
}

// StartProviderMigration implements PlansAPI.
func (p PlansClient) StartProviderMigration(ctx context.Context, id uuid.UUID, req *plansv1.StartProviderMigrationRequest) (*plansv1.ProviderMigration, error) {
	r, err := p.Client.StartProviderMigration(as(ctx, id), req)
	return r.GetMigration(), err
}

// CompleteProviderMigration implements PlansAPI.
func (p PlansClient) CompleteProviderMigration(ctx context.Context, id uuid.UUID, req *plansv1.CompleteProviderMigrationRequest) (*plansv1.CompleteProviderMigrationResponse, error) {
	return p.Client.CompleteProviderMigration(as(ctx, id), req)
}

// AbortProviderMigration implements PlansAPI.
func (p PlansClient) AbortProviderMigration(ctx context.Context, id uuid.UUID, migration string) error {
	_, err := p.Client.AbortProviderMigration(as(ctx, id), &plansv1.AbortProviderMigrationRequest{MigrationId: migration})
	return err
}
