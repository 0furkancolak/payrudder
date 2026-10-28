package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/durationpb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/env"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/payments/providers"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/gateway/internal/ratelimit"
)

// Go-live (phase 45). The checklist is computed by plans-svc, except for the one item that lives in payments-worker's database: is a payment provider
// connected in live mode and working. The gateway asks payments-worker, then passes the answer to plans-svc, which only the gateway can call. Nothing the
// browser sends decides it: the body of the activation names no provider.

func (s *Server) goLiveRoutes() []Route {
	return []Route{
		{"GET", "/app/v1/apps/{id}/go-live", AccessSession, ratelimit.ClassRead, s.appGoLive},
		{"POST", "/app/v1/apps/{id}/go-live", AccessSession, ratelimit.ClassWrite, s.appGoLiveActivate},
		{"PUT", "/app/v1/apps/{id}/billing-profile", AccessSession, ratelimit.ClassWrite, s.appBillingProfile},
		{"POST", "/app/v1/apps/{id}/go-live/terms", AccessSession, ratelimit.ClassWrite, s.appAcceptTerms},
		{"GET", "/app/v1/apps/{id}/go-live/providers", AccessSession, ratelimit.ClassRead, s.appLiveProviders},
		{"PUT", "/app/v1/apps/{id}/go-live/providers/{provider}", AccessSession, ratelimit.ClassWrite, s.appLiveProviderPut},
		{"DELETE", "/app/v1/apps/{id}/go-live/providers/{provider}", AccessSession, ratelimit.ClassWrite, s.appLiveProviderDelete},
		{"GET", "/app/v1/apps/{id}/go-live/routing", AccessSession, ratelimit.ClassRead, s.appLiveRoutingGet},
		{"PUT", "/app/v1/apps/{id}/go-live/routing", AccessSession, ratelimit.ClassWrite, s.appLiveRoutingPut},
		{"POST", "/app/v1/apps/{id}/go-live/providers/{provider}/default", AccessSession, ratelimit.ClassWrite, s.appLiveProviderDefault},
		{"POST", "/app/v1/apps/{id}/api-keys/{key_id}/rotate", AccessSession, ratelimit.ClassWrite, s.appKeyRotate},
		{"POST", "/app/v1/apps/{id}/promote", AccessSession, ratelimit.ClassWrite, s.appPromote},
		{"PUT", "/admin/v1/tenants/{id}/status", AccessOperator, "", s.setTenantStatus},
	}
}

// providerReady asks payments-worker whether the live tenant of the app has a working provider account. Anything but a clear yes is a no: a deployment
// without payments-worker cannot go live, and an outage does not let an incomplete list through.
func (s *Server) providerReady(r *http.Request, userID, appID string, ownerOnly bool) bool {
	app, err := s.d.Identity.GetMyApp(r.Context(), &plansv1.GetMyAppRequest{UserId: userID, AppId: appID})
	if err != nil || (ownerOnly && app.GetRole() != "owner") {
		// Not a member (the answer is "not found" anyway), or an activation by someone who may not activate: payments-worker is not bothered.
		return false
	}
	var live string
	for _, t := range app.GetApp().GetTenants() {
		if t.GetEnvironment() == env.Live.Proto() {
			live = t.GetId()
		}
	}
	if live == "" {
		return false
	}
	var out struct {
		Ready bool `json:"ready"`
	}
	code, err := s.paymentsDo(r.Context(), http.MethodGet, "/admin/tenants/"+url.PathEscape(live)+"/go-live", nil, nil, &out)
	return err == nil && code/100 == 2 && out.Ready
}

