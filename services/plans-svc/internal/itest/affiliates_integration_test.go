//go:build integration

package itest

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
)

// uniq makes a code that no other test of the shared database used: affiliate codes are unique across the platform.
func uniq(prefix string) string {
	return prefix + strings.ToUpper(strings.ReplaceAll(uuid.NewString()[:8], "-", ""))
}

// activeProgram sets a plain 10 % program, active.
func activeProgram(t *testing.T, e *env, ctx context.Context, mut func(p *plansv1.AffiliateProgram)) *plansv1.AffiliateProgram {
	t.Helper()
	p := &plansv1.AffiliateProgram{Status: "active", CommissionKind: "percent_bps", CommissionValue: 1000, CommissionTerm: "lifetime", AttributionWindowDays: 30, HoldDays: 30, DailyClickCap: 1000}
	if mut != nil {
		mut(p)
	}
	r, err := e.aff.SetAffiliateProgram(ctx, &plansv1.SetAffiliateProgramRequest{Program: p})
	if err != nil {
		t.Fatalf("SetAffiliateProgram: %v", err)
	}
	return r.GetProgram()
}

func newAffiliate(t *testing.T, e *env, ctx context.Context, email string) *plansv1.Affiliate {
	t.Helper()
	r, err := e.aff.CreateAffiliate(ctx, &plansv1.CreateAffiliateRequest{Email: email, Name: "Partner"})
	if err != nil {
		t.Fatalf("CreateAffiliate %s: %v", email, err)
	}
	return r.GetAffiliate()
}

func newCustomer(t *testing.T, e *env, ctx context.Context, ext, email, pm string) {
	t.Helper()
	if _, err := e.plans.CreateCustomer(ctx, &plansv1.CreateCustomerRequest{ExternalId: ext, Name: ext, Email: email, PaymentMethod: pm}); err != nil {
		t.Fatalf("CreateCustomer %s: %v", ext, err)
	}
}

