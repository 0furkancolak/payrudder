package httpapi

import (
	"net/http"
	"net/http/httputil"
	"net/url"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// payments-worker serves provider webhooks and the provider-account and ledger endpoints over plain HTTP. The gateway forwards to it:
// webhooks as they are (the provider is the caller, and the worker never believes a body anyway), operator calls with the shared
// internal token, and with the operator's own Authorization header removed.

func (s *Server) paymentsProxy(path func(*http.Request) string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.d.PaymentsURL == nil {
			writeError(w, r, status.Error(codes.Unavailable, "payment providers are not configured on this deployment"))
			return
		}
		proxy := &httputil.ReverseProxy{
			Rewrite: func(pr *httputil.ProxyRequest) {
				pr.SetURL(s.d.PaymentsURL)
				pr.Out.URL.Path = path(pr.In)
				pr.Out.URL.RawPath = ""
				pr.Out.Header.Del("Authorization")
				pr.Out.Header.Del("X-Internal-Token")
				if s.d.PaymentsToken != "" {
					pr.Out.Header.Set("X-Internal-Token", s.d.PaymentsToken)
				}
				pr.Out.Header.Set("X-Request-Id", requestID(pr.In.Context()))
			},
			ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
				writeError(w, r, status.Error(codes.Unavailable, "payments-worker is not reachable"))
			},
		}
		proxy.ServeHTTP(w, r)
	}
}

func (s *Server) providerWebhook(w http.ResponseWriter, r *http.Request) {
	s.paymentsProxy(func(r *http.Request) string { return "/webhooks/" + url.PathEscape(r.PathValue("provider")) })(w, r)
}

func (s *Server) providerAccounts(w http.ResponseWriter, r *http.Request) {
	s.paymentsProxy(func(r *http.Request) string {
		return "/admin/tenants/" + url.PathEscape(r.PathValue("tenant_id")) + "/provider-accounts"
	})(w, r)
}

func (s *Server) providerAccount(w http.ResponseWriter, r *http.Request) {
	s.paymentsProxy(func(r *http.Request) string {
		return "/admin/tenants/" + url.PathEscape(r.PathValue("tenant_id")) + "/provider-accounts/" + url.PathEscape(r.PathValue("provider"))
	})(w, r)
}

func (s *Server) paymentLedger(w http.ResponseWriter, r *http.Request) {
	s.paymentsProxy(func(r *http.Request) string {
		return "/admin/tenants/" + url.PathEscape(r.PathValue("tenant_id")) + "/ledger"
	})(w, r)
}

func (s *Server) tenantWebhook(w http.ResponseWriter, r *http.Request) {
	s.paymentsProxy(func(r *http.Request) string {
		return "/webhooks/" + url.PathEscape(r.PathValue("provider")) + "/" + url.PathEscape(r.PathValue("tenant_id")) + "/" + url.PathEscape(r.PathValue("environment"))
	})(w, r)
}

func (s *Server) providerSubscriptions(w http.ResponseWriter, r *http.Request) {
	s.paymentsProxy(func(r *http.Request) string {
		return "/admin/tenants/" + url.PathEscape(r.PathValue("tenant_id")) + "/provider-subscriptions"
	})(w, r)
}

func (s *Server) providerCheckout(w http.ResponseWriter, r *http.Request) {
	s.paymentsProxy(func(r *http.Request) string {
		return "/admin/tenants/" + url.PathEscape(r.PathValue("tenant_id")) + "/checkouts"
	})(w, r)
}
