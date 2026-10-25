// Package fees registers the fixed fee handlers of PlansService: request to command or query, result to response.
package fees

import (
	"context"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/fees/command"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/fees/query"
)

// Handler serves the fee methods of PlansService.
type Handler struct {
	cmd *command.Commands
	qry *query.Queries
}

// New returns the fee handlers.
func New(cmd *command.Commands, qry *query.Queries) *Handler { return &Handler{cmd: cmd, qry: qry} }

func (h *Handler) ListSubscriptionsFeeDue(ctx context.Context, req *plansv1.ListSubscriptionsFeeDueRequest) (*plansv1.ListSubscriptionsFeeDueResponse, error) {
	due, err := h.qry.Due(ctx, req)
	if err != nil {
		return nil, err
	}
	return &plansv1.ListSubscriptionsFeeDueResponse{Due: due}, nil
}

func (h *Handler) AdvanceSubscriptionFee(ctx context.Context, req *plansv1.AdvanceSubscriptionFeeRequest) (*plansv1.AdvanceSubscriptionFeeResponse, error) {
	if err := h.cmd.Advance(ctx, req); err != nil {
		return nil, err
	}
	return &plansv1.AdvanceSubscriptionFeeResponse{}, nil
}
