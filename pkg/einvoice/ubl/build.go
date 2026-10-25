// Package ubl renders an einvoice.Document as UBL 2.1: the UBL-TR 1.2 customisation for Turkey (e-fatura, e-arşiv) and EN 16931 / Peppol BIS Billing 3.0
// for the EU. The output is deterministic: the same document gives the same bytes (no clock, no map iteration, integer arithmetic only).
//
// UNVERIFIED: the output is checked structurally (well-formed, UBL 2.1 element order for the parts emitted, namespaces, arithmetic) but has not been run
// through the official UBL-TR 1.2 or Peppol BIS 3 schematron packages.
package ubl

import (
	"math/big"
	"regexp"
	"strings"

	"github.com/0furkancolak/quotify/pkg/einvoice"
)

//nolint:gosec // XML namespace URIs, not credentials
const (
	nsInvoice    = "urn:oasis:names:specification:ubl:schema:xsd:Invoice-2"
	nsCreditNote = "urn:oasis:names:specification:ubl:schema:xsd:CreditNote-2"
	nsCAC        = "urn:oasis:names:specification:ubl:schema:xsd:CommonAggregateComponents-2"
	nsCBC        = "urn:oasis:names:specification:ubl:schema:xsd:CommonBasicComponents-2"
	nsEXT        = "urn:oasis:names:specification:ubl:schema:xsd:CommonExtensionComponents-2"

	peppolCustomization = "urn:cen.eu:en16931:2017#compliant#urn:fdc:peppol.eu:2017:poid:3.0"
	peppolProfile       = "urn:fdc:peppol.eu:2017:poid:billing:01:1.0"
)

// Build renders doc for doc.Type.
func Build(doc einvoice.Document) ([]byte, error) {
	if err := validate(doc); err != nil {
		return nil, err
	}
	sums, err := Totals(doc)
	if err != nil {
		return nil, err
	}
	b := &builder{doc: doc, sums: sums, tr: doc.Type != einvoice.TypePeppol}
	b.w.b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.write()
	return []byte(b.w.b.String()), nil
}

var (
	currencyRe = regexp.MustCompile(`^[A-Z]{3}$`)
	countryRe  = regexp.MustCompile(`^[A-Z]{2}$`)
	qtyRe      = regexp.MustCompile(`^\d+(\.\d+)?$`)
)

func validate(d einvoice.Document) error {
	switch {
	case !d.Type.Valid():
		return invalid("type", "unknown document type %q", d.Type)
	case d.Kind != einvoice.KindInvoice && d.Kind != einvoice.KindCreditNote:
		return invalid("kind", "unknown kind %q", d.Kind)
	case d.UUID == "":
		return invalid("uuid", "is required")
	case d.Number == "":
		return invalid("number", "is required")
	case d.IssueDate.IsZero():
		return invalid("issue_date", "is required")
	case !currencyRe.MatchString(d.Currency):
		return invalid("currency", "must be an ISO 4217 code")
	case d.Exponent < 0 || d.Exponent > 4:
		return invalid("exponent", "must be 0..4")
	case len(d.Lines) == 0:
		return invalid("lines", "at least one line is required")
	case d.Kind == einvoice.KindCreditNote && d.Reference == nil:
		return invalid("reference", "a credit note refers to its invoice")
	}
	tr := d.Type != einvoice.TypePeppol
	for _, p := range []struct {
		field string
		p     einvoice.Party
		tax   bool
	}{{"seller", d.Seller, true}, {"buyer", d.Buyer, tr}} {
		if strings.TrimSpace(p.p.Name) == "" {
			return invalid(p.field+".name", "is required")
		}
		if p.tax && strings.TrimSpace(p.p.TaxID) == "" {
			return invalid(p.field+".tax_id", "is required")
		}
		if !countryRe.MatchString(p.p.Country) {
			return invalid(p.field+".country", "must be an ISO 3166-1 alpha-2 code")
		}
	}
	return nil
}

type builder struct {
	doc  einvoice.Document
	sums Sums
	tr   bool
	w    writer
}

func (b *builder) money(v int64) string { return formatMinor(v, b.doc.Exponent) }

func (b *builder) amount(tag string, v int64) { b.w.el(tag, b.money(v), "currencyID", b.doc.Currency) }

func (b *builder) credit() bool { return b.doc.Kind == einvoice.KindCreditNote && !b.tr }

