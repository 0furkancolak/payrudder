package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"google.golang.org/grpc/codes"

	"github.com/0furkancolak/payrudder/pkg/env"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/services/gateway/internal/ratelimit"
)

// Registering the webhook endpoint of a provider account (plan 08): for a provider whose API creates it (Stripe), the owner of an app asks Payrudder to make the
// endpoint at the provider instead of copying an address and a signing secret by hand. The address is the gateway's own public one for this app's tenant and
// environment (/webhooks/{provider}/{tenant}/{environment}); the body of the request is empty and decides nothing, and the secret the provider shows goes straight into
// the stored account and is never in an answer.

func (s *Server) providerWebhookRoutes() []Route {
	return []Route{
		{"POST", "/app/v1/apps/{id}/go-live/providers/{provider}/webhook", AccessSession, ratelimit.ClassWrite, s.appLiveProviderWebhook},
		{"POST", "/app/v1/apps/{id}/sandbox/providers/{provider}/webhook", AccessSession, ratelimit.ClassWrite, s.appSandboxProviderWebhook},
	}
}

func (s *Server) appLiveProviderWebhook(w http.ResponseWriter, r *http.Request) {
	s.registerProviderWebhook(w, r, env.Live)
}

func (s *Server) appSandboxProviderWebhook(w http.ResponseWriter, r *http.Request) {
	s.registerProviderWebhook(w, r, env.Sandbox)
}

func (s *Server) registerProviderWebhook(w http.ResponseWriter, r *http.Request, e env.Env) {
	sess := sessionFrom(r.Context())
	tenant, err := s.ownerEnvTenant(r, sess.UserID, r.PathValue("id"), e)
	if err != nil {
		writeError(w, r, err)
		return
	}
	provider := r.PathValue("provider")
	if !connectableProviders(provider) {
		writeError(w, r, errkey.New(codes.NotFound, "payments.provider.unknown", "unknown provider"))
		return
	}
	hook := strings.TrimRight(s.d.PublicURL, "/") + "/webhooks/" + url.PathEscape(provider) + "/" + tenant + "/" + e.String()
	body, _ := json.Marshal(map[string]string{"url": hook})
	_, _ = io.Copy(io.Discard, http.MaxBytesReader(w, r.Body, 1<<16))
	r.Body, r.ContentLength = io.NopCloser(bytes.NewReader(body)), int64(len(body))
	r.Header.Set("Content-Type", "application/json")
	noStore(w)
	s.paymentsForward(func(*http.Request) string {
		return "/admin/tenants/" + url.PathEscape(tenant) + "/provider-accounts/" + url.PathEscape(provider) + "/webhook-endpoint"
	}, func(pr *httputil.ProxyRequest) {
		pr.Out.URL.RawQuery = url.Values{"environment": {e.String()}}.Encode()
		pr.Out.Header.Set("X-Payrudder-Actor", "user:"+sess.UserID)
	})(w, r)
}
