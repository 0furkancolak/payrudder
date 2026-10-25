//go:build integration

package itest

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
)

// testOwnerGoLiveSteps covers what an owner does alone on the way to going live: the seller's billing profile and the terms of service. Only the owner may,
// the profile is checked and stored on the live tenant only, the checklist follows, and every change is in the audit trail without the values.
func testOwnerGoLiveSteps(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	owner := e.verified(t, "steps-owner@example.com", "Steps Co")
	admin := e.signUp(t, "steps-admin@example.com", "Admin Steps Co")
	if _, err := e.system.Exec(ctx, `INSERT INTO account_members (account_id, user_id, role) VALUES ($1::uuid, $2::uuid, 'admin')`, owner.account, admin.GetUser().GetId()); err != nil {
		t.Fatal(err)
	}
	created, err := e.ident.CreateMyApp(ctx, &plansv1.CreateMyAppRequest{UserId: owner.id, AccountId: owner.account, Name: "Steps", Currency: "EUR", Timezone: "Europe/Berlin"})
	if err != nil {
		t.Fatal(err)
	}
	app := created.GetApp()
	live, sandbox := envOf(app, plansv1.Environment_ENVIRONMENT_LIVE), envOf(app, plansv1.Environment_ENVIRONMENT_SANDBOX)
	save := func(uid string, addr *plansv1.Address, legal *plansv1.LegalInfo) (*plansv1.UpdateMyBillingProfileResponse, error) {
		return e.ident.UpdateMyBillingProfile(ctx, &plansv1.UpdateMyBillingProfileRequest{UserId: uid, AppId: app.GetId(), Address: addr, Legal: legal})
	}
	de := &plansv1.Address{Line1: "Hauptstr. 1", City: "Berlin", PostalCode: "10115", Country: "DE"}
	auditRows := func() []string {
		t.Helper()
		rows, err := e.system.Query(ctx, `SELECT action || ' ' || detail::text FROM go_live_audit WHERE app_id = $1::uuid ORDER BY created_at, id`, app.GetId())
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				t.Fatal(err)
			}
			out = append(out, s)
		}
		return out
	}

	t.Run("only the owner changes the profile", func(t *testing.T) {
		_, err := save(admin.GetUser().GetId(), de, &plansv1.LegalInfo{LegalName: "Shop GmbH", TaxId: "DE123456789"})
		wantCode(t, "admin", err, codes.PermissionDenied, "identity.go_live.owner_only")
		stranger := e.verified(t, "steps-stranger@example.com", "Stranger Steps Co")
		_, err = save(stranger.id, de, &plansv1.LegalInfo{LegalName: "Shop GmbH", TaxId: "DE123456789"})
		wantCode(t, "stranger", err, codes.NotFound, "identity.app.not_found")
		if rows := auditRows(); len(rows) != 0 {
			t.Errorf("a refused change was audited: %v", rows)
		}
	})

	t.Run("each missing part has its own key", func(t *testing.T) {
		for name, tc := range map[string]struct {
			addr  *plansv1.Address
			legal *plansv1.LegalInfo
			key   string
		}{
			"no legal name":                {de, &plansv1.LegalInfo{LegalName: "  ", TaxId: "DE123456789"}, "identity.billing_profile.legal_name_required"},
			"no country":                   {&plansv1.Address{Line1: "x", City: "y"}, &plansv1.LegalInfo{LegalName: "Shop"}, "identity.billing_profile.country_required"},
			"no street":                    {&plansv1.Address{City: "Berlin", Country: "DE"}, &plansv1.LegalInfo{LegalName: "Shop", TaxId: "DE123456789"}, "identity.billing_profile.address_required"},
			"no city":                      {&plansv1.Address{Line1: "Hauptstr. 1", Country: "DE"}, &plansv1.LegalInfo{LegalName: "Shop", TaxId: "DE123456789"}, "identity.billing_profile.address_required"},
			"EU seller without a tax id":   {de, &plansv1.LegalInfo{LegalName: "Shop GmbH"}, "identity.billing_profile.tax_id_required"},
			"Turkish seller without an id": {&plansv1.Address{Line1: "Bagdat Cd. 1", City: "Istanbul", Country: "tr"}, &plansv1.LegalInfo{LegalName: "Shop AS"}, "identity.billing_profile.tax_id_required"},
		} {
			_, err := save(owner.id, tc.addr, tc.legal)
			wantCode(t, name, err, codes.InvalidArgument, tc.key)
		}
		_, err := save(owner.id, de, &plansv1.LegalInfo{LegalName: "Shop GmbH", TaxId: "DE-12!"})
		wantCode(t, "a tax id that is not an id", err, codes.InvalidArgument, "plans.customer.tax_invalid")
		_, err = save(owner.id, &plansv1.Address{Line1: "x", City: "y", Country: "Germany"}, &plansv1.LegalInfo{LegalName: "Shop GmbH", TaxId: "DE123456789"})
		wantCode(t, "a country that is not a code", err, codes.InvalidArgument, "")
	})

	t.Run("a complete profile is stored on the live tenant, normalised, and completes the checklist item", func(t *testing.T) {
		before, _ := e.ident.GetMyGoLive(ctx, &plansv1.GetMyGoLiveRequest{UserId: owner.id, AppId: app.GetId()})
		if itemDone(before.GetGoLive(), "legal_profile") {
			t.Fatal("the item was done before anything was saved")
		}
		out, err := save(owner.id, &plansv1.Address{Line1: " Hauptstr. 1 ", City: "Berlin", PostalCode: "10115", Country: "de"}, &plansv1.LegalInfo{LegalName: " Shop GmbH ", TaxId: "de 123-456-789"})
		if err != nil {
			t.Fatal(err)
		}
		tn := out.GetTenant()
		if tn.GetId() != live.GetId() || tn.GetLegal().GetLegalName() != "Shop GmbH" || tn.GetLegal().GetTaxId() != "DE123456789" || tn.GetAddress().GetCountry() != "DE" || tn.GetAddress().GetLine1() != "Hauptstr. 1" {
			t.Fatalf("saved = %v", tn)
		}
		after, _ := e.ident.GetMyGoLive(ctx, &plansv1.GetMyGoLiveRequest{UserId: owner.id, AppId: app.GetId()})
		if !itemDone(after.GetGoLive(), "legal_profile") {
			t.Errorf("the checklist does not see the profile: %v", after.GetGoLive().GetItems())
		}
		// The sandbox's seller data is not the live environment's.
		got, _ := e.ident.GetMyApp(ctx, &plansv1.GetMyAppRequest{UserId: owner.id, AppId: app.GetId()})
		if sb := envOf(got.GetApp(), plansv1.Environment_ENVIRONMENT_SANDBOX); sb.GetId() != sandbox.GetId() || sb.GetLegal().GetLegalName() != "" {
			t.Errorf("the sandbox tenant was changed: %v", sb)
		}
		rows := auditRows()
		if len(rows) != 1 || !strings.HasPrefix(rows[0], "billing_profile_saved") || !strings.Contains(rows[0], `"country": "DE"`) || strings.Contains(rows[0], "Shop") || strings.Contains(rows[0], "123456789") {
			t.Errorf("audit = %v; want one row with the country and no values", rows)
		}
	})

	t.Run("the terms are accepted by the owner alone, once", func(t *testing.T) {
		_, err := e.ident.AcceptMyTerms(ctx, &plansv1.AcceptMyTermsRequest{UserId: admin.GetUser().GetId(), AppId: app.GetId()})
		wantCode(t, "admin", err, codes.PermissionDenied, "identity.go_live.owner_only")
		first, err := e.ident.AcceptMyTerms(ctx, &plansv1.AcceptMyTermsRequest{UserId: owner.id, AppId: app.GetId()})
		if err != nil || first.GetAcceptTime() == nil {
			t.Fatalf("accept: %v, %v", first, err)
		}
		again, err := e.ident.AcceptMyTerms(ctx, &plansv1.AcceptMyTermsRequest{UserId: owner.id, AppId: app.GetId()})
		if err != nil || !again.GetAcceptTime().AsTime().Equal(first.GetAcceptTime().AsTime()) {
			t.Errorf("a second acceptance moved the time: %v, %v", again, err)
		}
		g, _ := e.ident.GetMyGoLive(ctx, &plansv1.GetMyGoLiveRequest{UserId: owner.id, AppId: app.GetId()})
		if !itemDone(g.GetGoLive(), "terms_accepted") {
			t.Errorf("the checklist does not see the terms: %v", g.GetGoLive().GetItems())
		}
		var terms int
		for _, r := range auditRows() {
			if strings.HasPrefix(r, "terms_accepted") {
				terms++
			}
		}
		if terms != 1 {
			t.Errorf("terms_accepted audit rows = %d, want 1", terms)
		}
	})

	t.Run("the database says the audit is about a live tenant, and never rewrites it", func(t *testing.T) {
		if _, err := e.system.Exec(ctx, `INSERT INTO go_live_audit (id, app_id, live_tenant_id, user_id, action) VALUES (gen_random_uuid(), $1::uuid, $2::uuid, $3::uuid, 'terms_accepted')`,
			app.GetId(), sandbox.GetId(), owner.id); err == nil {
			t.Error("an audit row about a sandbox tenant was accepted")
		}
		if _, err := e.system.Exec(ctx, `UPDATE go_live_audit SET action = 'terms_accepted' WHERE app_id = $1::uuid`, app.GetId()); err == nil {
			t.Error("the audit trail was rewritten")
		}
		if _, err := e.system.Exec(ctx, `DELETE FROM go_live_audit WHERE app_id = $1::uuid`, app.GetId()); err == nil {
			t.Error("the audit trail was deleted from")
		}
	})
}