func (b *builder) write() {
	root, ns := "Invoice", nsInvoice
	if b.credit() {
		root, ns = "CreditNote", nsCreditNote
	}
	attrs := []string{"xmlns", ns, "xmlns:cac", nsCAC, "xmlns:cbc", nsCBC}
	if b.tr {
		attrs = append(attrs, "xmlns:ext", nsEXT)
	}
	b.w.open(root, attrs...)
	if b.tr {
		b.w.open("ext:UBLExtensions")
		b.w.open("ext:UBLExtension")
		b.w.empty("ext:ExtensionContent") // the integrator puts the e-seal here
		b.w.close("ext:UBLExtension")
		b.w.close("ext:UBLExtensions")
		b.w.el("cbc:UBLVersionID", "2.1")
		b.w.el("cbc:CustomizationID", "TR1.2")
		profile := "TEMELFATURA"
		if b.doc.Type == einvoice.TypeEArsiv {
			profile = "EARSIVFATURA"
		}
		b.w.el("cbc:ProfileID", profile)
	} else {
		b.w.el("cbc:CustomizationID", peppolCustomization)
		b.w.el("cbc:ProfileID", peppolProfile)
	}
	b.w.el("cbc:ID", b.doc.Number)
	if b.tr {
		b.w.el("cbc:CopyIndicator", "false")
	}
	b.w.el("cbc:UUID", b.doc.UUID)
	issue := b.doc.IssueDate
	b.w.el("cbc:IssueDate", issue.Format("2006-01-02"))
	if b.tr {
		b.w.el("cbc:IssueTime", issue.Format("15:04:05"))
	}
	switch {
	case b.credit():
		b.w.el("cbc:CreditNoteTypeCode", "381")
	case b.tr && b.doc.Kind == einvoice.KindCreditNote:
		b.w.el("cbc:InvoiceTypeCode", "IADE")
	case b.tr:
		b.w.el("cbc:InvoiceTypeCode", "SATIS")
	default:
		b.w.el("cbc:InvoiceTypeCode", "380")
	}
	for _, n := range b.notes() {
		b.w.el("cbc:Note", n)
	}
	b.w.el("cbc:DocumentCurrencyCode", b.doc.Currency)
	if b.tr {
		b.w.el("cbc:LineCountNumeric", itoa(b.itemCount()))
	} else {
		b.w.el("cbc:BuyerReference", b.doc.Number)
	}
	if b.doc.Reference != nil {
		b.w.open("cac:BillingReference")
		b.w.open("cac:InvoiceDocumentReference")
		b.w.el("cbc:ID", b.doc.Reference.Number)
		if !b.doc.Reference.IssueDate.IsZero() {
			b.w.el("cbc:IssueDate", b.doc.Reference.IssueDate.Format("2006-01-02"))
		}
		b.w.close("cac:InvoiceDocumentReference")
		b.w.close("cac:BillingReference")
	}
	if b.doc.Type == einvoice.TypeEArsiv {
		b.w.open("cac:AdditionalDocumentReference") // UNVERIFIED: e-arşiv sending type
		b.w.el("cbc:ID", "ELEKTRONIK")
		b.w.el("cbc:IssueDate", issue.Format("2006-01-02"))
		b.w.el("cbc:DocumentTypeCode", "SendingType")
		b.w.close("cac:AdditionalDocumentReference")
	}
	b.party("cac:AccountingSupplierParty", b.doc.Seller)
	b.party("cac:AccountingCustomerParty", b.doc.Buyer)
	if !b.tr {
		b.w.open("cac:PaymentMeans")
		b.w.el("cbc:PaymentMeansCode", "ZZ", "name", "Mutually defined") // UNVERIFIED: BR-49 needs a code; the payment is collected by Payrudder's provider
		b.w.close("cac:PaymentMeans")
	}
	if b.sums.Allowance > 0 {
		b.w.open("cac:AllowanceCharge")
		b.w.el("cbc:ChargeIndicator", "false")
		if !b.tr {
			b.w.el("cbc:AllowanceChargeReasonCode", "95") // discount
		}
		b.w.el("cbc:AllowanceChargeReason", "Discount")
		b.amount("cbc:Amount", b.sums.Allowance)
		b.amount("cbc:BaseAmount", b.sums.LineExtension)
		if !b.tr {
			b.taxCategory("cac:TaxCategory", true)
		}
		b.w.close("cac:AllowanceCharge")
	}
	b.taxTotal()
	b.legalTotals()
	b.lines()
	b.w.close(root)
}

func (b *builder) itemCount() int {
	n := 0
	for _, l := range b.doc.Lines {
		if l.Kind == einvoice.LineItem {
			n++
		}
	}
	return n
}

