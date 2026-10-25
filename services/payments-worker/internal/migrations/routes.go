// Package migrations serves the operator endpoints that move a subscription to another payment provider. It maps requests to the commands of its subpackage
// (ADR 0020); the orchestration of plans-svc, the providers and the checkout is there.
package migrations

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/0furkancolak/payrudder/services/payments-worker/internal/httpx"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/migrations/command"
)

// Handler serves the migration endpoints.
type Handler struct {
	Deps command.Deps
	Log  *slog.Logger
}

// Register adds the routes to mux, each wrapped by guard (the operator token check).
func (h *Handler) Register(mux *http.ServeMux, guard func(http.HandlerFunc) http.HandlerFunc) {
	const base = "/admin/tenants/{tenant}/migrations"
	mux.HandleFunc("POST "+base, guard(h.start))
	mux.HandleFunc("POST "+base+"/{migration}/abort", guard(h.abort))
}

func (h *Handler) start(w http.ResponseWriter, r *http.Request) {
	t, ok := httpx.Tenant(w, r)
	if !ok {
		return
	}
	env, ok := httpx.Environment(w, r)
	if !ok {
		return
	}
	if err := h.Deps.NeedPlans(); err != nil {
		httpx.Reply(h.Log, w, r, err)
		return
	}
	var in command.Start
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, httpx.MaxBody)).Decode(&in); err != nil {
		httpx.Fail(w, http.StatusBadRequest, "payments.migration.fields_required", "subscription_id and to_provider are required")
		return
	}
	res, err := command.StartMigration(r.Context(), h.Deps, t, env, in)
	if err != nil {
		httpx.Reply(h.Log, w, r, err)
		return
	}
	if res.State == command.StateStarted {
		httpx.WriteJSON(w, http.StatusCreated, map[string]any{"migration": res.Migration, "state": res.State, "checkout_url": res.CheckoutURL})
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"migration": res.Migration, "state": res.State, "new_subscription": res.NewSubscription, "cutover": res.Cutover})
}

func (h *Handler) abort(w http.ResponseWriter, r *http.Request) {
	t, ok := httpx.Tenant(w, r)
	if !ok {
		return
	}
	if err := command.AbortMigration(r.Context(), h.Deps, t, r.PathValue("migration")); err != nil {
		httpx.Reply(h.Log, w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"state": "aborted"})
}