func (s *Server) appGoLive(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	appID := r.PathValue("id")
	resp, err := s.d.Identity.GetMyGoLive(r.Context(), &plansv1.GetMyGoLiveRequest{UserId: sess.UserID, AppId: appID, ProviderReady: s.providerReady(r, sess.UserID, appID, false)})
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) appGoLiveActivate(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	appID := r.PathValue("id")
	var body struct {
		AcceptTerms bool `json:"accept_terms"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	resp, err := s.d.Identity.ActivateMyApp(r.Context(), &plansv1.ActivateMyAppRequest{UserId: sess.UserID, AppId: appID, AcceptTerms: body.AcceptTerms,
		ProviderReady: s.providerReady(r, sess.UserID, appID, true)})
	respond(w, r, http.StatusOK, resp, err)
}

// The owner's own steps to go live (phase 45 follow-up): the seller's billing profile, the terms, and the live payment provider. Nothing in a request names a
// tenant or an environment: both are the app's live environment, found through the signed-in person's membership, and only the owner may change anything.

// appBillingProfile saves the legal name, tax id and address of the app's live environment.
func (s *Server) appBillingProfile(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	req := &plansv1.UpdateMyBillingProfileRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	req.UserId, req.AppId = sess.UserID, r.PathValue("id")
	resp, err := s.d.Identity.UpdateMyBillingProfile(r.Context(), req)
	respond(w, r, http.StatusOK, resp.GetTenant(), err)
}

// appAcceptTerms records the owner's acceptance of the terms of service.
func (s *Server) appAcceptTerms(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	resp, err := s.d.Identity.AcceptMyTerms(r.Context(), &plansv1.AcceptMyTermsRequest{UserId: sess.UserID, AppId: r.PathValue("id")})
	respond(w, r, http.StatusOK, resp, err)
}

// liveTenantOf is the live tenant of an app the person belongs to, and whether they own it. An app that is not theirs is not found; a deployment without a
// live environment says so.
func (s *Server) liveTenantOf(r *http.Request, userID, appID string) (tenantID string, owner bool, err error) {
	return s.envTenantOf(r, userID, appID, env.Live)
}

// envTenantOf is the tenant of an app in an environment, found through the signed-in person's membership, and whether they own the app.
func (s *Server) envTenantOf(r *http.Request, userID, appID string, e env.Env) (tenantID string, owner bool, err error) {
	app, err := s.d.Identity.GetMyApp(r.Context(), &plansv1.GetMyAppRequest{UserId: userID, AppId: appID})
	if err != nil {
		return "", false, err
	}
	for _, t := range app.GetApp().GetTenants() {
		if t.GetEnvironment() == e.Proto() {
			return t.GetId(), app.GetRole() == "owner", nil
		}
	}
	if e == env.Sandbox {
		return "", false, errkey.New(codes.NotFound, "identity.app.not_found", "app not found")
	}
	return "", false, errkey.New(codes.FailedPrecondition, "identity.go_live.no_live", "this deployment has no live environment")
}

// ownerLiveTenant is liveTenantOf for a change: only the owner passes.
func (s *Server) ownerLiveTenant(r *http.Request, userID, appID string) (string, error) {
	return s.ownerEnvTenant(r, userID, appID, env.Live)
}

func (s *Server) ownerEnvTenant(r *http.Request, userID, appID string, e env.Env) (string, error) {
	tenant, owner, err := s.envTenantOf(r, userID, appID, e)
	if err != nil {
		return "", err
	}
	if !owner {
		return "", errkey.New(codes.PermissionDenied, "identity.go_live.owner_only", "only an owner of the account can connect a payment provider")
	}
	return tenant, nil
}

// connectableProviders are the providers a person may connect to their live environment: the ones Payrudder has adapters for. The mock never is one.
func connectableProviders(name string) bool {
	_, ok := providers.Lookup(name)
	return ok
}

// appLiveProviders lists the live provider accounts of the app and whether each one works, as payments-worker's go-live check sees them. No setting is in it.
// Any member reads it, like the checklist.
func (s *Server) appLiveProviders(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	tenant, _, err := s.liveTenantOf(r, sess.UserID, r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	var out json.RawMessage
	code, err := s.paymentsDo(r.Context(), http.MethodGet, "/admin/tenants/"+url.PathEscape(tenant)+"/go-live", nil, nil, &out)
	if err != nil || code/100 != 2 {
		writeError(w, r, errkey.New(codes.Unavailable, "gateway.payments.unreachable", "payments-worker is not reachable"))
		return
	}
	noStoreJSON(w)
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(out)
}

// appLiveProviderPut connects (or replaces) the app's live account at a provider. The settings go to payments-worker as they came and are never answered
// back; the environment is live whatever the request says, the provider must accept the credentials before they are stored (and only the provider's own hosts
// are allowed: payments-worker's ?verify=true), and the change is recorded for this person. The first account becomes the default.
func (s *Server) appLiveProviderPut(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	tenant, err := s.ownerLiveTenant(r, sess.UserID, r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	provider := r.PathValue("provider")
	if !connectableProviders(provider) {
		writeError(w, r, errkey.New(codes.NotFound, "payments.provider.unknown", "unknown provider"))
		return
	}
	var existing struct {
		Accounts []struct {
			Default bool `json:"default"`
		} `json:"accounts"`
	}
	hasDefault := false
	if code, err := s.paymentsDo(r.Context(), http.MethodGet, "/admin/tenants/"+url.PathEscape(tenant)+"/go-live", nil, nil, &existing); err == nil && code/100 == 2 {
		for _, a := range existing.Accounts {
			hasDefault = hasDefault || a.Default
		}
	}
	noStore(w)
	s.paymentsForward(func(*http.Request) string {
		return "/admin/tenants/" + url.PathEscape(tenant) + "/provider-accounts/" + url.PathEscape(provider)
	}, func(pr *httputil.ProxyRequest) {
		pr.Out.URL.RawQuery = url.Values{"environment": {env.Live.String()}, "verify": {"true"}, "default": {strconv.FormatBool(!hasDefault)}}.Encode()
		pr.Out.Header.Set("X-Payrudder-Actor", "user:"+sess.UserID)
	})(w, r)
}

// appLiveProviderDelete removes the app's live account at a provider. Only the owner; recorded for this person.
func (s *Server) appLiveProviderDelete(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	tenant, err := s.ownerLiveTenant(r, sess.UserID, r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	provider := r.PathValue("provider")
	if !connectableProviders(provider) {
		writeError(w, r, errkey.New(codes.NotFound, "payments.provider.unknown", "unknown provider"))
		return
	}
	s.paymentsForward(func(*http.Request) string {
		return "/admin/tenants/" + url.PathEscape(tenant) + "/provider-accounts/" + url.PathEscape(provider)
	}, func(pr *httputil.ProxyRequest) {
		pr.Out.URL.RawQuery = url.Values{"environment": {env.Live.String()}}.Encode()
		pr.Out.Header.Set("X-Payrudder-Actor", "user:"+sess.UserID)
	})(w, r)
}

// appLiveProviderDefault makes one of the app's live accounts the default: the one charges go to when no routing rule names another. Only the owner; recorded
// for this person (the account's settings are not touched, so none is sent).
func (s *Server) appLiveProviderDefault(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	tenant, err := s.ownerLiveTenant(r, sess.UserID, r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	provider := r.PathValue("provider")
	if !connectableProviders(provider) {
		writeError(w, r, errkey.New(codes.NotFound, "payments.provider.unknown", "unknown provider"))
		return
	}
	noStore(w)
	s.paymentsForward(func(*http.Request) string {
		return "/admin/tenants/" + url.PathEscape(tenant) + "/provider-accounts/" + url.PathEscape(provider) + "/default"
	}, func(pr *httputil.ProxyRequest) {
		pr.Out.URL.RawQuery = url.Values{"environment": {env.Live.String()}}.Encode()
		pr.Out.Header.Set("X-Payrudder-Actor", "user:"+sess.UserID)
	})(w, r)
}

// appLiveRoutingGet returns the routing rules of the app's live environment. Any member reads them.
func (s *Server) appLiveRoutingGet(w http.ResponseWriter, r *http.Request) {
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
	resp, err := s.d.Plans.GetRoutingRules(tenant.OutgoingFor(r.Context(), id), &plansv1.GetRoutingRulesRequest{})
	noStore(w)
	respond(w, r, http.StatusOK, resp, err)
}

// appLiveRoutingPut replaces the routing rules of the app's live environment: which account takes a customer of a country paying in a currency. Only the owner.
func (s *Server) appLiveRoutingPut(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	live, err := s.ownerLiveTenant(r, sess.UserID, r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	id, perr := uuid.Parse(live)
	if perr != nil {
		writeError(w, r, errkey.New(codes.Unavailable, "identity.tenant.lookup_unavailable", "tenant lookup unavailable"))
		return
	}
	req := &plansv1.SetRoutingRulesRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	noStore(w)
	resp, err := s.d.Plans.SetRoutingRules(tenant.OutgoingFor(r.Context(), id), req)
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) appKeyRotate(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	var body struct {
		// OverlapHours is how long the old key keeps working; absent is 24, 0 ends it now.
		OverlapHours *float64 `json:"overlap_hours"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	req := &plansv1.RotateMyApiKeyRequest{UserId: sess.UserID, AppId: r.PathValue("id"), ApiKeyId: r.PathValue("key_id")}
	if h := body.OverlapHours; h != nil {
		if *h < 0 || *h > 7*24 {
			writeError(w, r, invalid("overlap_hours", "between 0 and 168"))
			return
		}
		req.Overlap = durationpb.New(time.Duration(*h * float64(time.Hour)))
	}
	noStore(w)
	resp, err := s.d.Identity.RotateMyApiKey(r.Context(), req)
	respond(w, r, http.StatusCreated, resp, err)
}

// setTenantStatus is the operator's hand on a tenant: suspend, reinstate, or activate without the checklist (a reason is required for that).
func (s *Server) setTenantStatus(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Status string `json:"status"`
		Reason string `json:"reason"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	var st plansv1.TenantStatus
	switch body.Status {
	case "active":
		st = plansv1.TenantStatus_TENANT_STATUS_ACTIVE
	case "suspended":
		st = plansv1.TenantStatus_TENANT_STATUS_SUSPENDED
	default:
		writeError(w, r, errkey.New(codes.InvalidArgument, "plans.tenant.bad_status", "status: active or suspended"))
		return
	}
	resp, err := s.d.Admin.SetTenantStatus(r.Context(), &plansv1.SetTenantStatusRequest{TenantId: r.PathValue("id"), Status: st, Reason: body.Reason})
	respond(w, r, http.StatusOK, resp.GetTenant(), err)
}

// appPromote copies the app's sandbox configuration to its live environment (phase 45 D). With dry_run it only returns the diff; without, it applies it, and
// plan_hash (from a dry run) makes it refuse when the diff is no longer the one that was looked at. Nothing in the body names a tenant: both are the app's.
func (s *Server) appPromote(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	var body struct {
		DryRun   bool   `json:"dry_run"`
		PlanHash string `json:"plan_hash"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	noStore(w) // a new webhook secret is in the answer
	resp, err := s.d.Identity.PromoteMyApp(r.Context(), &plansv1.PromoteMyAppRequest{UserId: sess.UserID, AppId: r.PathValue("id"), DryRun: body.DryRun, PlanHash: body.PlanHash})
	respond(w, r, http.StatusOK, resp, err)
}
