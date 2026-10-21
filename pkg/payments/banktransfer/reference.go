package banktransfer

import (
	"errors"
	"fmt"
	"strings"
)

// DefaultReferenceTemplate makes the invoice number the payment reference.
const DefaultReferenceTemplate = "{invoice_number}"

// MaxReferenceLen is the longest payment reference. SEPA allows 140 characters, but the description field of a Turkish EFT or havale form is shorter, and a
// customer has to type the reference.
const MaxReferenceLen = 35

// placeholders a template may use.
var placeholders = []string{"{invoice_number}", "{invoice_id}"}

// ValidateTemplate checks a reference template: only the known placeholders between braces, and a rendering that is a usable reference.
func ValidateTemplate(t string) error {
	rest := t
	for _, p := range placeholders {
		rest = strings.ReplaceAll(rest, p, "")
	}
	if strings.ContainsAny(rest, "{}") {
		return fmt.Errorf("reference_template: only %s are allowed between braces", strings.Join(placeholders, " and "))
	}
	if Reference(t, "INV-2026-000123", "0192f0b4-6f6e-7c3a-9a3e-6a1f2b3c4d5e") == "" {
		return errors.New("reference_template: it renders to nothing")
	}
	return nil
}

// Reference renders a payment reference: the template with {invoice_number} and {invoice_id} (its first 8 characters) filled in, reduced to the characters
// every bank's reference field accepts (letters, digits and space - / . :), trimmed and cut to MaxReferenceLen. An empty template means the invoice number.
// An invoice without a number is referred to by its id.
func Reference(template, number, invoiceID string) string {
	if template == "" {
		template = DefaultReferenceTemplate
	}
	if number == "" {
		number = shortID(invoiceID)
	}
	out := strings.NewReplacer("{invoice_number}", number, "{invoice_id}", shortID(invoiceID)).Replace(template)
	var b strings.Builder
	for _, r := range out {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == ' ', r == '-', r == '/', r == '.', r == ':':
			b.WriteRune(r)
		}
	}
	s := strings.Join(strings.Fields(b.String()), " ")
	if len(s) > MaxReferenceLen {
		s = strings.TrimSpace(s[:MaxReferenceLen])
	}
	return s
}

func shortID(id string) string {
	id = strings.ReplaceAll(id, "-", "")
	if len(id) > 8 {
		id = id[:8]
	}
	return id
}
