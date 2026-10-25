// Package inputs reads what rating needs from plans-svc and the aggregator: the tenant, the customer, the subscriptions of a period, the plan versions in
// force, the meters, the tax rates and the usage. The pure pricing is in the rating package; this one is the I/O around it, shared by the billing runs, the fee
// invoices, the re-rating jobs and the payment links.
package inputs

import (
	"context"
	"errors"
	"fmt"

	"github.com/cockroachdb/apd/v3"
	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	aggregatorv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/aggregator/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/env"
	"github.com/0furkancolak/payrudder/pkg/tenant"
)

// Aggregator is the part of the aggregator billing uses.
type Aggregator interface {
	StreamPeriodUsage(ctx context.Context, in *aggregatorv1.StreamPeriodUsageRequest, opts ...grpc.CallOption) (grpc.ServerStreamingClient[aggregatorv1.StreamPeriodUsageResponse], error)
	StreamLateUsage(ctx context.Context, in *aggregatorv1.StreamLateUsageRequest, opts ...grpc.CallOption) (grpc.ServerStreamingClient[aggregatorv1.StreamLateUsageResponse], error)
	GetUsageBatch(ctx context.Context, in *aggregatorv1.GetUsageBatchRequest, opts ...grpc.CallOption) (*aggregatorv1.GetUsageBatchResponse, error)
	GetIngestWatermark(ctx context.Context, in *aggregatorv1.GetIngestWatermarkRequest, opts ...grpc.CallOption) (*aggregatorv1.GetIngestWatermarkResponse, error)
	FreezePeriod(ctx context.Context, in *aggregatorv1.FreezePeriodRequest, opts ...grpc.CallOption) (*aggregatorv1.FreezePeriodResponse, error)
	ClosePeriodEarly(ctx context.Context, in *aggregatorv1.ClosePeriodEarlyRequest, opts ...grpc.CallOption) (*aggregatorv1.ClosePeriodEarlyResponse, error)
}

// Plans is the part of plans-svc billing uses (PlansService and AdminService).
type Plans interface {
	GetCustomer(ctx context.Context, in *plansv1.GetCustomerRequest, opts ...grpc.CallOption) (*plansv1.GetCustomerResponse, error)
	ListMeters(ctx context.Context, in *plansv1.ListMetersRequest, opts ...grpc.CallOption) (*plansv1.ListMetersResponse, error)
	GetPlanVersion(ctx context.Context, in *plansv1.GetPlanVersionRequest, opts ...grpc.CallOption) (*plansv1.GetPlanVersionResponse, error)
	ListSubscriptionsForPeriod(ctx context.Context, in *plansv1.ListSubscriptionsForPeriodRequest, opts ...grpc.CallOption) (grpc.ServerStreamingClient[plansv1.ListSubscriptionsForPeriodResponse], error)
	GetTaxRates(ctx context.Context, in *plansv1.GetTaxRatesRequest, opts ...grpc.CallOption) (*plansv1.GetTaxRatesResponse, error)
	ListSubscriptionsFeeDue(ctx context.Context, in *plansv1.ListSubscriptionsFeeDueRequest, opts ...grpc.CallOption) (*plansv1.ListSubscriptionsFeeDueResponse, error)
	AdvanceSubscriptionFee(ctx context.Context, in *plansv1.AdvanceSubscriptionFeeRequest, opts ...grpc.CallOption) (*plansv1.AdvanceSubscriptionFeeResponse, error)
	ApplyScheduledChanges(ctx context.Context, in *plansv1.ApplyScheduledChangesRequest, opts ...grpc.CallOption) (*plansv1.ApplyScheduledChangesResponse, error)
	ListSubscriptionAdjustments(ctx context.Context, in *plansv1.ListSubscriptionAdjustmentsRequest, opts ...grpc.CallOption) (*plansv1.ListSubscriptionAdjustmentsResponse, error)
	MarkSubscriptionAdjustmentBilled(ctx context.Context, in *plansv1.MarkSubscriptionAdjustmentBilledRequest, opts ...grpc.CallOption) (*plansv1.MarkSubscriptionAdjustmentBilledResponse, error)
	RedeemPromotion(ctx context.Context, in *plansv1.RedeemPromotionRequest, opts ...grpc.CallOption) (*plansv1.RedeemPromotionResponse, error)
	ReversePromotion(ctx context.Context, in *plansv1.ReversePromotionRequest, opts ...grpc.CallOption) (*plansv1.ReversePromotionResponse, error)
}

// Admin is the part of plans-svc's AdminService billing uses.
type Admin interface {
	GetTenant(ctx context.Context, in *plansv1.GetTenantRequest, opts ...grpc.CallOption) (*plansv1.GetTenantResponse, error)
	ListTenants(ctx context.Context, in *plansv1.ListTenantsRequest, opts ...grpc.CallOption) (*plansv1.ListTenantsResponse, error)
}

