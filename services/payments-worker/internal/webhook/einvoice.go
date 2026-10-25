package webhook

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/0furkancolak/payrudder/pkg/einvoice"
	"github.com/0furkancolak/payrudder/pkg/einvoice/mock"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store"
)

// The operator API over a tenant's e-invoice integrator account (docs/phases/phase-30-e-invoice.md). The credentials are sealed on the way in and never come back.

func (s *Server) einvoiceRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /admin/tenants/{tenant}/e-invoice-accounts", s.admin(s.listEInvoiceAccounts))
	mux.HandleFunc("PUT /admin/tenants/{tenant}/e-invoice-accounts/{environment}", s.admin(s.putEInvoiceAccount))
	mux.HandleFunc("DELETE /admin/tenants/{tenant}/e-invoice-accounts/{environment}", s.admin(s.deleteEInvoiceAccount))
}

// keyFail answers in the public API's error envelope, so the console translates the key.
func keyFail(w http.ResponseWriter, status int, code, key, msg string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "key": key, "message": msg}})
}

func pathEnvironment(w http.ResponseWriter, r *http.Request) (string, bool) {
	env := r.PathValue("environment")
	if env != "sandbox" && env != "live" {
		keyFail(w, http.StatusBadRequest, "INVALID_ARGUMENT", errkey.Key("common.invalid_argument"), "environment must be sandbox or live")
		return "", false
	}
	return env, true
}

func (s *Server) einvoiceReady(w http.ResponseWriter) bool {
	if s.EInvoiceAccounts.DB == nil || s.EInvoiceRegistry == nil {
		keyFail(w, http.StatusServiceUnavailable, "UNAVAILABLE", errkey.Key("common.unavailable"), "e-invoice accounts are not configured on this deployment (PAYRUDDER_SECRETS_KEY)")
		return false
	}
	return true
}

func (s *Server) listEInvoiceAccounts(w http.ResponseWriter, r *http.Request) {
	t, ok := s.tenant(w, r)
	if !ok || !s.einvoiceReady(w) {
		return
	}
	list, err := s.EInvoiceAccounts.List(r.Context(), t)
	if err != nil {
		fail(w, http.StatusInternalServerError, "common.internal", "internal error")
		return
	}
	if list == nil {
		list = []store.EInvoiceAccountInfo{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"accounts": list})
}

type putEInvoiceAccountBody struct {
	Provider    string            `json:"provider"`
	Mode        string            `json:"mode"`
	Credentials map[string]string `json:"credentials"`
}

func (s *Server) putEInvoiceAccount(w http.ResponseWriter, r *http.Request) {
	t, ok := s.tenant(w, r)
	if !ok || !s.einvoiceReady(w) {
		return
	}
	env, ok := pathEnvironment(w, r)
	if !ok {
		return
	}
	var in putEInvoiceAccountBody
	if !decodeBody(w, r, &in) {
		return
	}
	switch {
	case env == "sandbox" && in.Provider != mock.Name:
		keyFail(w, http.StatusBadRequest, "INVALID_ARGUMENT", errkey.Key("einvoice.account.sandbox_mock_only"), "a sandbox account can only use the mock provider")
		return
	case env == "live" && in.Provider == mock.Name && !s.AllowMockLive:
		keyFail(w, http.StatusBadRequest, "INVALID_ARGUMENT", errkey.Key("einvoice.account.invalid"), "the mock provider is not allowed in the live environment")
		return
	}
	if in.Mode == "" {
		in.Mode = "production"
		if env == "sandbox" {
			in.Mode = "test"
		}
	}
	if in.Mode != "test" && in.Mode != "production" {
		keyFail(w, http.StatusBadRequest, "INVALID_ARGUMENT", errkey.Key("einvoice.account.invalid"), "mode must be test or production")
		return
	}
	settings := map[string]string{}
	for k, v := range in.Credentials {
		settings[k] = v
	}
	settings["mode"] = in.Mode
	raw, _ := json.Marshal(settings)
	// Building the adapter validates the settings (a URL that is not https, a missing key). The error text never contains a credential: adapters do not echo them.
	if _, err := s.EInvoiceRegistry.Build(in.Provider, raw); err != nil {
		if errors.Is(err, einvoice.ErrUnknownProvider) {
			keyFail(w, http.StatusNotFound, "NOT_FOUND", errkey.Key("einvoice.account.invalid"), "unknown provider; known: "+join(s.EInvoiceRegistry.Names()))
			return
		}
		keyFail(w, http.StatusBadRequest, "INVALID_ARGUMENT", errkey.Key("einvoice.account.invalid"), err.Error())
		return
	}
	if err := s.EInvoiceAccounts.Put(r.Context(), t, env, in.Provider, in.Mode, raw); err != nil {
		s.Log.ErrorContext(r.Context(), "e-invoice account write failed", "err", err)
		fail(w, http.StatusInternalServerError, "common.internal", "internal error")
		return
	}
	writeJSON(w, http.StatusOK, store.EInvoiceAccountInfo{Environment: env, Provider: in.Provider, Mode: in.Mode, HasCredentials: len(in.Credentials) > 0})
}

func (s *Server) deleteEInvoiceAccount(w http.ResponseWriter, r *http.Request) {
	t, ok := s.tenant(w, r)
	if !ok || !s.einvoiceReady(w) {
		return
	}
	env, ok := pathEnvironment(w, r)
	if !ok {
		return
	}
	deleted, err := s.EInvoiceAccounts.Delete(r.Context(), t, env)
	switch {
	case err != nil:
		fail(w, http.StatusInternalServerError, "common.internal", "internal error")
	case !deleted:
		keyFail(w, http.StatusNotFound, "NOT_FOUND", errkey.Key("einvoice.account.missing"), "no such account")
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}
