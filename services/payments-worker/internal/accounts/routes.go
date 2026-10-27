// Package accounts serves the operator API over a tenant's payment provider accounts: listing, storing and removing them, and the go-live check. It maps
// requests to the commands and queries of its subpackages (ADR 0020).
package accounts

import (
	"io"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/0furkancolak/payrudder/services/payments-worker/internal/accounts/command"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/accounts/query"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/httpx"
)

// ActorHeader names who an account change is recorded for: "user:<id>" for a person in the console, set by the gateway. Without it the change is the operator's.
const ActorHeader = "X-Payrudder-Actor"

func actor(r *http.Request) string {
	if a := r.Header.Get(ActorHeader); a != "" {
		return a
	}
	return "operator"
}

// Handler serves the provider account endpoints.
type Handler struct {
	Command command.Deps
	Query   query.Deps
	Log     *slog.Logger
}

// Register adds the routes to mux, each wrapped by guard (the operator token check).
func (h *Handler) Register(mux *http.ServeMux, guard func(http.HandlerFunc) http.HandlerFunc) {
	const base = "/admin/tenants/{tenant}"
	mux.HandleFunc("GET "+base+"/provider-accounts", guard(h.list))
	mux.HandleFunc("PUT "+base+"/provider-accounts/{provider}", guard(h.put))
	mux.HandleFunc("DELETE "+base+"/provider-accounts/{provider}", guard(h.delete))
	mux.HandleFunc("GET "+base+"/go-live", guard(h.goLive))
	mux.HandleFunc("GET "+base+"/provider-health", guard(h.health))
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
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"accounts": list})
}

// put stores (or replaces) an account. The body is the provider's settings; the settings are never echoed back.
func (h *Handler) put(w http.ResponseWriter, r *http.Request) {
	t, ok := httpx.Tenant(w, r)
	if !ok {
		return
	}
	mode, ok := httpx.Environment(w, r)
	if !ok {
		return
	}
	settings, err := io.ReadAll(http.MaxBytesReader(w, r.Body, httpx.MaxBody))
	if err != nil {
		httpx.Fail(w, http.StatusRequestEntityTooLarge, "payments.request.too_large", "body too large")
		return
	}
	// ?default= is a flag: anything that is not a boolean is not a request to make the account the default.
	makeDefault, _ := strconv.ParseBool(r.URL.Query().Get("default"))
	// ?verify= is set by the gateway for a person who connects their own account: the provider is asked to accept the credentials, and only its own hosts are allowed.
	verify, _ := strconv.ParseBool(r.URL.Query().Get("verify"))
	provider := r.PathValue("provider")
	if err := command.PutAccount(r.Context(), h.Command, command.Put{Tenant: t, Provider: provider, Environment: mode, Settings: settings, MakeDefault: makeDefault,
		Actor: actor(r), Verify: verify}); err != nil {
		httpx.Reply(h.Log, w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"provider": provider, "environment": mode, "default": makeDefault})
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	t, ok := httpx.Tenant(w, r)
	if !ok {
		return
	}
	mode, ok := httpx.Environment(w, r)
	if !ok {
		return
	}
	if err := command.DeleteAccount(r.Context(), h.Command, t, r.PathValue("provider"), mode, actor(r)); err != nil {
		httpx.Reply(h.Log, w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) goLive(w http.ResponseWriter, r *http.Request) {
	t, ok := httpx.Tenant(w, r)
	if !ok {
		return
	}
	res, err := query.GetGoLive(r.Context(), h.Query, t)
	if err != nil {
		httpx.Reply(h.Log, w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

// health is go-live for the environment named by ?environment=: the accounts of the tenant in it and whether each works.
func (h *Handler) health(w http.ResponseWriter, r *http.Request) {
	t, ok := httpx.Tenant(w, r)
	if !ok {
		return
	}
	mode, ok := httpx.Environment(w, r)
	if !ok {
		return
	}
	res, err := query.GetHealth(r.Context(), h.Query, t, mode)
	if err != nil {
		httpx.Reply(h.Log, w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}
