package httpapi

import (
	"net/http"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
)

// What the operator does to a live tenant over time, and what its owners may say back (plan 14).

// appHold is GET /app/v1/apps/{id}/hold: the status of the app's live environment as its owners may know it, a status scheduled for it, the date a banned app's data is kept
// until, and the latest appeal. Any member may read it. It never carries the operator's category or note.
func (s *Server) appHold(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	resp, err := s.d.Identity.GetMyAppHold(r.Context(), &plansv1.GetMyAppHoldRequest{UserId: sess.UserID, AppId: r.PathValue("id")})
	respond(w, r, http.StatusOK, resp, err)
}

// appAppeal is POST /app/v1/apps/{id}/appeal {statement}: an owner states why the operator should lift the hold. 201 with the appeal.
func (s *Server) appAppeal(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	var body struct {
		Statement string `json:"statement"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	resp, err := s.d.Identity.SubmitMyAppAppeal(r.Context(), &plansv1.SubmitMyAppAppealRequest{UserId: sess.UserID, AppId: r.PathValue("id"), Statement: body.Statement})
	respond(w, r, http.StatusCreated, resp.GetAppeal(), err)
}

func (s *Server) tenantLifecycle(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Admin.GetTenantLifecycle(r.Context(), &plansv1.GetTenantLifecycleRequest{TenantId: r.PathValue("id")})
	respond(w, r, http.StatusOK, resp.GetLifecycle(), err)
}

// scheduleTenantStatus is PUT /admin/v1/tenants/{id}/scheduled-status {status, schedule_time, category, reason}: a suspension or a freeze at a future time.
func (s *Server) scheduleTenantStatus(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Status       string `json:"status"`
		ScheduleTime string `json:"schedule_time"`
		Category     string `json:"category"`
		Reason       string `json:"reason"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	at, err := time.Parse(time.RFC3339, body.ScheduleTime)
	if err != nil {
		writeError(w, r, invalid("schedule_time", "an RFC 3339 time in the future"))
		return
	}
	resp, err := s.d.Admin.ScheduleTenantStatus(r.Context(), &plansv1.ScheduleTenantStatusRequest{TenantId: r.PathValue("id"), Status: body.Status, ScheduleTime: timestamppb.New(at),
		Category: body.Category, Reason: body.Reason, Actor: actorOf(r)})
	respond(w, r, http.StatusOK, resp.GetLifecycle(), err)
}

func (s *Server) cancelScheduledTenantStatus(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Admin.CancelScheduledTenantStatus(r.Context(), &plansv1.CancelScheduledTenantStatusRequest{TenantId: r.PathValue("id"), Actor: actorOf(r)})
	respond(w, r, http.StatusOK, resp.GetLifecycle(), err)
}

// listTenantAppeals is GET /admin/v1/appeals?tenant_id=&status=: the owners' appeals, newest first.
func (s *Server) listTenantAppeals(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	resp, err := s.d.Admin.ListTenantAppeals(r.Context(), &plansv1.ListTenantAppealsRequest{TenantId: q.Get("tenant_id"), Status: q.Get("status")})
	respond(w, r, http.StatusOK, resp, err)
}

// decideTenantAppeal is POST /admin/v1/appeals/{id}/decision {decision, answer}: accepted (the tenant is reinstated) or rejected (an answer is required).
func (s *Server) decideTenantAppeal(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Decision string `json:"decision"`
		Answer   string `json:"answer"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	resp, err := s.d.Admin.DecideTenantAppeal(r.Context(), &plansv1.DecideTenantAppealRequest{AppealId: r.PathValue("id"), Decision: body.Decision, Answer: body.Answer, Actor: actorOf(r)})
	respond(w, r, http.StatusOK, resp.GetAppeal(), err)
}
