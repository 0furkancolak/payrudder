package health

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/payments"
	"github.com/0furkancolak/payrudder/pkg/payments/banktransfer"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/httpx"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/refuse"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store"
)

// ActorHeader names who a change is recorded for (the gateway passes the caller's X-Payrudder-Actor).
const ActorHeader = "X-Payrudder-Actor"

// Handler serves the operator endpoints over the runtime health of a tenant's provider accounts and their failover.
type Handler struct {
	Store store.ProviderHealth
	Log   *slog.Logger
}

// Register adds the routes to mux, each wrapped by guard (the operator token check).
func (h *Handler) Register(mux *http.ServeMux, guard func(http.HandlerFunc) http.HandlerFunc) {
	const base = "/admin/tenants/{tenant}"
	mux.HandleFunc("GET "+base+"/account-health", guard(h.list))
	mux.HandleFunc("PUT "+base+"/provider-accounts/{provider}/failover", guard(h.putFailover))
	mux.HandleFunc("DELETE "+base+"/provider-accounts/{provider}/failover", guard(h.deleteFailover))
}

// list is GET /admin/tenants/{tenant}/account-health: every account with its last check, its circuit breaker and its failover account.
func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	t, ok := httpx.Tenant(w, r)
	if !ok {
		return
	}
	list, err := h.Store.List(r.Context(), t)
	if err != nil {
		httpx.Reply(h.Log, w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"accounts": list})
}

func badTarget(msg string) error {
	return refuse.New(http.StatusBadRequest, "payments.failover.bad_target", msg)
}

// putFailover is PUT /admin/tenants/{tenant}/provider-accounts/{provider}/failover?environment=live {"failover_to":"stripe~eu"}.
func (h *Handler) putFailover(w http.ResponseWriter, r *http.Request) {
	t, ok := httpx.Tenant(w, r)
	if !ok {
		return
	}
	environment, ok := httpx.Environment(w, r)
	if !ok {
		return
	}
	var in struct {
		FailoverTo string `json:"failover_to"`
	}
	if !httpx.DecodeBody(w, r, &in) {
		return
	}
	provider, to := r.PathValue("provider"), strings.TrimSpace(in.FailoverTo)
	switch {
	case !payments.ValidAccountKey(provider) || !payments.ValidAccountKey(to):
		httpx.Reply(h.Log, w, r, badTarget("both must be account keys such as stripe or stripe~eu"))
		return
	case provider == to:
		httpx.Reply(h.Log, w, r, badTarget("an account cannot fail over to itself"))
		return
	case payments.BaseProvider(provider) == banktransfer.Name || payments.BaseProvider(to) == banktransfer.Name:
		httpx.Reply(h.Log, w, r, badTarget("a bank transfer is confirmed by hand and cannot take part in a failover"))
		return
	}
	actor := strings.TrimSpace(r.Header.Get(ActorHeader))
	if actor == "" {
		actor = "operator"
	}
	if err := h.Store.SetFailover(r.Context(), t, environment, provider, to, actor); err != nil {
		if errors.Is(err, store.ErrNoAccount) {
			httpx.Fail(w, http.StatusNotFound, errkey.Key("payments.account.not_found"), "both accounts must exist in that environment")
			return
		}
		httpx.Reply(h.Log, w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"provider": provider, "environment": environment, "failover_to": to})
}

// deleteFailover is DELETE /admin/tenants/{tenant}/provider-accounts/{provider}/failover?environment=live.
func (h *Handler) deleteFailover(w http.ResponseWriter, r *http.Request) {
	t, ok := httpx.Tenant(w, r)
	if !ok {
		return
	}
	environment, ok := httpx.Environment(w, r)
	if !ok {
		return
	}
	found, err := h.Store.ClearFailover(r.Context(), t, environment, r.PathValue("provider"))
	if err != nil {
		httpx.Reply(h.Log, w, r, err)
		return
	}
	if !found {
		httpx.Fail(w, http.StatusNotFound, errkey.Key("payments.failover.not_found"), "no failover is set for that account")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
