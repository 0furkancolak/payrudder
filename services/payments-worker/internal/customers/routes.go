// Package customers serves the operator API that makes a tenant's customer known to a payment provider and opens the provider's customer portal. It maps
// requests to the commands of its subpackage (ADR 0020).
package customers

import (
	"log/slog"
	"net/http"

	"github.com/0furkancolak/payrudder/pkg/payments"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/customers/command"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/httpx"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/target"
)

// Handler serves the customer endpoints.
type Handler struct {
	Deps     command.Deps
	Resolver target.Resolver
	Log      *slog.Logger
}

// Register adds the routes to mux, each wrapped by guard (the operator token check).
func (h *Handler) Register(mux *http.ServeMux, guard func(http.HandlerFunc) http.HandlerFunc) {
	const base = "/admin/tenants/{tenant}/providers/{provider}/customers/{external_id}"
	mux.HandleFunc("POST "+base, guard(h.ensure))
	mux.HandleFunc("POST "+base+"/portal", guard(h.portal))
}

func (h *Handler) ensure(w http.ResponseWriter, r *http.Request) {
	tg, ok := httpx.ProviderTarget(w, r, h.Resolver)
	if !ok {
		return
	}
	api, err := target.As[payments.CustomerAPI](tg, "customers")
	if err != nil {
		httpx.Reply(h.Log, w, r, err)
		return
	}
	refs, err := command.Ensure(r.Context(), h.Deps, tg, api, r.PathValue("external_id"))
	if err != nil {
		httpx.Reply(h.Log, w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"customer_ref": refs.CustomerRef, "address_ref": refs.AddressRef, "business_ref": refs.BusinessRef})
}

func (h *Handler) portal(w http.ResponseWriter, r *http.Request) {
	tg, ok := httpx.ProviderTarget(w, r, h.Resolver)
	if !ok {
		return
	}
	api, err := target.As[payments.CustomerAPI](tg, "a customer portal")
	if err != nil {
		httpx.Reply(h.Log, w, r, err)
		return
	}
	url, err := command.PortalLink(r.Context(), h.Deps, tg, api, r.PathValue("external_id"))
	if err != nil {
		httpx.Reply(h.Log, w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"url": url})
}
