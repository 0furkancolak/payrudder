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
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store"
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
	mux.HandleFunc("GET "+base+"/disputes", guard(h.disputes))
	mux.HandleFunc("GET "+base+"/disputes/{dispute}/history", guard(h.disputeHistory))
	mux.HandleFunc("POST "+base+"/disputes/{dispute}/notes", guard(h.addDisputeNote))
	mux.HandleFunc("PUT "+base+"/disputes/{dispute}/handled", guard(h.setDisputeHandled))
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

// disputes is GET /admin/tenants/{tenant}/disputes?invoice_id=&outcome=&limit=: the cases customers opened with their banks, newest first.
func (h *Handler) disputes(w http.ResponseWriter, r *http.Request) {
	t, ok := httpx.Tenant(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	limit, err := query.Limit(q.Get("limit"))
	if err != nil {
		httpx.Reply(h.Log, w, r, err)
		return
	}
	rows, err := query.Disputes(r.Context(), h.Query, t, q.Get("invoice_id"), q.Get("outcome"), limit)
	if err != nil {
		httpx.Reply(h.Log, w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"disputes": rows})
}

// disputeHistory is GET /admin/tenants/{tenant}/disputes/{dispute}/history: the notes and marks the seller made on the case, newest first.
func (h *Handler) disputeHistory(w http.ResponseWriter, r *http.Request) {
	t, ok := httpx.Tenant(w, r)
	if !ok {
		return
	}
	id, err := query.DisputeID(r.PathValue("dispute"))
	if err == nil {
		var ev []store.DisputeEvent
		if ev, err = query.DisputeHistory(r.Context(), h.Query, t, id); err == nil {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{"events": ev})
			return
		}
	}
	httpx.Reply(h.Log, w, r, err)
}

// addDisputeNote is POST /admin/tenants/{tenant}/disputes/{dispute}/notes {"note": "..."}.
func (h *Handler) addDisputeNote(w http.ResponseWriter, r *http.Request) {
	t, ok := httpx.Tenant(w, r)
	if !ok {
		return
	}
	var in struct {
		Note string `json:"note"`
	}
	if !httpx.DecodeBody(w, r, &in) {
		return
	}
	id, err := query.DisputeID(r.PathValue("dispute"))
	if err == nil {
		var ev store.DisputeEvent
		if ev, err = command.AddNote(r.Context(), h.Command, t, id, in.Note); err == nil {
			httpx.WriteJSON(w, http.StatusCreated, ev)
			return
		}
	}
	httpx.Reply(h.Log, w, r, err)
}

// setDisputeHandled is PUT /admin/tenants/{tenant}/disputes/{dispute}/handled {"handled": true}: the case as it stands.
func (h *Handler) setDisputeHandled(w http.ResponseWriter, r *http.Request) {
	t, ok := httpx.Tenant(w, r)
	if !ok {
		return
	}
	var in struct {
		Handled *bool `json:"handled"`
	}
	if !httpx.DecodeBody(w, r, &in) {
		return
	}
	if in.Handled == nil {
		httpx.Fail(w, http.StatusBadRequest, errkey.Key("common.invalid_argument"), "handled is required: true or false")
		return
	}
	id, err := query.DisputeID(r.PathValue("dispute"))
	if err == nil {
		var c store.Dispute
		if c, err = command.SetHandled(r.Context(), h.Command, t, id, *in.Handled); err == nil {
			httpx.WriteJSON(w, http.StatusOK, c)
			return
		}
	}
	httpx.Reply(h.Log, w, r, err)
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
