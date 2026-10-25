// Package ledger serves the operator endpoints over the payment ledger: the tenant's rows, the rows of given invoices and the reset of a sandbox tenant.
// It maps requests to the commands and queries of its subpackages (ADR 0020).
package ledger

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/httpx"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/ledger/command"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/ledger/query"
)

// Handler serves the ledger endpoints.
type Handler struct {
	Query   query.Deps
	Command command.Deps
	Log     *slog.Logger
}

// Register adds the routes to mux, each wrapped by guard (the operator token check).
func (h *Handler) Register(mux *http.ServeMux, guard func(http.HandlerFunc) http.HandlerFunc) {
	const base = "/admin/tenants/{tenant}"
	mux.HandleFunc("GET "+base+"/ledger", guard(h.list))
	mux.HandleFunc("POST "+base+"/ledger/lookup", guard(h.lookup))
	mux.HandleFunc("POST "+base+"/sandbox-reset", guard(h.sandboxReset))
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	t, ok := httpx.Tenant(w, r)
	if !ok {
		return
	}
	limit, err := query.Limit(r.URL.Query().Get("limit"))
	if err != nil {
		httpx.Reply(h.Log, w, r, err)
		return
	}
	rows, err := query.List(r.Context(), h.Query, t, limit)
	if err != nil {
		httpx.Reply(h.Log, w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"entries": rows})
}

// lookup is POST /admin/tenants/{tenant}/ledger/lookup {"invoice_ids":[...]}.
func (h *Handler) lookup(w http.ResponseWriter, r *http.Request) {
	t, ok := httpx.Tenant(w, r)
	if !ok {
		return
	}
	var in struct {
		InvoiceIDs []string `json:"invoice_ids"`
	}
	if !httpx.DecodeBody(w, r, &in) {
		return
	}
	rows, err := query.Lookup(r.Context(), h.Query, t, in.InvoiceIDs)
	if err != nil {
		httpx.Reply(h.Log, w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"rows": rows})
}

// sandboxReset is POST /admin/tenants/{tenant}/sandbox-reset {"environment":"sandbox"}. The caller is plans-svc, behind the internal token.
func (h *Handler) sandboxReset(w http.ResponseWriter, r *http.Request) {
	t, ok := httpx.Tenant(w, r)
	if !ok {
		return
	}
	if !h.Command.Configured() {
		httpx.Fail(w, http.StatusServiceUnavailable, errkey.Key("common.unavailable"), "sandbox reset is not configured")
		return
	}
	var in struct {
		Environment string `json:"environment"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&in); err != nil {
		httpx.Fail(w, http.StatusBadRequest, errkey.Key("common.invalid_argument"), "body: {\"environment\":\"sandbox\"}")
		return
	}
	deleted, err := command.ResetSandbox(r.Context(), h.Command, t, in.Environment)
	if err != nil {
		httpx.Reply(h.Log, w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"deleted": deleted})
}
