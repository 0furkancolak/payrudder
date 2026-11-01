package render

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	commandsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/commands/v1"
)

var update = flag.Bool("update", false, "rewrite the golden texts in testdata")

// rich is a Turkish seller's invoice with everything the document can show: tax ids on both sides, a negative line, a quantity, a large amount, bank details.
func rich(locale string) *commandsv1.InvoiceSnapshot {
	return &commandsv1.InvoiceSnapshot{
		InvoiceId: "0192f0b4-6f6e-7c3a-9a3e-6a1f2b3c4d5e", Version: 1, Number: "INV-2026-000042", PeriodId: "2026-10", Currency: "EUR", CurrencyExponent: 2,
		TenantName: "Acme Yazılım A.Ş.", CustomerName: "Müller GmbH", CustomerEmail: "buchhaltung@mueller.example",
		IssuedAt: timestamppb.New(time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)),
		Lines: []*commandsv1.InvoiceLineSnapshot{
			{LineNo: 1, Kind: "BASE_FEE", Description: "Base fee", AmountMinor: 123456789},
			{LineNo: 2, Kind: "USAGE", Description: "Usage — gb_hours", Quantity: "1234.500", UnitLabel: "GB-h", AmountMinor: 61725},
			{LineNo: 3, Kind: "DISCOUNT", Description: "Launch discount", AmountMinor: -5000},
		},
		SubtotalMinor: 123513514, TotalMinor: 123513514,
		SellerAddressLines: []string{"Atatürk Cd. 1", "34000 İstanbul", "TR"}, CustomerAddressLines: []string{"Hauptstr. 5", "10115 Berlin", "DE"},
		PaymentInstructions: &commandsv1.PaymentInstructions{BankName: "Example Bank", AccountHolder: "Acme Yazılım A.Ş.", Iban: "TR330006100519786457841326", Bic: "TCZBTR2A", Reference: "INV-2026-000042"},
		Locale:              locale, CustomerType: "business", EInvoiceOn: "paid",
		SellerTaxId: "1234567890", SellerTaxOffice: "Kadıköy", SellerCountry: "TR", CustomerTaxId: "DE136695976", CustomerCountry: "DE",
	}
}

func TestEveryLocaleHasEveryKey(t *testing.T) {
	for code, l := range layouts {
		for _, k := range allKeys {
			if strings.TrimSpace(l.words[k]) == "" {
				t.Errorf("%s has no text for %q", code, k)
			}
		}
		if len(l.words) != len(allKeys) {
			t.Errorf("%s has %d texts, want %d: a key that no layout draws", code, len(l.words), len(allKeys))
		}
		if l.code != code {
			t.Errorf("layout %q calls itself %q", code, l.code)
		}
	}
	if len(layouts) != 5 {
		t.Errorf("%d layouts, want en, tr, de, fr, es", len(layouts))
	}
}

// Every character a layout can print, words, months and separators, is in the font: a missing glyph would print as '?'.
func TestLayoutsAreCoveredByTheFont(t *testing.T) {
	for code, l := range layouts {
		all := []string{l.decimal, l.group, l.moneyGap, strings.ReplaceAll(l.dateFmt, "%", ""), strings.Join(l.months[:], " "), "-0123456789.:/…—"}
		for _, w := range l.words {
			all = append(all, w)
		}
		for _, s := range all {
			if got := printable(s); got != s {
				t.Errorf("%s: %q would print as %q", code, s, got)
			}
		}
	}
}

