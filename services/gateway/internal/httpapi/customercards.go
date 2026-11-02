package httpapi

import (
	"net/http"
	"net/http/httputil"
	"net/url"
)

// The cards a payment provider holds for a customer (iyzico's card storage): the tenant lists them and deletes one. The tenant is the caller's own and the customer is
// found in it by the path; whose cards they are comes from the customer's payment method at payments-worker, never from the request, so a token of another customer
// is a 404. The sandbox has no stored cards (its payment methods are pm_sandbox_ tokens): the list is empty.

// customerCards forwards the list (del false) or the deletion of one card to payments-worker for the caller's tenant. An id may hold a "/": the path is sent both decoded and
// in its escaped form, so the worker's router reads the same customer the caller named.
func (s *Server) customerCards(del bool) http.HandlerFunc {
	parts := func(r *http.Request) []string {
		p := []string{"/admin/tenants/", principalFrom(r.Context()).Tenant.ID.String(), "/customers/", r.PathValue("external_id"), "/cards"}
		if del {
			p = append(p, "/", r.PathValue("token"))
		}
		return p
	}
	return s.paymentsForward(func(r *http.Request) string {
		out := ""
		for _, p := range parts(r) {
			out += p
		}
		return out
	}, func(pr *httputil.ProxyRequest) {
		out := ""
		for i, p := range parts(pr.In) {
			if i == 1 || i == 3 || (del && i == 6) { // the tenant, the customer and the token are names, the rest is the route
				p = url.PathEscape(p)
			}
			out += p
		}
		pr.Out.URL.RawPath = out
	})
}
