package webhook

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/tenant"
)

// PlansClient talks to plans-svc's tenant-scoped API as billing-svc does: the tenant travels in the call's metadata.
type PlansClient struct {
	Client plansv1.PlansServiceClient
	// Admin reads tenant settings (the currency); optional.
	Admin plansv1.AdminServiceClient
}

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

// GetPlanVersion implements PlansAPI. The gRPC status is kept so callers can relay it.
func (p PlansClient) GetPlanVersion(ctx context.Context, id uuid.UUID, req *plansv1.GetPlanVersionRequest) (*plansv1.PlanVersion, error) {
	r, err := p.Client.GetPlanVersion(as(ctx, id), req)
	return r.GetPlanVersion(), err
}

// GetCustomer implements PlansAPI.
func (p PlansClient) GetCustomer(ctx context.Context, id uuid.UUID, externalID string) (*plansv1.Customer, error) {
	r, err := p.Client.GetCustomer(as(ctx, id), &plansv1.GetCustomerRequest{ExternalId: externalID})
	return r.GetCustomer(), err
}

// PreviewRouting implements PlansAPI.
func (p PlansClient) PreviewRouting(ctx context.Context, id uuid.UUID, country, currency string) (string, error) {
	r, err := p.Client.PreviewRouting(as(ctx, id), &plansv1.PreviewRoutingRequest{Country: country, Currency: currency})
	return r.GetProvider(), err
}

// SetBankTransfer implements PlansAPI: it replaces the bank details printed on the tenant's invoices, or clears them for nil.
func (p PlansClient) SetBankTransfer(ctx context.Context, id uuid.UUID, d *plansv1.BankTransferDetails) error {
	if p.Admin == nil {
		return fmt.Errorf("plans-svc admin API is not configured")
	}
	req := &plansv1.UpdateTenantSettingsRequest{TenantId: id.String(), BankTransfer: d, ClearBankTransfer: d == nil}
	_, err := p.Admin.UpdateTenantSettings(tenant.OutgoingContext(tenant.WithOperator(ctx)), req)
	return err
}

// TenantCurrency implements PlansAPI: the currency of base_fee_minor.
func (p PlansClient) TenantCurrency(ctx context.Context, id uuid.UUID) (string, error) {
	if p.Admin == nil {
		return "", fmt.Errorf("plans-svc admin API is not configured")
	}
	r, err := p.Admin.GetTenant(tenant.OutgoingContext(tenant.WithOperator(ctx)), &plansv1.GetTenantRequest{TenantId: id.String()})
	return r.GetTenant().GetCurrency(), err
}
