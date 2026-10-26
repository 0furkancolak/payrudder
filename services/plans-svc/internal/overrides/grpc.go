// Package overrides registers the entitlement override handlers of PlansService: request to command or query, result to response.
package overrides

import (
	"context"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/overrides/command"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/overrides/query"
)

// Handler serves the override methods of PlansService.
type Handler struct {
	cmd *command.Commands
	qry *query.Queries
}

// New returns the override handlers.
func New(cmd *command.Commands, qry *query.Queries) *Handler { return &Handler{cmd: cmd, qry: qry} }

func (h *Handler) SetEntitlementOverride(ctx context.Context, req *plansv1.SetEntitlementOverrideRequest) (*plansv1.SetEntitlementOverrideResponse, error) {
	o, err := h.cmd.Set(ctx, req)
	if err != nil {
		return nil, err
	}
	return &plansv1.SetEntitlementOverrideResponse{Override: o}, nil
}

func (h *Handler) DeleteEntitlementOverride(ctx context.Context, req *plansv1.DeleteEntitlementOverrideRequest) (*plansv1.DeleteEntitlementOverrideResponse, error) {
	if err := h.cmd.Delete(ctx, req); err != nil {
		return nil, err
	}
	return &plansv1.DeleteEntitlementOverrideResponse{}, nil
}

func (h *Handler) ListEntitlementOverrides(ctx context.Context, req *plansv1.ListEntitlementOverridesRequest) (*plansv1.ListEntitlementOverridesResponse, error) {
	os, err := h.qry.List(ctx, req.GetCustomerExternalId())
	if err != nil {
		return nil, err
	}
	return &plansv1.ListEntitlementOverridesResponse{Overrides: os}, nil
}
