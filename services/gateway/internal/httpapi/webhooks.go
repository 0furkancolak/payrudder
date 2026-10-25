package httpapi

import (
	"net/http"
	"strconv"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
)

func (s *Server) createWebhookEndpoint(w http.ResponseWriter, r *http.Request) {
	req := &plansv1.CreateWebhookEndpointRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	resp, err := s.d.Webhooks.CreateWebhookEndpoint(r.Context(), req)
	respond(w, r, http.StatusCreated, resp.GetEndpoint(), err)
}

func (s *Server) listWebhookEndpoints(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Webhooks.ListWebhookEndpoints(r.Context(), &plansv1.ListWebhookEndpointsRequest{})
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) updateWebhookEndpoint(w http.ResponseWriter, r *http.Request) {
	req := &plansv1.UpdateWebhookEndpointRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	req.Id = r.PathValue("id")
	resp, err := s.d.Webhooks.UpdateWebhookEndpoint(r.Context(), req)
	respond(w, r, http.StatusOK, resp.GetEndpoint(), err)
}

func (s *Server) deleteWebhookEndpoint(w http.ResponseWriter, r *http.Request) {
	_, err := s.d.Webhooks.DeleteWebhookEndpoint(r.Context(), &plansv1.DeleteWebhookEndpointRequest{Id: r.PathValue("id")})
	if err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) rotateWebhookSecret(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Webhooks.RotateWebhookSecret(r.Context(), &plansv1.RotateWebhookSecretRequest{Id: r.PathValue("id")})
	respond(w, r, http.StatusOK, resp.GetEndpoint(), err)
}

func (s *Server) sendTestWebhook(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Webhooks.SendTestWebhook(r.Context(), &plansv1.SendTestWebhookRequest{Id: r.PathValue("id")})
	respond(w, r, http.StatusAccepted, resp.GetDelivery(), err)
}

// listWebhookDeliveries is GET /v1/webhook-deliveries?endpoint_id=&state=pending|delivered|failed&page_size=&page_token=.
func (s *Server) listWebhookDeliveries(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	req := &plansv1.ListWebhookDeliveriesRequest{EndpointId: q.Get("endpoint_id"), PageToken: q.Get("page_token")}
	switch q.Get("state") {
	case "":
	case "pending":
		req.State = plansv1.WebhookDeliveryState_WEBHOOK_DELIVERY_STATE_PENDING
	case "delivered":
		req.State = plansv1.WebhookDeliveryState_WEBHOOK_DELIVERY_STATE_DELIVERED
	case "failed":
		req.State = plansv1.WebhookDeliveryState_WEBHOOK_DELIVERY_STATE_FAILED
	default:
		writeError(w, r, invalid("state", "pending, delivered or failed"))
		return
	}
	if v := q.Get("page_size"); v != "" {
		n, err := strconv.ParseInt(v, 10, 32)
		if err != nil {
			writeError(w, r, invalid("page_size", "must be an integer"))
			return
		}
		req.PageSize = int32(n)
	}
	resp, err := s.d.Webhooks.ListWebhookDeliveries(r.Context(), req)
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) redeliverWebhook(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Webhooks.RedeliverWebhook(r.Context(), &plansv1.RedeliverWebhookRequest{DeliveryId: r.PathValue("id")})
	respond(w, r, http.StatusAccepted, resp.GetDelivery(), err)
}
