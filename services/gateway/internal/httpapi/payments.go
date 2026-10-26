package httpapi

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strings"

	"google.golang.org/grpc/codes"

	"github.com/0furkancolak/payrudder/pkg/errkey"
)

// payments-worker serves provider webhooks and the provider-account and ledger endpoints over plain HTTP. The gateway forwards to it:
// webhooks as they are (the provider is the caller, and the worker never believes a body anyway), operator calls with the shared
// internal token, and with the operator's own Authorization header removed.

func (s *Server) paymentsProxy(path func(*http.Request) string) http.HandlerFunc {
	return s.paymentsForward(path, nil)
}

// paymentsForward is paymentsProxy with a last word on the outgoing request: tweak may set a query or headers the caller's own request must not decide (the
// environment of an owner's provider account, who is asking). It runs after the caller's Authorization and internal-token headers are gone.
func (s *Server) paymentsForward(path func(*http.Request) string, tweak func(*httputil.ProxyRequest)) http.HandlerFunc {
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
			if who := operatorFrom(pr.In.Context()); who != "" {
				pr.Out.Header.Set(actorHeader, who) // a signed-in operator is who the receipt is recorded for, not whom the request claims
			}
			if tweak != nil {
				tweak(pr)
			}
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			writeError(w, r, errkey.New(codes.Unavailable, "gateway.payments.unreachable", "payments-worker is not reachable"))
		},
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if s.d.PaymentsURL == nil {
			writeError(w, r, errkey.New(codes.Unavailable, "gateway.payments.not_configured", "payment providers are not configured on this deployment"))
			return
		}
		proxy.ServeHTTP(w, r)
	}
}

// proxy forwards a route to payments-worker's path template: {name} is replaced by the (escaped) value of the path variable of that name. The proxy is built
// once, with the route.
func (s *Server) proxy(template string) http.HandlerFunc {
	return s.paymentsProxy(func(r *http.Request) string {
		return pathVarRe.ReplaceAllStringFunc(template, func(v string) string { return url.PathEscape(r.PathValue(v[1 : len(v)-1])) })
	})
}

var pathVarRe = regexp.MustCompile(`\{[a-z_]+\}`)

// providerAPI forwards what a provider can do beyond charging (catalogue, customers, subscriptions, discounts, documents) to payments-worker, which keeps the
// admin path below /admin/v1 as /admin/tenants/{tenant}/providers/....
func (s *Server) providerAPI() http.HandlerFunc {
	return s.paymentsProxy(func(r *http.Request) string {
		return "/admin/tenants/" + url.PathEscape(r.PathValue("tenant_id")) + strings.TrimPrefix(r.URL.Path, "/admin/v1/tenants/"+r.PathValue("tenant_id"))
	})
}

// invoiceTransfer forwards the bank transfer endpoints of an invoice (its state, a confirmed receipt, a rejection) to payments-worker, with the actor the
// receipt is recorded for: the signed-in operator, or for the machine token its X-Payrudder-Actor header.
func (s *Server) invoiceTransfer() http.HandlerFunc {
	return s.paymentsProxy(func(r *http.Request) string {
		return "/admin" + strings.TrimPrefix(r.URL.Path, "/admin/v1")
	})
}
