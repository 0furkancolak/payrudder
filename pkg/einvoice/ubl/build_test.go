package ubl_test

import (
	"bytes"
	"encoding/xml"
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/0furkancolak/payrudder/pkg/einvoice"
	"github.com/0furkancolak/payrudder/pkg/einvoice/ubl"
)

var update = flag.Bool("update", false, "rewrite the golden files")

func d(y int, m time.Month, day int) time.Time { return time.Date(y, m, day, 10, 30, 0, 0, time.UTC) }

func seller() einvoice.Party {
	return einvoice.Party{Name: "Acme Yazılım A.Ş.", TaxID: "1234567890", TaxOffice: "Kadıköy", RegistrationNo: "0123456789012345", TradeRegistryNo: "98765",
		Street: "Bağdat Cad. 1", District: "Kadıköy", City: "İstanbul", PostalCode: "34700", Country: "TR", Email: "billing@acme.example",
		EndpointScheme: "0088", EndpointID: "5790000435975"}
}

func trBuyer() einvoice.Party {
	return einvoice.Party{Name: "Müşteri & Oğul Ltd. \"Şti\"", TaxID: "9876543210", TaxOffice: "Çankaya", Street: "Atatürk Blv. 5", District: "Çankaya", City: "Ankara", Country: "TR", EInvoiceRegistered: true}
}

func lines() []einvoice.Line {
	return []einvoice.Line{
		{No: 1, Kind: einvoice.LineItem, Description: "Base fee", AmountMinor: 100000, ServiceFrom: d(2026, 3, 1), ServiceTo: d(2026, 3, 31)},
		{No: 2, Kind: einvoice.LineItem, Description: "API calls", Quantity: "1250.000", UnitLabel: "requests", AmountMinor: 12500},
		{No: 3, Kind: einvoice.LineItem, Description: "Storage", Quantity: "3", UnitLabel: "GB", AmountMinor: 1500},
		{No: 4, Kind: einvoice.LineDiscount, Description: "Launch coupon", AmountMinor: 11350},
		{No: 5, Kind: einvoice.LineTax, Description: "KDV 20%", AmountMinor: 20530},
		{No: 6, Kind: einvoice.LineCredit, Description: "Credit balance", AmountMinor: 5000},
	}
}

func invoice(t einvoice.DocumentType, buyer einvoice.Party) einvoice.Document {
	return einvoice.Document{Kind: einvoice.KindInvoice, Type: t, UUID: "3f1c2a52-9c3b-5e08-b6a1-2d8e0f5c7a93", Number: "INV-2026-000123", IssueDate: d(2026, 4, 1),
		Currency: "TRY", Exponent: 2, Seller: seller(), Buyer: buyer, Lines: lines()}
}

func creditNote(t einvoice.DocumentType, buyer einvoice.Party) einvoice.Document {
	return einvoice.Document{Kind: einvoice.KindCreditNote, Type: t, UUID: "8a0e6b3d-1c2f-5d4e-9a7b-6c5d4e3f2a10", Number: "CN-2026-000007", IssueDate: d(2026, 4, 9),
		Currency: "EUR", Exponent: 2, Seller: seller(), Buyer: buyer, Reason: "Service outage", Reference: &einvoice.Reference{Number: "ABC2026000000042", IssueDate: d(2026, 4, 1)},
		Lines: []einvoice.Line{
			{No: 1, Kind: einvoice.LineItem, Description: "Base fee (credited)", AmountMinor: 5000},
			{No: 2, Kind: einvoice.LineTax, Description: "VAT 19%", AmountMinor: 950},
		}}
}

func euBuyer() einvoice.Party {
	return einvoice.Party{Name: "Beispiel GmbH", TaxID: "DE123456789", Street: "Hauptstr. 1", City: "Berlin", Region: "Berlin", PostalCode: "10115", Country: "DE", Email: "ap@beispiel.example",
		EndpointScheme: "0088", EndpointID: "4012345000009"}
}

