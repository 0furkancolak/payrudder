//go:build integration

package itest

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"

	auditv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/audit/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/audit"
)

// actedAs is ctx with the actor the gateway would have put in it.
func actedAs(ctx context.Context, a audit.Actor) context.Context { return audit.NewContext(ctx, a) }

// events reads the log through AuditService the way the gateway does: ctx names the caller (a tenant, or the operator).
func (e *env) events(t *testing.T, ctx context.Context, req *auditv1.ListAuditEventsRequest) []*auditv1.AuditEvent {
	t.Helper()
	resp, err := e.audit.ListAuditEvents(ctx, req)
	if err != nil {
		t.Fatalf("ListAuditEvents: %v", err)
	}
	return resp.GetEvents()
}

func actions(evs []*auditv1.AuditEvent) []string {
	out := make([]string, len(evs))
	for i, ev := range evs {
		out[i] = ev.GetAction()
	}
	return out
}

func has(evs []*auditv1.AuditEvent, action string) *auditv1.AuditEvent {
	for _, ev := range evs {
		if ev.GetAction() == action {
			return ev
		}
	}
	return nil
}

// testAuditLog: every action the plan names leaves an entry in the transaction of the change, with the actor of the request, and no entry holds a secret; a tenant reads
// its own entries (and its account's members) and nobody else's; the table refuses any change to a row.
func testAuditLog(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	mine := e.account(t, "Audited")
	live := mine[plansv1.Environment_ENVIRONMENT_LIVE]
	other := e.account(t, "Somebody else")[plansv1.Environment_ENVIRONMENT_LIVE]
	owner := audit.Actor{Type: audit.ActorUser, ID: uuid.NewString(), Email: "owner@audited.example", IP: "203.0.113.5", RequestID: "req-audit-1"}
	tctx := actedAs(as(live.GetId()), owner)

	// API keys: created and revoked by the operator on the tenant's behalf.
	op := actedAs(operator(), audit.Actor{Type: audit.ActorOperator, ID: "ops@payrudder.example", Email: "ops@payrudder.example", IP: "198.51.100.7", RequestID: "req-audit-2"})
	key, err := e.admin.CreateApiKey(op, &plansv1.CreateApiKeyRequest{TenantId: live.GetId()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.admin.RevokeApiKey(op, &plansv1.RevokeApiKeyRequest{ApiKeyId: key.GetApiKey().GetId()}); err != nil {
		t.Fatal(err)
	}

	// Webhook endpoints, a plan and a customer block, as the tenant's own person.
	ep, err := e.hooks.CreateWebhookEndpoint(tctx, &plansv1.CreateWebhookEndpointRequest{Url: "https://hooks.example.com/payrudder?token=abc123secret", Description: "main"})
	if err != nil {
		t.Fatal(err)
	}
	rolled, err := e.hooks.RotateWebhookSecret(tctx, &plansv1.RotateWebhookSecretRequest{Id: ep.GetEndpoint().GetId()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.hooks.UpdateWebhookEndpoint(tctx, &plansv1.UpdateWebhookEndpointRequest{Id: ep.GetEndpoint().GetId(), Url: "https://hooks.example.com/v2", Active: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.hooks.DeleteWebhookEndpoint(tctx, &plansv1.DeleteWebhookEndpointRequest{Id: ep.GetEndpoint().GetId()}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.plans.CreatePlan(tctx, &plansv1.CreatePlanRequest{Code: "basic", Name: "basic"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.plans.PublishPlanVersion(tctx, &plansv1.PublishPlanVersionRequest{PlanCode: "basic", BaseFeeMinor: 3000, Fees: yearlyFees("basic", 3000)}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.plans.CreateCustomer(tctx, &plansv1.CreateCustomerRequest{ExternalId: "cus_bad", Name: "Bad", Email: "bad@example.com"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.plans.BlockCustomer(tctx, &plansv1.BlockCustomerRequest{ExternalId: "cus_bad", Reason: "chargebacks"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.plans.UnblockCustomer(tctx, &plansv1.UnblockCustomerRequest{ExternalId: "cus_bad"}); err != nil {
		t.Fatal(err)
	}
	// Tenant status, by the operator.
	if _, err := e.admin.SetTenantStatus(op, &plansv1.SetTenantStatusRequest{TenantId: live.GetId(), Status: plansv1.TenantStatus_TENANT_STATUS_FROZEN, Category: "nonpayment", Reason: "unpaid"}); err != nil {
		t.Fatal(err)
	}

	got := e.events(t, as(live.GetId()), &auditv1.ListAuditEventsRequest{PageSize: 200})
	for _, want := range []string{"api_key.created", "api_key.revoked", "webhook_endpoint.created", "webhook_endpoint.secret_rotated", "webhook_endpoint.updated",
		"webhook_endpoint.deleted", "plan.created", "plan.version_published", "customer.blocked", "customer.unblocked", "tenant.status_changed"} {
		if has(got, want) == nil {
			t.Errorf("no %s entry; the log says %v", want, actions(got))
		}
	}
	// Who, from where, under which request.
	if ev := has(got, "webhook_endpoint.created"); ev == nil || ev.GetActorType() != "user" || ev.GetActorEmail() != owner.Email || ev.GetSourceIp() != "203.0.113.5" || ev.GetRequestId() != "req-audit-1" || ev.GetTenantId() != live.GetId() {
		t.Errorf("webhook_endpoint.created = %v", ev)
	}
	if ev := has(got, "api_key.created"); ev == nil || ev.GetActorType() != "operator" || ev.GetActorEmail() != "ops@payrudder.example" || !strings.Contains(ev.GetDetailJson(), key.GetApiKey().GetPrefix()) {
		t.Errorf("api_key.created = %v", ev)
	}
	// Newest first.
	for i := 1; i < len(got); i++ {
		if got[i-1].GetCreateTime().AsTime().Before(got[i].GetCreateTime().AsTime()) {
			t.Fatalf("not newest first at %d", i)
		}
	}

	// No secret anywhere in the table: not the key, not the signing secrets, not the token in the endpoint's address.
	rows, err := e.system.Query(ctx, `SELECT row_to_json(a)::text FROM audit_events a`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var row string
		if err := rows.Scan(&row); err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{key.GetPlaintext(), ep.GetEndpoint().GetSecret(), rolled.GetEndpoint().GetSecret(), "abc123secret"} {
			if secret != "" && strings.Contains(row, secret) {
				t.Fatalf("the audit log holds a secret (%q...): %s", secret[:6], row)
			}
		}
	}

	// A tenant reads its own entries and not another's; the other tenant sees none of these.
	if theirs := e.events(t, as(other.GetId()), &auditv1.ListAuditEventsRequest{PageSize: 200}); has(theirs, "webhook_endpoint.created") != nil || has(theirs, "api_key.created") != nil {
		t.Errorf("another tenant reads this tenant's entries: %v", actions(theirs))
	}
	// An operator sees the entries of the pooled database, or those of the tenant they name (also one placed in a cell: its own entries are in its schema there, and
	// only a request for that tenant reads them).
	all := e.events(t, operator(), &auditv1.ListAuditEventsRequest{PageSize: 200})
	if has(all, "api_key.created") == nil || (!e.w.cells && has(all, "webhook_endpoint.created") == nil) {
		t.Errorf("the operator's view lacks pooled entries: %v", actions(all))
	}
	one := e.events(t, operator(), &auditv1.ListAuditEventsRequest{TenantId: live.GetId(), PageSize: 200})
	if has(one, "webhook_endpoint.created") == nil || has(one, "api_key.created") == nil {
		t.Errorf("the operator's view of one tenant lacks entries: %v", actions(one))
	}
	for _, ev := range one {
		if ev.GetTenantId() != live.GetId() && ev.GetAccountId() == "" {
			t.Errorf("the operator's view of one tenant holds %v", ev)
		}
	}
	// Nobody else may ask.
	if _, err := e.audit.ListAuditEvents(ctx, &auditv1.ListAuditEventsRequest{}); err == nil {
		t.Error("a call with neither a tenant nor the operator flag read the log")
	}

	t.Run("filters and pages", func(t *testing.T) {
		keys := e.events(t, as(live.GetId()), &auditv1.ListAuditEventsRequest{Actions: []string{"api_key.*"}})
		if len(keys) != 2 || has(keys, "api_key.created") == nil || has(keys, "api_key.revoked") == nil {
			t.Errorf("api_key.* = %v", actions(keys))
		}
		exact := e.events(t, as(live.GetId()), &auditv1.ListAuditEventsRequest{Actions: []string{"customer.blocked"}})
		if len(exact) != 1 {
			t.Errorf("customer.blocked = %v", actions(exact))
		}
		byActor := e.events(t, as(live.GetId()), &auditv1.ListAuditEventsRequest{Actor: strings.ToUpper(owner.Email)})
		if len(byActor) == 0 || has(byActor, "api_key.created") != nil {
			t.Errorf("by actor = %v", actions(byActor))
		}
		target := e.events(t, as(live.GetId()), &auditv1.ListAuditEventsRequest{TargetType: "plan", TargetId: has(got, "plan.created").GetTargetId()})
		if len(target) != 1 {
			t.Errorf("by target = %v", actions(target))
		}
		// Two entries a page, walked to the end: every entry once.
		var seen []string
		token := ""
		for range 50 {
			page := e.events(t, as(live.GetId()), &auditv1.ListAuditEventsRequest{PageSize: 2, PageToken: token})
			if len(page) == 0 {
				break
			}
			for _, ev := range page {
				seen = append(seen, ev.GetId())
			}
			last := page[len(page)-1]
			token = audit.Cursor{At: last.GetCreateTime().AsTime(), ID: uuid.MustParse(last.GetId())}.Token()
		}
		if len(seen) != len(got) {
			t.Errorf("paging saw %d entries, the log has %d", len(seen), len(got))
		}
		uniq := map[string]bool{}
		for _, id := range seen {
			uniq[id] = true
		}
		if len(uniq) != len(seen) {
			t.Error("paging repeated an entry")
		}
		if _, err := e.audit.ListAuditEvents(as(live.GetId()), &auditv1.ListAuditEventsRequest{PageToken: "garbage"}); code(t, err) != codes.InvalidArgument {
			t.Errorf("a bad page token: %v", err)
		}
	})

	t.Run("members and sign-in security", func(t *testing.T) {
		olga := e.verified(t, "olga@audit.example", "Olga Audit")
		adam := e.joinNew(t, olga, "adam@audit.example", "viewer")
		if _, err := e.ident.UpdateMemberRole(ctx, &plansv1.UpdateMemberRoleRequest{UserId: olga.id, AccountId: olga.account, MemberUserId: adam.id, Role: "admin"}); err != nil {
			t.Fatal(err)
		}
		if _, err := e.ident.RemoveMember(ctx, &plansv1.RemoveMemberRequest{UserId: olga.id, AccountId: olga.account, MemberUserId: adam.id}); err != nil {
			t.Fatal(err)
		}
		evs := e.events(t, operator(), &auditv1.ListAuditEventsRequest{AccountId: olga.account, Actions: []string{"member.*"}, PageSize: 200})
		for _, want := range []string{"member.invited", "member.joined", "member.role_changed", "member.removed"} {
			if has(evs, want) == nil {
				t.Errorf("no %s entry; the account's member entries are %v", want, actions(evs))
			}
		}
		if ev := has(evs, "member.role_changed"); ev != nil && (ev.GetTenantId() != "" || ev.GetAccountId() != olga.account || !strings.Contains(ev.GetSummary(), "adam@audit.example")) {
			t.Errorf("member.role_changed = %v", ev)
		}
		// A tenant of that account sees its members' entries too.
		made, err := e.ident.CreateMyApp(ctx, &plansv1.CreateMyAppRequest{UserId: olga.id, AccountId: olga.account, Name: "Olga's app", Currency: "EUR", Timezone: "Europe/Berlin"})
		if err != nil {
			t.Fatal(err)
		}
		var appTenant string
		for _, tn := range made.GetApp().GetTenants() {
			appTenant = tn.GetId()
		}
		if got := e.events(t, as(appTenant), &auditv1.ListAuditEventsRequest{Actions: []string{"member.*"}}); has(got, "member.removed") == nil {
			t.Errorf("a tenant does not see the entries of its account's members: %v", actions(got))
		}
		if got := e.events(t, as(live.GetId()), &auditv1.ListAuditEventsRequest{Actions: []string{"member.*"}}); len(got) != 0 {
			t.Errorf("a tenant of another account sees member entries: %v", actions(got))
		}
	})

	t.Run("the table refuses any change", func(t *testing.T) {
		// The system role has no UPDATE and the app role neither UPDATE nor DELETE; the owner has every right, and the trigger still refuses.
		owner, err := pgx.Connect(ctx, e.db.Migrator)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = owner.Close(ctx) }()
		// Row-level security is forced on the owner too: it sees the rows of the tenant it names, and the trigger refuses it those.
		if _, err := owner.Exec(ctx, `SELECT set_config('app.tenant_id', $1, false)`, live.GetId()); err != nil {
			t.Fatal(err)
		}
		for who, run := range map[string]func(string) error{
			"owner":  func(q string) error { _, err := owner.Exec(ctx, q); return err },
			"system": func(q string) error { _, err := e.system.Exec(ctx, q); return err },
			"app":    func(q string) error { _, err := e.app.Exec(ctx, q); return err },
		} {
			for _, stmt := range []string{`UPDATE audit_events SET summary = 'x'`, `DELETE FROM audit_events`} {
				if err := run(stmt); err == nil {
					t.Errorf("%s: %s was allowed", who, stmt)
				} else if who != "app" && who != "system" || strings.HasPrefix(stmt, "DELETE") && who == "system" {
					if !strings.Contains(err.Error(), "append-only") {
						t.Errorf("%s: %s failed for another reason: %v", who, stmt, err)
					}
				}
			}
		}
		var n int
		if err := e.system.QueryRow(ctx, `SELECT count(*) FROM audit_events`).Scan(&n); err != nil || n == 0 {
			t.Errorf("the log is empty after the attempts: %d, %v", n, err)
		}
	})
}
