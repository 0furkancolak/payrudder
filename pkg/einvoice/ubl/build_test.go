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
		Street: "Bağdat Cad. 1", City: "İstanbul", PostalCode: "34700", Country: "TR", Email: "billing@acme.example"}
}

func trBuyer() einvoice.Party {
	return einvoice.Party{Name: "Müşteri & Oğul Ltd. \"Şti\"", TaxID: "9876543210", TaxOffice: "Çankaya", Street: "Atatürk Blv. 5", City: "Ankara", Country: "TR", EInvoiceRegistered: true}
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
	return einvoice.Party{Name: "Beispiel GmbH", TaxID: "DE123456789", Street: "Hauptstr. 1", City: "Berlin", PostalCode: "10115", Country: "DE", Email: "ap@beispiel.example"}
}

func cases() map[string]einvoice.Document {
	trCN := creditNote(einvoice.TypeEArsiv, einvoice.Party{Name: "Ayşe Yılmaz", TaxID: "11111111111", TaxOffice: "-", Country: "TR"})
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
