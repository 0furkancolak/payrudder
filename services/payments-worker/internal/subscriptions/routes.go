// Package subscriptions serves the operator API over the subscriptions a provider runs: listing them, the hosted checkout where a customer subscribes, and
// changing, pausing and resuming one. Each provider endpoint works only when the tenant has an account at the provider and the provider's adapter drives
// subscriptions. It maps requests to the commands and queries of its subpackages (ADR 0020).
package subscriptions

import (
	"encoding/json"
	"log/slog"
	"net/http"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/payments"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/httpx"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/subscriptions/command"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/subscriptions/query"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/target"
)

// Handler serves the subscription endpoints.
type Handler struct {
	Query    query.Deps
	Command  command.Deps
	Resolver target.Resolver
	Log      *slog.Logger
}

// Register adds the routes to mux, each wrapped by guard (the operator token check).
func (h *Handler) Register(mux *http.ServeMux, guard func(http.HandlerFunc) http.HandlerFunc) {
	const tenant = "/admin/tenants/{tenant}"
	const sub = tenant + "/providers/{provider}/subscriptions/{id}"
	mux.HandleFunc("GET "+tenant+"/provider-subscriptions", guard(h.list))
	mux.HandleFunc("POST "+tenant+"/checkouts", guard(h.checkout))
	mux.HandleFunc("POST "+sub+"/change-preview", guard(h.previewChange))
	mux.HandleFunc("POST "+sub+"/change", guard(h.applyChange))
	mux.HandleFunc("POST "+sub+"/pause", guard(h.pause))
	mux.HandleFunc("POST "+sub+"/resume", guard(h.resume))
	mux.HandleFunc("POST "+sub+"/payment-method-link", guard(h.paymentMethodLink))
}

// subscription is what every endpoint of one subscription starts with: the provider's account, an adapter that drives subscriptions, and the subscription
// that provider runs. It answers and reports false when it cannot. decode, when set, reads the body between the adapter and the subscription.
func (h *Handler) subscription(w http.ResponseWriter, r *http.Request, decode func() bool) (target.Target, payments.SubscriptionAPI, *plansv1.Subscription, bool) {
	tg, ok := httpx.ProviderTarget(w, r, h.Resolver)
	if !ok {
		return tg, nil, nil, false
	}
	api, err := target.As[payments.SubscriptionAPI](tg, "driving subscriptions")
	if err != nil {
		httpx.Reply(h.Log, w, r, err)
		return tg, nil, nil, false
	}
	if decode != nil && !decode() {
		return tg, nil, nil, false
	}
	sub, err := query.Load(r.Context(), h.Query, tg, r.PathValue("id"))
	if err != nil {
		httpx.Reply(h.Log, w, r, err)
		return tg, nil, nil, false
	}
	return tg, api, sub, true
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	t, ok := httpx.Tenant(w, r)
	if !ok {
		return
	}
	list, err := query.List(r.Context(), h.Query, t)
	if err != nil {
		httpx.Reply(h.Log, w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"subscriptions": list})
}

// checkout asks a provider with a hosted checkout for a page where a customer subscribes.
func (h *Handler) checkout(w http.ResponseWriter, r *http.Request) {
	t, ok := httpx.Tenant(w, r)
	if !ok {
		return
	}
	env, ok := httpx.Environment(w, r)
	if !ok {
		return
	}
	var in command.Checkout
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, httpx.MaxBody)).Decode(&in); err != nil {
		httpx.Fail(w, http.StatusBadRequest, "payments.checkout.fields_required", "provider and price_ref are required")
		return
	}
	co, err := command.OpenCheckout(r.Context(), h.Command, t, env, in)
	if err != nil {
		httpx.Reply(h.Log, w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]string{"url": co.URL, "ref": co.Ref})
}

func (h *Handler) previewChange(w http.ResponseWriter, r *http.Request) {
	var in query.ChangeRequest
	tg, api, sub, ok := h.subscription(w, r, func() bool { return httpx.DecodeBody(w, r, &in) })
	if !ok {
		return
	}
	p, err := query.PreviewChange(r.Context(), h.Query, tg, api, sub, in)
	if err != nil {
		httpx.Reply(h.Log, w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, p)
}

func (h *Handler) applyChange(w http.ResponseWriter, r *http.Request) {
	var in query.ChangeRequest
	tg, api, sub, ok := h.subscription(w, r, func() bool { return httpx.DecodeBody(w, r, &in) })
	if !ok {
		return
	}
	res, err := command.ApplyChange(r.Context(), h.Command, tg, api, sub, in)
	if err != nil {
		httpx.Reply(h.Log, w, r, err)
		return
	}
	if !res.Followed {
		httpx.WriteJSON(w, http.StatusAccepted, map[string]any{"changed_at_provider": true, "message": "the provider applied the change; Payrudder will follow when the provider's notification arrives"})
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"changed_at_provider": true, "followed": true})
}

const followsNote = "Payrudder's copy follows when the provider's notification arrives"

func (h *Handler) pause(w http.ResponseWriter, r *http.Request) {
	var in command.Pause
	_, api, sub, ok := h.subscription(w, r, func() bool { return r.ContentLength == 0 || httpx.DecodeBody(w, r, &in) })
	if !ok {
		return
	}
	if err := command.PauseSubscription(r.Context(), api, sub, in); err != nil {
		httpx.Reply(h.Log, w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusAccepted, map[string]any{"requested": true, "note": followsNote})
}

func (h *Handler) resume(w http.ResponseWriter, r *http.Request) {
	_, api, sub, ok := h.subscription(w, r, nil)
	if !ok {
		return
	}
	if err := command.ResumeSubscription(r.Context(), api, sub); err != nil {
		httpx.Reply(h.Log, w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusAccepted, map[string]any{"requested": true, "note": followsNote})
}

func (h *Handler) paymentMethodLink(w http.ResponseWriter, r *http.Request) {
	_, api, sub, ok := h.subscription(w, r, nil)
	if !ok {
		return
	}
	url, err := query.PaymentMethodLink(r.Context(), api, sub)
	if err != nil {
		httpx.Reply(h.Log, w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"url": url})
}