func testAffiliates(t *testing.T) {
	e := newEnv(t)
	acct := e.account(t, "Affiliates")
	live, sandbox := acct[plansv1.Environment_ENVIRONMENT_LIVE], acct[plansv1.Environment_ENVIRONMENT_SANDBOX]
	ctx := as(live.GetId())
	other := as(sandbox.GetId())

	t.Run("the program is validated and replaced", func(t *testing.T) {
		if _, err := e.aff.GetAffiliateProgram(ctx, &plansv1.GetAffiliateProgramRequest{}); errkey.Of(err) != "affiliate.program.not_set" || code(t, err) != codes.NotFound {
			t.Fatalf("before a program is set: %v (%q)", err, errkey.Of(err))
		}
		for name, p := range map[string]*plansv1.AffiliateProgram{
			"percent over 100":         {CommissionKind: "percent_bps", CommissionValue: 10001},
			"fixed without a currency": {CommissionKind: "fixed", CommissionValue: 500},
			"unknown kind":             {CommissionKind: "flat", CommissionValue: 5},
			"term without a number":    {CommissionKind: "percent_bps", CommissionValue: 500, CommissionTerm: "months"},
			"lifetime with a number":   {CommissionKind: "percent_bps", CommissionValue: 500, CommissionTerm: "lifetime", CommissionTermValue: 3},
			"window too long":          {CommissionKind: "percent_bps", CommissionValue: 500, AttributionWindowDays: 400},
			"hold too long":            {CommissionKind: "percent_bps", CommissionValue: 500, HoldDays: 181},
			"percent with a currency":  {CommissionKind: "percent_bps", CommissionValue: 500, Currency: "EUR"},
			"landing not http":         {CommissionKind: "percent_bps", CommissionValue: 500, LandingUrl: "javascript:alert(1)"},
			"unknown coupon":           {CommissionKind: "percent_bps", CommissionValue: 500, ReferredCouponCode: "NOSUCH"},
		} {
			if _, err := e.aff.SetAffiliateProgram(ctx, &plansv1.SetAffiliateProgramRequest{Program: p}); err == nil {
				t.Errorf("%s was accepted", name)
			}
		}
		got := activeProgram(t, e, ctx, func(p *plansv1.AffiliateProgram) {
			p.LandingUrl = "https://shop.example.com/welcome"
			p.CommissionTerm = "months"
			p.CommissionTermValue = 12
		})
		if got.GetStatus() != "active" || got.GetCommissionTermValue() != 12 || got.GetLandingUrl() != "https://shop.example.com/welcome" {
			t.Fatalf("program: %v", got)
		}
		// Replaced, not merged.
		got = activeProgram(t, e, ctx, func(p *plansv1.AffiliateProgram) {
			p.CommissionKind = "fixed"
			p.CommissionValue = 700
			p.Currency = "try"
		})
		if got.GetCommissionKind() != "fixed" || got.GetCurrency() != "TRY" || got.GetCommissionTermValue() != 0 || got.GetLandingUrl() != "" {
			t.Fatalf("replaced program: %v", got)
		}
		activeProgram(t, e, ctx, nil)
		// The sandbox has its own: nothing set there.
		if _, err := e.aff.GetAffiliateProgram(other, &plansv1.GetAffiliateProgramRequest{}); errkey.Of(err) != "affiliate.program.not_set" {
			t.Fatalf("the sandbox sees the live program: %v", err)
		}
	})

	t.Run("affiliates, codes and their uniqueness across tenants", func(t *testing.T) {
		code1 := uniq("ANN")
		a, err := e.aff.CreateAffiliate(ctx, &plansv1.CreateAffiliateRequest{Email: "ann@partners.test", Name: "Ann", Code: code1})
		if err != nil || a.GetAffiliate().GetStatus() != "active" || a.GetAffiliate().GetCode() != code1 || a.GetAffiliate().GetApproveTime() == nil {
			t.Fatalf("create: %v %v", a, err)
		}
		// Same code, any case, in the other tenant: the code is the platform's.
		_, err = e.aff.CreateAffiliate(other, &plansv1.CreateAffiliateRequest{Email: "x@partners.test", Code: strings.ToLower(code1)})
		if code(t, err) != codes.AlreadyExists || errkey.Of(err) != "affiliate.code_taken" {
			t.Fatalf("a taken code: %v (%q)", err, errkey.Of(err))
		}
		_, err = e.aff.CreateAffiliate(ctx, &plansv1.CreateAffiliateRequest{Email: "ANN@partners.test", Code: uniq("ZZZ")})
		if errkey.Of(err) != "affiliate.email_taken" {
			t.Fatalf("a taken e-mail: %v (%q)", err, errkey.Of(err))
		}
		for _, bad := range []string{"ab", "has space", "a/b"} {
			if _, err := e.aff.CreateAffiliate(ctx, &plansv1.CreateAffiliateRequest{Email: uniq("b") + "@partners.test", Code: bad}); code(t, err) != codes.InvalidArgument {
				t.Errorf("code %q: %v", bad, err)
			}
		}
		if _, err := e.aff.CreateAffiliate(ctx, &plansv1.CreateAffiliateRequest{Email: "not-an-address"}); code(t, err) != codes.InvalidArgument {
			t.Errorf("a bad e-mail: %v", err)
		}
		rnd, err := e.aff.CreateAffiliate(ctx, &plansv1.CreateAffiliateRequest{Email: "rnd@partners.test", Pending: true})
		if err != nil || len(rnd.GetAffiliate().GetCode()) != 8 || rnd.GetAffiliate().GetStatus() != "pending" || rnd.GetAffiliate().GetApproveTime() != nil {
			t.Fatalf("random code, pending: %v %v", rnd, err)
		}
		// Approve, suspend, reinstate.
		id := rnd.GetAffiliate().GetId()
		for _, step := range []struct{ to string }{{"active"}, {"suspended"}, {"active"}} {
			u, err := e.aff.UpdateAffiliate(ctx, &plansv1.UpdateAffiliateRequest{Id: id, Status: step.to, PayoutMethod: "bank_transfer", PayoutDetails: "TR00 0000"})
			if err != nil || u.GetAffiliate().GetStatus() != step.to || u.GetAffiliate().GetPayoutMethod() != "bank_transfer" || u.GetAffiliate().GetName() != "" {
				t.Fatalf("-> %s: %v %v", step.to, u, err)
			}
		}
		if _, err := e.aff.UpdateAffiliate(ctx, &plansv1.UpdateAffiliateRequest{Id: id, Status: "pending"}); code(t, err) != codes.InvalidArgument {
			t.Errorf("back to pending: %v", err)
		}
		if _, err := e.aff.GetAffiliate(other, &plansv1.GetAffiliateRequest{Id: id}); errkey.Of(err) != "affiliate.not_found" {
			t.Errorf("another tenant's affiliate: %v", err)
		}
		list, err := e.aff.ListAffiliates(ctx, &plansv1.ListAffiliatesRequest{Status: "active"})
		if err != nil || list.GetTotal() < 2 {
			t.Fatalf("list: %v %v", list, err)
		}
		if l, err := e.aff.ListAffiliates(ctx, &plansv1.ListAffiliatesRequest{Search: "ann@partners"}); err != nil || l.GetTotal() != 1 {
			t.Fatalf("search: %v %v", l, err)
		}
		if l, err := e.aff.ListAffiliates(other, &plansv1.ListAffiliatesRequest{}); err != nil || l.GetTotal() != 0 {
			t.Fatalf("the sandbox sees the live affiliates: %v %v", l, err)
		}
	})

	t.Run("clicks are counted per day, capped, and nothing about the visitor is kept", func(t *testing.T) {
		activeProgram(t, e, ctx, func(p *plansv1.AffiliateProgram) { p.DailyClickCap = 3 })
		a := newAffiliate(t, e, ctx, uniq("clk")+"@partners.test").GetCode()
		var counted []bool
		for range 5 {
			r, err := e.pub.TrackAffiliateClick(context.Background(), &plansv1.TrackAffiliateClickRequest{Code: strings.ToLower(a)})
			if err != nil || !r.GetValid() || r.GetCode() != a || r.GetAttributionWindowDays() != 30 {
				t.Fatalf("click: %v %v", r, err)
			}
			counted = append(counted, r.GetCounted())
		}
		if want := []bool{true, true, true, false, false}; !equalBools(counted, want) {
			t.Fatalf("counted = %v, want %v (the cap is 3 a day)", counted, want)
		}
		// A visitor who carries the code already is answered, and not counted.
		if r, err := e.pub.TrackAffiliateClick(context.Background(), &plansv1.TrackAffiliateClickRequest{Code: a, SkipCount: true}); err != nil || !r.GetValid() || r.GetCounted() || r.GetAttributionWindowDays() != 30 {
			t.Fatalf("skip count: %v %v", r, err)
		}
		list, _ := e.aff.ListAffiliates(ctx, &plansv1.ListAffiliatesRequest{Search: a})
		if list.GetAffiliates()[0].GetClicks() != 3 {
			t.Fatalf("clicks = %d", list.GetAffiliates()[0].GetClicks())
		}
		// One row for the day, and the table has no column that could hold a visitor.
		var rows int
		var cols []string
		if err := pgxx.InTenantTx(context.Background(), e.app, uuid.MustParse(live.GetId()), func(tx pgx.Tx) error {
			if err := tx.QueryRow(context.Background(), `SELECT count(*) FROM affiliate_clicks_daily c JOIN affiliates a USING (tenant_id) WHERE c.affiliate_id = a.id AND a.code = $1`, a).Scan(&rows); err != nil {
				return err
			}
			r, err := tx.Query(context.Background(), `SELECT column_name FROM information_schema.columns WHERE table_name = 'affiliate_clicks_daily' ORDER BY ordinal_position`)
			if err != nil {
				return err
			}
			defer r.Close()
			for r.Next() {
				var c string
				if err := r.Scan(&c); err != nil {
					return err
				}
				cols = append(cols, c)
			}
			return r.Err()
		}); err != nil {
			t.Fatal(err)
		}
		if rows != 1 || strings.Join(cols, ",") != "tenant_id,affiliate_id,day,clicks" {
			t.Fatalf("rows %d, columns %v", rows, cols)
		}
		// Unknown, malformed and suspended codes count nothing and are not valid.
		sus := newAffiliate(t, e, ctx, uniq("sus")+"@partners.test")
		if _, err := e.aff.UpdateAffiliate(ctx, &plansv1.UpdateAffiliateRequest{Id: sus.GetId(), Status: "suspended"}); err != nil {
			t.Fatal(err)
		}
		for _, c := range []string{"NOSUCHCODE", "a b", "", sus.GetCode()} {
			if r, err := e.pub.TrackAffiliateClick(context.Background(), &plansv1.TrackAffiliateClickRequest{Code: c}); err != nil || r.GetValid() {
				t.Errorf("code %q: %v %v", c, r, err)
			}
		}
		// A paused program turns every link off.
		activeProgram(t, e, ctx, func(p *plansv1.AffiliateProgram) { p.Status = "paused" })
		if r, _ := e.pub.TrackAffiliateClick(context.Background(), &plansv1.TrackAffiliateClickRequest{Code: a}); r.GetValid() {
			t.Error("a paused program counts a click")
		}
		activeProgram(t, e, ctx, nil)
	})

	t.Run("first touch wins, once, and cannot be moved", func(t *testing.T) {
		activeProgram(t, e, ctx, nil)
		first, second := newAffiliate(t, e, ctx, uniq("f")+"@partners.test"), newAffiliate(t, e, ctx, uniq("s")+"@partners.test")
		ext := uniq("cus_ft")
		newCustomer(t, e, ctx, ext, ext+"@buyers.test", "")
		attribute := func(code string) *plansv1.AttributeCustomerResponse {
			t.Helper()
			r, err := e.aff.AttributeCustomer(ctx, &plansv1.AttributeCustomerRequest{CustomerExternalId: ext, Code: code, Source: "link"})
			if err != nil {
				t.Fatalf("attribute %s: %v", code, err)
			}
			return r
		}
		if r := attribute(first.GetCode()); !r.GetAttributed() {
			t.Fatalf("first: %v", r)
		}
		if r := attribute(second.GetCode()); r.GetAttributed() || r.GetReason() != "already_attributed" {
			t.Fatalf("a later touch moved the attribution: %v", r)
		}
		if r := attribute(first.GetCode()); r.GetAttributed() || r.GetReason() != "already_attributed" {
			t.Fatalf("the same again: %v", r)
		}
		refs, err := e.aff.ListAffiliateReferrals(ctx, &plansv1.ListAffiliateReferralsRequest{AffiliateId: first.GetId()})
		if err != nil || refs.GetTotal() != 1 || refs.GetReferrals()[0].GetCustomerEmailMasked() != "c***@buyers.test" {
			t.Fatalf("referrals of the first: %v %v", refs, err)
		}
		if refs, _ := e.aff.ListAffiliateReferrals(ctx, &plansv1.ListAffiliateReferralsRequest{AffiliateId: second.GetId()}); refs.GetTotal() != 0 {
			t.Fatalf("the second has referrals: %v", refs)
		}
		// Even the database refuses: not even the owner of the table moves or deletes an attribution (a trigger, below the grants).
		owner, err := pgxx.NewPool(context.Background(), e.db.Migrator)
		if err != nil {
			t.Fatal(err)
		}
		defer owner.Close()
		for _, stmt := range []string{`UPDATE affiliate_attributions SET affiliate_id = $1`, `DELETE FROM affiliate_attributions WHERE affiliate_id <> $1`} {
			err = pgxx.InTenantTx(context.Background(), owner, uuid.MustParse(live.GetId()), func(tx pgx.Tx) error {
				_, err := tx.Exec(context.Background(), stmt, uuid.MustParse(second.GetId()))
				return err
			})
			if err == nil || !strings.Contains(err.Error(), "set once") {
				t.Fatalf("%s: want the trigger's refusal, got %v", stmt, err)
			}
		}
	})

	t.Run("a customer who looks like the affiliate is not attributed, and the owner can see why", func(t *testing.T) {
		activeProgram(t, e, ctx, nil)
		aff := newAffiliate(t, e, ctx, "owner-of-link@acme-corp.test")
		// The affiliate is also a customer, with a card.
		newCustomer(t, e, ctx, uniq("cus_aff"), "owner-of-link@acme-corp.test", "pm_shared_card")
		cases := []struct{ name, email, pm, reason string }{
			{"same mailbox", "Owner-Of-Link+shop@acme-corp.test", "", "same_email"},
			{"same company domain", "colleague@acme-corp.test", "", "same_domain"},
			{"same card", "stranger@elsewhere.test", "pm_shared_card", "same_payment_method"},
		}
		for _, c := range cases {
			ext := uniq("cus_self")
			newCustomer(t, e, ctx, ext, c.email, c.pm)
			r, err := e.aff.AttributeCustomer(ctx, &plansv1.AttributeCustomerRequest{CustomerExternalId: ext, Code: aff.GetCode()})
			if err != nil || r.GetAttributed() || r.GetReason() != c.reason {
				t.Errorf("%s: %v %v, want reason %s", c.name, r, err, c.reason)
			}
		}
		// A different person at a shared mailbox provider is welcome.
		ext := uniq("cus_ok")
		newCustomer(t, e, ctx, ext, uniq("p")+"@gmail.com", "")
		aff2 := newAffiliate(t, e, ctx, uniq("g")+"@gmail.com")
		if r, err := e.aff.AttributeCustomer(ctx, &plansv1.AttributeCustomerRequest{CustomerExternalId: ext, Code: aff2.GetCode()}); err != nil || !r.GetAttributed() {
			t.Errorf("two people at gmail: %v %v", r, err)
		}
		var n int
		if err := pgxx.InTenantTx(context.Background(), e.app, uuid.MustParse(live.GetId()), func(tx pgx.Tx) error {
			return tx.QueryRow(context.Background(), `SELECT count(*) FROM affiliate_attribution_rejections WHERE affiliate_id = $1`, uuid.MustParse(aff.GetId())).Scan(&n)
		}); err != nil || n != 3 {
			t.Fatalf("rejections kept: %d (%v)", n, err)
		}
		// Nobody was attributed to it.
		if refs, _ := e.aff.ListAffiliateReferrals(ctx, &plansv1.ListAffiliateReferralsRequest{AffiliateId: aff.GetId()}); refs.GetTotal() != 0 {
			t.Fatalf("a self-referral was attributed: %v", refs)
		}
	})

	t.Run("the window, the program and the affiliate's status gate an attribution", func(t *testing.T) {
		activeProgram(t, e, ctx, func(p *plansv1.AffiliateProgram) { p.AttributionWindowDays = 7 })
		aff := newAffiliate(t, e, ctx, uniq("w")+"@partners.test")
		try := func(touch time.Time) *plansv1.AttributeCustomerResponse {
			ext := uniq("cus_w")
			newCustomer(t, e, ctx, ext, ext+"@buyers.test", "")
			r, err := e.aff.AttributeCustomer(ctx, &plansv1.AttributeCustomerRequest{CustomerExternalId: ext, Code: aff.GetCode(), TouchTime: timestamppb.New(touch)})
			if err != nil {
				t.Fatal(err)
			}
			return r
		}
		if r := try(time.Now().AddDate(0, 0, -6)); !r.GetAttributed() {
			t.Errorf("inside the window: %v", r)
		}
		if r := try(time.Now().AddDate(0, 0, -8)); r.GetAttributed() || r.GetReason() != "expired" {
			t.Errorf("outside the window: %v", r)
		}
		if r := try(time.Now().Add(48 * time.Hour)); r.GetAttributed() || r.GetReason() != "expired" {
			t.Errorf("a touch in the future: %v", r)
		}
		if _, err := e.aff.UpdateAffiliate(ctx, &plansv1.UpdateAffiliateRequest{Id: aff.GetId(), Status: "suspended"}); err != nil {
			t.Fatal(err)
		}
		if r := try(time.Now()); r.GetAttributed() || r.GetReason() != "affiliate_inactive" {
			t.Errorf("suspended: %v", r)
		}
		if _, err := e.aff.UpdateAffiliate(ctx, &plansv1.UpdateAffiliateRequest{Id: aff.GetId(), Status: "active"}); err != nil {
			t.Fatal(err)
		}
		activeProgram(t, e, ctx, func(p *plansv1.AffiliateProgram) { p.Status = "paused" })
		if r := try(time.Now()); r.GetAttributed() || r.GetReason() != "program_inactive" {
			t.Errorf("paused: %v", r)
		}
		activeProgram(t, e, ctx, nil)
		ext := uniq("cus_unk")
		newCustomer(t, e, ctx, ext, ext+"@buyers.test", "")
		if r, err := e.aff.AttributeCustomer(ctx, &plansv1.AttributeCustomerRequest{CustomerExternalId: ext, Code: "NOSUCHCODE"}); err != nil || r.GetAttributed() || r.GetReason() != "unknown_code" {
			t.Errorf("unknown: %v %v", r, err)
		}
		// Another tenant's code is unknown here.
		foreign := newAffiliate(t, e, other, uniq("fo")+"@partners.test")
		if r, err := e.aff.AttributeCustomer(ctx, &plansv1.AttributeCustomerRequest{CustomerExternalId: ext, Code: foreign.GetCode()}); err != nil || r.GetAttributed() || r.GetReason() != "unknown_code" {
			t.Errorf("the sandbox's code in live: %v %v", r, err)
		}
		if _, err := e.aff.AttributeCustomer(ctx, &plansv1.AttributeCustomerRequest{CustomerExternalId: "nobody", Code: aff.GetCode()}); errkey.Of(err) != "affiliate.customer_not_found" {
			t.Errorf("unknown customer: %v", err)
		}
	})

	t.Run("a new customer arrives with a code, and a referred customer gets the coupon", func(t *testing.T) {
		coupon := uniq("REFERRED")
		if _, err := e.plans.CreateCoupon(ctx, &plansv1.CreateCouponRequest{Coupon: &plansv1.Coupon{Code: coupon, PercentBps: 2000, Duration: plansv1.CouponDuration_COUPON_DURATION_ONCE}}); err != nil {
			t.Fatal(err)
		}
		activeProgram(t, e, ctx, func(p *plansv1.AffiliateProgram) { p.ReferredCouponCode = coupon })
		aff := newAffiliate(t, e, ctx, uniq("rc")+"@partners.test")
		ext := uniq("cus_new")
		if _, err := e.plans.CreateCustomer(ctx, &plansv1.CreateCustomerRequest{ExternalId: ext, Name: "New", Email: ext + "@buyers.test", ReferralCode: aff.GetCode()}); err != nil {
			t.Fatal(err)
		}
		refs, _ := e.aff.ListAffiliateReferrals(ctx, &plansv1.ListAffiliateReferralsRequest{AffiliateId: aff.GetId()})
		if refs.GetTotal() != 1 || refs.GetReferrals()[0].GetSource() != "api" {
			t.Fatalf("referrals: %v", refs)
		}
		var waiting int
		if err := pgxx.InTenantTx(context.Background(), e.app, uuid.MustParse(live.GetId()), func(tx pgx.Tx) error {
			return tx.QueryRow(context.Background(), `SELECT count(*) FROM customer_coupons cc JOIN customers c ON c.tenant_id = cc.tenant_id AND c.id = cc.customer_id
				WHERE c.external_id = $1 AND cc.source = 'affiliate' AND cc.source_ref = $2 AND cc.consumed_at IS NULL`, ext, aff.GetId()).Scan(&waiting)
		}); err != nil || waiting != 1 {
			t.Fatalf("the referred customer's coupon: %d (%v)", waiting, err)
		}
		// A code that cannot attribute does not fail the sign-up.
		bad := uniq("cus_bad")
		if _, err := e.plans.CreateCustomer(ctx, &plansv1.CreateCustomerRequest{ExternalId: bad, Name: "Bad", Email: bad + "@buyers.test", ReferralCode: "NOSUCHCODE"}); err != nil {
			t.Fatalf("an unknown referral code failed the customer: %v", err)
		}
		activeProgram(t, e, ctx, nil)
	})

	t.Run("a promotion code that belongs to an affiliate attributes whoever redeems it", func(t *testing.T) {
		activeProgram(t, e, ctx, nil)
		cp := uniq("PROMO")
		if _, err := e.plans.CreateCoupon(ctx, &plansv1.CreateCouponRequest{Coupon: &plansv1.Coupon{Code: cp, PercentBps: 1000, Duration: plansv1.CouponDuration_COUPON_DURATION_ONCE, WithoutDefaultCode: true}}); err != nil {
			t.Fatal(err)
		}
		text := uniq("ANNPROMO")
		if _, err := e.plans.CreatePromotionCodes(ctx, &plansv1.CreatePromotionCodesRequest{CouponCode: cp, Codes: []string{text}}); err != nil {
			t.Fatal(err)
		}
		aff := newAffiliate(t, e, ctx, uniq("pc")+"@partners.test")
		if _, err := e.aff.LinkAffiliatePromotionCode(ctx, &plansv1.LinkAffiliatePromotionCodeRequest{AffiliateId: aff.GetId(), PromotionCode: strings.ToLower(text)}); err != nil {
			t.Fatal(err)
		}
		if _, err := e.aff.LinkAffiliatePromotionCode(ctx, &plansv1.LinkAffiliatePromotionCodeRequest{AffiliateId: aff.GetId(), PromotionCode: "NOSUCH"}); errkey.Of(err) != "affiliate.promotion_code_not_found" {
			t.Errorf("linking a code that does not exist: %v", err)
		}
		ext := uniq("cus_pc")
		newCustomer(t, e, ctx, ext, ext+"@buyers.test", "")
		// A preview claims nothing and attributes nothing.
		if _, err := e.plans.RedeemPromotion(ctx, &plansv1.RedeemPromotionRequest{Code: text, CustomerExternalId: ext, Target: plansv1.CouponTarget_COUPON_TARGET_PAYMENT, TargetId: uuid.NewString(),
			AmountMinor: 10000, Currency: "TRY", Preview: true}); err != nil {
			t.Fatal(err)
		}
		if refs, _ := e.aff.ListAffiliateReferrals(ctx, &plansv1.ListAffiliateReferralsRequest{AffiliateId: aff.GetId()}); refs.GetTotal() != 0 {
			t.Fatalf("a preview attributed: %v", refs)
		}
		id := uuid.NewString()
		if _, err := e.plans.RedeemPromotion(ctx, &plansv1.RedeemPromotionRequest{Code: text, CustomerExternalId: ext, Target: plansv1.CouponTarget_COUPON_TARGET_PAYMENT, TargetId: id, InvoiceId: id,
			AmountMinor: 10000, Currency: "TRY"}); err != nil {
			t.Fatal(err)
		}
		refs, _ := e.aff.ListAffiliateReferrals(ctx, &plansv1.ListAffiliateReferralsRequest{AffiliateId: aff.GetId()})
		if refs.GetTotal() != 1 || refs.GetReferrals()[0].GetSource() != "code" {
			t.Fatalf("referrals after the redemption: %v", refs)
		}
	})
}

