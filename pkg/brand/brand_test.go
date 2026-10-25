package brand

import "testing"

func TestRenaming(t *testing.T) {
	t.Cleanup(func() { Set("") })
	if Name() != "Payrudder" || Slug() != "quotify" || Header("Signature") != "Quotify-Signature" || UserAgent("Webhooks", 1) != "Quotify-Webhooks/1" {
		t.Fatalf("defaults: %q %q %q", Name(), Slug(), Header("Signature"))
	}
	Set("  Acme Billing! ")
	if Name() != "Acme Billing!" || Slug() != "acme-billing" || Header("Signature") != "Acme-Billing-Signature" || UserAgent("Webhooks", 1) != "Acme-Billing-Webhooks/1" {
		t.Fatalf("renamed: %q %q %q", Name(), Slug(), Header("Signature"))
	}
}
