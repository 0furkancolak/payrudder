// Package catalogue serves the operator API over what Payrudder created at a provider: syncing a plan version's product and prices, listing them, and
// creating discounts. Each endpoint works only when the tenant has an account at the provider and the provider's adapter implements what it needs. It maps
// requests to the commands and queries of its subpackages (ADR 0020).
package catalogue

import (
	"log/slog"
	"net/http"

	"github.com/0furkancolak/payrudder/pkg/payments"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/catalogue/command"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/catalogue/query"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/httpx"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/target"
)

// Handler serves the catalogue endpoints.
type Handler struct {
	Deps     command.Deps
	Resolver target.Resolver
	Log      *slog.Logger
}

// Register adds the routes to mux, each wrapped by guard (the operator token check).
func (h *Handler) Register(mux *http.ServeMux, guard func(http.HandlerFunc) http.HandlerFunc) {
	const base = "/admin/tenants/{tenant}/providers/{provider}"
	mux.HandleFunc("POST "+base+"/catalogue/sync", guard(h.sync))
	mux.HandleFunc("GET "+base+"/catalogue", guard(h.list))
	mux.HandleFunc("POST "+base+"/discounts", guard(h.createDiscount))
}

func (h *Handler) sync(w http.ResponseWriter, r *http.Request) {
	tg, ok := httpx.ProviderTarget(w, r, h.Resolver)
	if !ok {
		return
	}
	cat, err := target.As[payments.Catalogue](tg, "a catalogue")
	if err == nil {
		err = h.Deps.NeedPlans()
	}
	if err != nil {
		httpx.Reply(h.Log, w, r, err)
		return
	}
	var in command.Sync
	if !httpx.DecodeBody(w, r, &in) {
		return
	}
	res, err := command.SyncCatalogue(r.Context(), h.Deps, tg, cat, in)
	if err != nil {
		httpx.Reply(h.Log, w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"plan_version_id": res.PlanVersionID, "product_ref": res.ProductRef, "created": res.Created, "existing": res.Existing, "skipped": res.Skipped})
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	tg, ok := httpx.ProviderTarget(w, r, h.Resolver)
	if !ok {
		return
	}
	list, err := query.List(r.Context(), h.Deps.Objects, tg)
	if err != nil {
		httpx.Reply(h.Log, w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"objects": list})
}

func (h *Handler) createDiscount(w http.ResponseWriter, r *http.Request) {
	tg, ok := httpx.ProviderTarget(w, r, h.Resolver)
	if !ok {
		return
	}
	api, err := target.As[payments.DiscountAPI](tg, "discounts")
	if err != nil {
		httpx.Reply(h.Log, w, r, err)
		return
	}
	var in payments.DiscountSpec
	if !httpx.DecodeBody(w, r, &in) {
		return
	}
	refs, err := command.CreateDiscount(r.Context(), h.Deps, tg, api, in)
	if err != nil {
		httpx.Reply(h.Log, w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]string{"discount_ref": refs.DiscountRef, "code": refs.Code})
}
