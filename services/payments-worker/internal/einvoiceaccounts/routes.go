// Package einvoiceaccounts serves the operator API over a tenant's e-invoice integrator account. It maps requests to the
// commands and queries of its subpackages (ADR 0020).
package einvoiceaccounts

import (
	"log/slog"
	"net/http"

	"github.com/0furkancolak/payrudder/pkg/env"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/einvoiceaccounts/command"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/einvoiceaccounts/query"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/httpx"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store"
)

// Handler serves the e-invoice account endpoints.
type Handler struct {
	Deps command.Deps
	Log  *slog.Logger
}

// Register adds the routes to mux, each wrapped by guard (the operator token check).
func (h *Handler) Register(mux *http.ServeMux, guard func(http.HandlerFunc) http.HandlerFunc) {
	const base = "/admin/tenants/{tenant}/e-invoice-accounts"
	mux.HandleFunc("GET "+base, guard(h.list))
	mux.HandleFunc("PUT "+base+"/{environment}", guard(h.put))
	mux.HandleFunc("DELETE "+base+"/{environment}", guard(h.delete))
	mux.HandleFunc("POST "+base+"/{environment}/check", guard(h.check))
	mux.HandleFunc("POST "+base+"/{environment}/default", guard(h.makeDefault))
	mux.HandleFunc("GET "+base+"/{environment}/routing", guard(h.routing))
	mux.HandleFunc("PUT "+base+"/{environment}/routing", guard(h.setRouting))
}

// ActorHeader names who an account change is recorded for: "user:<id>" for a person in the console, set by the gateway. Without it the change is the operator's.
const ActorHeader = "X-Payrudder-Actor"

func actor(r *http.Request) string {
	if a := r.Header.Get(ActorHeader); a != "" {
		return a
	}
	return "operator"
}

// pathEnvironment reads the {environment} of the path, or answers 400 and reports false.
func pathEnvironment(w http.ResponseWriter, r *http.Request) (string, bool) {
	e, err := env.Parse(r.PathValue("environment"))
	if err != nil {
		httpx.Fail(w, http.StatusBadRequest, errkey.Key("common.invalid_argument"), "environment must be sandbox or live")
		return "", false
	}
	return e.String(), true
}

// ready answers 503 and reports false when the deployment holds no e-invoice accounts.
func (h *Handler) ready(w http.ResponseWriter, r *http.Request) bool {
	if err := h.Deps.Ready(); err != nil {
		httpx.Reply(h.Log, w, r, err)
		return false
	}
	return true
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	t, ok := httpx.Tenant(w, r)
	if !ok || !h.ready(w, r) {
		return
	}
	list, err := query.List(r.Context(), h.Deps.Accounts, t)
	if err != nil {
		httpx.Reply(h.Log, w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"accounts": list})
}

func (h *Handler) put(w http.ResponseWriter, r *http.Request) {
	t, ok := httpx.Tenant(w, r)
	if !ok || !h.ready(w, r) {
		return
	}
	env, ok := pathEnvironment(w, r)
	if !ok {
		return
	}
	var in command.Put
	if !httpx.DecodeBody(w, r, &in) {
		return
	}
	info, err := command.PutAccount(r.Context(), h.Deps, t, env, actor(r), in)
	if err != nil {
		httpx.Reply(h.Log, w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, info)
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	t, ok := httpx.Tenant(w, r)
	if !ok || !h.ready(w, r) {
		return
	}
	env, ok := pathEnvironment(w, r)
	if !ok {
		return
	}
	if err := command.DeleteAccount(r.Context(), h.Deps, t, env, r.URL.Query().Get("account"), actor(r)); err != nil {
		httpx.Reply(h.Log, w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// check tests the stored account: the integrator is asked whether the credentials work. The body may name a tax id to ask about.
func (h *Handler) check(w http.ResponseWriter, r *http.Request) {
	t, ok := httpx.Tenant(w, r)
	if !ok || !h.ready(w, r) {
		return
	}
	env, ok := pathEnvironment(w, r)
	if !ok {
		return
	}
	var in struct {
		Account string `json:"account"`
		TaxID   string `json:"tax_id"`
	}
	if r.ContentLength != 0 && !httpx.DecodeBody(w, r, &in) {
		return
	}
	res, err := command.CheckAccount(r.Context(), h.Deps, t, env, in.Account, in.TaxID)
	if err != nil {
		httpx.Reply(h.Log, w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

// makeDefault makes the account named by ?account= the default of the environment.
func (h *Handler) makeDefault(w http.ResponseWriter, r *http.Request) {
	t, ok := httpx.Tenant(w, r)
	if !ok || !h.ready(w, r) {
		return
	}
	env, ok := pathEnvironment(w, r)
	if !ok {
		return
	}
	if err := command.SetDefaultAccount(r.Context(), h.Deps, t, env, r.URL.Query().Get("account"), actor(r)); err != nil {
		httpx.Reply(h.Log, w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) routing(w http.ResponseWriter, r *http.Request) {
	t, ok := httpx.Tenant(w, r)
	if !ok || !h.ready(w, r) {
		return
	}
	env, ok := pathEnvironment(w, r)
	if !ok {
		return
	}
	rules, err := query.Routing(r.Context(), h.Deps.Accounts, t, env)
	if err != nil {
		httpx.Reply(h.Log, w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"rules": rules})
}

func (h *Handler) setRouting(w http.ResponseWriter, r *http.Request) {
	t, ok := httpx.Tenant(w, r)
	if !ok || !h.ready(w, r) {
		return
	}
	env, ok := pathEnvironment(w, r)
	if !ok {
		return
	}
	var in struct {
		Rules []store.EInvoiceRoute `json:"rules"`
	}
	if !httpx.DecodeBody(w, r, &in) {
		return
	}
	if err := command.SetRouting(r.Context(), h.Deps, t, env, actor(r), in.Rules); err != nil {
		httpx.Reply(h.Log, w, r, err)
		return
	}
	rules, err := query.Routing(r.Context(), h.Deps.Accounts, t, env)
	if err != nil {
		httpx.Reply(h.Log, w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"rules": rules})
}
