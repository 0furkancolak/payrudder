package mailcategory

import "testing"

func TestForTemplate(t *testing.T) {
	for template, want := range map[string]string{
		"invoice_issued.v1": InvoiceIssued,
		"invoice_paid.v1":   PaymentReceipts,
		"invoice_paid.v2":   PaymentReceipts,
		"payment_failed.v1": PaymentFailed,
		"limit_exceeded.v1": UsageAlerts,
		"welcome.v1":        System,
		"":                  System,
	} {
		if got := ForTemplate(template); got != want {
			t.Errorf("ForTemplate(%q) = %q, want %q", template, got, want)
		}
	}
}

func TestEveryTemplateCategoryIsListed(t *testing.T) {
	for family, c := range templateCategory {
		if !Valid(c) {
			t.Errorf("%s maps to %q, which All() does not list", family, c)
		}
	}
	if Valid("") || Valid("nope") {
		t.Error("unknown categories must not be valid")
	}
	got := All()
	got[0] = "changed"
	if All()[0] == "changed" {
		t.Error("All must return a copy")
	}
}
