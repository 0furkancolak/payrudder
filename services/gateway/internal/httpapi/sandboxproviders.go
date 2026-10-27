package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"

	"google.golang.org/grpc/codes"

	"github.com/0furkancolak/payrudder/pkg/env"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/payments/iyzico"
	"github.com/0furkancolak/payrudder/pkg/payments/paddle"
	"github.com/0furkancolak/payrudder/services/gateway/internal/ratelimit"
)

// The sandbox side of the payment providers of an app (phase 54). The sandbox runs on mock-psp until the owner connects a provider's own test mode (an iyzico or
// Paddle sandbox account): a sandbox charge that carries that provider's token then goes to it, the scenario tokens keep going to the mock. Like the live
// connection (go-live/providers), nothing in a request names a tenant or an environment: the tenant is the app's sandbox, the person is found through their
// membership, only the owner changes anything, the provider must accept the credentials before they are stored, and nothing a request sends can make the
// account a live one.

func (s *Server) sandboxProviderRoutes() []Route {
	return []Route{
		{"GET", "/app/v1/apps/{id}/sandbox/providers", AccessSession, ratelimit.ClassRead, s.appSandboxProviders},
		{"PUT", "/app/v1/apps/{id}/sandbox/providers/{provider}", AccessSession, ratelimit.ClassWrite, s.appSandboxProviderPut},
		{"DELETE", "/app/v1/apps/{id}/sandbox/providers/{provider}", AccessSession, ratelimit.ClassWrite, s.appSandboxProviderDelete},
	}
}

// sandboxHosts are the providers with a test mode a person can connect, and the host of it. A bank transfer has no test mode: its sandbox is the simulator.
var sandboxHosts = map[string]string{iyzico.Name: iyzico.SandboxURL, paddle.Name: paddle.SandboxURL}

// sandboxProvider checks the provider of the path: one Payrudder has an adapter for, and one that has a test mode.
func sandboxProvider(w http.ResponseWriter, r *http.Request) (string, bool) {
	provider := r.PathValue("provider")
	if !connectableProviders(provider) {
		writeError(w, r, errkey.New(codes.NotFound, "payments.provider.unknown", "unknown provider"))
		return "", false
	}
	if _, ok := sandboxHosts[provider]; !ok {
		writeError(w, r, errkey.New(codes.InvalidArgument, "payments.provider.not_supported", provider+" has no test mode to connect: the sandbox runs on the built-in simulator"))
		return "", false
	}
	return provider, true
}

// sandboxHealth reads the sandbox accounts of the tenant and whether each works.
func (s *Server) sandboxHealth(r *http.Request, tenant string) (json.RawMessage, int, error) {
	var out json.RawMessage
	code, err := s.paymentsDo(r.Context(), http.MethodGet, "/admin/tenants/"+url.PathEscape(tenant)+"/provider-health", url.Values{"environment": {env.Sandbox.String()}}, nil, &out)
	return out, code, err
}

// appSandboxProviders lists the sandbox provider accounts of the app and whether each works. No setting is in it. Any member reads it.
func (s *Server) appSandboxProviders(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	tenant, _, err := s.envTenantOf(r, sess.UserID, r.PathValue("id"), env.Sandbox)
	if err != nil {
		writeError(w, r, err)
		return
	}
	out, code, err := s.sandboxHealth(r, tenant)
	if err != nil || code/100 != 2 {
		writeError(w, r, errkey.New(codes.Unavailable, "gateway.payments.unreachable", "payments-worker is not reachable"))
		return
	}
	noStoreJSON(w)
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(out)
}

// appSandboxProviderPut connects (or replaces) the app's sandbox account at a provider. The person types the credentials and nothing else: the endpoint is the
// provider's test host unless the body names one, and one that is not the provider's own test host is refused (payments-worker's ?verify=true), so a live
// key sent to the live host, a live-format key, or a host of the person's own cannot be stored. The first sandbox account becomes the default.
func (s *Server) appSandboxProviderPut(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	tenant, err := s.ownerEnvTenant(r, sess.UserID, r.PathValue("id"), env.Sandbox)
	if err != nil {
		writeError(w, r, err)
		return
	}
	provider, ok := sandboxProvider(w, r)
	if !ok {
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		writeError(w, r, errkey.New(codes.InvalidArgument, "gateway.request.unreadable_body", "the request body could not be read"))
		return
	}
	var settings map[string]any
	if json.Unmarshal(raw, &settings) != nil || settings == nil {
		writeError(w, r, errkey.New(codes.InvalidArgument, "payments.account.bad_settings", "the settings must be a JSON object"))
		return
	}
	if v, _ := settings["base_url"].(string); v == "" {
		settings["base_url"] = sandboxHosts[provider]
	}
	body, _ := json.Marshal(settings)
	r.Body, r.ContentLength = io.NopCloser(bytes.NewReader(body)), int64(len(body))

	var existing struct {
		Accounts []struct {
			Default bool `json:"default"`
		} `json:"accounts"`
	}
	hasDefault := false
	if out, code, err := s.sandboxHealth(r, tenant); err == nil && code/100 == 2 && json.Unmarshal(out, &existing) == nil {
		for _, a := range existing.Accounts {
			hasDefault = hasDefault || a.Default
		}
	}
	noStore(w)
	s.paymentsForward(func(*http.Request) string {
		return "/admin/tenants/" + url.PathEscape(tenant) + "/provider-accounts/" + url.PathEscape(provider)
	}, func(pr *httputil.ProxyRequest) {
		pr.Out.URL.RawQuery = url.Values{"environment": {env.Sandbox.String()}, "verify": {"true"}, "default": {strconv.FormatBool(!hasDefault)}}.Encode()
		pr.Out.Header.Set("X-Payrudder-Actor", "user:"+sess.UserID)
	})(w, r)
}

// appSandboxProviderDelete removes the app's sandbox account at a provider. Only the owner; recorded for this person. The sandbox then runs on the simulator again.
func (s *Server) appSandboxProviderDelete(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	tenant, err := s.ownerEnvTenant(r, sess.UserID, r.PathValue("id"), env.Sandbox)
	if err != nil {
		writeError(w, r, err)
		return
	}
	provider, ok := sandboxProvider(w, r)
	if !ok {
		return
	}
	s.paymentsForward(func(*http.Request) string {
		return "/admin/tenants/" + url.PathEscape(tenant) + "/provider-accounts/" + url.PathEscape(provider)
	}, func(pr *httputil.ProxyRequest) {
		pr.Out.URL.RawQuery = url.Values{"environment": {env.Sandbox.String()}}.Encode()
		pr.Out.Header.Set("X-Payrudder-Actor", "user:"+sess.UserID)
	})(w, r)
}
