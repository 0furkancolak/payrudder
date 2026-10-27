package model

import (
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/0furkancolak/payrudder/pkg/errkey"
)

func TestLimitBlocks(t *testing.T) {
	cases := []struct {
		name  string
		l     Limit
		used  int64
		block bool
	}{
		{"unlimited never blocks", Limit{Units: 0, Mode: Hard}, 1_000_000, false},
		{"room left", Limit{Units: 3, Mode: Hard}, 2, false},
		{"at the limit the next one is refused", Limit{Units: 3, Mode: Hard}, 3, true},
		{"over the limit", Limit{Units: 3, Mode: Hard}, 9, true},
		{"a soft limit is the quota engine's business", Limit{Units: 3, Mode: Soft}, 9, false},
		{"a notify limit only alerts", Limit{Units: 3, Mode: Notify}, 9, false},
	}
	for _, c := range cases {
		if got := c.l.Blocks(c.used); got != c.block {
			t.Errorf("%s: Blocks(%d) = %v, want %v", c.name, c.used, got, c.block)
		}
	}
}

func TestLimitOverAndFits(t *testing.T) {
	l := Limit{Units: 5, Mode: Hard}
	if l.Over(4) || !l.Over(5) || !l.Over(6) {
		t.Error("Over: reached means used >= limit")
	}
	if !l.Fits(5) || l.Fits(6) {
		t.Error("Fits: usage up to the limit fits")
	}
	free := Limit{}
	if free.Over(1<<40) || !free.Fits(1<<40) {
		t.Error("an unlimited limit is never over and everything fits")
	}
}

func TestLimitsOfAbsentResourceIsUnlimited(t *testing.T) {
	l := Limits{Apps: {Units: 2, Mode: Hard}}
	if !l.Of(Members).Unlimited() {
		t.Error("a resource a plan does not mention is unlimited")
	}
	if l.Of(Apps).Units != 2 {
		t.Error("a named resource keeps its limit")
	}
}

func TestMergeLetsTheOrganisationsOwnLimitsWin(t *testing.T) {
	plan := Limits{Apps: {Units: 1, Mode: Hard}, Members: {Units: 2, Mode: Hard}}
	own := Limits{Apps: {Units: 0, Mode: Hard}}
	got, overridden := Merge(plan, own)
	if !got.Of(Apps).Unlimited() || got.Of(Members).Units != 2 {
		t.Errorf("merged = %+v", got)
	}
	if !overridden[Apps] || overridden[Members] {
		t.Errorf("overridden = %v", overridden)
	}
	if plan.Of(Apps).Units != 1 {
		t.Error("Merge changed the plan's own limits")
	}
}

func TestFromEntitlementsKeepsOnlyResources(t *testing.T) {
	got := FromEntitlements([]Entitlement{
		{Meter: "events", Units: 10, Mode: "notify"},
		{Meter: "api_keys", Units: 4, Mode: "hard"},
		{Meter: "storage_gb", Units: 1, Mode: "hard"},
		{Meter: "apps", Units: 3, Mode: "weird"},
	})
	if len(got) != 3 {
		t.Fatalf("got %d limits, want 3: %+v", len(got), got)
	}
	if got.Of(Events).Mode != Notify || got.Of(APIKeys).Units != 4 {
		t.Errorf("limits = %+v", got)
	}
	if got.Of(Apps).Mode != Hard {
		t.Error("an unknown mode reads as hard, the safe reading")
	}
}

func TestParseResource(t *testing.T) {
	for _, r := range Resources {
		if got, ok := ParseResource(string(r)); !ok || got != r {
			t.Errorf("ParseResource(%q) = %v, %v", r, got, ok)
		}
	}
	if _, ok := ParseResource("seats"); ok {
		t.Error("ParseResource accepted an unknown resource")
	}
}

func TestQuotaErrorKeys(t *testing.T) {
	for r, key := range map[Resource]string{Apps: "plans.saas.quota.apps", Members: "plans.saas.quota.members", APIKeys: "plans.saas.quota.api_keys"} {
		err := QuotaError(r)
		if errkey.Of(err) != key || status.Code(err) != codes.ResourceExhausted {
			t.Errorf("QuotaError(%s) = %v key %q", r, status.Code(err), errkey.Of(err))
		}
	}
	if errkey.Of(QuotaError(Events)) != "plans.saas.bad_resource" {
		t.Error("the events quota is not refused through the guard")
	}
}

func TestDefaultPlansAreSane(t *testing.T) {
	plans := DefaultPlans()
	if len(plans) != 3 || plans[0].Code != PlanFree || plans[1].Code != PlanPro || plans[2].Code != PlanScale {
		t.Fatalf("plans = %+v", plans)
	}
	if plans[0].MonthlyFee != 0 || plans[1].MonthlyFee <= 0 || plans[2].MonthlyFee <= plans[1].MonthlyFee {
		t.Error("Free costs nothing and each plan above it costs more")
	}
	for i := 1; i < len(plans); i++ {
		for _, r := range Resources {
			lo, hi := plans[i-1].Limits.Of(r), plans[i].Limits.Of(r)
			if !hi.Unlimited() && hi.Units <= lo.Units {
				t.Errorf("%s of %s (%d) is not above %s's (%d)", r, plans[i].Code, hi.Units, plans[i-1].Code, lo.Units)
			}
		}
	}
	for _, p := range plans {
		for _, r := range Resources {
			if _, ok := p.Limits[r]; !ok {
				t.Errorf("plan %s has no limit for %s", p.Code, r)
			}
		}
	}
}

func TestEveryResourceHasAMeter(t *testing.T) {
	have := map[string]bool{}
	for _, m := range Meters() {
		have[m.Code] = true
	}
	for _, r := range Resources {
		if !have[r.Meter()] {
			t.Errorf("resource %s has no meter", r)
		}
	}
}
