package httpapi

import (
	"context"
	"net/http"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
)

func (f *fakeIdentity) GetMyAppHold(_ context.Context, req *plansv1.GetMyAppHoldRequest) (*plansv1.GetMyAppHoldResponse, error) {
	f.seen(req.GetUserId())
	return &plansv1.GetMyAppHoldResponse{Status: plansv1.TenantStatus_TENANT_STATUS_BANNED, CanAppeal: true, RetentionUntil: timestamppb.Now()}, nil
}

func (f *fakeIdentity) SubmitMyAppAppeal(_ context.Context, req *plansv1.SubmitMyAppAppealRequest) (*plansv1.SubmitMyAppAppealResponse, error) {
	f.seen(req.GetUserId())
	f.apps.appeals = append(f.apps.appeals, req)
	return &plansv1.SubmitMyAppAppealResponse{Appeal: &plansv1.TenantAppeal{Id: "a1", Status: "open", Statement: req.GetStatement()}}, nil
}

// An owner reads the hold and appeals through the session: the user is the session's, the app is the path's, and no session is no answer.
func TestAppHoldAndAppealRoutes(t *testing.T) {
	p := newProduct(t, nil, nil)
	if resp, m := p.call(t, "GET", "/app/v1/apps/"+adaApp+"/hold", tokAda, "", ""); resp.StatusCode != http.StatusOK || m["can_appeal"] != true {
		t.Fatalf("hold: %d %v", resp.StatusCode, m)
	}
	resp, m := p.call(t, "POST", "/app/v1/apps/"+adaApp+"/appeal", tokAda, "", `{"statement":"Please look again.","user_id":"forged"}`)
	if resp.StatusCode != http.StatusCreated || m["status"] != "open" {
		t.Fatalf("appeal: %d %v", resp.StatusCode, m)
	}
	if got := p.ident.apps.appeals[0]; got.GetUserId() != adaUser || got.GetAppId() != adaApp || got.GetStatement() != "Please look again." {
		t.Errorf("request = %v: the user comes from the session and the app from the path", got)
	}
	for _, path := range []string{"/hold", "/appeal"} {
		method := map[string]string{"/hold": "GET", "/appeal": "POST"}[path]
		if resp, _ := p.call(t, method, "/app/v1/apps/"+adaApp+path, "", "", `{"statement":"x"}`); resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s without a session: %d", path, resp.StatusCode)
		}
	}
}

func (f *fakePlans) GetTenantLifecycle(context.Context, *plansv1.GetTenantLifecycleRequest) (*plansv1.GetTenantLifecycleResponse, error) {
	return &plansv1.GetTenantLifecycleResponse{Lifecycle: &plansv1.TenantLifecycle{ScheduledStatus: "suspended"}}, nil
}

func (f *fakePlans) ScheduleTenantStatus(_ context.Context, req *plansv1.ScheduleTenantStatusRequest) (*plansv1.ScheduleTenantStatusResponse, error) {
	f.lastSchedule = req
	return &plansv1.ScheduleTenantStatusResponse{Lifecycle: &plansv1.TenantLifecycle{ScheduledStatus: req.GetStatus(), ScheduledTime: req.GetScheduleTime()}}, nil
}

func (f *fakePlans) CancelScheduledTenantStatus(_ context.Context, req *plansv1.CancelScheduledTenantStatusRequest) (*plansv1.CancelScheduledTenantStatusResponse, error) {
	f.lastCancel = req
	return &plansv1.CancelScheduledTenantStatusResponse{Lifecycle: &plansv1.TenantLifecycle{}}, nil
}

func (f *fakePlans) ListTenantAppeals(_ context.Context, req *plansv1.ListTenantAppealsRequest) (*plansv1.ListTenantAppealsResponse, error) {
	f.lastAppeals = req
	return &plansv1.ListTenantAppealsResponse{Appeals: []*plansv1.TenantAppeal{{Id: "a1", Status: "open"}}, AppNames: map[string]string{"t1": "Shop"}}, nil
}

