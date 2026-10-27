// Package templates holds the email templates: Go html/template files embedded in the binary and versioned by name
// (invoice_paid.v1, ...). A new wording is a new version, never an edit of a sent one.
package templates

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"strings"
)

//go:embed files/*.html
var files embed.FS

// Rendered is a ready-to-send message body.
type Rendered struct {
	Subject string
	HTML    string
	Text    string
}

// subjects are the subject lines; they use the same data as the template.
var subjects = map[string]string{
	"invoice_paid.v1":     "Receipt for invoice {{.invoice_number}}",
	"payment_failed.v1":   "Payment failed for invoice {{.invoice_number}}",
	"invoice_transfer.v1": "Invoice {{.invoice_number}}: how to pay by bank transfer",
	"limit_exceeded.v1":   "You have used {{.threshold_pct}}% of your {{.meter_code}} limit",
	"trial_will_end.v1":   "Your {{.plan_code}} trial ends on {{.trial_end_date}}",
}

var set = template.Must(template.New("").ParseFS(files, "files/*.html"))

// Render executes a template with data. sandbox prefixes the subject with "[SANDBOX] ".
func Render(name string, data map[string]string, sandbox bool) (Rendered, error) {
	subjTpl, ok := subjects[name]
	if !ok {
		return Rendered{}, fmt.Errorf("templates: unknown template %q", name)
	}
	t := set.Lookup(name + ".html")
	if t == nil {
		return Rendered{}, fmt.Errorf("templates: unknown template %q", name)
	}
	var body bytes.Buffer
	if err := t.Execute(&body, map[string]any{"D": data, "Sandbox": sandbox}); err != nil {
		return Rendered{}, fmt.Errorf("templates: render %s: %w", name, err)
	}
	subj, err := template.New("s").Option("missingkey=zero").Parse(subjTpl)
	if err != nil {
		return Rendered{}, err
	}
	var sb bytes.Buffer
	if err := subj.Execute(&sb, data); err != nil {
		return Rendered{}, err
	}
	subject := strings.TrimSpace(sb.String())
	if sandbox {
		subject = "[SANDBOX] " + subject
	}
	return Rendered{Subject: subject, HTML: body.String(), Text: stripTags(body.String())}, nil
}

func stripTags(s string) string {
	var b strings.Builder
	in := false
	for _, r := range s {
		switch {
		case r == '<':
			in = true
		case r == '>':
			in = false
			b.WriteRune(' ')
		case !in:
			b.WriteRune(r)
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}
