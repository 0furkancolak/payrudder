// Package accounts serves the operator API over a tenant's payment provider accounts: listing, storing and removing them, and the go-live check. It maps
// requests to the commands and queries of its subpackages (ADR 0020).
package accounts

import (
	"cmp"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/0furkancolak/payrudder/services/payments-worker/internal/accounts/command"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/accounts/query"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/httpx"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store"
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
	mux.HandleFunc("POST "+base+"/provider-accounts/{provider}/default", guard(h.makeDefault))
	mux.HandleFunc("POST "+base+"/provider-accounts/{provider}/webhook-endpoint", guard(h.registerWebhook))
	mux.HandleFunc("GET "+base+"/go-live", guard(h.goLive))
	mux.HandleFunc("GET "+base+"/provider-health", guard(h.health))
	mux.HandleFunc("GET "+base+"/provider-account-events", guard(h.events))
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

// makeDefault makes a stored account the default of the environment named by ?environment=.
func (h *Handler) makeDefault(w http.ResponseWriter, r *http.Request) {
	t, ok := httpx.Tenant(w, r)
	if !ok {
		return
	}
	mode, ok := httpx.Environment(w, r)
	if !ok {
		return
	}
	if err := command.SetDefaultAccount(r.Context(), h.Command, t, r.PathValue("provider"), mode, actor(r)); err != nil {
		httpx.Reply(h.Log, w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// registerWebhook has the provider create the endpoint its notifications go to, at the address in the body, and stores the signing secret in the account. The
// answer is the endpoint's id; the secret is never in it.
func (h *Handler) registerWebhook(w http.ResponseWriter, r *http.Request) {
	t, ok := httpx.Tenant(w, r)
	if !ok {
		return
	}
	mode, ok := httpx.Environment(w, r)
	if !ok {
		return
	}
	var in struct {
		URL string `json:"url"`
	}
	if !httpx.DecodeBody(w, r, &in) {
		return
	}
	id, err := command.RegisterWebhook(r.Context(), h.Command, t, r.PathValue("provider"), mode, in.URL, actor(r))
	if err != nil {
		httpx.Reply(h.Log, w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]string{"endpoint_id": id})
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

// events is the audit trail of the tenant's accounts: ?kind=payment|einvoice, ?limit= and ?before= (the next_before of the previous page).
func (h *Handler) events(w http.ResponseWriter, r *http.Request) {
	t, ok := httpx.Tenant(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	kind := q.Get("kind")
	if kind != "" && kind != store.KindPayment && kind != store.KindEInvoice {
		httpx.Fail(w, http.StatusBadRequest, "common.invalid_argument", "kind must be payment or einvoice")
		return
	}
	before, err1 := strconv.ParseInt(cmp.Or(q.Get("before"), "0"), 10, 64)
	limit, err2 := strconv.Atoi(cmp.Or(q.Get("limit"), "0"))
	if err1 != nil || err2 != nil || before < 0 || limit < 0 {
		httpx.Fail(w, http.StatusBadRequest, "common.invalid_argument", "before and limit must be non-negative numbers")
		return
	}
	events, next, err := query.Events(r.Context(), h.Query, t, kind, before, limit)
	if err != nil {
		httpx.Reply(h.Log, w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"events": events, "next_before": next})
}