// testAffiliateRole: a member with the role affiliate sees nothing of the account it belongs to, and the account's owners cannot make anyone of it.
func testAffiliateRole(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	olga := e.verified(t, strings.ToLower(uniq("olga"))+"@aff-role.example", "Olga Co")
	pat := e.verified(t, strings.ToLower(uniq("pat"))+"@aff-role.example", "Pat Co")
	app, err := e.ident.CreateMyApp(ctx, &plansv1.CreateMyAppRequest{UserId: olga.id, AccountId: olga.account, Name: "Shop", Currency: "EUR", Timezone: "Europe/Berlin"})
	if err != nil {
		t.Fatal(err)
	}
	tenantID := app.GetApp().GetTenants()[0].GetId()
	if _, err := e.system.Exec(ctx, `INSERT INTO account_members (account_id, user_id, role) VALUES ($1::uuid, $2::uuid, 'affiliate')`, olga.account, pat.id); err != nil {
		t.Fatalf("the role is not allowed: %v", err)
	}
	// Everything account-wide is "not found" for the partner, the same answer a stranger gets.
	_, err = e.ident.GetMyAccount(ctx, &plansv1.GetMyAccountRequest{UserId: pat.id, AccountId: olga.account})
	wantCode(t, "account", err, codes.NotFound, "identity.account.not_found")
	_, err = e.ident.ListMyApps(ctx, &plansv1.ListMyAppsRequest{UserId: pat.id, AccountId: olga.account})
	wantCode(t, "apps", err, codes.NotFound, "identity.account.not_found")
	_, err = e.ident.GetMyApp(ctx, &plansv1.GetMyAppRequest{UserId: pat.id, AppId: app.GetApp().GetId()})
	wantCode(t, "app", err, codes.NotFound, "identity.app.not_found")
	_, err = e.ident.ResolveMyTenant(ctx, &plansv1.ResolveMyTenantRequest{UserId: pat.id, TenantId: tenantID})
	wantCode(t, "tenant", err, codes.NotFound, "identity.tenant.not_found")
	_, err = e.ident.ListMyApiKeys(ctx, &plansv1.ListMyApiKeysRequest{UserId: pat.id, AppId: app.GetApp().GetId()})
	wantCode(t, "keys", err, codes.NotFound, "identity.app.not_found")
	_, err = e.ident.CreateMyApp(ctx, &plansv1.CreateMyAppRequest{UserId: pat.id, AccountId: olga.account, Name: "Mine", Currency: "EUR", Timezone: "Europe/Berlin"})
	if err == nil {
		t.Error("a partner created an app in the account")
	}
	// The owner does not see the partner as a member, cannot invite one, and cannot change or remove one through the member calls.
	acct, err := e.ident.GetMyAccount(ctx, &plansv1.GetMyAccountRequest{UserId: olga.id, AccountId: olga.account})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range acct.GetMembers() {
		if m.GetRole() == "affiliate" || m.GetUserId() == pat.id {
			t.Errorf("the partner is listed as a member: %v", m)
		}
	}
	_, err = e.invite(olga, olga.account, "someone@aff-role.example", "affiliate")
	wantCode(t, "inviting an affiliate", err, codes.InvalidArgument, "")
	_, err = e.ident.UpdateMemberRole(ctx, &plansv1.UpdateMemberRoleRequest{UserId: olga.id, AccountId: olga.account, MemberUserId: pat.id, Role: "viewer"})
	wantCode(t, "promoting a partner", err, codes.PermissionDenied, "")
	_, err = e.ident.RemoveMember(ctx, &plansv1.RemoveMemberRequest{UserId: olga.id, AccountId: olga.account, MemberUserId: pat.id})
	wantCode(t, "removing a partner as a member", err, codes.PermissionDenied, "")
}

