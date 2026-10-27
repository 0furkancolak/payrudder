package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/env"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/gateway/internal/ratelimit"
)

// The Integrations page of an app (phase 53): the e-invoice integrator and the e-mail provider of the app's live environment, next to the payment providers
// (go-live/providers). Like those, nothing in a request names a tenant or an environment: both are the app's live ones, found through the signed-in
// person's membership; only the owner may change anything, and a credential goes in once and is never in an answer. The sandbox has neither: its e-invoices
// go to the built-in simulator and its mail to the sandbox inbox.

func (s *Server) integrationRoutes() []Route {
	return []Route{
		{"GET", "/app/v1/apps/{id}/integrations/e-invoice", AccessSession, ratelimit.ClassRead, s.appEInvoiceGet},
		{"PUT", "/app/v1/apps/{id}/integrations/e-invoice", AccessSession, ratelimit.ClassWrite, s.appEInvoicePut},
		{"DELETE", "/app/v1/apps/{id}/integrations/e-invoice", AccessSession, ratelimit.ClassWrite, s.appEInvoiceDelete},
		{"POST", "/app/v1/apps/{id}/integrations/e-invoice/test", AccessSession, ratelimit.ClassWrite, s.appEInvoiceTest},
		{"GET", "/app/v1/apps/{id}/integrations/mail", AccessSession, ratelimit.ClassRead, s.appMailGet},
		{"PUT", "/app/v1/apps/{id}/integrations/mail", AccessSession, ratelimit.ClassWrite, s.appMailPut},
		{"DELETE", "/app/v1/apps/{id}/integrations/mail", AccessSession, ratelimit.ClassWrite, s.appMailDelete},
		{"POST", "/app/v1/apps/{id}/integrations/mail/test", AccessSession, ratelimit.ClassWrite, s.appMailTest},
	}
}

// einvoiceProviders are the integrators a person may connect: the ones with an adapter that talks to a real service. The mock never is one.
var einvoiceProviders = map[string]bool{"nilvera": true, "webhook": true}

func (s *Server) eInvoicePath(tenantID string, suffix string) func(*http.Request) string {
	return func(*http.Request) string {
		return "/admin/tenants/" + url.PathEscape(tenantID) + "/e-invoice-accounts/" + env.Live.String() + suffix
	}
}

// appEInvoiceGet lists the live e-invoice account of the app (no credential is in it). Any member reads it.
func (s *Server) appEInvoiceGet(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	live, _, err := s.liveTenantOf(r, sess.UserID, r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	var out struct {
		Accounts []struct {
			Environment string `json:"environment"`
			Provider    string `json:"provider"`
			Mode        string `json:"mode"`
			UpdatedAt   string `json:"updated_at"`
		} `json:"accounts"`
	}
	code, err := s.paymentsDo(r.Context(), http.MethodGet, "/admin/tenants/"+url.PathEscape(live)+"/e-invoice-accounts", nil, nil, &out)
	if err != nil || code/100 != 2 {
		writeError(w, r, errkey.New(codes.Unavailable, "gateway.payments.unreachable", "payments-worker is not reachable"))
		return
	}
	// Only the live account: the sandbox one is the simulator and is not the person's to see or change.
	type account struct {
		Provider  string `json:"provider"`
		Mode      string `json:"mode"`
		UpdatedAt string `json:"updated_at,omitempty"`
	}
	res := struct {
		Account *account `json:"account"`
	}{}
	for _, a := range out.Accounts {
		if a.Environment == env.Live.String() {
			res.Account = &account{Provider: a.Provider, Mode: a.Mode, UpdatedAt: a.UpdatedAt}
		}
	}
	noStoreJSON(w)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(res)
}

// appEInvoicePut connects (or replaces) the live integrator account. The credentials go to payments-worker as they came, which validates and seals them.
func (s *Server) appEInvoicePut(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	live, err := s.ownerLiveTenant(r, sess.UserID, r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, r, errkey.New(codes.InvalidArgument, "gateway.request.unreadable_body", "the request body could not be read"))
		return
	}
	var in struct {
		Provider string `json:"provider"`
	}
	if json.Unmarshal(raw, &in) != nil || !einvoiceProviders[in.Provider] {
		writeError(w, r, invalid("provider", "nilvera or webhook"))
		return
	}
	r.Body, r.ContentLength = io.NopCloser(bytes.NewReader(raw)), int64(len(raw))
	noStore(w)
	s.paymentsForward(s.eInvoicePath(live, ""), func(pr *httputil.ProxyRequest) { pr.Out.Header.Set("X-Payrudder-Actor", "user:"+sess.UserID) })(w, r)
}

func (s *Server) appEInvoiceDelete(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	live, err := s.ownerLiveTenant(r, sess.UserID, r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.paymentsForward(s.eInvoicePath(live, ""), func(pr *httputil.ProxyRequest) { pr.Out.Header.Set("X-Payrudder-Actor", "user:"+sess.UserID) })(w, r)
}

// appEInvoiceTest asks the integrator whether the stored credentials work now (payments-worker's check). The body may carry the seller's tax id, which an
// integrator that can look up a registered business is asked about.
func (s *Server) appEInvoiceTest(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	live, err := s.ownerLiveTenant(r, sess.UserID, r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	noStore(w)
	s.paymentsForward(s.eInvoicePath(live, "/check"), nil)(w, r)
}

// mailTenant is the live tenant of the app as a uuid, owner only.
func (s *Server) mailTenant(r *http.Request) (uuid.UUID, error) {
	sess := sessionFrom(r.Context())
	id, err := s.ownerLiveTenant(r, sess.UserID, r.PathValue("id"))
	if err != nil {
		return uuid.Nil, err
	}
	return uuid.Parse(id)
}

// appMailGet is the connection of the live environment (no key in it). Any member reads it.
func (s *Server) appMailGet(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	live, _, err := s.liveTenantOf(r, sess.UserID, r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	id, perr := uuid.Parse(live)
	if perr != nil {
		writeError(w, r, errkey.New(codes.Unavailable, "identity.tenant.lookup_unavailable", "tenant lookup unavailable"))
		return
	}
	resp, err := s.d.MailIntegration.GetMailIntegration(tenant.OutgoingFor(r.Context(), id), &plansv1.GetMailIntegrationRequest{})
	noStore(w)
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) appMailPut(w http.ResponseWriter, r *http.Request) {
	id, err := s.mailTenant(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	req := &plansv1.PutMailIntegrationRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	noStore(w)
	resp, err := s.d.MailIntegration.PutMailIntegration(tenant.OutgoingFor(r.Context(), id), req)
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) appMailDelete(w http.ResponseWriter, r *http.Request) {
	id, err := s.mailTenant(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if _, err := s.d.MailIntegration.DeleteMailIntegration(tenant.OutgoingFor(r.Context(), id), &plansv1.DeleteMailIntegrationRequest{}); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) appMailTest(w http.ResponseWriter, r *http.Request) {
	id, err := s.mailTenant(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	req := &plansv1.TestMailIntegrationRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	noStore(w)
	resp, err := s.d.MailIntegration.TestMailIntegration(tenant.OutgoingFor(r.Context(), id), req)
	respond(w, r, http.StatusOK, resp, err)
}
