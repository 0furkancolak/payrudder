// Package changes registers the subscription change handlers of PlansService: request to command or query, result to response.
package changes

import (
	"context"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/changes/command"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/changes/query"
)

// Handler serves the change and adjustment methods of PlansService.
type Handler struct {
	cmd *command.Commands
	qry *query.Queries
}

// New returns the change handlers.
func New(cmd *command.Commands, qry *query.Queries) *Handler { return &Handler{cmd: cmd, qry: qry} }

func (h *Handler) PreviewSubscriptionChange(ctx context.Context, req *plansv1.PreviewSubscriptionChangeRequest) (*plansv1.PreviewSubscriptionChangeResponse, error) {
	quote, err := h.qry.Preview(ctx, req)
	if err != nil {
		return nil, err
	}
	return &plansv1.PreviewSubscriptionChangeResponse{Quote: quote}, nil
}

func (h *Handler) ChangeSubscription(ctx context.Context, req *plansv1.ChangeSubscriptionRequest) (*plansv1.ChangeSubscriptionResponse, error) {
	return h.cmd.Change(ctx, req)
}

func (h *Handler) CancelScheduledChange(ctx context.Context, req *plansv1.CancelScheduledChangeRequest) (*plansv1.CancelScheduledChangeResponse, error) {
	sub, err := h.cmd.CancelScheduled(ctx, req)
	if err != nil {
		return nil, err
	}
	return &plansv1.CancelScheduledChangeResponse{Subscription: sub}, nil
}

func (h *Handler) ApplyScheduledChanges(ctx context.Context, req *plansv1.ApplyScheduledChangesRequest) (*plansv1.ApplyScheduledChangesResponse, error) {
	applied, err := h.cmd.ApplyScheduled(ctx, req)
	if err != nil {
		return nil, err
	}
	return &plansv1.ApplyScheduledChangesResponse{Applied: applied}, nil
}

func (h *Handler) ListSubscriptionAdjustments(ctx context.Context, req *plansv1.ListSubscriptionAdjustmentsRequest) (*plansv1.ListSubscriptionAdjustmentsResponse, error) {
	adjustments, err := h.qry.Adjustments(ctx, req.GetPageSize())
	if err != nil {
		return nil, err
	}
	return &plansv1.ListSubscriptionAdjustmentsResponse{Adjustments: adjustments}, nil
}

func (h *Handler) MarkSubscriptionAdjustmentBilled(ctx context.Context, req *plansv1.MarkSubscriptionAdjustmentBilledRequest) (*plansv1.MarkSubscriptionAdjustmentBilledResponse, error) {
	if err := h.cmd.MarkAdjustmentBilled(ctx, req.GetAdjustmentId()); err != nil {
		return nil, err
	}
	return &plansv1.MarkSubscriptionAdjustmentBilledResponse{}, nil
}
