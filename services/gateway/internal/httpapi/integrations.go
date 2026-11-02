package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/env"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/payments"
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
		{"POST", "/app/v1/apps/{id}/integrations/e-invoice/default", AccessSession, ratelimit.ClassWrite, s.appEInvoiceDefault},
		{"GET", "/app/v1/apps/{id}/integrations/e-invoice/routing", AccessSession, ratelimit.ClassRead, s.appEInvoiceRoutingGet},
		{"PUT", "/app/v1/apps/{id}/integrations/e-invoice/routing", AccessSession, ratelimit.ClassWrite, s.appEInvoiceRoutingPut},
		{"GET", "/app/v1/apps/{id}/integrations/audit", AccessSession, ratelimit.ClassRead, s.appIntegrationAudit},
		{"GET", "/app/v1/apps/{id}/integrations/payment-accounts/health", AccessSession, ratelimit.ClassRead, s.appAccountHealth},
		{"PUT", "/app/v1/apps/{id}/integrations/payment-accounts/{account}/failover", AccessSession, ratelimit.ClassWrite, s.appFailover},
		{"DELETE", "/app/v1/apps/{id}/integrations/payment-accounts/{account}/failover", AccessSession, ratelimit.ClassWrite, s.appFailover},
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

// eInvoiceAccountJSON is an account of the live environment as the app sees it: Account is its key ("nilvera", "nilvera~tr"), Provider the integrator and Label
// the part after the tilde. No credential is ever in it.
type eInvoiceAccountJSON struct {
	Account   string `json:"account"`
	Provider  string `json:"provider"`
	Label     string `json:"label"`
	Mode      string `json:"mode"`
	IsDefault bool   `json:"is_default"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

// appEInvoiceGet lists the live e-invoice accounts of the app (no credential is in it), and the default among them as `account`. Any member reads it.
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
			eInvoiceAccountJSON
		} `json:"accounts"`
	}
	code, err := s.paymentsDo(r.Context(), http.MethodGet, "/admin/tenants/"+url.PathEscape(live)+"/e-invoice-accounts", nil, nil, &out)
	if err != nil || code/100 != 2 {
		writeError(w, r, errkey.New(codes.Unavailable, "gateway.payments.unreachable", "payments-worker is not reachable"))
		return
	}
	// Only the live accounts: the sandbox one is the simulator and is not the person's to see or change.
	res := struct {
		Account  *eInvoiceAccountJSON  `json:"account"`
		Accounts []eInvoiceAccountJSON `json:"accounts"`
	}{Accounts: []eInvoiceAccountJSON{}}
	for _, a := range out.Accounts {
		if a.Environment != env.Live.String() {
			continue
		}
		acc := a.eInvoiceAccountJSON
		res.Accounts = append(res.Accounts, acc)
		if acc.IsDefault {
			res.Account = &acc
		}
	}
	noStoreJSON(w)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(res)
}

// appEInvoicePut connects (or replaces) a live integrator account. The credentials go to payments-worker as they came, which validates and seals them. A body
// with a label connects a further account at the same integrator; one that says `default` makes it the default.
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

// accountParam reads the `account` of a request (a key like "nilvera~tr", or empty for the only account) and refuses one that is not a key.
func accountParam(v string) (string, error) {
	if v != "" && !payments.ValidAccountKey(v) {
		return "", invalid("account", "an account key such as nilvera or nilvera~tr")
	}
	return v, nil
}

// withAccount sends the account named by the request to payments-worker as its only query, whatever else the caller put there.
func withAccount(account string, sess session) func(*httputil.ProxyRequest) {
	return func(pr *httputil.ProxyRequest) {
		pr.Out.URL.RawQuery = ""
		if account != "" {
			pr.Out.URL.RawQuery = url.Values{"account": {account}}.Encode()
		}
		pr.Out.Header.Set("X-Payrudder-Actor", "user:"+sess.UserID)
	}
}

// appEInvoiceDelete disconnects the live account named by ?account= (the only one when the app has just one).
func (s *Server) appEInvoiceDelete(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	live, err := s.ownerLiveTenant(r, sess.UserID, r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	account, err := accountParam(r.URL.Query().Get("account"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.paymentsForward(s.eInvoicePath(live, ""), withAccount(account, sess))(w, r)
}

// appEInvoiceTest asks the integrator whether the stored credentials of an account work now (payments-worker's check). The body may name the account (the only
// one when absent) and carry the seller's tax id, which an integrator that can look up a registered business is asked about.
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

// appEInvoiceDefault makes one of the live accounts the default: the one a document goes to when no routing rule names its buyer's country. The account is not
// touched, so no credential is sent. Only the owner; recorded for this person.
func (s *Server) appEInvoiceDefault(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	live, err := s.ownerLiveTenant(r, sess.UserID, r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	var in struct {
		Account string `json:"account"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	account, err := accountParam(in.Account)
	if err != nil || account == "" {
		writeError(w, r, invalid("account", "an account key such as nilvera or nilvera~tr"))
		return
	}
	r.Body, r.ContentLength = http.NoBody, 0
	noStore(w)
	s.paymentsForward(s.eInvoicePath(live, "/default"), withAccount(account, sess))(w, r)
}

