package webhook

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/tenant"
)

// PlansClient syncs through plans-svc's tenant-scoped API, as billing-svc reads it: the tenant travels in the call's metadata.
type PlansClient struct{ Client plansv1.PlansServiceClient }

// SyncProviderSubscription implements PlansSync.
func (p PlansClient) SyncProviderSubscription(ctx context.Context, id uuid.UUID, req *plansv1.SyncProviderSubscriptionRequest) error {
	ctx = tenant.OutgoingContext(tenant.NewContext(ctx, tenant.Info{ID: id}))
	if _, err := p.Client.SyncProviderSubscription(ctx, req); err != nil {
		return fmt.Errorf("sync with plans-svc: %w", err)
	}
	return nil
}
