// Package reconciliation serves the operator's comparison of what billing calls paid with what the payment ledger holds. It maps the request to the query
// (ADR 0020); the rules are there.
package reconciliation

import (
	"log/slog"
	"net/http"

	"github.com/0furkancolak/payrudder/services/payments-worker/internal/httpx"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/reconciliation/query"
)

// Handler serves the reconciliation endpoint.
type Handler struct {
	Query query.Deps
	Log   *slog.Logger
}

// Register adds the route to mux, wrapped by guard (the operator token check).
func (h *Handler) Register(mux *http.ServeMux, guard func(http.HandlerFunc) http.HandlerFunc) {
	mux.HandleFunc("GET /admin/tenants/{tenant}/reconciliation", guard(h.get))
}

// get is GET /admin/tenants/{tenant}/reconciliation?period=YYYY-MM.
func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	t, ok := httpx.Tenant(w, r)
	if !ok {
		return
	}
	rec, err := query.Get(r.Context(), h.Query, t, r.URL.Query().Get("period"))
	if err != nil {
		httpx.Reply(h.Log, w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, rec)
}
