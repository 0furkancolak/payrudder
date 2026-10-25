package command

import (
	"slices"
	"testing"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
)

func set(category string, enabled bool) *plansv1.MailCategorySetting {
	return &plansv1.MailCategorySetting{Category: category, Enabled: enabled}
}

func TestApply(t *testing.T) {
	for name, tc := range map[string]struct {
		current []string
		changes []*plansv1.MailCategorySetting
		want    []string
	}{
		"nothing changes":     {nil, nil, []string{}},
		"switch one off":      {nil, []*plansv1.MailCategorySetting{set("usage_alerts", false)}, []string{"usage_alerts"}},
		"unlisted keep state": {[]string{"system"}, []*plansv1.MailCategorySetting{set("usage_alerts", false)}, []string{"usage_alerts", "system"}},
		"switch back on":      {[]string{"system", "payment_failed"}, []*plansv1.MailCategorySetting{set("system", true)}, []string{"payment_failed"}},
		"last value wins":     {nil, []*plansv1.MailCategorySetting{set("system", false), set("system", true)}, []string{}},
		"canonical order":     {nil, []*plansv1.MailCategorySetting{set("system", false), set("invoice_issued", false)}, []string{"invoice_issued", "system"}},
	} {
		t.Run(name, func(t *testing.T) {
			if got := apply(tc.current, tc.changes); !slices.Equal(got, tc.want) {
				t.Errorf("apply = %v, want %v", got, tc.want)
			}
		})
	}
}