func cases() map[string]einvoice.Document {
	trCN := creditNote(einvoice.TypeEArsiv, einvoice.Party{Name: "Ayşe Yılmaz", TaxID: "11111111111", TaxOffice: "-", Street: "Kızılay Sk. 2", District: "Çankaya", City: "Ankara", Country: "TR"})
	trCN.Currency = "TRY"
	return map[string]einvoice.Document{
		"ubl-tr-efatura-invoice": invoice(einvoice.TypeEFatura, trBuyer()),
		"ubl-tr-earsiv-credit":   trCN,
		"peppol-invoice":         peppolInvoice(),
		"peppol-credit":          creditNote(einvoice.TypePeppol, euBuyer()),
	}
}

func peppolInvoice() einvoice.Document {
	doc := invoice(einvoice.TypePeppol, euBuyer())
	doc.Currency = "EUR"
	doc.Lines[4].Description = "VAT 19%"
	doc.Lines[4].AmountMinor = 19504
	return doc
}

// extraCases are documents the golden files do not hold but the official rules should see too: no tax line (exempt), a currency without minor units, a
// credit note over the e-fatura channel.
func extraCases() map[string]einvoice.Document {
	exempt := func(typ einvoice.DocumentType, buyer einvoice.Party) einvoice.Document {
		doc := invoice(typ, buyer)
		doc.Lines = []einvoice.Line{{No: 1, Kind: einvoice.LineItem, Description: "Fee", AmountMinor: 1000}}
		return doc
	}
	jpy := invoice(einvoice.TypePeppol, euBuyer())
	jpy.Currency, jpy.Exponent = "JPY", 0
	jpy.Lines = []einvoice.Line{{No: 1, Kind: einvoice.LineItem, Description: "Fee", AmountMinor: 1500}, {No: 2, Kind: einvoice.LineTax, Description: "VAT 10%", AmountMinor: 150}}
	trCN := creditNote(einvoice.TypeEFatura, trBuyer())
	trCN.Currency = "TRY"
	return map[string]einvoice.Document{
		"peppol-exempt": exempt(einvoice.TypePeppol, euBuyer()), "peppol-jpy": jpy,
		"ubl-tr-exempt": exempt(einvoice.TypeEFatura, trBuyer()), "ubl-tr-efatura-credit": trCN,
	}
}

