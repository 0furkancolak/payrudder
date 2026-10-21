package httpapi

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

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

// tenantPayments is GET /v1/payments: the payment ledger of the caller's own tenant, the same rows the operator reads at /admin/v1/tenants/{id}/ledger. The
// tenant is the one the credentials name (an API key, or a person's session acting as a tenant of their account), never one taken from the request.
func (s *Server) tenantPayments(w http.ResponseWriter, r *http.Request) {
	id := principalFrom(r.Context()).Tenant.ID.String()
	s.paymentsProxy(func(*http.Request) string { return "/admin/tenants/" + url.PathEscape(id) + "/ledger" })(w, r)
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

func (s *Server) providerMigrate(w http.ResponseWriter, r *http.Request) {
	s.paymentsProxy(func(r *http.Request) string {
		return "/admin/tenants/" + url.PathEscape(r.PathValue("tenant_id")) + "/migrations"
	})(w, r)
}

func (s *Server) providerMigrationAbort(w http.ResponseWriter, r *http.Request) {
	s.paymentsProxy(func(r *http.Request) string {
		return "/admin/tenants/" + url.PathEscape(r.PathValue("tenant_id")) + "/migrations/" + url.PathEscape(r.PathValue("migration_id")) + "/abort"
	})(w, r)
}

// providerAPI forwards what a provider can do beyond charging (catalogue, customers, subscriptions, discounts, documents) to payments-worker, which keeps the
// admin path below /admin/v1 as /admin/tenants/{tenant}/providers/....
func (s *Server) providerAPI(w http.ResponseWriter, r *http.Request) {
	s.paymentsProxy(func(r *http.Request) string {
		return "/admin/tenants/" + url.PathEscape(r.PathValue("tenant_id")) + strings.TrimPrefix(r.URL.Path, "/admin/v1/tenants/"+r.PathValue("tenant_id"))
	})(w, r)
}

// invoiceTransfer forwards the bank transfer endpoints of an invoice (its state, a confirmed receipt, a rejection) to payments-worker. The caller's
// X-Quotify-Actor header travels with it: it is who the receipt is recorded for.
func (s *Server) invoiceTransfer(w http.ResponseWriter, r *http.Request) {
	s.paymentsProxy(func(r *http.Request) string {
		return "/admin" + strings.TrimPrefix(r.URL.Path, "/admin/v1")
	})(w, r)
}

func (s *Server) paymentReconciliation(w http.ResponseWriter, r *http.Request) {
	s.paymentsProxy(func(r *http.Request) string {
		return "/admin/tenants/" + url.PathEscape(r.PathValue("tenant_id")) + "/reconciliation"
	})(w, r)
}
