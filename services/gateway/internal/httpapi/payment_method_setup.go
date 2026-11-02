package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

// POST /v1/customers/{external_id}/payment-method-setup makes the link to the provider's own page where a customer saves a payment method for later charges (Stripe's
// Checkout in setup mode; plan 08). The tenant is the caller's own, in the environment of the key; the provider's customer is the one Payrudder remembers for this customer.
// Nothing is charged and nothing is set on the customer here: the method is set when the provider's notification says it was saved. Payrudder collects and keeps no consent in
// this flow: the seller sends the link with their own terms (the provider's guide asks for that consent), which is why there is no button on the customer's page.

// setupProviders are the providers whose adapter can open such a page; the worker refuses any other with its own key, this keeps the path it builds to a known name.
var setupProviders = map[string]bool{"stripe": true}

// paymentMethodSetup validates the body (a provider, default stripe, and the https address the customer returns to), then forwards to payments-worker's operator route for
// the caller's tenant and environment. The worker's answer, and its refusals with their keys, go to the caller as they are.
func (s *Server) paymentMethodSetup() http.HandlerFunc {
	// The path is sent decoded and in its escaped form (as the customer's cards are), so that a customer id that holds a "/" is one name for the worker's router too.
	route := func(r *http.Request, escape func(string) string) string {
		return "/admin/tenants/" + escape(principalFrom(r.Context()).Tenant.ID.String()) + "/providers/" + escape(r.PathValue("provider")) + "/customers/" + escape(r.PathValue("external_id")) + "/payment-method-setup"
	}
	forward := s.paymentsForward(func(r *http.Request) string { return route(r, func(v string) string { return v }) }, func(pr *httputil.ProxyRequest) {
		pr.Out.URL.RawPath = route(pr.In, url.PathEscape)
		pr.Out.URL.RawQuery = url.Values{"environment": {string(principalFrom(pr.In.Context()).Tenant.Environment)}}.Encode()
	})
	return func(w http.ResponseWriter, r *http.Request) {
		raw, err := readBody(r)
		if err != nil {
			writeError(w, r, err)
			return
		}
		var in struct {
			Provider  string `json:"provider"`
			ReturnURL string `json:"return_url"`
		}
		if err := json.Unmarshal(raw, &in); err != nil {
			writeError(w, r, invalid("body", `must be JSON like {"return_url": "https://..."}`))
			return
		}
		in.Provider = strings.ToLower(strings.TrimSpace(in.Provider))
		if in.Provider == "" {
			in.Provider = "stripe"
		}
		if !setupProviders[in.Provider] {
			writeError(w, r, invalid("provider", "must be stripe: the only provider that has the customer save a payment method on its own page"))
			return
		}
		if u, err := url.Parse(in.ReturnURL); err != nil || u.Scheme != "https" || u.Host == "" || len(in.ReturnURL) > 2000 {
			writeError(w, r, invalid("return_url", "must be an https:// address (at most 2000 characters)"))
			return
		}
		r.SetPathValue("provider", in.Provider)
		body, _ := json.Marshal(map[string]string{"return_url": in.ReturnURL}) // only what the worker takes
		r.Body, r.ContentLength = io.NopCloser(bytes.NewReader(body)), int64(len(body))
		forward(w, r)
	}
}
