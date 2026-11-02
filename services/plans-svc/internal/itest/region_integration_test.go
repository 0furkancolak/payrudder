//go:build integration

package itest

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/durationpb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
)

// An app is pinned to a region when it is created (plan 17): the one the request names, else the deployment's default (PAYRUDDER_DEFAULT_REGION), else none; both of its tenants
// get it, it is in what the API answers and in tenants.region (which the gateway's region guard reads through the directory), and a name the table would refuse is a 400.
func testAppRegion(t *testing.T) {
	defaultRegionForTests = "eu"
	t.Cleanup(func() { defaultRegionForTests = "" })
	e := newEnv(t)
	ctx := context.Background()
	ada := e.signUp(t, "region@example.com", "Region Labs")
	uid, acct := ada.GetUser().GetId(), ada.GetMemberships()[0].GetAccountId()

	regions := func(app *plansv1.App) []string {
		var out []string
		for _, tn := range app.GetTenants() {
			out = append(out, tn.GetRegion())
		}
		return out
	}
	chosen, err := e.ident.CreateMyApp(ctx, &plansv1.CreateMyAppRequest{UserId: uid, AccountId: acct, Name: "Turkish shop", Currency: "TRY", Timezone: "Europe/Istanbul", Region: "tr"})
	if err != nil {
		t.Fatal(err)
	}
	if got := regions(chosen.GetApp()); len(got) != 2 || got[0] != "tr" || got[1] != "tr" {
		t.Errorf("tenants of the app created with region tr = %v, want both tr", got)
	}
	byDefault, err := e.ident.CreateMyApp(ctx, &plansv1.CreateMyAppRequest{UserId: uid, AccountId: acct, Name: "Default shop", Currency: "USD", Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	if got := regions(byDefault.GetApp()); len(got) != 2 || got[0] != "eu" || got[1] != "eu" {
		t.Errorf("tenants of the app created with no region = %v, want the default eu", got)
	}
	_, err = e.ident.CreateMyApp(ctx, &plansv1.CreateMyAppRequest{UserId: uid, AccountId: acct, Name: "Bad region", Currency: "USD", Timezone: "UTC", Region: "EU West"})
	wantCode(t, "a region the table would refuse", err, codes.InvalidArgument, "plans.app.bad_region")

	// What the table holds is what the API shows, for the tenants of both apps.
	for _, app := range []*plansv1.App{chosen.GetApp(), byDefault.GetApp()} {
		for _, tn := range app.GetTenants() {
			var stored string
			if err := e.system.QueryRow(ctx, `SELECT region FROM tenants WHERE id = $1`, tn.GetId()).Scan(&stored); err != nil || stored != tn.GetRegion() {
				t.Errorf("tenants.region of %s = %q (%v), the API said %q", tn.GetId(), stored, err, tn.GetRegion())
			}
		}
	}

	// The operator's way in takes the same field, and refuses the same names.
	op, err := e.admin.CreateApp(operator(), &plansv1.CreateAppRequest{AccountId: acct, Name: "Operator app", Currency: "USD", Timezone: "UTC", GracePeriod: durationpb.New(0), Region: "tr"})
	if err != nil {
		t.Fatal(err)
	}
	if got := regions(op.GetApp()); len(got) != 2 || got[0] != "tr" || got[1] != "tr" {
		t.Errorf("tenants of the operator's app with region tr = %v, want both tr", got)
	}
	_, err = e.admin.CreateApp(operator(), &plansv1.CreateAppRequest{AccountId: acct, Name: "Operator bad", Currency: "USD", Timezone: "UTC", GracePeriod: durationpb.New(0), Region: "-eu"})
	wantCode(t, "an operator's region the table would refuse", err, codes.InvalidArgument, "plans.app.bad_region")
}