func (f *fakePlans) DecideTenantAppeal(_ context.Context, req *plansv1.DecideTenantAppealRequest) (*plansv1.DecideTenantAppealResponse, error) {
	f.lastDecision = req
	return &plansv1.DecideTenantAppealResponse{Appeal: &plansv1.TenantAppeal{Id: req.GetAppealId(), Status: req.GetDecision()}}, nil
}

// The operator's calls: the schedule carries the time as RFC 3339 and the signed-in operator, the appeal list its filters, and the decision its answer; an API key reaches none.
func TestOperatorLifecycleAndAppealRoutes(t *testing.T) {
	ts, f := newTestServer(t)
	const id = "0192f0b4-6f6e-7c3a-9a3e-6a1f2b3c4d5e"
	if r, body := do(t, ts, http.MethodGet, "/admin/v1/tenants/"+id+"/lifecycle", operatorToken, ""); r.StatusCode != http.StatusOK || body["scheduled_status"] != "suspended" {
		t.Errorf("lifecycle: %d %v", r.StatusCode, body)
	}
	at := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	r, body := do(t, ts, http.MethodPut, "/admin/v1/tenants/"+id+"/scheduled-status", operatorToken, `{"status":"frozen","schedule_time":"`+at+`","category":"nonpayment","reason":"unpaid"}`)
	if r.StatusCode != http.StatusOK || f.lastSchedule.GetStatus() != "frozen" || f.lastSchedule.GetCategory() != "nonpayment" || f.lastSchedule.GetActor() != "operator" || f.lastSchedule.GetScheduleTime().AsTime().Format(time.RFC3339) != at {
		t.Errorf("schedule: %d %v %v", r.StatusCode, body, f.lastSchedule)
	}
	if r, body := do(t, ts, http.MethodPut, "/admin/v1/tenants/"+id+"/scheduled-status", operatorToken, `{"status":"frozen","schedule_time":"tomorrow"}`); r.StatusCode != http.StatusBadRequest {
		t.Errorf("a time that is not RFC 3339: %d %v", r.StatusCode, body)
	}
	if r, _ := do(t, ts, http.MethodDelete, "/admin/v1/tenants/"+id+"/scheduled-status", operatorToken, ""); r.StatusCode != http.StatusOK || f.lastCancel.GetTenantId() != id {
		t.Errorf("cancel: %d %v", r.StatusCode, f.lastCancel)
	}
	if r, body := do(t, ts, http.MethodGet, "/admin/v1/appeals?tenant_id="+id+"&status=open", operatorToken, ""); r.StatusCode != http.StatusOK || f.lastAppeals.GetTenantId() != id || f.lastAppeals.GetStatus() != "open" || body["appeals"] == nil {
		t.Errorf("appeals: %d %v %v", r.StatusCode, body, f.lastAppeals)
	}
	if r, body := do(t, ts, http.MethodPost, "/admin/v1/appeals/a1/decision", operatorToken, `{"decision":"rejected","answer":"Not yet."}`); r.StatusCode != http.StatusOK || f.lastDecision.GetAppealId() != "a1" || f.lastDecision.GetAnswer() != "Not yet." || f.lastDecision.GetActor() != "operator" || body["status"] != "rejected" {
		t.Errorf("decision: %d %v %v", r.StatusCode, body, f.lastDecision)
	}
	for _, c := range [][2]string{{http.MethodGet, "/admin/v1/appeals"}, {http.MethodPost, "/admin/v1/appeals/a1/decision"}, {http.MethodGet, "/admin/v1/tenants/" + id + "/lifecycle"}} {
		if r, _ := do(t, ts, c[0], c[1], "keyed-key", `{}`); r.StatusCode == http.StatusOK {
			t.Errorf("an API key reached %s %s", c[0], c[1])
		}
	}
}
