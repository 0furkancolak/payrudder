//go:build integration

package itest

import (
	"testing"

	"google.golang.org/grpc/codes"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
)

func states(cs []*plansv1.MailCategorySetting) map[string]bool {
	out := map[string]bool{}
	for _, c := range cs {
		out[c.GetCategory()] = c.GetEnabled()
	}
	return out
}

func testMailSettings(t *testing.T) {
	e := newEnv(t)
	a := e.account(t, "Mail A")[plansv1.Environment_ENVIRONMENT_LIVE]
	b := e.account(t, "Mail B")[plansv1.Environment_ENVIRONMENT_LIVE]
	ctx := as(a.GetId())

	got, err := e.plans.GetMailSettings(ctx, &plansv1.GetMailSettingsRequest{})
	if err != nil || len(got.GetCategories()) != 6 {
		t.Fatalf("a new tenant lists all six categories: %v, %v", got, err)
	}
	for c, on := range states(got.GetCategories()) {
		// The notice to a blocked customer is the one category that is off until the tenant switches it on (plan 13).
		if on == (c == "customer_notices") {
			t.Errorf("%s is %v for a tenant that never changed it", c, on)
		}
	}

	set, err := e.plans.SetMailSettings(ctx, &plansv1.SetMailSettingsRequest{Categories: []*plansv1.MailCategorySetting{
		{Category: "payment_receipts", Enabled: false}, {Category: "usage_alerts", Enabled: false}}})
	if err != nil {
		t.Fatal(err)
	}
	if s := states(set.GetCategories()); s["payment_receipts"] || s["usage_alerts"] || !s["payment_failed"] || !s["system"] {
		t.Fatalf("set = %v", s)
	}

	// Categories not listed keep their setting; a listed one can be switched back on.
	set, err = e.plans.SetMailSettings(ctx, &plansv1.SetMailSettingsRequest{Categories: []*plansv1.MailCategorySetting{{Category: "usage_alerts", Enabled: true}}})
	if s := states(set.GetCategories()); err != nil || s["payment_receipts"] || !s["usage_alerts"] {
		t.Fatalf("partial set = %v, %v", s, err)
	}
	if got, _ = e.plans.GetMailSettings(ctx, &plansv1.GetMailSettingsRequest{}); states(got.GetCategories())["payment_receipts"] {
		t.Errorf("get after set = %v", got)
	}

	// An unknown category is refused with its key and changes nothing.
	_, err = e.plans.SetMailSettings(ctx, &plansv1.SetMailSettingsRequest{Categories: []*plansv1.MailCategorySetting{{Category: "marketing", Enabled: true}, {Category: "system", Enabled: false}}})
	if code(t, err) != codes.InvalidArgument {
		t.Fatalf("unknown category: %v", err)
	}
	if got, _ = e.plans.GetMailSettings(ctx, &plansv1.GetMailSettingsRequest{}); !states(got.GetCategories())["system"] {
		t.Error("a refused request changed the setting")
	}

	// The notice to a blocked customer is switched on like any other category, and the other categories keep their setting when it is.
	set, err = e.plans.SetMailSettings(ctx, &plansv1.SetMailSettingsRequest{Categories: []*plansv1.MailCategorySetting{{Category: "customer_notices", Enabled: true}}})
	if s := states(set.GetCategories()); err != nil || !s["customer_notices"] || s["payment_receipts"] || !s["usage_alerts"] {
		t.Fatalf("switching the notices on = %v, %v", s, err)
	}
	if got, _ = e.plans.GetMailSettings(ctx, &plansv1.GetMailSettingsRequest{}); !states(got.GetCategories())["customer_notices"] {
		t.Errorf("get after switching the notices on = %v", got)
	}
	// Changing another category keeps the notices on; switching them off again is remembered.
	set, err = e.plans.SetMailSettings(ctx, &plansv1.SetMailSettingsRequest{Categories: []*plansv1.MailCategorySetting{{Category: "system", Enabled: false}}})
	if s := states(set.GetCategories()); err != nil || !s["customer_notices"] || s["system"] {
		t.Fatalf("a change elsewhere = %v, %v", s, err)
	}
	set, err = e.plans.SetMailSettings(ctx, &plansv1.SetMailSettingsRequest{Categories: []*plansv1.MailCategorySetting{{Category: "customer_notices", Enabled: false}, {Category: "system", Enabled: true}}})
	if s := states(set.GetCategories()); err != nil || s["customer_notices"] || !s["system"] {
		t.Fatalf("switching the notices off = %v, %v", s, err)
	}

	// Another tenant has its own switch.
	if other, _ := e.plans.GetMailSettings(as(b.GetId()), &plansv1.GetMailSettingsRequest{}); !states(other.GetCategories())["payment_receipts"] || states(other.GetCategories())["customer_notices"] {
		t.Errorf("another tenant sees %v", other.GetCategories())
	}
}
