package httpapi

import (
	"bytes"
	"html/template"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode"

	"google.golang.org/protobuf/types/known/timestamppb"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
)

// renderCase is one page of the public site as a view with every optional part switched on, so that a template that prints something is exercised with it present.
type renderCase struct {
	name, tmpl string
	view       any
}

func richBase(lang string, logo bool) pageBase {
	b := newBase(lang)
	b.Test, b.SideSeller, b.SideAmount, b.SideNote = true, "Acme & Sons", b.F.Amount(123456, 2)+" USD", b.L["st_open"]
	if logo {
		b.Logo = "/i/token/logo"
	}
	return b
}

func renderCases(lang string) []renderCase {
	b, plain := richBase(lang, true), richBase(lang, false)
	f := b.F
	day := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	header := invoiceHeader{Seller: "Acme & Sons", Customer: "Ada Lovelace", Number: "INV-2026-000042", Date: f.Date(day), Currency: "USD", Total: f.Amount(123456, 2),
		SellerAddress: []string{"1 Market St", "London", "GB"}, CustomerAddress: []string{"Atatürk Cd. 1", "34000 İstanbul"},
		Lines: []hostedLine{{Description: "Pro plan", Period: f.Date(day) + " – " + f.Date(day.AddDate(0, 1, 0)), Amount: f.Amount(129456, 2)}, {Description: "Credit", Amount: f.Amount(-6000, 2), Negative: true}}}
	invoice := func(contracts *contractView) *invoiceView {
		return &invoiceView{pageBase: b, invoiceHeader: header, Status: "open", State: "open", StateLabel: b.L["st_open"], Subtotal: f.Amount(123456, 2), HasPDF: true, PDF: "/i/token/pdf",
			Receipt: "/i/token/receipt", Back: "/c/token", Pay: "/i/token/pay", Payable: true, NoMethod: contracts == nil, Hint: b.L["hint_failed"], Contracts: contracts,
			Bank:    &hostedBank{Name: "Example Bank", Holder: "Acme Ltd", IBAN: "TR33 0006 1005 1978 6457 8413 26", BIC: "TCZBTR2A", Reference: "INV-2026-000042"},
			Methods: []methodView{{ID: "checkout", Label: b.L["pay_checkout"]}, {ID: "bank", Label: b.L["pay_instructions"]}}}
	}
	contracts := &contractView{Consents: true, Error: b.L["contract_title"], Items: []contractItemView{
		{Kind: "distance_sales", Label: "Distance sales", VersionID: "v1", CheckboxText: "I accept", Number: 3, HTML: template.HTML("<h3>Terms</h3><p>Text</p><ul><li>One</li></ul>")},
		{Kind: "privacy", Label: "Privacy", VersionID: "v2", Number: 1, Accepted: true, AcceptedOn: f.Date(day), HTML: template.HTML("<p>Privacy</p>")}}}
	adhoc := func(consumed bool) *adhocView {
		return &adhocView{pageBase: b, Seller: "Acme & Sons", Description: "Consulting day", Amount: f.Amount(25000, 2), Currency: "EUR", Consumed: consumed, Start: "/i/token/start",
			Pack: &packView{Name: "Starter", Credits: f.Amount(20000, 0), Valid: b.L["cr_forever"], Quantity: 2}, PromoAction: "/i/token/promo", Code: "SAVE10",
			Quote: &quoteView{Discount: f.Amount(2500, 2), Total: f.Amount(22500, 2), Bonus: f.Amount(500, 0), HasDiscount: true}, PromoError: b.L["promo_label"]}
	}
	subs := portalSubRows([]*billingv1.PortalSubscription{
		{PlanName: "Pro", State: "trialing", Interval: "month", Quantity: 3, PriceKnown: true, PriceMinor: 8700, Currency: "USD", CurrencyExponent: 2, TrialEndTime: timestamppb.New(day)},
		{PlanName: "Team", State: "active", Interval: "year", PriceKnown: true, PriceMinor: 1234500, Currency: "EUR", CurrencyExponent: 2, CancelAtPeriodEnd: true, EndTime: timestamppb.New(day), ProviderManaged: true},
		{PlanName: "Basic", State: "past_due", Interval: "month", ScheduledPlanName: "Free", ScheduledTime: timestamppb.New(day)},
		{PlanName: "Solo", State: "paused", Interval: "month", NextRenewalTime: timestamppb.New(day)}}, b)
	portal := &portalView{pageBase: b, Seller: "Acme & Sons", Customer: "Ada Lovelace", Subscriptions: subs, ShowCredits: true, Balance: f.Amount(12500, 0), CanBuy: true, Buy: "/c/token/credits/buy",
		Invoices: []portalInvoiceRow{{Number: "INV-1", Date: f.Date(day), State: "open", StateLabel: b.L["st_open"], Total: "50.00", Currency: "USD", Href: "/c/token/invoices/1", Payable: true}, {Number: "INV-2", Date: f.Date(day), State: "paid", StateLabel: b.L["st_paid"], Total: "15.00", Currency: "USD", Href: "/c/token/invoices/2"}},
		Payments: []portalPaymentRow{{Date: f.Date(day), Kind: b.L["kind_charge"], Number: "INV-2", Status: b.L["pay_succeeded"], Provider: "paddle", Amount: "15.00", Currency: "USD"}, {Date: f.Date(day), Kind: b.L["kind_refund"], Number: "INV-2", Status: b.L["pay_succeeded"], Provider: "paddle", Amount: f.Minus + "5.00", Currency: "USD", Negative: true}},
		Lots:     []portalLotRow{{Granted: "10,000", Remaining: "2,500", Expires: f.Date(day)}, {Granted: "10,000", Remaining: "10,000"}},
		Packs:    []portalPackRow{{ID: "p1", Name: "Starter", Credits: "10,000", Price: "5.00", Currency: "USD", Valid: b.L["cr_forever"]}}}
	return []renderCase{
		{"invoice", "invoice", invoice(nil)}, {"invoice-agreements", "invoice", invoice(contracts)},
		{"invoice-processing", "invoice", func() *invoiceView {
			v := invoice(nil)
			v.CheckAgain, v.Self, v.State, v.Payable, v.Bank = true, "/i/token", "processing", false, nil
			return v
		}()},
		{"adhoc", "adhoc", adhoc(false)}, {"adhoc-used", "adhoc", adhoc(true)},
		{"receipt", "receipt", &receiptView{pageBase: b, invoiceHeader: header, PaidOn: f.Date(day), PaidWith: "paddle", Back: "/i/token"}},
		{"instructions", "instructions", &instructionsView{pageBase: plain, Seller: "Acme", Number: "INV-1", Total: "1,500.00", Currency: "TRY", Reference: "INV-1", Back: "/i/token",
			Lines: []struct{ Label, Value string }{{"IBAN", "TR33 0006 1005 1978 6457 8413 26"}}}},
		{"message", "message", &messageView{pageBase: plain, Heading: plain.L["gone"], Text: plain.L["goneHint"], Back: "/c/token"}},
		{"referral", "referral", &referralView{pageBase: plain, Privacy: "/privacy"}}, {"privacy", "privacy", &privacyView{pageBase: plain}},
		{"portal", "portal", portal},
		{"portal-empty", "portal", &portalView{pageBase: b, Seller: "Acme", Customer: "Ada"}},
	}
}

