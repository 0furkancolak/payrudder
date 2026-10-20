// Package webhookendpoints registers the handlers of WebhookService: request to command or query, result to response.
package webhookendpoints

import (
	"context"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/webhookendpoints/command"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/webhookendpoints/query"
)

// Handler serves plansv1.WebhookService.
type Handler struct {
	plansv1.UnimplementedWebhookServiceServer
	cmd *command.Commands
	qry *query.Queries
}

// New returns the webhook handlers.
func New(cmd *command.Commands, qry *query.Queries) *Handler { return &Handler{cmd: cmd, qry: qry} }

func (h *Handler) CreateWebhookEndpoint(ctx context.Context, req *plansv1.CreateWebhookEndpointRequest) (*plansv1.CreateWebhookEndpointResponse, error) {
	ep, err := h.cmd.CreateEndpoint(ctx, req)
	if err != nil {
		return nil, err
	}
	return &plansv1.CreateWebhookEndpointResponse{Endpoint: ep}, nil
}

func (h *Handler) ListWebhookEndpoints(ctx context.Context, _ *plansv1.ListWebhookEndpointsRequest) (*plansv1.ListWebhookEndpointsResponse, error) {
	eps, err := h.qry.Endpoints(ctx)
	if err != nil {
		return nil, err
	}
	return &plansv1.ListWebhookEndpointsResponse{Endpoints: eps}, nil
}

func (h *Handler) UpdateWebhookEndpoint(ctx context.Context, req *plansv1.UpdateWebhookEndpointRequest) (*plansv1.UpdateWebhookEndpointResponse, error) {
	ep, err := h.cmd.UpdateEndpoint(ctx, req)
	if err != nil {
		return nil, err
	}
	return &plansv1.UpdateWebhookEndpointResponse{Endpoint: ep}, nil
}

func (h *Handler) DeleteWebhookEndpoint(ctx context.Context, req *plansv1.DeleteWebhookEndpointRequest) (*plansv1.DeleteWebhookEndpointResponse, error) {
	if err := h.cmd.DeleteEndpoint(ctx, req.GetId()); err != nil {
		return nil, err
	}
	return &plansv1.DeleteWebhookEndpointResponse{}, nil
}

func (h *Handler) RotateWebhookSecret(ctx context.Context, req *plansv1.RotateWebhookSecretRequest) (*plansv1.RotateWebhookSecretResponse, error) {
	ep, err := h.cmd.RotateSecret(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	return &plansv1.RotateWebhookSecretResponse{Endpoint: ep}, nil
}

func (h *Handler) SendTestWebhook(ctx context.Context, req *plansv1.SendTestWebhookRequest) (*plansv1.SendTestWebhookResponse, error) {
	d, err := h.cmd.SendTest(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	return &plansv1.SendTestWebhookResponse{Delivery: d}, nil
}

func (h *Handler) ListWebhookDeliveries(ctx context.Context, req *plansv1.ListWebhookDeliveriesRequest) (*plansv1.ListWebhookDeliveriesResponse, error) {
	return h.qry.Deliveries(ctx, req)
}

func (h *Handler) RedeliverWebhook(ctx context.Context, req *plansv1.RedeliverWebhookRequest) (*plansv1.RedeliverWebhookResponse, error) {
	d, err := h.cmd.Redeliver(ctx, req.GetDeliveryId())
	if err != nil {
		return nil, err
	}
	return &plansv1.RedeliverWebhookResponse{Delivery: d}, nil
}

func (h *Handler) RecordTenantEvent(ctx context.Context, req *plansv1.RecordTenantEventRequest) (*plansv1.RecordTenantEventResponse, error) {
	queued, err := h.cmd.RecordEvent(ctx, req)
	if err != nil {
		return nil, err
	}
	return &plansv1.RecordTenantEventResponse{Queued: queued}, nil
}