// TestWriteSamples writes every case, golden or not, to $EINVOICE_SAMPLES_DIR for scripts/einvoice-validate.sh, which checks them against the official XSD and
// schematron. Without the variable it does nothing.
func TestWriteSamples(t *testing.T) {
	dir := os.Getenv("EINVOICE_SAMPLES_DIR")
	if dir == "" {
		t.Skip("EINVOICE_SAMPLES_DIR is not set")
	}
	all := cases()
	for name, doc := range extraCases() {
		all[name] = doc
	}
	for name, doc := range all {
		out, err := ubl.Build(doc)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name+".xml"), out, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestGolden(t *testing.T) {
	for name, doc := range cases() {
		t.Run(name, func(t *testing.T) {
			got, err := ubl.Build(doc)
			if err != nil {
				t.Fatal(err)
			}
			again, _ := ubl.Build(doc)
			if !bytes.Equal(got, again) {
				t.Fatal("not deterministic")
			}
			path := filepath.Join("testdata", name+".xml")
			if *update {
				if err := os.WriteFile(path, got, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("%v (run with -update)", err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("output differs from %s (run with -update to accept):\n%s", path, got)
			}
		})
	}
}

// node is a generic XML tree for structural checks.
type node struct {
	Name     string
	Attrs    map[string]string
	Text     string
	Children []*node
}

func parse(t *testing.T, b []byte) *node {
	t.Helper()
	dec := xml.NewDecoder(bytes.NewReader(b))
	var stack []*node
	var root *node
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("not well-formed: %v", err)
		}
		switch e := tok.(type) {
		case xml.StartElement:
			n := &node{Name: e.Name.Local, Attrs: map[string]string{}}
			for _, a := range e.Attr {
				n.Attrs[a.Name.Local] = a.Value
			}
			if len(stack) > 0 {
				p := stack[len(stack)-1]
				p.Children = append(p.Children, n)
			} else {
				root = n
			}
			stack = append(stack, n)
		case xml.EndElement:
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if len(stack) > 0 {
				stack[len(stack)-1].Text += string(e)
			}
		}
	}
	return root
}

func (n *node) child(name string) *node {
	for _, c := range n.Children {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func (n *node) all(name string) []*node {
	var out []*node
	for _, c := range n.Children {
		if c.Name == name {
			out = append(out, c)
		}
	}
	return out
}

func (n *node) text(path ...string) string {
	cur := n
	for _, p := range path {
		if cur = cur.child(p); cur == nil {
			return ""
		}
	}
	return strings.TrimSpace(cur.Text)
}

func cents(t *testing.T, s string) int64 {
	t.Helper()
	if strings.ContainsAny(s, "eE") || strings.Count(s, ".") != 1 || len(s)-strings.Index(s, ".")-1 != 2 {
		t.Fatalf("amount %q is not a plain 2-decimal number", s)
	}
	var v int64
	neg := strings.HasPrefix(s, "-")
	for _, c := range strings.TrimPrefix(strings.Replace(s, ".", "", 1), "-") {
		v = v*10 + int64(c-'0')
	}
	if neg {
		v = -v
	}
	return v
}

func TestStructure(t *testing.T) {
	for name, doc := range cases() {
		t.Run(name, func(t *testing.T) {
			out, err := ubl.Build(doc)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.HasPrefix(out, []byte(`<?xml version="1.0" encoding="UTF-8"?>`)) {
				t.Fatal("no XML declaration")
			}
			root := parse(t, out)
			wantRoot := "Invoice"
			if doc.Type == einvoice.TypePeppol && doc.Kind == einvoice.KindCreditNote {
				wantRoot = "CreditNote"
			}
			if root.Name != wantRoot {
				t.Fatalf("root %s", root.Name)
			}
			for _, ns := range []string{"urn:oasis:names:specification:ubl:schema:xsd:CommonAggregateComponents-2", "urn:oasis:names:specification:ubl:schema:xsd:CommonBasicComponents-2"} {
				if !bytes.Contains(out, []byte(ns)) {
					t.Errorf("namespace %s missing", ns)
				}
			}
			for _, e := range []string{"CustomizationID", "ProfileID", "ID", "UUID", "IssueDate", "DocumentCurrencyCode", "AccountingSupplierParty", "AccountingCustomerParty", "TaxTotal", "LegalMonetaryTotal"} {
				if root.child(e) == nil {
					t.Errorf("element %s missing", e)
				}
			}
			if root.text("UUID") != doc.UUID || root.text("ID") != doc.Number {
				t.Error("identity wrong")
			}
			lm := root.child("LegalMonetaryTotal")
			ext, excl, incl := cents(t, lm.text("LineExtensionAmount")), cents(t, lm.text("TaxExclusiveAmount")), cents(t, lm.text("TaxInclusiveAmount"))
			var allow, prepaid int64
			if s := lm.text("AllowanceTotalAmount"); s != "" {
				allow = cents(t, s)
			}
			if s := lm.text("PrepaidAmount"); s != "" {
				prepaid = cents(t, s)
			}
			pay := cents(t, lm.text("PayableAmount"))
			tax := cents(t, root.text("TaxTotal", "TaxAmount"))
			if ext-allow != excl || excl+tax != incl || incl-prepaid != pay {
				t.Errorf("totals do not add up: ext %d allow %d excl %d tax %d incl %d prepaid %d pay %d", ext, allow, excl, tax, incl, prepaid, pay)
			}
			var subs int64
			for _, s := range root.child("TaxTotal").all("TaxSubtotal") {
				subs += cents(t, s.text("TaxAmount"))
			}
			if subs != tax {
				t.Errorf("subtotals %d != tax %d", subs, tax)
			}
			lineTag := "InvoiceLine"
			if wantRoot == "CreditNote" {
				lineTag = "CreditNoteLine"
			}
			var lsum, ltax int64
			for _, l := range root.all(lineTag) {
				lsum += cents(t, l.text("LineExtensionAmount"))
				if tt := l.child("TaxTotal"); tt != nil {
					ltax += cents(t, tt.text("TaxAmount"))
				}
			}
			if lsum != ext {
				t.Errorf("lines sum %d != %d", lsum, ext)
			}
			if doc.Type != einvoice.TypePeppol && ltax != tax {
				t.Errorf("line taxes %d != %d", ltax, tax)
			}
			if doc.Kind == einvoice.KindCreditNote && root.child("BillingReference") == nil {
				t.Error("no BillingReference")
			}
			if strings.Contains(string(out), "e+") || strings.Contains(string(out), "NaN") {
				t.Error("float artefact")
			}
		})
	}
}

func TestTRSpecifics(t *testing.T) {
	out, _ := ubl.Build(invoice(einvoice.TypeEFatura, trBuyer()))
	root := parse(t, out)
	if root.text("ProfileID") != "TEMELFATURA" || root.text("InvoiceTypeCode") != "SATIS" || root.text("CustomizationID") != "TR1.2" {
		t.Error("TR header wrong")
	}
	s := string(out)
	for _, want := range []string{"Müşteri &amp; Oğul Ltd. &quot;Şti&quot;", "Kadıköy", `schemeID="VKN"`, `unitCode="E34"`, "<cbc:Percent>20.00</cbc:Percent>", "<cbc:PriceAmount currencyID=\"TRY\">0.10</cbc:PriceAmount>"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q", want)
		}
	}
	cn, _ := ubl.Build(cases()["ubl-tr-earsiv-credit"])
	r := parse(t, cn)
	if r.text("InvoiceTypeCode") != "IADE" || r.text("ProfileID") != "EARSIVFATURA" || !strings.Contains(string(cn), `schemeID="TCKN"`) {
		t.Error("e-arsiv credit wrong")
	}
}

func TestPeppolSpecifics(t *testing.T) {
	out, _ := ubl.Build(cases()["peppol-credit"])
	root := parse(t, out)
	if root.Name != "CreditNote" || root.text("CreditNoteTypeCode") != "381" || !strings.HasPrefix(root.text("CustomizationID"), "urn:cen.eu:en16931:2017") {
		t.Error("peppol credit wrong")
	}
	if !strings.Contains(string(out), "CreditedQuantity") || strings.Contains(string(out), "UBLExtensions") {
		t.Error("peppol credit structure wrong")
	}
	inv, _ := ubl.Build(cases()["peppol-invoice"])
	if r := parse(t, inv); r.text("InvoiceTypeCode") != "380" {
		t.Error("380 expected")
	}
}

func TestExemptWithoutTaxLine(t *testing.T) {
	doc := invoice(einvoice.TypePeppol, euBuyer())
	doc.Lines = []einvoice.Line{{Kind: einvoice.LineItem, Description: "Fee", AmountMinor: 1000}}
	out, err := ubl.Build(doc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "<cbc:ID>E</cbc:ID>") || !strings.Contains(string(out), "VAT not charged") {
		t.Error("exempt category missing")
	}
	doc.Type = einvoice.TypeEFatura
	doc.Buyer = trBuyer()
	out, _ = ubl.Build(doc)
	if !strings.Contains(string(out), "<cbc:TaxExemptionReasonCode>351</cbc:TaxExemptionReasonCode>") {
		t.Error("TR exemption code missing")
	}
}

func TestZeroDecimalCurrency(t *testing.T) {
	doc := invoice(einvoice.TypePeppol, euBuyer())
	doc.Currency, doc.Exponent = "JPY", 0
	doc.Lines = []einvoice.Line{{Kind: einvoice.LineItem, Description: "Fee", AmountMinor: 1500}, {Kind: einvoice.LineTax, Description: "VAT 10%", AmountMinor: 150}}
	out, err := ubl.Build(doc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `<cbc:PayableAmount currencyID="JPY">1650</cbc:PayableAmount>`) {
		t.Errorf("JPY amounts wrong:\n%s", out)
	}
}

func TestTotals(t *testing.T) {
	s, err := ubl.Totals(invoice(einvoice.TypeEFatura, trBuyer()))
	if err != nil {
		t.Fatal(err)
	}
	// 100000+12500+1500 = 114000; - 11350 = 102650; +20530 = 123180; -5000 = 118180. 20530/102650 = 20.00 %.
	want := ubl.Sums{LineExtension: 114000, Allowance: 11350, TaxExclusive: 102650, TaxAmount: 20530, TaxInclusive: 123180, Prepaid: 5000, Payable: 118180, HasTax: true, TaxRateBps: 2000, TaxName: "KDV"}
	if s != want {
		t.Errorf("got %+v want %+v", s, want)
	}
	// Rate derived when the label has none.
	doc := invoice(einvoice.TypeEFatura, trBuyer())
	doc.Lines[4].Description = "Tax"
	s, _ = ubl.Totals(doc)
	if s.TaxRateBps != 2000 || s.TaxName != "Tax" && s.TaxName != "VAT" {
		t.Errorf("derived rate %d name %q", s.TaxRateBps, s.TaxName)
	}
	doc.Lines[4].Description = "VAT 7.5%"
	if s, _ = ubl.Totals(doc); s.TaxRateBps != 750 {
		t.Errorf("rate %d", s.TaxRateBps)
	}
}

func TestValidation(t *testing.T) {
	mutate := map[string]func(*einvoice.Document){
		"type":         func(d *einvoice.Document) { d.Type = "x" },
		"currency":     func(d *einvoice.Document) { d.Currency = "try" },
		"no lines":     func(d *einvoice.Document) { d.Lines = nil },
		"seller name":  func(d *einvoice.Document) { d.Seller.Name = " " },
		"seller tax":   func(d *einvoice.Document) { d.Seller.TaxID = "" },
		"buyer name":   func(d *einvoice.Document) { d.Buyer.Name = "" },
		"buyer tax TR": func(d *einvoice.Document) { d.Buyer.TaxID = "" },
		"country":      func(d *einvoice.Document) { d.Buyer.Country = "Turkey" },
		"credit no ref": func(d *einvoice.Document) {
			d.Kind = einvoice.KindCreditNote
			d.Reference = nil
		},
		"discount too big": func(d *einvoice.Document) { d.Lines[3].AmountMinor = 1 << 40 },
		"no items":         func(d *einvoice.Document) { d.Lines = d.Lines[3:] },
		"negative":         func(d *einvoice.Document) { d.Lines[0].AmountMinor = -1 },
	}
	for name, m := range mutate {
		t.Run(name, func(t *testing.T) {
			doc := invoice(einvoice.TypeEFatura, trBuyer())
			doc.Lines = append([]einvoice.Line(nil), doc.Lines...)
			m(&doc)
			_, err := ubl.Build(doc)
			var ie *ubl.InvalidError
			if !errors.Is(err, ubl.ErrInvalid) || !errors.As(err, &ie) {
				t.Fatalf("want InvalidError, got %v", err)
			}
		})
	}
	// A Peppol buyer needs no tax id.
	doc := invoice(einvoice.TypePeppol, euBuyer())
	doc.Buyer.TaxID = ""
	if _, err := ubl.Build(doc); err != nil {
		t.Errorf("peppol buyer without tax id: %v", err)
	}
}

// What the official rules (scripts/einvoice-validate.sh: EN 16931, Peppol BIS 3, GIB's UBL-TR XSD and schematron) turned up, pinned so a change that brings the
// old output back fails here without Java.
func TestPeppolRuleFixes(t *testing.T) {
	out, err := ubl.Build(cases()["peppol-invoice"])
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{
		"urn:fdc:peppol.eu:2017:poacc:billing:3.0",                    // PEPPOL-EN16931-R004: poacc, not poid
		"<cbc:ProfileID>urn:fdc:peppol.eu:2017:poacc:billing:01:1.0<", // R007
		`<cbc:PaymentMeansCode name="Mutually defined">ZZZ<`,          // BR-CL-16
		"<cbc:CompanyID>TR1234567890</cbc:CompanyID>",                 // BR-CO-09: the seller's VAT id gets its country prefix
		"<cbc:CompanyID>DE123456789</cbc:CompanyID>",                  // ... and one that has it keeps it
		`<cbc:EndpointID schemeID="0088">5790000435975</cbc:EndpointID>`,
		"<cbc:CountrySubentity>Berlin</cbc:CountrySubentity>", // the UBL name; CountrySubdivisionName does not exist
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(s, "<cbc:BaseAmount") || strings.Contains(s, `schemeID="EM"`) {
		t.Error("PEPPOL-EN16931-R042 / CL008: a base amount without a percentage, or an e-mail endpoint, is back")
	}
	// Without a participant id no endpoint is invented.
	doc := cases()["peppol-invoice"]
	doc.Seller.EndpointID, doc.Buyer.EndpointScheme = "", ""
	out, _ = ubl.Build(doc)
	if strings.Contains(string(out), "EndpointID") {
		t.Error("an endpoint was written without a participant id")
	}
}

func TestTRRuleFixes(t *testing.T) {
	out, err := ubl.Build(cases()["ubl-tr-efatura-invoice"])
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{
		"<cac:Signature>", `<cbc:URI>#Signature</cbc:URI>`, // UBL-TR's XSD wants the signature block before the seller
		"<cbc:CitySubdivisionName>Kadıköy</cbc:CitySubdivisionName>", // and the district in every address
		"<cbc:PayableAmount currencyID=\"TRY\">1231.80</cbc:PayableAmount>",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(s, "PrepaidAmount") || strings.Contains(s, "InvoicePeriod") { // the fixture has a credit line and a service period on line 1
		t.Error("the UBL-TR XSD has neither PrepaidAmount nor a period on a line")
	}
	cn, _ := ubl.Build(cases()["ubl-tr-earsiv-credit"])
	c := string(cn)
	for _, want := range []string{"<cbc:DocumentTypeCode>IADE</cbc:DocumentTypeCode>", "<cbc:FirstName>Ayşe</cbc:FirstName>", "<cbc:FamilyName>Yılmaz</cbc:FamilyName>"} {
		if !strings.Contains(c, want) {
			t.Errorf("credit note missing %q", want)
		}
	}
	// The surname is the last word; a single word has no family name to give.
	for name, want := range map[string][2]string{"Ayşe Nur Yılmaz": {"Ayşe Nur", "Yılmaz"}, "Madonna": {"Madonna", "-"}} {
		doc := cases()["ubl-tr-earsiv-credit"]
		doc.Buyer.Name = name
		got, _ := ubl.Build(doc)
		if !strings.Contains(string(got), "<cbc:FirstName>"+want[0]+"</cbc:FirstName>") || !strings.Contains(string(got), "<cbc:FamilyName>"+want[1]+"</cbc:FamilyName>") {
			t.Errorf("%q split wrongly:\n%s", name, got)
		}
	}
}