var (
	htmlLang    = regexp.MustCompile(`<html lang="([a-z]+)" dir="(ltr|rtl)">`)
	htmlStyle   = regexp.MustCompile(`(?s)<style>.*?</style>`)
	htmlTitle   = regexp.MustCompile(`<title>([^<]+)</title>`)
	htmlImgs    = regexp.MustCompile(`<img\b[^>]*\bsrc="([^"]*)"`)
	htmlForms   = regexp.MustCompile(`<form\b[^>]*\baction="([^"]*)"`)
	htmlBanned  = regexp.MustCompile(`(?i)<(script|iframe|object|embed|link|base|audio|video|source|track|applet|frame|frameset|svg)\b|\sstyle=|\son[a-z]+\s*=|javascript:|\burl\(|@import|@font-face|http-equiv`)
	cspDirected = regexp.MustCompile(`([a-z-]+) ([^;]+)`)
)

func parseCSP(csp string) map[string]string {
	out := map[string]string{}
	for _, m := range cspDirected.FindAllStringSubmatch(csp, -1) {
		out[m[1]] = strings.TrimSpace(m[2])
	}
	return out
}

// Every page in every language renders: the right lang and dir, a title and one heading, no text key or template variable left unfilled, and nothing the page's
// Content-Security-Policy would block. HOSTED_PAGES_DIR=<dir> writes the pages there as files (the screenshots and the axe run of docs/hosted-pages.md use them).
func TestEveryPageRendersInEveryLanguage(t *testing.T) {
	// A key a template names that the text maps lack is an error, not an empty string.
	strict := parsePageTemplates()
	strict.Option("missingkey=error")
	dir := os.Getenv("HOSTED_PAGES_DIR")
	for _, lang := range hostedLangs {
		for _, c := range renderCases(lang) {
			var buf bytes.Buffer
			if err := strict.ExecuteTemplate(&buf, c.tmpl, c.view); err != nil {
				t.Errorf("%s/%s: %v", lang, c.name, err)
				continue
			}
			page := buf.String()
			if dir != "" {
				if err := os.WriteFile(filepath.Join(dir, lang+"-"+c.name+".html"), buf.Bytes(), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			m := htmlLang.FindStringSubmatch(page)
			if m == nil || m[1] != lang || (m[2] == "rtl") != (lang == "ar") {
				t.Errorf("%s/%s: lang and dir are %v", lang, c.name, m)
			}
			if tt := htmlTitle.FindStringSubmatch(page); tt == nil || strings.TrimSpace(tt[1]) == "" {
				t.Errorf("%s/%s: no title", lang, c.name)
			}
			if n := strings.Count(page, "<h1"); n != 1 {
				t.Errorf("%s/%s: %d headings of level 1, want one", lang, c.name, n)
			}
			body := htmlStyle.ReplaceAllString(page, "")
			for _, left := range []string{"<no value>", "%!", "{{", "}}", "%s", "%[", "&lt;nil&gt;"} {
				if strings.Contains(body, left) {
					t.Errorf("%s/%s: the page contains %q", lang, c.name, left)
				}
			}
			if c.name == "invoice-processing" && !strings.Contains(page, `id="check-again"`) {
				t.Errorf("%s/%s: a processing page offers to check again", lang, c.name)
			}
			stayInsidePolicy(t, lang+"/"+c.name, c, page)
		}
	}
}

// stayInsidePolicy fails when a page uses something its Content-Security-Policy forbids, or its policy allows something the page does not use.
func stayInsidePolicy(t *testing.T, name string, c renderCase, page string) {
	t.Helper()
	v := c.view.(interface {
		hasLogo() bool
		formTargets() string
	})
	pol := parseCSP(pageCSP(v.hasLogo(), v.formTargets()))
	if pol["default-src"] != "'none'" || pol["style-src"] != "'unsafe-inline'" || pol["base-uri"] != "'none'" || pol["frame-ancestors"] != "'none'" || pol["script-src"] != "" {
		t.Errorf("%s: the policy %v is not the closed one", name, pol)
	}
	if b := htmlBanned.FindString(page); b != "" {
		t.Errorf("%s: the page uses %q, which the policy (no script, frame, object, inline style attribute, remote resource) forbids", name, b)
	}
	imgs := htmlImgs.FindAllStringSubmatch(page, -1)
	if (len(imgs) > 0) != (pol["img-src"] != "") {
		t.Errorf("%s: %d images, img-src %q: a page lists img-src exactly when it shows an image", name, len(imgs), pol["img-src"])
	}
	for _, m := range imgs {
		if !strings.HasPrefix(m[1], "/") || strings.HasPrefix(m[1], "//") {
			t.Errorf("%s: image %q is not from this origin", name, m[1])
		}
	}
	forms := htmlForms.FindAllStringSubmatch(page, -1)
	targets := strings.Fields(pol["form-action"])
	if len(forms) == 0 && !slices.Equal(targets, []string{"'none'"}) {
		t.Errorf("%s: no form, but form-action is %q", name, pol["form-action"])
	}
	if len(forms) > 0 && !slices.Contains(targets, "'self'") {
		t.Errorf("%s: %d forms post to themselves, form-action is %q", name, len(forms), pol["form-action"])
	}
	for _, m := range forms {
		if !strings.HasPrefix(m[1], "/") || strings.HasPrefix(m[1], "//") {
			t.Errorf("%s: a form posts to %q, which is not this origin", name, m[1])
		}
	}
	if slices.Contains(targets, "https:") && c.tmpl != "invoice" {
		t.Errorf("%s: only the invoice page ends in a provider's https redirect, but its form-action is %q", name, pol["form-action"])
	}
	// The stylesheet loads nothing.
	if strings.Contains(pageCSS, "url(") || strings.Contains(pageCSS, "@import") {
		t.Errorf("the stylesheet loads a resource")
	}
}

// The text of every language has the keys of English and nothing empty, in every map the pages merge; a text that reads the same as the English one is allowed only
// when it is one word or a name (so a forgotten translation of a sentence is found).
func TestHostedTextsAreCompleteInEveryLanguage(t *testing.T) {
	sameAsEnglish := map[string]bool{"iban": true, "bic": true, "pay_mock": true}
	for i, src := range textSources {
		en := src["en"]
		for _, lang := range hostedLangs {
			m, ok := src[lang]
			if !ok {
				t.Errorf("text source %d has no %s", i, lang)
				continue
			}
			for k, v := range en {
				got, has := m[k]
				switch {
				case !has || strings.TrimSpace(got) == "":
					t.Errorf("source %d: %s lacks %q", i, lang, k)
				case lang != "en" && got == v && len(strings.Fields(v)) > 2 && !sameAsEnglish[k] && strings.IndexFunc(v, unicode.IsLetter) >= 0:
					t.Errorf("source %d: %s: %q is still English: %q", i, lang, k, v)
				}
			}
			for k := range m {
				if _, ok := en[k]; !ok {
					t.Errorf("source %d: %s has %q, which English lacks", i, lang, k)
				}
			}
		}
	}
}
