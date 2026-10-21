//go:build integration

package itest

import (
	"testing"

	"google.golang.org/grpc/codes"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
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
	if err != nil || len(got.GetCategories()) != 5 {
		t.Fatalf("a new tenant lists all five categories: %v, %v", got, err)
	}
	for c, on := range states(got.GetCategories()) {
		if !on {
			t.Errorf("%s is off for a tenant that never changed it", c)
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

	// Another tenant has its own switch.
	if other, _ := e.plans.GetMailSettings(as(b.GetId()), &plansv1.GetMailSettingsRequest{}); !states(other.GetCategories())["payment_receipts"] {
		t.Errorf("another tenant sees %v", other.GetCategories())
	}
}