func TestNumbersMoneyAndDatesGolden(t *testing.T) {
	day := time.Date(2026, 11, 2, 23, 59, 0, 0, time.UTC)
	for _, c := range []struct {
		loc                                                         string
		small, big, neg, zero, jpy, quantity, fiveDigits, date, per string
	}{
		{"en", "11.16 USD", "1,234,567.89 USD", "-50.00 USD", "0.00 USD", "1,016 JPY", "1,250.500", "12,345.00 USD", "2 November 2026", "October 2026"},
		{"tr", "11,16\u00a0USD", "1.234.567,89\u00a0USD", "-50,00\u00a0USD", "0,00\u00a0USD", "1.016\u00a0JPY", "1.250,500", "12.345,00\u00a0USD", "2 Kasım 2026", "Ekim 2026"},
		{"de", "11,16\u00a0USD", "1.234.567,89\u00a0USD", "-50,00\u00a0USD", "0,00\u00a0USD", "1.016\u00a0JPY", "1.250,500", "12.345,00\u00a0USD", "2. November 2026", "Oktober 2026"},
		{"fr", "11,16\u00a0USD", "1\u202f234\u202f567,89\u00a0USD", "-50,00\u00a0USD", "0,00\u00a0USD", "1\u202f016\u00a0JPY", "1\u202f250,500", "12\u202f345,00\u00a0USD", "2 novembre 2026", "octobre 2026"},
		// Spanish groups from five digits: 1234,56 but 12.345,67.
		{"es", "11,16\u00a0USD", "1.234.567,89\u00a0USD", "-50,00\u00a0USD", "0,00\u00a0USD", "1016\u00a0JPY", "1250,500", "12.345,00\u00a0USD", "2 de noviembre de 2026", "octubre de 2026"},
	} {
		l := layouts[c.loc]
		got := []string{l.money(1116, 2, "USD"), l.money(123456789, 2, "USD"), l.money(-5000, 2, "USD"), l.money(0, 2, "USD"), l.money(1016, 0, "JPY"), l.number("1250.500"),
			l.money(1234500, 2, "USD"), l.date(day), l.period("2026-10")}
		want := []string{c.small, c.big, c.neg, c.zero, c.jpy, c.quantity, c.fiveDigits, c.date, c.per}
		for i := range got {
			if got[i] != want[i] {
				t.Errorf("%s #%d: %q, want %q", c.loc, i, got[i], want[i])
			}
		}
	}
	// A period that is not a calendar month is printed as it is.
	if got := english.period("2026-W44"); got != "2026-W44" {
		t.Errorf("period = %q", got)
	}
}

func TestChooseFollowsCustomerThenSellersCountryThenEnglish(t *testing.T) {
	for _, c := range []struct{ customer, def, layout, requested string }{
		{"tr", "de", "tr", "tr"},
		{"", "de", "de", "de"},
		{"", "", "en", ""},
		{"xx", "tr", "tr", "xx"}, // an unknown language falls through to the seller's
		{"xx", "", "en", "xx"},
		{"de", "", "de", "de"},
		// Arabic is a language of the customer the PDF cannot print: English, and not the seller's language, but what was asked for is kept.
		{"ar", "tr", "en", "ar"},
		{"ar", "", "en", "ar"},
		{"", "ar", "en", "ar"},
	} {
		l, req := choose(c.customer, c.def)
		if l.code != c.layout || req != c.requested {
			t.Errorf("choose(%q, %q) = %s, %q; want %s, %q", c.customer, c.def, l.code, req, c.layout, c.requested)
		}
	}
}

