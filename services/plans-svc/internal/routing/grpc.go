// Package routing registers the routing rule handlers of PlansService: request to command or query, result to response.
package routing

import (
	"context"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/routing/command"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/routing/query"
)

// Handler serves the routing methods of PlansService.
type Handler struct {
	cmd *command.Commands
	qry *query.Queries
}

// New returns the routing handlers.
func New(cmd *command.Commands, qry *query.Queries) *Handler { return &Handler{cmd: cmd, qry: qry} }

func (h *Handler) GetRoutingRules(ctx context.Context, _ *plansv1.GetRoutingRulesRequest) (*plansv1.GetRoutingRulesResponse, error) {
	rules, err := h.qry.List(ctx)
	if err != nil {
		return nil, err
	}
	return &plansv1.GetRoutingRulesResponse{Rules: rules}, nil
}

func (h *Handler) SetRoutingRules(ctx context.Context, req *plansv1.SetRoutingRulesRequest) (*plansv1.SetRoutingRulesResponse, error) {
	rules, err := h.cmd.SetRules(ctx, req.GetRules())
	if err != nil {
		return nil, err
	}
	return &plansv1.SetRoutingRulesResponse{Rules: rules}, nil
}

func (h *Handler) PreviewRouting(ctx context.Context, req *plansv1.PreviewRoutingRequest) (*plansv1.PreviewRoutingResponse, error) {
	return h.qry.Preview(ctx, req.GetCountry(), req.GetCurrency())
}
