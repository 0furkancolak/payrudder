// Package subscriptions registers the subscription handlers of PlansService: request to command or query, result to response.
package subscriptions

import (
	"context"

	"google.golang.org/grpc"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/subscriptions/command"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/subscriptions/query"
)

// Handler serves the subscription methods of PlansService.
type Handler struct {
	cmd *command.Commands
	qry *query.Queries
}

// New returns the subscription handlers.
func New(cmd *command.Commands, qry *query.Queries) *Handler { return &Handler{cmd: cmd, qry: qry} }

func (h *Handler) CreateSubscription(ctx context.Context, req *plansv1.CreateSubscriptionRequest) (*plansv1.CreateSubscriptionResponse, error) {
	sub, err := h.cmd.Create(ctx, req)
	if err != nil {
		return nil, err
	}
	return &plansv1.CreateSubscriptionResponse{Subscription: sub}, nil
}

func (h *Handler) GetSubscription(ctx context.Context, req *plansv1.GetSubscriptionRequest) (*plansv1.GetSubscriptionResponse, error) {
	id, err := query.ParseID(req.GetSubscriptionId())
	if err != nil {
		return nil, err
	}
	sub, err := h.qry.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	return &plansv1.GetSubscriptionResponse{Subscription: sub}, nil
}

func (h *Handler) ListSubscriptions(ctx context.Context, req *plansv1.ListSubscriptionsRequest) (*plansv1.ListSubscriptionsResponse, error) {
	return h.qry.List(ctx, req)
}

func (h *Handler) ListSubscriptionsForPeriod(req *plansv1.ListSubscriptionsForPeriodRequest, stream grpc.ServerStreamingServer[plansv1.ListSubscriptionsForPeriodResponse]) error {
	return h.qry.ForPeriod(stream.Context(), req, func(sub *plansv1.Subscription) error {
		return stream.Send(&plansv1.ListSubscriptionsForPeriodResponse{Subscription: sub})
	})
}

func (h *Handler) CancelSubscription(ctx context.Context, req *plansv1.CancelSubscriptionRequest) (*plansv1.CancelSubscriptionResponse, error) {
	res, err := h.cmd.Cancel(ctx, req)
	if err != nil {
		return nil, err
	}
	return &plansv1.CancelSubscriptionResponse{Subscription: res.Subscription, CreditedMinor: res.CreditedMinor}, nil
}

func (h *Handler) PauseSubscription(ctx context.Context, req *plansv1.PauseSubscriptionRequest) (*plansv1.PauseSubscriptionResponse, error) {
	sub, err := h.cmd.Pause(ctx, req)
	if err != nil {
		return nil, err
	}
	return &plansv1.PauseSubscriptionResponse{Subscription: sub}, nil
}

func (h *Handler) ResumeSubscription(ctx context.Context, req *plansv1.ResumeSubscriptionRequest) (*plansv1.ResumeSubscriptionResponse, error) {
	sub, err := h.cmd.Resume(ctx, req)
	if err != nil {
		return nil, err
	}
	return &plansv1.ResumeSubscriptionResponse{Subscription: sub}, nil
}

func (h *Handler) ScheduleCancellation(ctx context.Context, req *plansv1.ScheduleCancellationRequest) (*plansv1.ScheduleCancellationResponse, error) {
	sub, err := h.cmd.ScheduleCancellation(ctx, req)
	if err != nil {
		return nil, err
	}
	return &plansv1.ScheduleCancellationResponse{Subscription: sub}, nil
}

func (h *Handler) ChangePlan(ctx context.Context, req *plansv1.ChangePlanRequest) (*plansv1.ChangePlanResponse, error) {
	res, err := h.cmd.ChangePlan(ctx, req)
	if err != nil {
		return nil, err
	}
	return &plansv1.ChangePlanResponse{Subscription: res.Subscription, OldPlanFeeMinor: res.OldPortion, NewPlanFeeMinor: res.NewPortion}, nil
}

func (h *Handler) SyncProviderSubscription(ctx context.Context, req *plansv1.SyncProviderSubscriptionRequest) (*plansv1.SyncProviderSubscriptionResponse, error) {
	sub, err := h.cmd.SyncProvider(ctx, req)
	if err != nil {
		return nil, err
	}
	return &plansv1.SyncProviderSubscriptionResponse{Subscription: sub}, nil
}