// Each language's invoice is compared with a text kept in testdata (go test -update rewrites them; read the diff before committing). The same text is what pdftotext
// reads out of the PDF.
func TestGoldenInvoicePerLanguage(t *testing.T) {
	for _, loc := range []string{"en", "tr", "de", "fr", "es"} {
		t.Run(loc, func(t *testing.T) {
			out, err := PDF(rich(loc), Options{})
			if err != nil {
				t.Fatal(err)
			}
			got := pdfText(t, out) + "\n"
			path := filepath.Join("testdata", "invoice_"+loc+".txt")
			if *update {
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if got != string(want) {
				t.Errorf("the %s invoice changed:\n%s\nwant:\n%s", loc, got, want)
			}
			if kw := info(t, out, "Keywords"); kw != "locale="+loc+" layout="+loc {
				t.Errorf("keywords = %q", kw)
			}
			again, _ := PDF(rich(loc), Options{})
			if !bytes.Equal(out, again) {
				t.Error("rendering twice gave different bytes")
			}
		})
	}
}

// An Arabic customer gets the English layout; the language that was asked for is recorded in the document, so a later layout for Arabic can find what was owed.
func TestArabicCustomerGetsEnglishAndTheRequestIsRecorded(t *testing.T) {
	ar, err := PDF(rich("ar"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	en, _ := PDF(rich("en"), Options{})
	if pdfText(t, ar) != pdfText(t, en) {
		t.Errorf("the Arabic customer's invoice is not the English layout:\n%s", pdfText(t, ar))
	}
	if kw := info(t, ar, "Keywords"); kw != "locale=ar layout=en" {
		t.Errorf("keywords = %q, want locale=ar layout=en", kw)
	}
}

func TestSellersCountryLanguageIsTheFallback(t *testing.T) {
	s := rich("")
	s.DefaultLocale = "de"
	out, _ := PDF(s, Options{})
	if text := pdfText(t, out); !strings.Contains(text, "RECHNUNG") || !strings.Contains(text, "Gesamtbetrag") {
		t.Errorf("no customer language, German seller's country: %s", text)
	}
	if kw := info(t, out, "Keywords"); kw != "locale=de layout=de" {
		t.Errorf("keywords = %q", kw)
	}
	s.Locale = "fr"
	out, _ = PDF(s, Options{})
	if !strings.Contains(pdfText(t, out), "FACTURE") {
		t.Error("the customer's own language beats the fallback")
	}
	if out, _ := PDF(snapshot(), Options{}); !strings.Contains(pdfText(t, out), "INVOICE") {
		t.Error("with nothing asked for, English")
	}
}

func TestTaxIdentityLines(t *testing.T) {
	s := rich("en")
	s.SellerTaxId, s.CustomerTaxId, s.CustomerTaxOffice = "1234567890", "10000000146", "Berlin Mitte"
	text := pdfText(t, mustPDF(t, s))
	for _, want := range []string{"VKN (tax number): 1234567890", "Tax office: Kadıköy", "TCKN (national ID): 10000000146", "Tax office: Berlin Mitte"} {
		if !strings.Contains(text, want) {
			t.Errorf("the PDF lacks %q:\n%s", want, text)
		}
	}
	s.CustomerTaxId, s.CustomerTaxOffice = "DE136695976", ""
	if text := pdfText(t, mustPDF(t, s)); !strings.Contains(text, "VAT ID: DE136695976") || strings.Contains(text, "Berlin Mitte") {
		t.Errorf("a VAT id: %s", text)
	}
	// An id that no checksum knows is labelled for what it is not claimed to be; no id, no line.
	s.CustomerTaxId = "US-123"
	s.SellerTaxId, s.SellerTaxOffice = "", ""
	text = pdfText(t, mustPDF(t, s))
	if !strings.Contains(text, "Tax ID: US-123") || strings.Contains(text, "VKN") || strings.Contains(text, "Tax office") {
		t.Errorf("an unrecognised id: %s", text)
	}
	if text := pdfText(t, mustPDF(t, snapshot())); strings.Contains(text, "Tax ") {
		t.Errorf("a party without a tax id has no tax line: %s", text)
	}
}

// The line about the legal e-document is for a Turkish seller whose tenant e-invoices, and nobody else.
func TestEDocumentLineIsForTurkishSellersThatEInvoice(t *testing.T) {
	has := func(s *commandsv1.InvoiceSnapshot) bool {
		return strings.Contains(pdfText(t, mustPDF(t, s)), "e-Arşiv")
	}
	s := rich("en")
	if !has(s) {
		t.Error("a Turkish seller that e-invoices: the line is missing")
	}
	s.EInvoiceOn = "finalized"
	if !has(s) {
		t.Error("finalized triggers it too")
	}
	for _, on := range []string{"off", ""} {
		s.EInvoiceOn = on
		if has(s) {
			t.Errorf("e_invoice_on %q must print no e-document line", on)
		}
	}
	s.EInvoiceOn, s.SellerCountry = "paid", "DE"
	if has(s) {
		t.Error("no such line for a German seller")
	}
}

func TestLongDescriptionIsCutNotOverlapping(t *testing.T) {
	s := snapshot()
	s.Lines[0].Description = "Base fee (monthly) — professional_plan_with_a_very_long_code — 2026-10-01 to 2026-10-31 — extra words to run over"
	text := pdfText(t, mustPDF(t, s))
	if !strings.Contains(text, "Base fee (monthly)") || !strings.Contains(text, "…") || strings.Contains(text, "extra words") {
		t.Errorf("a long description must end in an ellipsis: %s", text)
	}
}

func mustPDF(t *testing.T, s *commandsv1.InvoiceSnapshot) []byte {
	t.Helper()
	out, err := PDF(s, Options{})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
