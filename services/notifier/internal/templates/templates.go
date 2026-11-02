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
	"invoice_paid.v1":                 "Receipt for invoice {{.invoice_number}}",
	"payment_failed.v1":               "Payment failed for invoice {{.invoice_number}}",
	"invoice_transfer.v1":             "Invoice {{.invoice_number}}: how to pay by bank transfer",
	"limit_exceeded.v1":               "You have used {{.threshold_pct}}% of your {{.meter_code}} limit",
	"trial_will_end.v1":               "Your {{.plan_code}} trial ends on {{.trial_end_date}}",
	"price_change.v1":                 "Your {{.plan_code}} price changes on {{.effective_date}}",
	"contract_accepted.v1":            "Your agreement with {{.tenant_name}}",
	"renewal_notice.v1":               "Your {{.plan_code}} subscription renews on {{.renewal_date}}",
	"contract_withdrawal_received.v1": "We received your withdrawal",
}

// localeSubjects are the subject lines in the other languages (en is `subjects`); a template or language that has none uses the English one.
var localeSubjects = map[string]map[string]string{
	"tr": {
		"invoice_paid.v1":                 "{{.invoice_number}} numaralı fatura için makbuz",
		"payment_failed.v1":               "{{.invoice_number}} numaralı faturanın ödemesi başarısız",
		"invoice_transfer.v1":             "Fatura {{.invoice_number}}: banka havalesiyle nasıl ödenir",
		"limit_exceeded.v1":               "{{.meter_code}} limitinizin %{{.threshold_pct}} kadarını kullandınız",
		"trial_will_end.v1":               "{{.plan_code}} denemeniz {{.trial_end_date}} tarihinde bitiyor",
		"price_change.v1":                 "{{.plan_code}} fiyatınız {{.effective_date}} tarihinde değişiyor",
		"contract_accepted.v1":            "{{.tenant_name}} ile sözleşmeniz",
		"renewal_notice.v1":               "{{.plan_code}} aboneliğiniz {{.renewal_date}} tarihinde yenileniyor",
		"contract_withdrawal_received.v1": "Cayma bildiriminizi aldık",
	},
	"de": {
		"invoice_paid.v1":                 "Beleg für Rechnung {{.invoice_number}}",
		"payment_failed.v1":               "Zahlung für Rechnung {{.invoice_number}} fehlgeschlagen",
		"invoice_transfer.v1":             "Rechnung {{.invoice_number}}: so bezahlen Sie per Überweisung",
		"limit_exceeded.v1":               "Sie haben {{.threshold_pct}}% Ihres {{.meter_code}}-Limits verbraucht",
		"trial_will_end.v1":               "Ihre Testphase für {{.plan_code}} endet am {{.trial_end_date}}",
		"price_change.v1":                 "Der Preis Ihres Abonnements {{.plan_code}} ändert sich am {{.effective_date}}",
		"contract_accepted.v1":            "Ihre Vereinbarung mit {{.tenant_name}}",
		"renewal_notice.v1":               "Ihr Abonnement {{.plan_code}} verlängert sich am {{.renewal_date}}",
		"contract_withdrawal_received.v1": "Wir haben Ihren Widerruf erhalten",
	},
	"fr": {
		"invoice_paid.v1":                 "Reçu de la facture {{.invoice_number}}",
		"payment_failed.v1":               "Échec du paiement de la facture {{.invoice_number}}",
		"invoice_transfer.v1":             "Facture {{.invoice_number}} : comment payer par virement",
		"limit_exceeded.v1":               "Vous avez utilisé {{.threshold_pct}} % de votre limite {{.meter_code}}",
		"trial_will_end.v1":               "Votre essai {{.plan_code}} se termine le {{.trial_end_date}}",
		"price_change.v1":                 "Le tarif de votre abonnement {{.plan_code}} change le {{.effective_date}}",
		"contract_accepted.v1":            "Votre accord avec {{.tenant_name}}",
		"renewal_notice.v1":               "Votre abonnement {{.plan_code}} se renouvelle le {{.renewal_date}}",
		"contract_withdrawal_received.v1": "Nous avons reçu votre rétractation",
	},
	"es": {
		"invoice_paid.v1":                 "Recibo de la factura {{.invoice_number}}",
		"payment_failed.v1":               "Falló el pago de la factura {{.invoice_number}}",
		"invoice_transfer.v1":             "Factura {{.invoice_number}}: cómo pagar por transferencia",
		"limit_exceeded.v1":               "Ha usado el {{.threshold_pct}}% de su límite de {{.meter_code}}",
		"trial_will_end.v1":               "Su prueba de {{.plan_code}} termina el {{.trial_end_date}}",
		"price_change.v1":                 "El precio de su suscripción {{.plan_code}} cambia el {{.effective_date}}",
		"contract_accepted.v1":            "Su acuerdo con {{.tenant_name}}",
		"renewal_notice.v1":               "Su suscripción {{.plan_code}} se renueva el {{.renewal_date}}",
		"contract_withdrawal_received.v1": "Hemos recibido su desistimiento",
	},
	"ar": {
		"invoice_paid.v1":                 "إيصال الفاتورة {{.invoice_number}}",
		"payment_failed.v1":               "فشل دفع الفاتورة {{.invoice_number}}",
		"invoice_transfer.v1":             "الفاتورة {{.invoice_number}}: كيفية الدفع بالتحويل المصرفي",
		"limit_exceeded.v1":               "استخدمت {{.threshold_pct}}% من حد {{.meter_code}}",
		"trial_will_end.v1":               "تنتهي تجربتك لـ {{.plan_code}} في {{.trial_end_date}}",
		"price_change.v1":                 "سعر اشتراكك {{.plan_code}} يتغير في {{.effective_date}}",
		"contract_accepted.v1":            "اتفاقيتك مع {{.tenant_name}}",
		"renewal_notice.v1":               "سيتجدد اشتراكك {{.plan_code}} في {{.renewal_date}}",
		"contract_withdrawal_received.v1": "تلقينا طلب انسحابك",
	},
}

// trusted prints HTML another service wrote and escaped: the snapshot of a contract acceptance, whose hash the consumer checked before it got here. Nothing else is trusted.
var set = template.Must(template.New("").Funcs(template.FuncMap{"trusted": func(s string) template.HTML { return template.HTML(s) }}).ParseFS(files, "files/*.html")) //nolint:gosec // see trusted

// Locales are the languages a mail can be in; the first is the fallback (English, the files without a language in their name).
var Locales = []string{"en", "tr", "de", "fr", "es", "ar"}

// Render executes a template with data, in the language data["locale"] names (a language the template has no file for is English). sandbox prefixes the
// subject with "[SANDBOX] ".
func Render(name string, data map[string]string, sandbox bool) (Rendered, error) {
	subjTpl, ok := subjects[name]
	if !ok {
		return Rendered{}, fmt.Errorf("templates: unknown template %q", name)
	}
	t := set.Lookup(name + ".html")
	if loc := data["locale"]; loc != "" && loc != "en" {
		if lt := set.Lookup(name + "." + loc + ".html"); lt != nil {
			t = lt
			if ls, ok := localeSubjects[loc][name]; ok {
				subjTpl = ls
			}
		}
	}
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
