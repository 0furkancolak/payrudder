// Package mailcategory names the kinds of e-mail Payrudder sends for a tenant, so that a tenant can switch each kind off (plans-svc stores the switch,
// notifier enforces it). Both sides share this list, so a category the API accepts is one the notifier can recognise.
package mailcategory

import "strings"

// The categories. A tenant's setting is the set of categories it has switched off; none switched off is the default.
const (
	// InvoiceIssued is the mail that announces a finalized invoice.
	InvoiceIssued = "invoice_issued"
	// PaymentReceipts is the receipt of a successful payment (invoice_paid).
	PaymentReceipts = "payment_receipts"
	// PaymentFailed is the dunning and give-up mail of a payment that did not go through (payment_failed).
	PaymentFailed = "payment_failed"
	// UsageAlerts is the limit alert of a metered plan (limit_exceeded).
	UsageAlerts = "usage_alerts"
	// System is every other mail: anything whose template is not in a category above.
	System = "system"
)

var all = []string{InvoiceIssued, PaymentReceipts, PaymentFailed, UsageAlerts, System}

// All returns the categories in the order the API and the console list them.
func All() []string { return append([]string(nil), all...) }

// Valid reports whether c is a category.
func Valid(c string) bool {
	for _, a := range all {
		if a == c {
			return true
		}
	}
	return false
}

// templateCategory maps a template family (its name without the ".vN" version) to its category.
var templateCategory = map[string]string{
	"invoice_issued": InvoiceIssued,
	"invoice_paid":   PaymentReceipts,
	"payment_failed": PaymentFailed,
	"limit_exceeded": UsageAlerts,
}

// ForTemplate returns the category of a versioned template name such as "invoice_paid.v1". A template that is in no family is System.
func ForTemplate(template string) string {
	family, _, _ := strings.Cut(template, ".")
	if c, ok := templateCategory[family]; ok {
		return c
	}
	return System
}