func (b *builder) notes() []string {
	var n []string
	if b.doc.Sandbox {
		n = append(n, "SANDBOX - this is a test document and has no legal effect")
	}
	if b.doc.Reason != "" {
		n = append(n, b.doc.Reason)
	}
	if b.doc.Kind == einvoice.KindCreditNote && b.tr {
		n = append(n, "Credit note (iade) for "+b.doc.Reference.Number)
	}
	return n
}

var countryNames = map[string]string{"TR": "Türkiye", "DE": "Germany", "FR": "France", "NL": "Netherlands", "IT": "Italy", "ES": "Spain", "GB": "United Kingdom", "US": "United States"}

func (b *builder) party(tag string, p einvoice.Party) {
	b.w.open(tag)
	b.w.open("cac:Party")
	if !b.tr && p.Email != "" {
		b.w.el("cbc:EndpointID", p.Email, "schemeID", "EM") // UNVERIFIED: the real endpoint is the Peppol participant id
	}
	if b.tr {
		scheme := "VKN"
		if len(p.TaxID) == 11 {
			scheme = "TCKN"
		}
		b.w.open("cac:PartyIdentification")
		b.w.el("cbc:ID", p.TaxID, "schemeID", scheme)
		b.w.close("cac:PartyIdentification")
		if p.RegistrationNo != "" {
			b.w.open("cac:PartyIdentification")
			b.w.el("cbc:ID", p.RegistrationNo, "schemeID", "MERSISNO")
			b.w.close("cac:PartyIdentification")
		}
		if p.TradeRegistryNo != "" {
			b.w.open("cac:PartyIdentification")
			b.w.el("cbc:ID", p.TradeRegistryNo, "schemeID", "TICARETSICILNO")
			b.w.close("cac:PartyIdentification")
		}
	} else if p.RegistrationNo != "" {
		b.w.open("cac:PartyIdentification")
		b.w.el("cbc:ID", p.RegistrationNo)
		b.w.close("cac:PartyIdentification")
	}
	b.w.open("cac:PartyName")
	b.w.el("cbc:Name", p.Name)
	b.w.close("cac:PartyName")
	b.w.open("cac:PostalAddress")
	if p.Street != "" {
		b.w.el("cbc:StreetName", p.Street)
	}
	if p.AdditionalLine != "" {
		b.w.el("cbc:AdditionalStreetName", p.AdditionalLine)
	}
	if p.City != "" {
		b.w.el("cbc:CityName", p.City)
	}
	if p.PostalCode != "" {
		b.w.el("cbc:PostalZone", p.PostalCode)
	}
	if p.Region != "" {
		b.w.el("cbc:CountrySubdivisionName", p.Region)
	}
	b.w.open("cac:Country")
	b.w.el("cbc:IdentificationCode", p.Country)
	if b.tr {
		name := countryNames[p.Country]
		if name == "" {
			name = p.Country
		}
		b.w.el("cbc:Name", name)
	}
	b.w.close("cac:Country")
	b.w.close("cac:PostalAddress")
	switch {
	case b.tr:
		b.w.open("cac:PartyTaxScheme")
		b.w.open("cac:TaxScheme")
		b.w.el("cbc:Name", p.TaxOffice)
		b.w.close("cac:TaxScheme")
		b.w.close("cac:PartyTaxScheme")
	case p.TaxID != "":
		b.w.open("cac:PartyTaxScheme")
		b.w.el("cbc:CompanyID", p.TaxID)
		b.w.open("cac:TaxScheme")
		b.w.el("cbc:ID", "VAT")
		b.w.close("cac:TaxScheme")
		b.w.close("cac:PartyTaxScheme")
	}
	b.w.open("cac:PartyLegalEntity")
	b.w.el("cbc:RegistrationName", p.Name)
	if !b.tr && p.RegistrationNo != "" {
		b.w.el("cbc:CompanyID", p.RegistrationNo)
	}
	b.w.close("cac:PartyLegalEntity")
	if p.Email != "" {
		b.w.open("cac:Contact")
		b.w.el("cbc:ElectronicMail", p.Email)
		b.w.close("cac:Contact")
	}
	b.w.close("cac:Party")
	b.w.close(tag)
}

