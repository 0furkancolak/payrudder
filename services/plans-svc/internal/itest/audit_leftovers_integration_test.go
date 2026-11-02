//go:build integration

package itest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	auditv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/audit/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/audit"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/identity/usermail"
)

// testAuditLeftovers: the areas plan 22 G4 listed as not recorded yet each leave an entry, in the transaction of the change, with the action's name and its target,
// and none holds what is secret (the promotion codes, the mail provider's key, the seller's own wording).
func testAuditLeftovers(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	live := e.account(t, "Audit leftovers")[plansv1.Environment_ENVIRONMENT_LIVE]
	seller := audit.Actor{Type: audit.ActorUser, ID: "7c1d3d52-0000-4000-8000-000000000001", Email: "seller@leftovers.example", IP: "203.0.113.9", RequestID: "req-left-1"}
	tctx := actedAs(as(live.GetId()), seller)
	// detailOf decodes an entry's detail (the database keeps it as jsonb, which re-spaces it).
	detailOf := func(ev *auditv1.AuditEvent) map[string]any {
		var d map[string]any
		if err := json.Unmarshal([]byte(ev.GetDetailJson()), &d); err != nil {
			t.Fatal(err)
		}
		return d
	}
	// one is the single entry of an action for the tenant, whose detail is decoded.
	one := func(t *testing.T, action string) (*auditv1.AuditEvent, map[string]any) {
		t.Helper()
		got := e.events(t, as(live.GetId()), &auditv1.ListAuditEventsRequest{Actions: []string{action}, PageSize: 20})
		if len(got) != 1 {
			t.Fatalf("%s: %d entries, want 1 (the log says %v)", action, len(got), actions(e.events(t, as(live.GetId()), &auditv1.ListAuditEventsRequest{PageSize: 200})))
		}
		if got[0].GetActorEmail() != seller.Email || got[0].GetSourceIp() != seller.IP || got[0].GetRequestId() != seller.RequestID {
			t.Errorf("%s: who = %q %q %q, want the request's actor", action, got[0].GetActorEmail(), got[0].GetSourceIp(), got[0].GetRequestId())
		}
		return got[0], detailOf(got[0])
	}
	var secrets []string // what no row of the table may hold

	t.Run("price changes", func(t *testing.T) {
		if _, err := e.plans.CreatePlan(tctx, &plansv1.CreatePlanRequest{Code: "basic", Name: "basic"}); err != nil {
			t.Fatal(err)
		}
		for _, fee := range []int64{3000, 4000} {
			if _, err := e.plans.PublishPlanVersion(tctx, &plansv1.PublishPlanVersionRequest{PlanCode: "basic", BaseFeeMinor: fee}); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := e.plans.CreateCustomer(tctx, &plansv1.CreateCustomerRequest{ExternalId: "cus_pc", Name: "pc", Email: "pc@example.com"}); err != nil {
			t.Fatal(err)
		}
		start := time.Now().Add(-5 * 24 * time.Hour).Truncate(time.Microsecond)
		sub, err := e.plans.CreateSubscription(tctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: "cus_pc", PlanCode: "basic", PlanVersion: 1, StartTime: timestamppb.New(start), FeeTiming: plansv1.FeeTiming_FEE_TIMING_ADVANCE})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.plans.AdvanceSubscriptionFee(tctx, &plansv1.AdvanceSubscriptionFeeRequest{SubscriptionId: sub.GetSubscription().GetId(), PeriodStart: timestamppb.New(start)}); err != nil {
			t.Fatal(err)
		}
		// A dry run writes no entry.
		if _, err := e.plans.SchedulePriceChange(tctx, &plansv1.SchedulePriceChangeRequest{PlanCode: "basic", FromVersion: 1, NoticeDays: 14, DryRun: true}); err != nil {
			t.Fatal(err)
		}
		if got := e.events(t, as(live.GetId()), &auditv1.ListAuditEventsRequest{Actions: []string{"price_change.*"}}); len(got) != 0 {
			t.Fatalf("a dry run left %v", actions(got))
		}
		run, err := e.plans.SchedulePriceChange(tctx, &plansv1.SchedulePriceChangeRequest{PlanCode: "basic", FromVersion: 1, NoticeDays: 14})
		if err != nil {
			t.Fatal(err)
		}
		ev, d := one(t, "price_change.scheduled")
		if ev.GetTargetType() != "price_change" || ev.GetTargetId() != run.GetPriceChangeId() || d["plan"] != "basic" || d["from_version"] != float64(1) || d["to_version"] != float64(2) || d["scheduled"] != float64(1) {
			t.Errorf("price_change.scheduled = %v %v", ev, d)
		}
		for range 2 { // the second call finishes nothing and records nothing
			if _, err := e.plans.WithdrawPriceChange(tctx, &plansv1.WithdrawPriceChangeRequest{Id: run.GetPriceChangeId(), Actor: "boss@example.com"}); err != nil {
				t.Fatal(err)
			}
		}
		if ev, _ := one(t, "price_change.withdrawn"); ev.GetTargetId() != run.GetPriceChangeId() {
			t.Errorf("price_change.withdrawn = %v", ev)
		}
	})

	t.Run("coupons and promotion codes", func(t *testing.T) {
		if _, err := e.plans.CreateCoupon(tctx, &plansv1.CreateCouponRequest{Coupon: &plansv1.Coupon{Code: "SPRING", PercentBps: 1000, Duration: plansv1.CouponDuration_COUPON_DURATION_ONCE}}); err != nil {
			t.Fatal(err)
		}
		if ev, d := one(t, "coupon.created"); ev.GetTargetType() != "coupon" || ev.GetTargetId() != "SPRING" || d["percent_bps"] != float64(1000) {
			t.Errorf("coupon.created = %v %v", ev, d)
		}
		batch, err := e.plans.CreatePromotionCodes(tctx, &plansv1.CreatePromotionCodesRequest{CouponCode: "SPRING", Count: 3, Prefix: "AUD"})
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range batch.GetCodes()[1:] { // the first is deactivated below, which names it: a dead code is no secret
			secrets = append(secrets, c.GetCode())
		}
		if ev, d := one(t, "promotion_code.created"); ev.GetTargetId() != "SPRING" || d["count"] != float64(3) {
			t.Errorf("promotion_code.created = %v %v", ev, d)
		}
		if _, err := e.plans.DeactivatePromotionCode(tctx, &plansv1.DeactivatePromotionCodeRequest{Code: batch.GetCodes()[0].GetCode()}); err != nil {
			t.Fatal(err)
		}
		if ev, d := one(t, "promotion_code.deactivated"); ev.GetTargetType() != "promotion_code" || d["coupon"] != "SPRING" {
			t.Errorf("promotion_code.deactivated = %v %v", ev, d)
		}
		if _, err := e.plans.DeactivateCoupon(tctx, &plansv1.DeactivateCouponRequest{Code: "SPRING"}); err != nil {
			t.Fatal(err)
		}
		if ev, _ := one(t, "coupon.deactivated"); ev.GetTargetId() != "SPRING" {
			t.Errorf("coupon.deactivated = %v", ev)
		}
	})

	t.Run("routing rules, tax rates and mail settings", func(t *testing.T) {
		if _, err := e.plans.SetRoutingRules(tctx, &plansv1.SetRoutingRulesRequest{Rules: []*plansv1.RoutingRule{{Country: "TR", Currency: "TRY", Provider: "iyzico"}}}); err != nil {
			t.Fatal(err)
		}
		if ev, d := one(t, "routing_rules.changed"); ev.GetTargetType() != "routing_rules" || ev.GetAfterHash() == "" || d["count"] != float64(1) || !strings.Contains(ev.GetDetailJson(), "iyzico") {
			t.Errorf("routing_rules.changed = %v %v", ev, d)
		}
		for _, rates := range [][]*plansv1.TaxRate{{{Country: "DE", Name: "VAT", RateBps: 1900}, {Country: "TR", Name: "KDV", RateBps: 2000}}, {{Country: "DE", Name: "VAT", RateBps: 1900}}} {
			if _, err := e.plans.SetTaxRates(tctx, &plansv1.SetTaxRatesRequest{Rates: rates}); err != nil {
				t.Fatal(err)
			}
		}
		taxes := e.events(t, as(live.GetId()), &auditv1.ListAuditEventsRequest{Actions: []string{"tax_rates.changed"}})
		if len(taxes) != 2 || taxes[0].GetBeforeHash() == taxes[0].GetAfterHash() || detailOf(taxes[0])["previous_count"] != float64(2) || detailOf(taxes[0])["count"] != float64(1) {
			t.Errorf("tax_rates.changed (newest first) = %v", taxes)
		}
		if _, err := e.plans.SetMailSettings(tctx, &plansv1.SetMailSettingsRequest{Categories: []*plansv1.MailCategorySetting{{Category: "usage_alerts", Enabled: false}}}); err != nil {
			t.Fatal(err)
		}
		if ev, d := one(t, "mail_settings.changed"); ev.GetTargetType() != "mail_settings" || !strings.Contains(ev.GetDetailJson(), "usage_alerts") || d["previously_disabled"] == nil {
			t.Errorf("mail_settings.changed = %v %v", ev, d)
		}
	})

	t.Run("the mail provider", func(t *testing.T) {
		const key = "nk_audit_leftovers_key_9876"
		secrets = append(secrets, key)
		nafru := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer "+key {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"data":[]}`))
		}))
		defer nafru.Close()
		if _, err := e.mailprov.PutMailIntegration(tctx, &plansv1.PutMailIntegrationRequest{Provider: "nafru", BaseUrl: nafru.URL, ApiKey: key, From: "Acme <billing@acme.example>"}); err != nil {
			t.Fatal(err)
		}
		if ev, d := one(t, "mail_integration.saved"); ev.GetTargetId() != "nafru" || d["provider"] != "nafru" || d["from"] != "Acme <billing@acme.example>" {
			t.Errorf("mail_integration.saved = %v %v", ev, d)
		}
		if _, err := e.mailprov.DeleteMailIntegration(tctx, &plansv1.DeleteMailIntegrationRequest{}); err != nil {
			t.Fatal(err)
		}
		one(t, "mail_integration.removed")
	})

	t.Run("contracts", func(t *testing.T) {
		v := publishContract(t, e, live.GetId(), "tos", "consumer", true, map[string]string{"en": "Terms of the audit leftovers shop."})
		if _, err := e.contracts.PublishContractVersion(tctx, &plansv1.PublishContractVersionRequest{Kind: "tos", Bodies: map[string]string{"en": "Terms, second."}}); err != nil {
			t.Fatal(err)
		}
		pub := e.events(t, as(live.GetId()), &auditv1.ListAuditEventsRequest{Actions: []string{"contract.version_published"}})
		if len(pub) != 2 {
			t.Fatalf("contract.version_published: %v", actions(pub))
		}
		first := pub[1] // newest first
		if d := detailOf(first); first.GetTargetType() != "contract_version" || first.GetTargetId() != v.GetId() || d["kind"] != "tos" || d["number"] != float64(1) || d["material"] != true || strings.Contains(first.GetDetailJson(), "Terms of the audit") {
			t.Errorf("contract.version_published = %v (the text of a contract is not in the entry)", first)
		}
		const wording = "Cancel by mail only, says the seller"
		secrets = append(secrets, wording)
		if _, err := e.contracts.SetContractSettings(tctx, &plansv1.SetContractSettingsRequest{RequiredKinds: []string{"tos"}, RetentionMonths: 24, RenewalCancelTexts: map[string]string{"en": wording}}); err != nil {
			t.Fatal(err)
		}
		if ev, d := one(t, "contract.settings_changed"); ev.GetBeforeHash() == ev.GetAfterHash() || d["retention_months"] != float64(24) || !strings.Contains(ev.GetDetailJson(), `"tos"`) || strings.Contains(ev.GetDetailJson(), "Cancel by mail") {
			t.Errorf("contract.settings_changed = %v %v", ev, d)
		}
	})

	t.Run("the go-live profile", func(t *testing.T) {
		owner := e.verified(t, "leftovers-owner@example.com", "Leftovers Co")
		created, err := e.ident.CreateMyApp(ctx, &plansv1.CreateMyAppRequest{UserId: owner.id, AccountId: owner.account, Name: "Shop", Currency: "EUR", Timezone: "Europe/Berlin"})
		if err != nil {
			t.Fatal(err)
		}
		app := created.GetApp()
		lt := envOf(app, plansv1.Environment_ENVIRONMENT_LIVE)
		oc := actedAs(ctx, audit.Actor{Type: audit.ActorUser, ID: owner.id, Email: owner.email, IP: "203.0.113.10", RequestID: "req-left-2"})
		if _, err := e.ident.UpdateMyBillingProfile(oc, &plansv1.UpdateMyBillingProfileRequest{UserId: owner.id, AppId: app.GetId(), Address: &plansv1.Address{Line1: "Unter den Linden 1", City: "Berlin", Country: "DE", PostalCode: "10117"}, Legal: &plansv1.LegalInfo{LegalName: "Shop GmbH", TaxId: "DE123456789"}}); err != nil {
			t.Fatal(err)
		}
		if _, err := e.ident.AcceptMyTerms(oc, &plansv1.AcceptMyTermsRequest{UserId: owner.id, AppId: app.GetId()}); err != nil {
			t.Fatal(err)
		}
		got := e.events(t, as(lt.GetId()), &auditv1.ListAuditEventsRequest{Actions: []string{"go_live.*"}})
		for _, want := range []string{"go_live.billing_profile_saved", "go_live.terms_accepted"} {
			ev := has(got, want)
			if ev == nil || ev.GetTargetType() != "app" || ev.GetTargetId() != app.GetId() || ev.GetActorEmail() != owner.email {
				t.Errorf("%s = %v (log: %v)", want, ev, actions(got))
			}
		}
		if ev := has(got, "go_live.billing_profile_saved"); ev == nil || strings.Contains(ev.GetDetailJson(), "DE123456789") || strings.Contains(ev.GetDetailJson(), "Unter den Linden") {
			t.Errorf("the profile itself is in the entry: %v", ev)
		}
		// Asking for the terms again changes nothing and writes nothing.
		if _, err := e.ident.AcceptMyTerms(oc, &plansv1.AcceptMyTermsRequest{UserId: owner.id, AppId: app.GetId()}); err != nil {
			t.Fatal(err)
		}
		if again := e.events(t, as(lt.GetId()), &auditv1.ListAuditEventsRequest{Actions: []string{"go_live.terms_accepted"}}); len(again) != 1 {
			t.Errorf("terms accepted twice left %d entries", len(again))
		}
	})

	t.Run("a ticket's status, changed by support", func(t *testing.T) {
		ada := e.verified(t, "leftovers-support@example.com", "Support Leftovers Co")
		open, err := e.support.OpenMySupportTicket(ctx, &plansv1.OpenMySupportTicketRequest{UserId: ada.id, AccountId: ada.account, Subject: "Late webhooks", Category: "integration", Body: "Since this morning."})
		if err != nil {
			t.Fatal(err)
		}
		op := actedAs(operator(), audit.Actor{Type: audit.ActorOperator, ID: "ops@payrudder.example", Email: "ops@payrudder.example", IP: "198.51.100.7", RequestID: "req-left-3"})
		pending, solved := "pending", "solved"
		for _, s := range []*string{&pending, &pending, &solved} { // the second call changes nothing
			if _, err := e.support.UpdateSupportTicket(op, &plansv1.UpdateSupportTicketRequest{TicketId: open.GetTicket().GetId(), Status: s, AuthorName: "Sam"}); err != nil {
				t.Fatal(err)
			}
		}
		got := e.events(t, operator(), &auditv1.ListAuditEventsRequest{Actions: []string{"support.status_changed"}, TargetType: "support_ticket", TargetId: open.GetTicket().GetId()})
		if len(got) != 2 {
			t.Fatalf("support.status_changed = %v, want open to pending and pending to solved", got)
		}
		if d := detailOf(got[0]); got[0].GetTargetId() != open.GetTicket().GetId() || got[0].GetAccountId() != ada.account || got[0].GetActorType() != "operator" || got[0].GetActorEmail() != "ops@payrudder.example" || d["from"] != "pending" || d["to"] != "solved" {
			t.Errorf("support.status_changed (newest first) = %v", got)
		}
	})

	t.Run("a locked sign-in and a new password", func(t *testing.T) {
		const addr = "leftovers-lock@example.com"
		p := e.verified(t, addr, "Lock Leftovers Co")
		ac := actedAs(ctx, audit.Actor{IP: "198.51.100.77", RequestID: "req-left-4"})
		for range 6 {
			_, _ = e.ident.SignIn(ac, &plansv1.SignInRequest{Email: addr, Password: "not the password!"})
		}
		locked := e.events(t, operator(), &auditv1.ListAuditEventsRequest{Actions: []string{"user.sign_in_locked"}, TargetType: "user", TargetId: p.id})
		if len(locked) != 1 || locked[0].GetTargetId() != p.id || locked[0].GetSourceIp() != "198.51.100.77" || locked[0].GetActorType() != "system" || detailOf(locked[0])["step"] != "password" {
			t.Fatalf("user.sign_in_locked = %v (one entry for the failure that set the lock, with the address the failures came from)", locked)
		}
		if _, err := e.ident.RequestPasswordReset(ctx, &plansv1.RequestPasswordResetRequest{Email: addr}); err != nil {
			t.Fatal(err)
		}
		token := e.mail.lastToken(usermail.KindReset, addr)
		secrets = append(secrets, token, "a brand new password")
		if _, err := e.ident.ResetPassword(actedAs(ctx, audit.Actor{IP: "198.51.100.78", RequestID: "req-left-5"}), &plansv1.ResetPasswordRequest{Token: token, NewPassword: "a brand new password"}); err != nil {
			t.Fatal(err)
		}
		reset := e.events(t, operator(), &auditv1.ListAuditEventsRequest{Actions: []string{"user.password_reset"}, TargetType: "user", TargetId: p.id})
		if len(reset) != 1 || reset[0].GetTargetId() != p.id || reset[0].GetActorEmail() != addr || reset[0].GetSourceIp() != "198.51.100.78" {
			t.Errorf("user.password_reset = %v", reset)
		}
	})

	// No row holds a secret, whatever its action.
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
		for _, s := range secrets {
			if s != "" && strings.Contains(row, s) {
				t.Fatalf("the audit log holds a secret (%q...): %s", s[:min(len(s), 4)], row)
			}
		}
	}
}