// appEInvoiceRoutingGet is the rules that send the documents of a buyer's country to an account. Any member reads them.
func (s *Server) appEInvoiceRoutingGet(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	live, _, err := s.liveTenantOf(r, sess.UserID, r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	noStore(w)
	s.paymentsForward(s.eInvoicePath(live, "/routing"), withAccount("", sess))(w, r)
}

// appEInvoiceRoutingPut replaces the rules. Only the owner; recorded for this person.
func (s *Server) appEInvoiceRoutingPut(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	live, err := s.ownerLiveTenant(r, sess.UserID, r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	noStore(w)
	s.paymentsForward(s.eInvoicePath(live, "/routing"), withAccount("", sess))(w, r)
}

// appAccountHealth is what payments-worker last saw of each live payment account (the periodic check, the circuit breaker) and the account a charge fails over
// to (plan 22 G8, docs/provider-health.md). Any member reads it.
func (s *Server) appAccountHealth(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	live, _, err := s.liveTenantOf(r, sess.UserID, r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	noStore(w)
	s.paymentsForward(func(*http.Request) string { return "/admin/tenants/" + url.PathEscape(live) + "/account-health" }, withAccount("", sess))(w, r)
}

// appFailover sets (PUT, {"failover_to": "stripe~eu"}) or removes (DELETE) the account a new charge goes to when the account in the path definitely did not process
// it. Only the owner; the live environment only; recorded for this person. payments-worker checks that both accounts exist and that the target can take part.
func (s *Server) appFailover(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	live, err := s.ownerLiveTenant(r, sess.UserID, r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	account, err := accountParam(r.PathValue("account"))
	if err != nil || account == "" {
		writeError(w, r, invalid("account", "an account key such as stripe or stripe~eu"))
		return
	}
	noStore(w)
	path := func(*http.Request) string {
		return "/admin/tenants/" + url.PathEscape(live) + "/provider-accounts/" + url.PathEscape(account) + "/failover"
	}
	s.paymentsForward(path, func(pr *httputil.ProxyRequest) {
		pr.Out.URL.RawQuery = url.Values{"environment": {env.Live.String()}}.Encode()
		pr.Out.Header.Set("X-Payrudder-Actor", "user:"+sess.UserID)
	})(w, r)
}

// auditKinds are the kinds of account the audit trail tells apart; the mail provider's changes are not in it (they are plans-svc's, not payments-worker's).
var auditKinds = map[string]bool{"": true, "payment": true, "einvoice": true}

// appIntegrationAudit is the audit trail of the live environment's payment and e-invoice accounts: who connected, replaced, removed or made default which
// account, and who changed the routing, newest first. No setting is ever in it. Any member reads it.
func (s *Server) appIntegrationAudit(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	live, _, err := s.liveTenantOf(r, sess.UserID, r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	q := r.URL.Query()
	if !auditKinds[q.Get("kind")] {
		writeError(w, r, invalid("kind", "payment or einvoice"))
		return
	}
	size, err := intParam(q, "page_size")
	if err != nil {
		writeError(w, r, err)
		return
	}
	query := url.Values{"limit": {strconv.Itoa(int(size))}, "kind": {q.Get("kind")}}
	if tok := q.Get("page_token"); tok != "" {
		if n, err := strconv.ParseInt(tok, 10, 64); err != nil || n <= 0 {
			writeError(w, r, invalid("page_token", "a token from a previous answer"))
			return
		}
		query.Set("before", q.Get("page_token"))
	}
	var out struct {
		Events []json.RawMessage `json:"events"`
		Next   int64             `json:"next_before"`
	}
	code, err := s.paymentsDo(r.Context(), http.MethodGet, "/admin/tenants/"+url.PathEscape(live)+"/provider-account-events", query, nil, &out)
	if err != nil || code/100 != 2 {
		writeError(w, r, errkey.New(codes.Unavailable, "gateway.payments.unreachable", "payments-worker is not reachable"))
		return
	}
	res := struct {
		Events        []json.RawMessage `json:"events"`
		NextPageToken string            `json:"next_page_token"`
	}{Events: out.Events}
	if res.Events == nil {
		res.Events = []json.RawMessage{}
	}
	if out.Next > 0 {
		res.NextPageToken = strconv.FormatInt(out.Next, 10)
	}
	noStoreJSON(w)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(res)
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