// seedAffiliates gives the tenant a row in every table of the affiliate program, through the API: a program, an affiliate who is also the seed customer (so the
// attribution is refused and a rejection is kept), another who brought a customer, and a click. Parts of phase 47 that add tables extend it.
func seedAffiliates(t *testing.T, e *env, tenantID, suffix string) {
	t.Helper()
	ctx := as(tenantID)
	must := func(_ any, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(e.aff.SetAffiliateProgram(ctx, &plansv1.SetAffiliateProgramRequest{Program: &plansv1.AffiliateProgram{Status: "active", CommissionKind: "percent_bps", CommissionValue: 1000,
		AttributionWindowDays: 30, HoldDays: 0}}))
	self, err := e.aff.CreateAffiliate(ctx, &plansv1.CreateAffiliateRequest{Email: "ada@example.com", Code: "SEEDSELF" + strings.ToUpper(suffix)})
	if err != nil {
		t.Fatal(err)
	}
	must(e.aff.AttributeCustomer(ctx, &plansv1.AttributeCustomerRequest{CustomerExternalId: "cus_" + suffix, Code: self.GetAffiliate().GetCode()}))
	partner, err := e.aff.CreateAffiliate(ctx, &plansv1.CreateAffiliateRequest{Email: "bo@partners.test", Code: "SEEDPART" + strings.ToUpper(suffix)})
	if err != nil {
		t.Fatal(err)
	}
	kim, err := e.plans.CreateCustomer(ctx, &plansv1.CreateCustomerRequest{ExternalId: "cus_ref_" + suffix, Name: "Kim", Email: "kim@buyers.test"})
	if err != nil {
		t.Fatal(err)
	}
	must(e.aff.AttributeCustomer(ctx, &plansv1.AttributeCustomerRequest{CustomerExternalId: "cus_ref_" + suffix, Code: partner.GetAffiliate().GetCode(), Source: "link"}))
	// A paid invoice of the customer earns a commission, and a credit note against it is recorded as a reversal.
	invoice := uuid.NewString()
	for _, ev := range []struct{ typ, data string }{
		{"invoice.paid", `{"invoice_id":"` + invoice + `","customer_id":"` + kim.GetCustomer().GetId() + `","currency":"TRY","total_minor":"10000","tax_minor":"0","net_minor":"10000"}`},
		{"credit_note.created", `{"credit_note_id":"cn_seed_` + suffix + `","invoice_id":"` + invoice + `","total_minor":"2500"}`},
	} {
		must(e.hooks.RecordTenantEvent(ctx, &plansv1.RecordTenantEventRequest{EventId: uuid.NewString(), Type: ev.typ, DataJson: ev.data}))
	}
	// Paid out: a payout and its postings.
	payouts, err := e.aff.CreateAffiliatePayouts(ctx, &plansv1.CreateAffiliatePayoutsRequest{AffiliateId: partner.GetAffiliate().GetId()})
	if err != nil || len(payouts.GetPayouts()) != 1 {
		t.Fatalf("seed payout: %v %v", payouts, err)
	}
	must(e.aff.MarkAffiliatePayoutPaid(ctx, &plansv1.MarkAffiliatePayoutPaidRequest{Id: payouts.GetPayouts()[0].GetId(), Reference: "seed"}))
	must(e.pub.TrackAffiliateClick(context.Background(), &plansv1.TrackAffiliateClickRequest{Code: partner.GetAffiliate().GetCode()}))
}

func equalBools(a, b []bool) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
