//go:build integration

package itest

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/safehttp"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/identity/usermail"
	webhooksmodel "github.com/0furkancolak/payrudder/services/plans-svc/internal/webhooks/model"
	webhooksworker "github.com/0furkancolak/payrudder/services/plans-svc/internal/webhooks/worker"
)

const (
	sActive    = plansv1.TenantStatus_TENANT_STATUS_ACTIVE
	sSuspended = plansv1.TenantStatus_TENANT_STATUS_SUSPENDED
	sFrozen    = plansv1.TenantStatus_TENANT_STATUS_FROZEN
	sBanned    = plansv1.TenantStatus_TENANT_STATUS_BANNED
)

// An operator can freeze, suspend and ban a live tenant and reinstate it; every change is kept with its category, its reason and who made it; a tenant on hold starts
// nothing and sends no webhook until it is reinstated.
func testTenantStatus(t *testing.T) {
	e := newEnv(t)
	live := e.account(t, "Status")[plansv1.Environment_ENVIRONMENT_LIVE]
	ctx := as(live.GetId())
	if _, err := e.plans.CreatePlan(ctx, &plansv1.CreatePlanRequest{Code: "basic", Name: "basic"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.plans.PublishPlanVersion(ctx, &plansv1.PublishPlanVersionRequest{PlanCode: "basic", BaseFeeMinor: 3000}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.plans.CreateCustomer(ctx, &plansv1.CreateCustomerRequest{ExternalId: "cus_s", Name: "s", Email: "s@example.com"}); err != nil {
		t.Fatal(err)
	}
	key, err := e.admin.CreateApiKey(operator(), &plansv1.CreateApiKeyRequest{TenantId: live.GetId()})
	if err != nil {
		t.Fatal(err)
	}
	set := func(to plansv1.TenantStatus, category, reason, actor string) error {
		_, err := e.admin.SetTenantStatus(operator(), &plansv1.SetTenantStatusRequest{TenantId: live.GetId(), Status: to, Category: category, Reason: reason, Actor: actor})
		return err
	}
	resolved := func() plansv1.TenantStatus {
		t.Helper()
		r, err := e.admin.ResolveApiKey(ctx, &plansv1.ResolveApiKeyRequest{Prefix: key.GetApiKey().GetPrefix()})
		if err != nil {
			t.Fatal(err)
		}
		return r.GetTenantStatus()
	}
	history := func() []*plansv1.TenantStatusEvent {
		t.Helper()
		r, err := e.admin.ListTenantStatusEvents(operator(), &plansv1.ListTenantStatusEventsRequest{TenantId: live.GetId()})
		if err != nil {
			t.Fatal(err)
		}
		return r.GetEvents()
	}

	// The activation the test account went through is the first line of the history.
	base := len(history())
	if base != 1 {
		t.Fatalf("the history of a new live tenant has %d lines, want its activation", base)
	}

	t.Run("freezing is recorded, and the gateway learns it", func(t *testing.T) {
		e.evicted.mu.Lock()
		e.evicted.prefixes = nil
		e.evicted.mu.Unlock()
		if err := set(sFrozen, "nonpayment", "unpaid platform invoice", "boss@example.com"); err != nil {
			t.Fatal(err)
		}
		if resolved() != sFrozen {
			t.Fatalf("the gateway resolves %v", resolved())
		}
		e.evicted.mu.Lock()
		evicted := append([]string(nil), e.evicted.prefixes...)
		e.evicted.mu.Unlock()
		if len(evicted) != 1 || evicted[0] != key.GetApiKey().GetPrefix() {
			t.Errorf("evicted = %v: the gateway must resolve the key again", evicted)
		}
		h := history()
		if len(h) != base+1 || h[0].GetKind() != "changed" || h[0].GetFromStatus() != sActive || h[0].GetToStatus() != sFrozen || h[0].GetCategory() != "nonpayment" ||
			h[0].GetReason() != "unpaid platform invoice" || h[0].GetActor() != "boss@example.com" || h[0].GetCreateTime() == nil {
			t.Fatalf("history = %v", h)
		}
		if err := set(sFrozen, "", "", "boss@example.com"); err != nil || len(history()) != base+1 {
			t.Fatalf("setting the status it has: %v, history %d", err, len(history()))
		}
	})

	t.Run("a tenant on hold starts no subscription", func(t *testing.T) {
		_, err := e.plans.CreateSubscription(ctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: "cus_s", PlanCode: "basic", PlanVersion: 1})
		wantCode(t, "a subscription of a frozen tenant", err, codes.FailedPrecondition, "plans.tenant.on_hold")
	})

	t.Run("its webhook deliveries wait, and go out when it is reinstated", func(t *testing.T) {
		if err := set(sActive, "", "", ""); err != nil {
			t.Fatal(err)
		}
		e.clearDeliveries(t)
		rcv := newReceiver(t)
		if _, err := e.hooks.CreateWebhookEndpoint(ctx, &plansv1.CreateWebhookEndpointRequest{Url: rcv.URL}); err != nil {
			t.Fatal(err)
		}
		now := time.Now().Add(time.Minute)
		clock := &now
		disp := &webhooksworker.Dispatcher{Pool: e.system, Places: e.places, Box: e.box, Client: safehttp.NewClient(2*time.Second, true), Now: func() time.Time { return *clock }}
		ev := uuid.NewString()
		if _, err := e.hooks.RecordTenantEvent(ctx, &plansv1.RecordTenantEventRequest{EventId: ev, Type: webhooksmodel.InvoicePaid, DataJson: `{"invoice_id":"inv_1"}`, OccurredTime: timestamppb.New(*clock)}); err != nil {
			t.Fatal(err)
		}
		delivery := func() *plansv1.WebhookDelivery {
			t.Helper()
			r, err := e.hooks.ListWebhookDeliveries(ctx, &plansv1.ListWebhookDeliveriesRequest{})
			if err != nil || len(r.GetDeliveries()) != 1 {
				t.Fatalf("deliveries = %v, %v", r, err)
			}
			return r.GetDeliveries()[0]
		}
		if err := set(sSuspended, "abuse", "spam", "boss@example.com"); err != nil {
			t.Fatal(err)
		}
		for range 3 { // more passes than attempts a delivery may have used up
			if n, err := disp.Once(ctx); err != nil || n != 1 {
				t.Fatalf("a pass claimed %d, %v", n, err)
			}
			*clock = clock.Add(11 * time.Minute)
		}
		if d := delivery(); len(rcv.calls()) != 0 || d.GetState() != plansv1.WebhookDeliveryState_WEBHOOK_DELIVERY_STATE_PENDING || d.GetAttempts() != 0 {
			t.Fatalf("a suspended tenant sent %d calls; delivery %v", len(rcv.calls()), d)
		}
		if err := set(sActive, "", "", ""); err != nil {
			t.Fatal(err)
		}
		if n, err := disp.Once(ctx); err != nil || n != 1 || len(rcv.calls()) != 1 {
			t.Fatalf("after reinstating: claimed %d, %v, calls %d", n, err, len(rcv.calls()))
		}
		if d := delivery(); d.GetState() != plansv1.WebhookDeliveryState_WEBHOOK_DELIVERY_STATE_DELIVERED || d.GetAttempts() != 1 {
			t.Fatalf("delivery = %v", d)
		}
	})

	t.Run("a ban and lifting one need a reason", func(t *testing.T) {
		wantCode(t, "a ban without a reason", set(sBanned, "fraud", "  ", ""), codes.InvalidArgument, "plans.tenant.reason_required")
		if resolved() != sActive {
			t.Fatal("a refused ban changed the status")
		}
		if err := set(sBanned, "fraud", "card testing", "boss@example.com"); err != nil {
			t.Fatal(err)
		}
		wantCode(t, "lifting a ban without a reason", set(sActive, "", "", ""), codes.InvalidArgument, "plans.tenant.reason_required")
		if err := set(sSuspended, "", "downgraded to a suspension after review", ""); err != nil {
			t.Fatalf("a ban to a suspension: %v", err)
		}
		if err := set(sActive, "", "", ""); err != nil {
			t.Fatalf("reinstating a suspension needs no reason: %v", err)
		}
		if h := history(); h[0].GetToStatus() != sActive || h[0].GetActor() != "operator" {
			t.Fatalf("newest first, and the machine token is \"operator\": %v", h[0])
		}
	})

	t.Run("the fields are checked", func(t *testing.T) {
		wantCode(t, "an unknown category", set(sFrozen, "boredom", "x", ""), codes.InvalidArgument, "plans.request.invalid")
		wantCode(t, "a reason of 501 characters", set(sFrozen, "", strings.Repeat("x", 501), ""), codes.InvalidArgument, "plans.request.invalid")
		wantCode(t, "inactive is not a state to move to", set(plansv1.TenantStatus_TENANT_STATUS_INACTIVE, "", "x", ""), codes.InvalidArgument, "plans.tenant.bad_status")
		if resolved() != sActive {
			t.Fatal("a refused change changed the status")
		}
		sbx := e.inactiveAccount(t, "Status sandbox")[plansv1.Environment_ENVIRONMENT_SANDBOX]
		_, err := e.admin.SetTenantStatus(operator(), &plansv1.SetTenantStatusRequest{TenantId: sbx.GetId(), Status: sFrozen, Reason: "x"})
		wantCode(t, "freezing a sandbox", err, codes.FailedPrecondition, "plans.tenant.bad_transition")
	})
}

// Where a ban needs a second operator, the first request is only recorded; the same request from another operator applies it, and the history names both.
func testTenantStatusApproval(t *testing.T) {
	banApproval = true
	t.Cleanup(func() { banApproval = false })
	e := newEnv(t)
	live := e.account(t, "Approval")[plansv1.Environment_ENVIRONMENT_LIVE]
	ban := func(actor string) error {
		_, err := e.admin.SetTenantStatus(operator(), &plansv1.SetTenantStatusRequest{TenantId: live.GetId(), Status: sBanned, Category: "fraud", Reason: "card testing", Actor: actor})
		return err
	}
	status := func() plansv1.TenantStatus {
		t.Helper()
		r, err := e.admin.GetTenant(operator(), &plansv1.GetTenantRequest{TenantId: live.GetId()})
		if err != nil {
			t.Fatal(err)
		}
		return r.GetTenant().GetStatus()
	}
	history := func() []*plansv1.TenantStatusEvent {
		t.Helper()
		r, err := e.admin.ListTenantStatusEvents(operator(), &plansv1.ListTenantStatusEventsRequest{TenantId: live.GetId()})
		if err != nil {
			t.Fatal(err)
		}
		return r.GetEvents()
	}
	wantCode(t, "the first request", ban("alice@example.com"), codes.FailedPrecondition, "plans.tenant.approval_pending")
	if status() != sActive {
		t.Fatal("the first request banned the tenant")
	}
	wantCode(t, "the same operator again", ban("alice@example.com"), codes.FailedPrecondition, "plans.tenant.approval_pending")
	if status() != sActive {
		t.Fatal("one operator banned the tenant alone")
	}
	// Suspending is not a ban: it needs no second operator.
	if _, err := e.admin.SetTenantStatus(operator(), &plansv1.SetTenantStatusRequest{TenantId: live.GetId(), Status: sSuspended, Actor: "alice@example.com"}); err != nil {
		t.Fatalf("a suspension with the approval on: %v", err)
	}
	if _, err := e.admin.SetTenantStatus(operator(), &plansv1.SetTenantStatusRequest{TenantId: live.GetId(), Status: sActive, Actor: "alice@example.com"}); err != nil {
		t.Fatal(err)
	}
	wantCode(t, "a request after another change", ban("alice@example.com"), codes.FailedPrecondition, "plans.tenant.approval_pending")
	if err := ban("bob@example.com"); err != nil {
		t.Fatalf("the second operator: %v", err)
	}
	if status() != sBanned {
		t.Fatal("the second operator's request did not ban the tenant")
	}
	h := history()
	if h[0].GetKind() != "changed" || h[0].GetToStatus() != sBanned || h[0].GetActor() != "bob@example.com" || h[0].GetApprovedBy() != "alice@example.com" {
		t.Fatalf("the ban = %v", h[0])
	}
	requests := 0
	for _, x := range h {
		if x.GetKind() == "requested" {
			requests++
		}
	}
	if requests != 3 {
		t.Errorf("%d requests in the history, want 3 (the first, the repeat of the same operator and the one after the other changes): %v", requests, h)
	}
}

// The owners of an account hear of it when the operator suspends, freezes, bans or reinstates their live app: every owner with a proven address, in their own
// language, naming the app; nobody else (an owner whose address is not proven, an administrator), nothing when the status does not change or when an inactive tenant
// is activated, and nothing of the operator's category or reason.
func testTenantStatusMails(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	ada := e.verified(t, "status-ada@example.com", "Status Co")
	if _, err := e.system.Exec(ctx, `UPDATE users SET locale = 'tr' WHERE id = $1::uuid`, ada.id); err != nil {
		t.Fatal(err)
	}
	bob := e.verified(t, "status-bob@example.com", "Bob Co")
	cy := e.signUp(t, "status-cy@example.com", "Cy Co") // never verifies the address
	for _, m := range []struct{ user, role string }{{bob.id, "owner"}, {cy.GetUser().GetId(), "owner"}} {
		if _, err := e.system.Exec(ctx, `INSERT INTO account_members (account_id, user_id, role) VALUES ($1::uuid, $2::uuid, $3)`, ada.account, m.user, m.role); err != nil {
			t.Fatal(err)
		}
	}
	dee := e.verified(t, "status-dee@example.com", "Dee Co")
	if _, err := e.system.Exec(ctx, `INSERT INTO account_members (account_id, user_id, role) VALUES ($1::uuid, $2::uuid, 'admin')`, ada.account, dee.id); err != nil {
		t.Fatal(err)
	}
	created, err := e.ident.CreateMyApp(ctx, &plansv1.CreateMyAppRequest{UserId: ada.id, AccountId: ada.account, Name: "Shop", Currency: "TRY", Timezone: "Europe/Istanbul"})
	if err != nil {
		t.Fatal(err)
	}
	tenantID, app := envOf(created.GetApp(), plansv1.Environment_ENVIRONMENT_LIVE).GetId(), created.GetApp().GetName()
	mails := func(kind usermail.Kind, to string) int { return len(e.mail.to(kind, to)) }
	total := func() int {
		n := 0
		for _, k := range []usermail.Kind{usermail.KindTenantSuspended, usermail.KindTenantFrozen, usermail.KindTenantBanned, usermail.KindTenantReinstated} {
			for _, to := range []string{ada.email, bob.email, "status-cy@example.com", dee.email} {
				n += mails(k, to)
			}
		}
		return n
	}
	set := func(to plansv1.TenantStatus) {
		t.Helper()
		if _, err := e.admin.SetTenantStatus(operator(), &plansv1.SetTenantStatusRequest{TenantId: tenantID, Status: to, Category: "fraud", Reason: "stolen cards, see case 12", Actor: "boss@example.com"}); err != nil {
			t.Fatal(err)
		}
	}

	e.activate(t, tenantID)
	if total() != 0 {
		t.Fatal("activating an inactive tenant is not a reinstatement: nobody is mailed")
	}
	for _, step := range []struct {
		to   plansv1.TenantStatus
		kind usermail.Kind
	}{{sSuspended, usermail.KindTenantSuspended}, {sFrozen, usermail.KindTenantFrozen}, {sBanned, usermail.KindTenantBanned}, {sActive, usermail.KindTenantReinstated}} {
		before := total()
		set(step.to)
		if got := e.mail.to(step.kind, ada.email); len(got) != 1 || got[0].Locale != "tr" || got[0].App != app || got[0].Account != "Status Co" || got[0].Link != testAppURL+"/app/support" {
			t.Fatalf("%s: mail to the owner = %+v (want one, in Turkish, naming %q)", step.kind, got, app)
		}
		if mails(step.kind, bob.email) != 1 || mails(step.kind, "status-cy@example.com") != 0 || mails(step.kind, dee.email) != 0 {
			t.Errorf("%s: a second owner gets it, an owner whose address is not proven and an administrator do not", step.kind)
		}
		if total() != before+2 {
			t.Errorf("%s: %d mails sent, want 2 (one per owner with a proven address)", step.kind, total()-before)
		}
		if m := e.mail.to(step.kind, ada.email)[0]; m.Subject != "" || m.Status != "" || m.Ticket != "" {
			t.Errorf("%s: the mail carries more than the app and the account: %+v", step.kind, m)
		}
	}
	// A call that leaves the status as it is mails nobody.
	before := total()
	set(sActive)
	if total() != before {
		t.Error("reinstating an app that is in service sent a mail")
	}
}