// Inputs reads rating's inputs from the services that own them.
type Inputs struct {
	Plans      Plans
	Admin      Admin
	Aggregator Aggregator
}

// permanent marks an error that retrying cannot fix: the run goes to FAILED for the operator.
type permanent struct{ error }

func (p permanent) Unwrap() error { return p.error }

// Permanent marks err as one that retrying cannot fix.
func Permanent(err error) error { return permanent{err} }

// IsTransient reports whether retrying may help, which is so unless the error is Permanent.
func IsTransient(err error) bool {
	var p permanent
	return !errors.As(err, &p)
}

// IsPermanent reports whether err is one that retrying cannot fix.
func IsPermanent(err error) bool { return !IsTransient(err) }

// TenantCtx returns a context carrying the tenant for calls to tenant-scoped services.
func TenantCtx(ctx context.Context, id uuid.UUID) context.Context {
	return tenant.OutgoingContext(tenant.NewContext(ctx, tenant.Info{ID: id}))
}

// OperatorCtx returns a context for operator-only calls.
func OperatorCtx(ctx context.Context) context.Context {
	return tenant.OutgoingContext(tenant.WithOperator(ctx))
}

// Tenant reads a tenant.
func (in *Inputs) Tenant(ctx context.Context, tenantID uuid.UUID) (*plansv1.Tenant, error) {
	resp, err := in.Admin.GetTenant(OperatorCtx(ctx), &plansv1.GetTenantRequest{TenantId: tenantID.String()})
	if err != nil {
		return nil, fmt.Errorf("get tenant: %w", err)
	}
	return resp.GetTenant(), nil
}

// Customer is a customer of a tenant as plans-svc knows it; ok is false when there is none.
func (in *Inputs) Customer(ctx context.Context, tenantID uuid.UUID, externalID string) (*plansv1.Customer, bool, error) {
	resp, err := in.Plans.GetCustomer(TenantCtx(ctx, tenantID), &plansv1.GetCustomerRequest{ExternalId: externalID})
	if status.Code(err) == codes.NotFound {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if resp.GetCustomer() == nil {
		return nil, false, nil
	}
	return resp.GetCustomer(), true, nil
}

// Environment is the environment frozen into an invoice's snapshot. A tenant that names none yields "", which no snapshot is allowed to hold (a CHECK in
// billing_db and the checks of every command refuse it): it is never read as live.
func Environment(t *plansv1.Tenant) string {
	e, err := env.FromProto(t.GetEnvironment())
	if err != nil {
		return ""
	}
	return e.String()
}

// LogoKey is the object key of the tenant's invoice logo, an assets-svc object under {tenant}/assets/{asset id}.
func LogoKey(tenantID uuid.UUID, t *plansv1.Tenant) string {
	if id := t.GetInvoiceLogoAssetId(); id != "" {
		return tenantID.String() + "/assets/" + id
	}
	return ""
}

// Billable reports whether the scheduler may raise invoices for a tenant: a live tenant that is not active yet (the go-live gate) or is suspended is
// left alone. A status the plans-svc did not send (an older release during a rollout) counts as active.
func Billable(t *plansv1.Tenant) bool {
	switch t.GetStatus() {
	case plansv1.TenantStatus_TENANT_STATUS_INACTIVE, plansv1.TenantStatus_TENANT_STATUS_SUSPENDED:
		return false
	case plansv1.TenantStatus_TENANT_STATUS_UNSPECIFIED, plansv1.TenantStatus_TENANT_STATUS_ACTIVE:
	}
	return true
}

// OrDefault returns v, or def when v is empty: a subscription without a currency is in the tenant's.
func OrDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// Decimal parses a decimal quantity or price.
func Decimal(s string) (*apd.Decimal, error) {
	d, _, err := apd.NewFromString(s)
	if err != nil {
		return nil, fmt.Errorf("invalid decimal %q: %w", s, err)
	}
	return d, nil
}

// tenantPage is how many tenants one ListTenants call asks for.
const tenantPage = 500

// EachBillableTenant calls fn for every tenant the scheduler may raise invoices for (see Billable), page by page. A tenant whose id is not a UUID is skipped.
// The walk stops at the first error fn returns.
func (in *Inputs) EachBillableTenant(ctx context.Context, fn func(id uuid.UUID, t *plansv1.Tenant) error) error {
	token := ""
	for {
		resp, err := in.Admin.ListTenants(OperatorCtx(ctx), &plansv1.ListTenantsRequest{PageSize: tenantPage, PageToken: token})
		if err != nil {
			return err
		}
		for _, t := range resp.GetTenants() {
			if !Billable(t) {
				continue
			}
			id, err := uuid.Parse(t.GetId())
			if err != nil {
				continue
			}
			if err := fn(id, t); err != nil {
				return err
			}
		}
		if resp.GetNextPageToken() == "" {
			return nil
		}
		token = resp.GetNextPageToken()
	}
}
