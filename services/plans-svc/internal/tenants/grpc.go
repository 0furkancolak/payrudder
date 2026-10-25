// Package tenants is the module behind the tenant calls of AdminService: an operator looks tenants up, changes their settings and their status.
// The packages beside it, tenants/query for the caller's own tenant, are what the other modules read.
package tenants

import (
	"context"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/tenants/command"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/tenants/query"
)

// Handler serves the tenant calls of AdminService.
type Handler struct {
	cmd *command.Commands
	qry *query.Queries
}

// New returns the tenant handlers.
func New(cmd *command.Commands, qry *query.Queries) *Handler { return &Handler{cmd: cmd, qry: qry} }

func (h *Handler) GetTenant(ctx context.Context, req *plansv1.GetTenantRequest) (*plansv1.GetTenantResponse, error) {
	if err := tenant.RequireOperator(ctx); err != nil {
		return nil, err
	}
	id, err := domain.ParseID("tenant_id", req.GetTenantId())
	if err != nil {
		return nil, err
	}
	t, err := h.qry.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	return &plansv1.GetTenantResponse{Tenant: t}, nil
}

func (h *Handler) ListTenants(ctx context.Context, req *plansv1.ListTenantsRequest) (*plansv1.ListTenantsResponse, error) {
	if err := tenant.RequireOperator(ctx); err != nil {
		return nil, err
	}
	tenants, next, err := h.qry.List(ctx, req.GetPageSize(), req.GetPageToken())
	if err != nil {
		return nil, err
	}
	return &plansv1.ListTenantsResponse{Tenants: tenants, NextPageToken: next}, nil
}

func (h *Handler) UpdateTenantSettings(ctx context.Context, req *plansv1.UpdateTenantSettingsRequest) (*plansv1.UpdateTenantSettingsResponse, error) {
	if err := tenant.RequireOperator(ctx); err != nil {
		return nil, err
	}
	t, err := h.cmd.UpdateSettings(ctx, req)
	if err != nil {
		return nil, err
	}
	return &plansv1.UpdateTenantSettingsResponse{Tenant: t}, nil
}

func (h *Handler) SetTenantStatus(ctx context.Context, req *plansv1.SetTenantStatusRequest) (*plansv1.SetTenantStatusResponse, error) {
	if err := tenant.RequireOperator(ctx); err != nil {
		return nil, err
	}
	t, err := h.cmd.SetStatus(ctx, req)
	if err != nil {
		return nil, err
	}
	return &plansv1.SetTenantStatusResponse{Tenant: t}, nil
}
