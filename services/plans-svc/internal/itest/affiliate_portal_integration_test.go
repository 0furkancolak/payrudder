//go:build integration

package itest

import (
	"context"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
)

func testAffiliatePortal(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	lower := func(s string) string { return strings.ToLower(uniq(s)) }

	// A seller with a program, and a partner the seller made for an address that a person will sign up with.
	w := newAffiliateWorld(t, e, "AffPortal", noHold)
	ann := e.verified(t, lower("ann")+"@portal.example", "Ann Partners")
	made, err := e.aff.CreateAffiliate(w.ctx, &plansv1.CreateAffiliateRequest{Email: ann.email, Name: "Ann", PayoutMethod: "bank_transfer", PayoutDetails: "TR33 0006"})
	if err != nil {
		t.Fatal(err)
	}
	mine := made.GetAffiliate()
	bob := e.verified(t, lower("bob")+"@portal.example", "Bob Partners")

	t.Run("a person claims the records made for their verified address", func(t *testing.T) {
		if r, err := e.portal.ListMyAffiliations(ctx, &plansv1.ListMyAffiliationsRequest{UserId: ann.id}); err != nil || len(r.GetAffiliations()) != 0 {
			t.Fatalf("before claiming: %v %v", r, err)
		}
		// Someone who has not proven an address cannot claim what is made for it.
		stranger := e.signUp(t, lower("unproven")+"@portal.example", "Unproven Co")
		_, err := e.portal.ClaimMyAffiliations(ctx, &plansv1.ClaimMyAffiliationsRequest{UserId: stranger.GetUser().GetId()})
		wantCode(t, "an unverified person", err, codes.PermissionDenied, "identity.email.not_verified")
		// Another verified person gets nothing of Ann's.
		if r, err := e.portal.ClaimMyAffiliations(ctx, &plansv1.ClaimMyAffiliationsRequest{UserId: bob.id}); err != nil || r.GetClaimed() != 0 || len(r.GetAffiliations()) != 0 {
			t.Fatalf("bob claimed: %v %v", r, err)
		}
		r, err := e.portal.ClaimMyAffiliations(ctx, &plansv1.ClaimMyAffiliationsRequest{UserId: ann.id})
		if err != nil || r.GetClaimed() != 1 || len(r.GetAffiliations()) != 1 {
			t.Fatalf("claim: %v %v", r, err)
		}
		a := r.GetAffiliations()[0]
		if a.GetId() != mine.GetId() || a.GetCode() != mine.GetCode() || a.GetStatus() != "active" || a.GetEnvironment() != "live" || a.GetTenantId() != w.tenantID || a.GetPayoutDetails() != "TR33 0006" ||
			a.GetProgram().GetCommissionValue() != 1000 || a.GetProgram().GetStatus() != "active" || a.GetProgram().GetAttributionWindowDays() != 30 {
			t.Fatalf("affiliation = %v", a)
		}
		if again, err := e.portal.ClaimMyAffiliations(ctx, &plansv1.ClaimMyAffiliationsRequest{UserId: ann.id}); err != nil || again.GetClaimed() != 0 || len(again.GetAffiliations()) != 1 {
			t.Fatalf("claiming again: %v %v", again, err)
		}
		// The owner sees the partner as signed in, and the partner is a member of the account only with the role that sees none of it.
		got, _ := e.aff.GetAffiliate(w.ctx, &plansv1.GetAffiliateRequest{Id: mine.GetId()})
		if !got.GetAffiliate().GetSignedIn() {
			t.Error("the owner does not see the partner as signed in")
		}
		var role string
		if err := e.system.QueryRow(ctx, `SELECT m.role FROM account_members m JOIN tenants t ON t.account_id = m.account_id WHERE t.id = $1::uuid AND m.user_id = $2::uuid`, w.tenantID, ann.id).Scan(&role); err != nil || role != "affiliate" {
			t.Errorf("membership = %q (%v)", role, err)
		}
		if _, err := e.ident.GetMyApp(ctx, &plansv1.GetMyAppRequest{UserId: ann.id, AppId: "00000000-0000-0000-0000-000000000000"}); err == nil {
			t.Error("a partner reads an app")
		}
	})

	t.Run("a partner sees their own figures, with the customers masked", func(t *testing.T) {
		cust := w.customerWith(t, "Jane.Doe@buyers.test", mine.GetCode())
		inv := w.newInvoice(cust, 20000, 0) // 2000 commission
		w.paid(t, inv, time.Now(), "")
		other := w.customerWith(t, "kim@buyers.test", mine.GetCode())
		inv2 := w.newInvoice(other, 10000, 0) // 1000
		w.paid(t, inv2, time.Now(), "")
		if _, err := e.pub.TrackAffiliateClick(ctx, &plansv1.TrackAffiliateClickRequest{Code: mine.GetCode()}); err != nil {
			t.Fatal(err)
		}
		w.creditNote(t, inv2, "cn_portal", 5000, time.Now()) // 500 taken back
		p := w.createPayoutFor(t, mine.GetId())
		w.pay(t, p, "ref-1")
		inv3 := w.newInvoice(cust, 40000, 0) // 4000, approved, not yet paid out
		w.paid(t, inv3, time.Now(), "")

		g, err := e.portal.GetMyAffiliation(ctx, &plansv1.GetMyAffiliationRequest{UserId: ann.id, AffiliateId: mine.GetId()})
		if err != nil {
			t.Fatal(err)
		}
		if g.GetClicks() != 1 || g.GetReferrals() != 2 || g.GetPayingCustomers() != 2 || len(g.GetEarned()) != 1 || g.GetEarned()[0].GetEarnedMinor() != 7000 || g.GetEarned()[0].GetReversedMinor() != 500 {
			t.Fatalf("figures = %v", g)
		}
		if len(g.GetBalances()) != 1 || g.GetBalances()[0].GetPaidMinor() != 2500 || g.GetBalances()[0].GetApprovedMinor() != 4000 || g.GetBalances()[0].GetPendingMinor() != 0 {
			t.Fatalf("balances = %v", g.GetBalances())
		}
		refs, err := e.portal.ListMyReferrals(ctx, &plansv1.ListMyReferralsRequest{UserId: ann.id, AffiliateId: mine.GetId()})
		if err != nil || refs.GetTotal() != 2 {
			t.Fatalf("referrals: %v %v", refs, err)
		}
		masked := map[string]bool{}
		for _, r := range refs.GetReferrals() {
			masked[r.GetCustomerEmailMasked()] = true
		}
		if !masked["J***@buyers.test"] && !masked["j***@buyers.test"] || !masked["k***@buyers.test"] {
			t.Errorf("masked referrals = %v", masked)
		}
		cs, err := e.portal.ListMyCommissions(ctx, &plansv1.ListMyCommissionsRequest{UserId: ann.id, AffiliateId: mine.GetId()})
		if err != nil || cs.GetTotal() != 3 {
			t.Fatalf("commissions: %v %v", cs, err)
		}
		for _, c := range cs.GetCommissions() {
			if strings.Contains(c.GetCustomerEmailMasked(), "Doe") || strings.Contains(c.GetCustomerEmailMasked(), "kim@") || !strings.Contains(c.GetCustomerEmailMasked(), "***@buyers.test") {
				t.Errorf("an unmasked customer: %v", c)
			}
		}
		approved, _ := e.portal.ListMyCommissions(ctx, &plansv1.ListMyCommissionsRequest{UserId: ann.id, AffiliateId: mine.GetId(), Status: "approved"})
		if approved.GetTotal() != 1 || approved.GetCommissions()[0].GetAmountMinor() != 4000 {
			t.Errorf("approved: %v", approved)
		}
		ps, err := e.portal.ListMyPayouts(ctx, &plansv1.ListMyPayoutsRequest{UserId: ann.id, AffiliateId: mine.GetId()})
		if err != nil || ps.GetTotal() != 1 || ps.GetPayouts()[0].GetTotalMinor() != 2500 || ps.GetPayouts()[0].GetStatus() != "paid" || ps.GetPayouts()[0].GetReference() != "ref-1" || ps.GetPayouts()[0].GetFake() {
			t.Fatalf("payouts: %v %v", ps, err)
		}
		if _, err := e.portal.ListMyCommissions(ctx, &plansv1.ListMyCommissionsRequest{UserId: ann.id, AffiliateId: mine.GetId(), Status: "bogus"}); code(t, err) != codes.InvalidArgument {
			t.Errorf("a bad status: %v", err)
		}
	})

	t.Run("nobody else's data is reachable", func(t *testing.T) {
		for name, call := range map[string]func() error{
			"get": func() error {
				_, err := e.portal.GetMyAffiliation(ctx, &plansv1.GetMyAffiliationRequest{UserId: bob.id, AffiliateId: mine.GetId()})
				return err
			},
			"referrals": func() error {
				_, err := e.portal.ListMyReferrals(ctx, &plansv1.ListMyReferralsRequest{UserId: bob.id, AffiliateId: mine.GetId()})
				return err
			},
			"commissions": func() error {
				_, err := e.portal.ListMyCommissions(ctx, &plansv1.ListMyCommissionsRequest{UserId: bob.id, AffiliateId: mine.GetId()})
				return err
			},
			"payouts": func() error {
				_, err := e.portal.ListMyPayouts(ctx, &plansv1.ListMyPayoutsRequest{UserId: bob.id, AffiliateId: mine.GetId()})
				return err
			},
			"update": func() error {
				_, err := e.portal.UpdateMyAffiliation(ctx, &plansv1.UpdateMyAffiliationRequest{UserId: bob.id, AffiliateId: mine.GetId(), PayoutMethod: "manual", PayoutDetails: "mine now"})
				return err
			},
			"malformed ids": func() error {
				_, err := e.portal.GetMyAffiliation(ctx, &plansv1.GetMyAffiliationRequest{UserId: "x", AffiliateId: "y"})
				return err
			},
			"no user": func() error {
				_, err := e.portal.GetMyAffiliation(ctx, &plansv1.GetMyAffiliationRequest{AffiliateId: mine.GetId()})
				return err
			},
		} {
			if err := call(); errkey.Of(err) != "affiliate.not_found" || code(t, err) != codes.NotFound {
				t.Errorf("%s: %v (%q)", name, err, errkey.Of(err))
			}
		}
		got, _ := e.aff.GetAffiliate(w.ctx, &plansv1.GetAffiliateRequest{Id: mine.GetId()})
		if got.GetAffiliate().GetPayoutDetails() != "TR33 0006" {
			t.Errorf("another person changed the payout details: %v", got)
		}
	})

	t.Run("a partner changes their payout details, and keeps seeing their data while suspended", func(t *testing.T) {
		u, err := e.portal.UpdateMyAffiliation(ctx, &plansv1.UpdateMyAffiliationRequest{UserId: ann.id, AffiliateId: mine.GetId(), Name: "Ann P.", PayoutMethod: "manual", PayoutDetails: "cash on delivery"})
		if err != nil || u.GetAffiliation().GetPayoutDetails() != "cash on delivery" || u.GetAffiliation().GetName() != "Ann P." {
			t.Fatalf("update: %v %v", u, err)
		}
		if _, err := e.portal.UpdateMyAffiliation(ctx, &plansv1.UpdateMyAffiliationRequest{UserId: ann.id, AffiliateId: mine.GetId(), PayoutMethod: "crypto"}); code(t, err) != codes.InvalidArgument {
			t.Errorf("a bad method: %v", err)
		}
		if _, err := e.aff.UpdateAffiliate(w.ctx, &plansv1.UpdateAffiliateRequest{Id: mine.GetId(), Status: "suspended"}); err != nil {
			t.Fatal(err)
		}
		g, err := e.portal.GetMyAffiliation(ctx, &plansv1.GetMyAffiliationRequest{UserId: ann.id, AffiliateId: mine.GetId()})
		if err != nil || g.GetAffiliation().GetStatus() != "suspended" || len(g.GetBalances()) != 1 {
			t.Fatalf("suspended: %v %v", g, err)
		}
		if _, err := e.aff.UpdateAffiliate(w.ctx, &plansv1.UpdateAffiliateRequest{Id: mine.GetId(), Status: "active"}); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("applying: terms, approval, and nothing for a seller without an open program", func(t *testing.T) {
		look := func(who person, tenant string) (*plansv1.LookupAffiliateProgramResponse, error) {
			return e.portal.LookupAffiliateProgram(ctx, &plansv1.LookupAffiliateProgramRequest{UserId: who.id, TenantId: tenant})
		}
		terms, err := look(bob, w.tenantID)
		if err != nil || terms.GetProgram().GetCommissionKind() != "percent_bps" || terms.GetProgram().GetCommissionValue() != 1000 || terms.GetApplied() || terms.GetEnvironment() != "live" || terms.GetSellerName() == "" {
			t.Fatalf("terms: %v %v", terms, err)
		}
		// A tenant that does not exist and one without an open program are the same answer.
		for name, tenant := range map[string]string{"unknown": "11111111-1111-1111-1111-111111111111", "malformed": "nope"} {
			if _, err := look(bob, tenant); errkey.Of(err) != "affiliate.portal.program_unavailable" {
				t.Errorf("%s: %v", name, err)
			}
		}
		noProgram := e.account(t, "AffPortalNoProgram")[plansv1.Environment_ENVIRONMENT_LIVE]
		if _, err := look(bob, noProgram.GetId()); errkey.Of(err) != "affiliate.portal.program_unavailable" {
			t.Errorf("no program: %v", err)
		}
		activeProgram(t, e, w.ctx, func(p *plansv1.AffiliateProgram) { p.Status = "paused"; p.HoldDays = 0 })
		if _, err := look(bob, w.tenantID); errkey.Of(err) != "affiliate.portal.program_unavailable" {
			t.Errorf("a paused program: %v", err)
		}
		_, err = e.portal.ApplyAsAffiliate(ctx, &plansv1.ApplyAsAffiliateRequest{UserId: bob.id, TenantId: w.tenantID})
		if errkey.Of(err) != "affiliate.portal.program_unavailable" {
			t.Errorf("applying to a paused program: %v", err)
		}
		activeProgram(t, e, w.ctx, noHold)

		unproven := e.signUp(t, lower("unproven2")+"@portal.example", "Unproven Two")
		_, err = e.portal.ApplyAsAffiliate(ctx, &plansv1.ApplyAsAffiliateRequest{UserId: unproven.GetUser().GetId(), TenantId: w.tenantID})
		wantCode(t, "an unverified applicant", err, codes.PermissionDenied, "identity.email.not_verified")
		_, err = e.portal.ApplyAsAffiliate(ctx, &plansv1.ApplyAsAffiliateRequest{UserId: bob.id, TenantId: w.tenantID, PayoutMethod: "crypto"})
		wantCode(t, "a bad payout method", err, codes.InvalidArgument, "")

		r, err := e.portal.ApplyAsAffiliate(ctx, &plansv1.ApplyAsAffiliateRequest{UserId: bob.id, TenantId: w.tenantID, Name: "Bob", PayoutMethod: "bank_transfer", PayoutDetails: "DE00"})
		if err != nil {
			t.Fatal(err)
		}
		a := r.GetAffiliation()
		if a.GetStatus() != "pending" || a.GetEmail() != bob.email || len(a.GetCode()) != 8 || a.GetTenantId() != w.tenantID {
			t.Fatalf("application = %v", a)
		}
		if again, err := look(bob, w.tenantID); err != nil || !again.GetApplied() {
			t.Errorf("applied flag: %v %v", again, err)
		}
		_, err = e.portal.ApplyAsAffiliate(ctx, &plansv1.ApplyAsAffiliateRequest{UserId: bob.id, TenantId: w.tenantID})
		wantCode(t, "applying twice", err, codes.AlreadyExists, "affiliate.portal.already_applied")

		// The owner sees the application, as pending and from an application, and approves it; a pending partner earns nothing until then.
		list, err := e.aff.ListAffiliates(w.ctx, &plansv1.ListAffiliatesRequest{Status: "pending"})
		if err != nil || list.GetTotal() != 1 || list.GetAffiliates()[0].GetSource() != "application" || !list.GetAffiliates()[0].GetSignedIn() {
			t.Fatalf("pending list: %v %v", list, err)
		}
		cust := w.customerWith(t, "lee@buyers.test", a.GetCode())
		if refs, _ := e.aff.ListAffiliateReferrals(w.ctx, &plansv1.ListAffiliateReferralsRequest{AffiliateId: a.GetId()}); refs.GetTotal() != 0 {
			t.Errorf("a pending partner brought %v", refs)
		}
		_ = cust
		if _, err := e.aff.UpdateAffiliate(w.ctx, &plansv1.UpdateAffiliateRequest{Id: a.GetId(), Status: "active"}); err != nil {
			t.Fatal(err)
		}
		seen, err := e.portal.GetMyAffiliation(ctx, &plansv1.GetMyAffiliationRequest{UserId: bob.id, AffiliateId: a.GetId()})
		if err != nil || seen.GetAffiliation().GetStatus() != "active" {
			t.Fatalf("after approval: %v %v", seen, err)
		}

		// A program that approves at once.
		activeProgram(t, e, w.ctx, func(p *plansv1.AffiliateProgram) { p.AutoApprove = true; p.HoldDays = 0 })
		cy := e.verified(t, lower("cy")+"@portal.example", "Cy Partners")
		auto, err := e.portal.ApplyAsAffiliate(ctx, &plansv1.ApplyAsAffiliateRequest{UserId: cy.id, TenantId: w.tenantID})
		if err != nil || auto.GetAffiliation().GetStatus() != "active" {
			t.Fatalf("auto approval: %v %v", auto, err)
		}
	})

	t.Run("a sandbox partner is marked, and its payouts are fake", func(t *testing.T) {
		sb := newAffiliateWorldIn(t, e, "AffPortalSandbox", plansv1.Environment_ENVIRONMENT_SANDBOX, noHold)
		dee := e.verified(t, lower("dee")+"@portal.example", "Dee Partners")
		made, err := e.aff.CreateAffiliate(sb.ctx, &plansv1.CreateAffiliateRequest{Email: dee.email})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.portal.ClaimMyAffiliations(ctx, &plansv1.ClaimMyAffiliationsRequest{UserId: dee.id}); err != nil {
			t.Fatal(err)
		}
		cust := sb.customerWith(t, "sam@buyers.test", made.GetAffiliate().GetCode())
		sb.paid(t, sb.newInvoice(cust, 10000, 0), time.Now(), "")
		sb.pay(t, sb.createPayoutFor(t, made.GetAffiliate().GetId()), "")
		as, _ := e.portal.ListMyAffiliations(ctx, &plansv1.ListMyAffiliationsRequest{UserId: dee.id})
		if len(as.GetAffiliations()) != 1 || as.GetAffiliations()[0].GetEnvironment() != "sandbox" {
			t.Fatalf("affiliations = %v", as)
		}
		ps, _ := e.portal.ListMyPayouts(ctx, &plansv1.ListMyPayoutsRequest{UserId: dee.id, AffiliateId: made.GetAffiliate().GetId()})
		if ps.GetTotal() != 1 || !ps.GetPayouts()[0].GetFake() {
			t.Fatalf("payouts = %v", ps)
		}
	})
}

// customerWith creates a customer with this address that the partner with the code brought.
func (w *affiliateWorld) customerWith(t *testing.T, email, code string) *plansv1.Customer {
	t.Helper()
	ext := uniq("cus_")
	r, err := w.e.plans.CreateCustomer(w.ctx, &plansv1.CreateCustomerRequest{ExternalId: ext, Name: ext, Email: email, ReferralCode: code})
	if err != nil {
		t.Fatal(err)
	}
	return r.GetCustomer()
}

// createPayoutFor batches one partner's approved commissions and returns the payout's id.
func (w *affiliateWorld) createPayoutFor(t *testing.T, affiliateID string) string {
	t.Helper()
	r, err := w.e.aff.CreateAffiliatePayouts(w.ctx, &plansv1.CreateAffiliatePayoutsRequest{AffiliateId: affiliateID})
	if err != nil || len(r.GetPayouts()) != 1 {
		t.Fatalf("CreateAffiliatePayouts: %v %v", r, err)
	}
	return r.GetPayouts()[0].GetId()
}
