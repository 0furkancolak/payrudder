// Package auditlog serves payments-worker's audit log (pkg/audit) to the gateway over the operator HTTP API: the same question AuditService asks of plans-svc and
// billing-svc, with the same fields in the query string and the same message as the answer. A tenant's entries are written in its own transactions, so they are where its
// other rows are: the pooled database, or its schema in a cell.
package auditlog

import (
	"log/slog"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/0furkancolak/payrudder/pkg/audit"
	"github.com/0furkancolak/payrudder/pkg/audit/auditgrpc"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/httpx"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store/sqlc"
)

// Handler serves the audit endpoints.
type Handler struct {
	DB  store.DB
	Log *slog.Logger
}

// Register adds the routes to mux, each wrapped by guard (the operator token check).
func (h *Handler) Register(mux *http.ServeMux, guard func(http.HandlerFunc) http.HandlerFunc) {
	mux.HandleFunc("GET /admin/tenants/{tenant}/audit-events", guard(h.tenant))
	mux.HandleFunc("GET /admin/audit-events", guard(h.all))
}

func (h *Handler) tenant(w http.ResponseWriter, r *http.Request) {
	t, ok := httpx.Tenant(w, r)
	if !ok {
		return
	}
	h.list(w, r, t)
}

// all is the operator's view across tenants: the pooled database. The entries of a tenant placed in a cell are read by asking for that tenant.
func (h *Handler) all(w http.ResponseWriter, r *http.Request) { h.list(w, r, uuid.Nil) }

func (h *Handler) list(w http.ResponseWriter, r *http.Request, tenant uuid.UUID) {
	req, err := auditgrpc.RequestFromQuery(r.URL.Query())
	if err != nil {
		httpx.Fail(w, http.StatusBadRequest, "payments.request.bad_audit_filter", err.Error())
		return
	}
	f, err := auditgrpc.FilterOf(req)
	if err != nil {
		httpx.Fail(w, http.StatusBadRequest, "payments.request.bad_audit_filter", err.Error())
		return
	}
	f.TenantID = tenant
	var rows []audit.Row
	err = store.Tx(r.Context(), h.DB, tenant, func(tx pgx.Tx, _ *sqlc.Queries, _ string) (err error) {
		rows, err = audit.List(r.Context(), tx, f)
		return err
	})
	if err != nil {
		httpx.Reply(h.Log, w, r, err)
		return
	}
	auditgrpc.WriteJSON(w, rows)
}