// taxCategory writes the category of the document's tax: standard (S) with the rate, or exempt (E) with a reason.
func (b *builder) taxCategory(tag string, withID bool) {
	b.w.open(tag)
	if b.tr {
		if !b.sums.HasTax {
			b.w.el("cbc:TaxExemptionReasonCode", "351") // UNVERIFIED: "other, not an exception" is the default code
			b.w.el("cbc:TaxExemptionReason", "VAT not charged")
		}
		b.w.open("cac:TaxScheme")
		b.w.el("cbc:Name", "KDV")
		b.w.el("cbc:TaxTypeCode", "0015")
		b.w.close("cac:TaxScheme")
	} else {
		id := "S"
		if !b.sums.HasTax {
			id = "E"
		}
		if withID {
			b.w.el("cbc:ID", id)
		}
		b.w.el("cbc:Percent", formatBps(b.sums.TaxRateBps))
		if !b.sums.HasTax {
			b.w.el("cbc:TaxExemptionReason", "VAT not charged")
		}
		b.w.open("cac:TaxScheme")
		b.w.el("cbc:ID", "VAT")
		b.w.close("cac:TaxScheme")
	}
	b.w.close(tag)
}

func (b *builder) taxTotal() {
	b.w.open("cac:TaxTotal")
	b.amount("cbc:TaxAmount", b.sums.TaxAmount)
	b.w.open("cac:TaxSubtotal")
	b.amount("cbc:TaxableAmount", b.sums.TaxExclusive)
	b.amount("cbc:TaxAmount", b.sums.TaxAmount)
	if b.tr {
		b.w.el("cbc:Percent", formatBps(b.sums.TaxRateBps))
	}
	b.taxCategory("cac:TaxCategory", true)
	b.w.close("cac:TaxSubtotal")
	b.w.close("cac:TaxTotal")
}

func (b *builder) legalTotals() {
	b.w.open("cac:LegalMonetaryTotal")
	b.amount("cbc:LineExtensionAmount", b.sums.LineExtension)
	b.amount("cbc:TaxExclusiveAmount", b.sums.TaxExclusive)
	b.amount("cbc:TaxInclusiveAmount", b.sums.TaxInclusive)
	if b.sums.Allowance > 0 {
		b.amount("cbc:AllowanceTotalAmount", b.sums.Allowance)
	}
	if b.sums.Prepaid > 0 {
		b.amount("cbc:PrepaidAmount", b.sums.Prepaid)
	}
	b.amount("cbc:PayableAmount", b.sums.Payable)
	b.w.close("cac:LegalMonetaryTotal")
}

// items returns the item lines with the tax allocated to them (UBL-TR wants tax on every line; the allocation is largest-remainder so it sums exactly).
func (b *builder) items() (items []einvoice.Line, taxable, tax []int64) {
	var weights []int64
	for _, l := range b.doc.Lines {
		if l.Kind == einvoice.LineItem {
			items = append(items, l)
			weights = append(weights, l.AmountMinor)
		}
	}
	// The document-level discount is spread over the lines, so each line's taxable amount adds up to the document's.
	disc := allocate(b.sums.Allowance, weights)
	taxable = make([]int64, len(items))
	for i, l := range items {
		taxable[i] = l.AmountMinor - disc[i]
	}
	return items, taxable, allocate(b.sums.TaxAmount, taxable)
}

// allocate splits total over weights proportionally; the remainder goes to the largest fractional parts, earlier lines first.
func allocate(total int64, weights []int64) []int64 {
	out := make([]int64, len(weights))
	var sum int64
	for _, w := range weights {
		sum += w
	}
	if sum == 0 || total == 0 {
		return out
	}
	rem := make([]int64, len(weights))
	var given int64
	for i, w := range weights {
		num := new(big.Int).Mul(big.NewInt(total), big.NewInt(w))
		q, r := new(big.Int).QuoRem(num, big.NewInt(sum), new(big.Int))
		out[i], rem[i] = q.Int64(), r.Int64()
		given += out[i]
	}
	for left := total - given; left > 0; left-- {
		best := 0
		for i := range rem {
			if rem[i] > rem[best] {
				best = i
			}
		}
		out[best]++
		rem[best] = -1
	}
	return out
}

