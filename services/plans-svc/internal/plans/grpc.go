// Package plans registers the plan handlers of PlansService: request to command or query, result to response.
package plans

import (
	"context"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/plans/command"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/plans/query"
)

// Handler serves the plan methods of PlansService.
type Handler struct {
	cmd *command.Commands
	qry *query.Queries
}

// New returns the plan handlers.
func New(cmd *command.Commands, qry *query.Queries) *Handler { return &Handler{cmd: cmd, qry: qry} }

func (h *Handler) CreatePlan(ctx context.Context, req *plansv1.CreatePlanRequest) (*plansv1.CreatePlanResponse, error) {
	p, err := h.cmd.Create(ctx, req)
	if err != nil {
		return nil, err
	}
	return &plansv1.CreatePlanResponse{Plan: p}, nil
}

func (h *Handler) ListPlans(ctx context.Context, _ *plansv1.ListPlansRequest) (*plansv1.ListPlansResponse, error) {
	ps, err := h.qry.List(ctx)
	if err != nil {
		return nil, err
	}
	return &plansv1.ListPlansResponse{Plans: ps}, nil
}

func (h *Handler) PublishPlanVersion(ctx context.Context, req *plansv1.PublishPlanVersionRequest) (*plansv1.PublishPlanVersionResponse, error) {
	pv, err := h.cmd.PublishVersion(ctx, req)
	if err != nil {
		return nil, err
	}
	return &plansv1.PublishPlanVersionResponse{PlanVersion: pv}, nil
}

func (h *Handler) GetPlanVersion(ctx context.Context, req *plansv1.GetPlanVersionRequest) (*plansv1.GetPlanVersionResponse, error) {
	pv, err := h.qry.Version(ctx, req)
	if err != nil {
		return nil, err
	}
	return &plansv1.GetPlanVersionResponse{PlanVersion: pv}, nil
}
