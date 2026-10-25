package httpapi

import (
	"net/http"
	"net/url"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/durationpb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/env"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/services/gateway/internal/ratelimit"
)

// Go-live (phase 45). The checklist is computed by plans-svc, except for the one item that lives in payments-worker's database: is a payment provider
// connected in live mode and working. The gateway asks payments-worker, then passes the answer to plans-svc, which only the gateway can call. Nothing the
// browser sends decides it: the body of the activation names no provider.

func (s *Server) goLiveRoutes() []Route {
	return []Route{
		{"GET", "/app/v1/apps/{id}/go-live", AccessSession, ratelimit.ClassRead, s.appGoLive},
		{"POST", "/app/v1/apps/{id}/go-live", AccessSession, ratelimit.ClassWrite, s.appGoLiveActivate},
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
