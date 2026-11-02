//go:build integration

package itest

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"

	auditv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/audit/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	apikeyscmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/apikeys/command"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/identity/usermail"
	tenantscmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/tenants/command"
)

// What the operator does to a live tenant over time (plan 14): a ban starts a retention period that a job marks due and never acts on, a suspension or a freeze scheduled for a
// date is applied by a sweeper to an active tenant only, and an owner's appeal is decided by the operator, who mails the owners.
func testTenantLifecycle(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	ada := e.verified(t, "life-ada@example.com", "Life Co")
	created, err := e.ident.CreateMyApp(ctx, &plansv1.CreateMyAppRequest{UserId: ada.id, AccountId: ada.account, Name: "Shop", Currency: "TRY", Timezone: "Europe/Istanbul"})
	if err != nil {
		t.Fatal(err)
	}
	app := created.GetApp()
	tenantID, sandboxID := envOf(app, plansv1.Environment_ENVIRONMENT_LIVE).GetId(), envOf(app, plansv1.Environment_ENVIRONMENT_SANDBOX).GetId()
	e.activate(t, tenantID)
	tctx := as(tenantID)
	if _, err := e.plans.CreateCustomer(tctx, &plansv1.CreateCustomerRequest{ExternalId: "cus_keep", Name: "Keep", Email: "keep@example.com"}); err != nil {
		t.Fatal(err)
	}
	set := func(to plansv1.TenantStatus, actor string) {
		t.Helper()
		if _, err := e.admin.SetTenantStatus(operator(), &plansv1.SetTenantStatusRequest{TenantId: tenantID, Status: to, Category: "fraud", Reason: "case 12", Actor: actor}); err != nil {
			t.Fatal(err)
		}
	}
	status := func() plansv1.TenantStatus {
		t.Helper()
		r, err := e.admin.GetTenant(operator(), &plansv1.GetTenantRequest{TenantId: tenantID})
		if err != nil {
			t.Fatal(err)
		}
		return r.GetTenant().GetStatus()
	}
	lifecycle := func() *plansv1.TenantLifecycle {
		t.Helper()
		r, err := e.admin.GetTenantLifecycle(operator(), &plansv1.GetTenantLifecycleRequest{TenantId: tenantID})
		if err != nil {
			t.Fatal(err)
		}
		return r.GetLifecycle()
	}
	// The jobs the service runs, on a clock the test moves.
	clock := time.Now()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	jobs := tenantscmd.New(e.system, apikeyscmd.New(e.system, e.evicted, log), log).WithRetention(testBanRetentionDays).WithMail(e.mail, testAppURL).WithClock(func() time.Time { return clock })
	logged := func(action string) []*auditv1.AuditEvent {
		return e.events(t, tctx, &auditv1.ListAuditEventsRequest{Actions: []string{action}, PageSize: 20})
	}

	t.Run("a ban starts a retention period that ends with the ban; the job marks it due and deletes nothing", func(t *testing.T) {
		if l := lifecycle(); l.GetRetentionUntil() != nil {
			t.Fatalf("an active tenant has a deadline: %v", l)
		}
		set(sBanned, "boss@example.com")
		until := lifecycle().GetRetentionUntil().AsTime()
		if d := time.Until(until); d < 89*24*time.Hour || d > 91*24*time.Hour {
			t.Fatalf("the deadline is %v from now, want about %d days", d, testBanRetentionDays)
		}
		// A move from the ban to a suspension ends the period; a new ban starts one.
		set(sSuspended, "boss@example.com")
		if l := lifecycle(); l.GetRetentionUntil() != nil {
			t.Fatalf("leaving the ban kept the deadline: %v", l)
		}
		set(sBanned, "boss@example.com")
		if lifecycle().GetRetentionUntil() == nil {
			t.Fatal("a new ban has no deadline")
		}
		// Before the deadline the job marks nothing; after it, once.
		if n, err := jobs.MarkRetentionDue(ctx); err != nil || n != 0 {
			t.Fatalf("before the deadline: marked %d, %v", n, err)
		}
		clock = time.Now().Add(100 * 24 * time.Hour)
		if n, err := jobs.MarkRetentionDue(ctx); err != nil || n < 1 {
			t.Fatalf("after the deadline: marked %d, %v", n, err)
		}
		if lifecycle().GetRetentionDueTime() == nil {
			t.Error("the deadline is not marked due")
		}
		if n, err := jobs.MarkRetentionDue(ctx); err != nil || n != 0 {
			t.Errorf("a second pass marks %d again, %v", n, err)
		}
		if got := logged("tenant.retention_due"); len(got) != 1 {
			t.Errorf("audit entries of the mark: %d, want 1", len(got))
		}
		// Nothing was deleted: the tenant and its customer are as they were.
		if status() != sBanned {
			t.Error("the job changed the status")
		}
		if _, err := e.plans.GetCustomer(tctx, &plansv1.GetCustomerRequest{ExternalId: "cus_keep"}); err != nil {
			t.Errorf("the job deleted a customer: %v", err)
		}
		set(sActive, "boss@example.com")
		if l := lifecycle(); l.GetRetentionUntil() != nil || l.GetRetentionDueTime() != nil {
			t.Errorf("reinstating kept the deadline: %v", l)
		}
		clock = time.Now()
	})

	t.Run("a scheduled suspension is checked, applied to an active tenant by the sweeper as the system, and spent", func(t *testing.T) {
		at := func(d time.Duration) *timestamppb.Timestamp { return timestamppb.New(time.Now().Add(d)) }
		sched := func(id, status string, when *timestamppb.Timestamp) error {
			_, err := e.admin.ScheduleTenantStatus(operator(), &plansv1.ScheduleTenantStatusRequest{TenantId: id, Status: status, ScheduleTime: when, Category: "nonpayment", Reason: "platform invoice unpaid", Actor: "boss@example.com"})
			return err
		}
		wantCode(t, "a time in the past", sched(tenantID, "suspended", at(-time.Minute)), codes.InvalidArgument, "plans.request.invalid")
		wantCode(t, "a time beyond a year", sched(tenantID, "suspended", at(400*24*time.Hour)), codes.InvalidArgument, "plans.request.invalid")
		wantCode(t, "a ban", sched(tenantID, "banned", at(time.Hour)), codes.InvalidArgument, "plans.tenant.bad_status")
		wantCode(t, "the sandbox", sched(sandboxID, "suspended", at(time.Hour)), codes.FailedPrecondition, "plans.tenant.bad_transition")
		if err := sched(tenantID, "frozen", at(time.Hour)); err != nil {
			t.Fatal(err)
		}
		if err := sched(tenantID, "suspended", at(2*time.Hour)); err != nil { // one schedule per tenant: this replaces that
			t.Fatal(err)
		}
		if l := lifecycle(); l.GetScheduledStatus() != "suspended" || l.GetScheduledBy() != "boss@example.com" || l.GetScheduledReason() != "platform invoice unpaid" {
			t.Fatalf("schedule = %v", l)
		}
		// Not due yet: nothing happens.
		if n, err := jobs.ApplyDueSchedules(ctx); err != nil || n != 0 || status() != sActive {
			t.Fatalf("before the time: applied %d, %v, status %v", n, err, status())
		}
		before := len(e.mail.to(usermail.KindTenantSuspended, ada.email))
		clock = time.Now().Add(3 * time.Hour)
		if n, err := jobs.ApplyDueSchedules(ctx); err != nil || n < 1 {
			t.Fatalf("at the time: applied %d, %v", n, err)
		}
		if status() != sSuspended {
			t.Fatalf("status = %v, want suspended", status())
		}
		if l := lifecycle(); l.GetScheduledStatus() != "" || l.GetScheduledTime() != nil {
			t.Errorf("the schedule is not spent: %v", l)
		}
		ev, err := e.admin.ListTenantStatusEvents(operator(), &plansv1.ListTenantStatusEventsRequest{TenantId: tenantID})
		if err != nil || ev.GetEvents()[0].GetActor() != "system" || ev.GetEvents()[0].GetToStatus() != sSuspended || ev.GetEvents()[0].GetCategory() != "nonpayment" {
			t.Fatalf("history = %v, %v", ev.GetEvents(), err)
		}
		if got := len(e.mail.to(usermail.KindTenantSuspended, ada.email)); got != before+1 {
			t.Errorf("the owners got %d suspension mails, want one more than %d", got, before)
		}
		if len(logged("tenant.status_scheduled")) != 2 || len(logged("tenant.status_schedule_applied")) != 1 {
			t.Errorf("audit: scheduled %d, applied %d", len(logged("tenant.status_scheduled")), len(logged("tenant.status_schedule_applied")))
		}

		// A schedule that comes due for a tenant that is not active any more is spent and changes nothing; a schedule can be cancelled.
		set(sActive, "boss@example.com")
		clock = time.Now()
		if err := sched(tenantID, "frozen", at(time.Hour)); err != nil {
			t.Fatal(err)
		}
		set(sBanned, "boss@example.com")
		clock = time.Now().Add(2 * time.Hour)
		if _, err := jobs.ApplyDueSchedules(ctx); err != nil || status() != sBanned {
			t.Fatalf("a scheduled freeze met a banned tenant: %v, status %v", err, status())
		}
		if len(logged("tenant.status_schedule_skipped")) != 1 || lifecycle().GetScheduledStatus() != "" {
			t.Error("the skipped schedule is not spent and recorded")
		}
		set(sActive, "boss@example.com")
		clock = time.Now()
		if err := sched(tenantID, "frozen", at(time.Hour)); err != nil {
			t.Fatal(err)
		}
		if r, err := e.admin.CancelScheduledTenantStatus(operator(), &plansv1.CancelScheduledTenantStatusRequest{TenantId: tenantID, Actor: "boss@example.com"}); err != nil || r.GetLifecycle().GetScheduledStatus() != "" {
			t.Fatalf("cancel: %v %v", r, err)
		}
		if _, err := e.admin.CancelScheduledTenantStatus(operator(), &plansv1.CancelScheduledTenantStatusRequest{TenantId: tenantID}); err != nil {
			t.Errorf("cancelling nothing: %v", err)
		}
	})

	t.Run("an owner appeals, the operator decides, and the owners are told", func(t *testing.T) {
		bob := e.verified(t, "life-bob@example.com", "Bob Co")
		if _, err := e.system.Exec(ctx, `INSERT INTO account_members (account_id, user_id, role) VALUES ($1::uuid, $2::uuid, 'admin')`, ada.account, bob.id); err != nil {
			t.Fatal(err)
		}
		appeal := func(user, statement string) (*plansv1.SubmitMyAppAppealResponse, error) {
			return e.ident.SubmitMyAppAppeal(ctx, &plansv1.SubmitMyAppAppealRequest{UserId: user, AppId: app.GetId(), Statement: statement})
		}
		hold := func(user string) *plansv1.GetMyAppHoldResponse {
			t.Helper()
			r, err := e.ident.GetMyAppHold(ctx, &plansv1.GetMyAppHoldRequest{UserId: user, AppId: app.GetId()})
			if err != nil {
				t.Fatal(err)
			}
			return r
		}
		if status() != sActive {
			set(sActive, "boss@example.com")
		}
		_, err := appeal(ada.id, "Please look again.")
		wantCode(t, "an appeal of an app that is not held", err, codes.FailedPrecondition, "plans.appeal.not_held")
		set(sBanned, "boss@example.com")
		_, err = appeal(bob.id, "I am an administrator.")
		wantCode(t, "an appeal of an administrator", err, codes.PermissionDenied, "")
		_, err = appeal(ada.id, "   ")
		wantCode(t, "an empty statement", err, codes.InvalidArgument, "plans.request.invalid")
		h := hold(ada.id)
		if !h.GetCanAppeal() || h.GetStatus() != sBanned || h.GetRetentionUntil() == nil || h.GetAppeal() != nil {
			t.Fatalf("hold before the appeal = %v", h)
		}
		if hb := hold(bob.id); hb.GetCanAppeal() || hb.GetStatus() != sBanned || hb.GetRetentionUntil() == nil {
			t.Errorf("an administrator reads the hold and cannot appeal: %v", hb)
		}
		first, err := appeal(ada.id, "The cards were a customer's test. We have stopped it.")
		if err != nil || first.GetAppeal().GetStatus() != "open" || first.GetAppeal().GetHeldStatus() != "banned" || first.GetAppeal().GetSubmittedBy() != ada.email {
			t.Fatalf("appeal = %v, %v", first, err)
		}
		_, err = appeal(ada.id, "Again.")
		wantCode(t, "a second appeal while one is open", err, codes.FailedPrecondition, "plans.appeal.exists")
		if h := hold(ada.id); h.GetCanAppeal() || h.GetAppeal().GetId() != first.GetAppeal().GetId() {
			t.Errorf("hold with an open appeal = %v", h)
		}
		open, err := e.admin.ListTenantAppeals(operator(), &plansv1.ListTenantAppealsRequest{Status: "open"})
		if err != nil || len(open.GetAppeals()) < 1 || open.GetAppNames()[tenantID] != "Shop" {
			t.Fatalf("open appeals = %v, %v", open, err)
		}
		decide := func(id, decision, answer string) (*plansv1.DecideTenantAppealResponse, error) {
			return e.admin.DecideTenantAppeal(operator(), &plansv1.DecideTenantAppealRequest{AppealId: id, Decision: decision, Answer: answer, Actor: "boss@example.com"})
		}
		_, err = decide(first.GetAppeal().GetId(), "rejected", "")
		wantCode(t, "rejecting without an answer", err, codes.InvalidArgument, "plans.request.invalid")
		_, err = decide(first.GetAppeal().GetId(), "maybe", "x")
		wantCode(t, "another word for a decision", err, codes.InvalidArgument, "plans.request.invalid")
		rejected, err := decide(first.GetAppeal().GetId(), "rejected", "The hold stays: the dispute is not closed.")
		if err != nil || rejected.GetAppeal().GetStatus() != "rejected" || rejected.GetAppeal().GetDecidedBy() != "boss@example.com" || status() != sBanned {
			t.Fatalf("rejected = %v, %v, status %v", rejected, err, status())
		}
		if got := e.mail.to(usermail.KindTenantAppealRejected, ada.email); len(got) != 1 || got[0].Answer != "The hold stays: the dispute is not closed." || got[0].App != "Shop" {
			t.Errorf("the rejection mail = %+v", got)
		}
		_, err = decide(first.GetAppeal().GetId(), "accepted", "")
		wantCode(t, "deciding twice", err, codes.FailedPrecondition, "plans.appeal.not_open")

		// A new appeal after a rejection is allowed; accepting it reinstates the tenant and the owners get the mail of the appeal, not that of the reinstatement.
		second, err := appeal(ada.id, "The dispute is closed now.")
		if err != nil {
			t.Fatal(err)
		}
		reinstated := len(e.mail.to(usermail.KindTenantReinstated, ada.email))
		accepted, err := decide(second.GetAppeal().GetId(), "accepted", "Welcome back.")
		if err != nil || accepted.GetAppeal().GetStatus() != "accepted" || status() != sActive {
			t.Fatalf("accepted = %v, %v, status %v", accepted, err, status())
		}
		if got := e.mail.to(usermail.KindTenantAppealAccepted, ada.email); len(got) != 1 || got[0].Answer != "Welcome back." {
			t.Errorf("the acceptance mail = %+v", got)
		}
		if got := len(e.mail.to(usermail.KindTenantReinstated, ada.email)); got != reinstated {
			t.Errorf("an accepted appeal also sent the mail of the reinstatement (%d, was %d)", got, reinstated)
		}
		if h := hold(ada.id); h.GetCanAppeal() || h.GetAppeal().GetStatus() != "accepted" || h.GetRetentionUntil() != nil {
			t.Errorf("hold after the acceptance = %v", h)
		}
		if len(logged("tenant_appeal.submitted")) != 2 || len(logged("tenant_appeal.decided")) != 2 {
			t.Errorf("audit: submitted %d, decided %d", len(logged("tenant_appeal.submitted")), len(logged("tenant_appeal.decided")))
		}
	})
}