func (b *builder) lines() {
	items, taxable, tax := b.items()
	exp := b.doc.Exponent
	for i, l := range items {
		lineTag, qtyTag := "cac:InvoiceLine", "cbc:InvoicedQuantity"
		if b.credit() {
			lineTag, qtyTag = "cac:CreditNoteLine", "cbc:CreditedQuantity"
		}
		qty, price, _, exact := linePrice(l, exp)
		desc := l.Description
		if !exact {
			qty, price = "1", formatMinor(l.AmountMinor, exp)
			if l.Quantity != "" {
				desc = strings.TrimSpace(desc + " (" + l.Quantity + " " + l.UnitLabel + ")")
			}
		}
		b.w.open(lineTag)
		b.w.el("cbc:ID", itoa(i+1))
		b.w.el(qtyTag, qty, "unitCode", unitCode(l.UnitLabel, exact))
		b.amount("cbc:LineExtensionAmount", l.AmountMinor)
		if !l.ServiceFrom.IsZero() && !l.ServiceTo.IsZero() {
			b.w.open("cac:InvoicePeriod")
			b.w.el("cbc:StartDate", l.ServiceFrom.Format("2006-01-02"))
			b.w.el("cbc:EndDate", l.ServiceTo.Format("2006-01-02"))
			b.w.close("cac:InvoicePeriod")
		}
		if b.tr {
			b.w.open("cac:TaxTotal")
			b.amount("cbc:TaxAmount", tax[i])
			b.w.open("cac:TaxSubtotal")
			b.amount("cbc:TaxableAmount", taxable[i])
			b.amount("cbc:TaxAmount", tax[i])
			b.w.el("cbc:Percent", formatBps(b.sums.TaxRateBps))
			b.taxCategory("cac:TaxCategory", true)
			b.w.close("cac:TaxSubtotal")
			b.w.close("cac:TaxTotal")
		}
		b.w.open("cac:Item")
		b.w.el("cbc:Name", desc)
		if !b.tr {
			id := "S"
			if !b.sums.HasTax {
				id = "E"
			}
			b.w.open("cac:ClassifiedTaxCategory")
			b.w.el("cbc:ID", id)
			b.w.el("cbc:Percent", formatBps(b.sums.TaxRateBps))
			b.w.open("cac:TaxScheme")
			b.w.el("cbc:ID", "VAT")
			b.w.close("cac:TaxScheme")
			b.w.close("cac:ClassifiedTaxCategory")
		}
		b.w.close("cac:Item")
		b.w.open("cac:Price")
		b.w.el("cbc:PriceAmount", price, "currencyID", b.doc.Currency)
		b.w.close("cac:Price")
		b.w.close(lineTag)
	}
}

// linePrice returns quantity and unit price when quantity * price reproduces the amount exactly with up to 6 extra decimals; exact is false otherwise.
func linePrice(l einvoice.Line, exp int) (qty, price string, priceExp int, exact bool) {
	if !qtyRe.MatchString(l.Quantity) {
		return "", "", 0, false
	}
	whole, frac, _ := strings.Cut(l.Quantity, ".")
	units, ok := new(big.Int).SetString(whole+frac, 10)
	if !ok || units.Sign() == 0 || l.AmountMinor == 0 {
		return "", "", 0, false
	}
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(len(frac))), nil)
	for k := 0; k <= 6; k++ {
		num := new(big.Int).Mul(big.NewInt(l.AmountMinor), scale)
		num.Mul(num, new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(k)), nil))
		q, r := new(big.Int).QuoRem(num, units, new(big.Int))
		if r.Sign() == 0 {
			return l.Quantity, formatBig(q, exp+k), exp + k, true
		}
	}
	return "", "", 0, false
}

func formatBig(v *big.Int, exp int) string {
	s := v.String()
	if exp > 0 {
		for len(s) <= exp {
			s = "0" + s
		}
		s = s[:len(s)-exp] + "." + s[len(s)-exp:]
	}
	return s
}

var unitCodes = map[string]string{
	"": "C62", "unit": "C62", "units": "C62", "piece": "C62", "pcs": "C62", "request": "C62", "requests": "C62", "call": "C62", "calls": "C62",
	"gb": "E34", "mb": "4L", "tb": "E35", "hour": "HUR", "hours": "HUR", "h": "HUR", "minute": "MIN", "minutes": "MIN", "second": "SEC", "seconds": "SEC",
	"day": "DAY", "days": "DAY", "month": "MON", "months": "MON", "year": "ANN", "years": "ANN", "kg": "KGM", "m": "MTR", "km": "KMT", "l": "LTR", "seat": "C62", "seats": "C62",
}

// unitCode maps a unit label to a UN/ECE Rec 20 code; unknown labels and flat amounts are C62 (one).
func unitCode(label string, quantified bool) string {
	if !quantified {
		return "C62"
	}
	if c, ok := unitCodes[strings.ToLower(strings.TrimSpace(label))]; ok {
		return c
	}
	return "C62"
}

func itoa(n int) string { return formatMinor(int64(n), 0) }
