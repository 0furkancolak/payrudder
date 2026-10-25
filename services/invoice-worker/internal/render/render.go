// Package render draws an invoice PDF from the snapshot a RenderInvoicePdf command carries. The output is deterministic:
// the same snapshot yields the same bytes (fixed fonts, no wall-clock in the content or the document info), which is what
// makes re-rendering after a redelivery harmless.
package render

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-pdf/fpdf"

	commandsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/commands/v1"
	"github.com/0furkancolak/payrudder/pkg/quantity"
)

// Layout of the page. Lengths are millimetres, font sizes points.
const (
	fontFamily = "Helvetica"
	pageMargin = 18.0

	// Table columns: description, quantity, amount. The totals labels span the first two.
	colDescription = 110.0
	colQuantity    = 30.0
	colAmount      = 32.0
	colLabelSpan   = colDescription + colQuantity

	sizeTitle     = 22
	sizeBody      = 11
	sizeTable     = 10
	sizeTotal     = 12
	sizeWatermark = 44
	sizeNote      = 9

	rowTitle   = 10.0
	rowBody    = 6.0
	rowAddress = 5.0
	rowTable   = 7.0
	rowTotal   = 9.0

	// Logo placement; a height of 0 keeps the aspect ratio.
	logoX, logoY, logoWidth = 150.0, 14.0, 40.0
)

// Options are the parts of a render that are not in the snapshot.
type Options struct {
	// Sandbox draws the "SANDBOX — not a real invoice" watermark.
	Sandbox bool
	// Logo is the tenant's logo (PNG or JPEG bytes), or nil.
	Logo []byte
}

// ErrInvalid means the snapshot cannot be rendered; retrying will not help.
var ErrInvalid = errors.New("render: invalid invoice snapshot")

// money formats minor units in the currency's exponent: 1116 USD → "11.16 USD".
func money(minor int64, exp int, currency string) string {
	return quantity.FormatUnits(minor, exp) + " " + currency
}

// latin1 maps text to what the core PDF fonts can draw (ISO-8859-1 as cp1252). The em dash becomes "-", control
// characters other than newline become a space and every rune above U+00FF becomes '?': the Turkish letters ğ, ş and ı
// are printed as '?' because the core fonts have no glyph for them (printing them needs an embedded UTF-8 font).
func latin1(tr func(string) string, s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '—':
			b.WriteString("-")
		case r < 0x20 && r != '\n':
			b.WriteRune(' ')
		case r > 0xFF:
			b.WriteRune('?')
		default:
			b.WriteRune(r)
		}
	}
	return tr(b.String())
}

// groupIBAN writes an IBAN in blocks of four, the way bank documents print it.
func groupIBAN(iban string) string {
	var b strings.Builder
	for i, c := range iban {
		if i > 0 && i%4 == 0 {
			b.WriteByte(' ')
		}
		b.WriteRune(c)
	}
	return b.String()
}

// paymentInstructions prints where to send the money and what to write on the transfer, for an invoice paid by bank transfer.
func paymentInstructions(pdf *fpdf.Fpdf, t func(string) string, in *commandsv1.PaymentInstructions) {
	pdf.Ln(8)
	pdf.SetFont(fontFamily, "B", sizeBody)
	pdf.CellFormat(0, rowBody, "Pay by bank transfer", "", 1, "L", false, 0, "")
	pdf.SetFont(fontFamily, "", sizeTable)
	rows := [][2]string{{"Bank", in.GetBankName()}, {"Account holder", in.GetAccountHolder()}, {"IBAN", groupIBAN(in.GetIban())}, {"BIC / SWIFT", in.GetBic()},
		{"Payment reference", in.GetReference()}}
	for _, r := range rows {
		if r[1] == "" {
			continue
		}
		pdf.CellFormat(40, rowBody, r[0], "", 0, "L", false, 0, "")
		pdf.CellFormat(0, rowBody, t(r[1]), "", 1, "L", false, 0, "")
	}
	pdf.SetFont(fontFamily, "I", sizeNote)
	pdf.CellFormat(0, rowBody, "Please write the payment reference on your transfer so that we can match it to this invoice.", "", 1, "L", false, 0, "")
}

// PDF renders the invoice.
func PDF(inv *commandsv1.InvoiceSnapshot, o Options) ([]byte, error) {
	if inv.GetInvoiceId() == "" || inv.GetNumber() == "" || inv.GetCurrency() == "" || inv.GetCurrencyExponent() < 0 || inv.GetCurrencyExponent() > 3 {
		return nil, fmt.Errorf("%w: id, number, currency and exponent are required", ErrInvalid)
	}
	exp := int(inv.GetCurrencyExponent())
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.SetCompression(false)
	pdf.SetCatalogSort(true)
	issued := time.Unix(0, 0).UTC()
	if inv.GetIssuedAt() != nil {
		issued = inv.GetIssuedAt().AsTime().UTC()
	}
	pdf.SetCreationDate(issued)
	pdf.SetModificationDate(issued)
	pdf.SetTitle("Invoice "+inv.GetNumber(), false)
	pdf.SetAuthor(inv.GetTenantName(), false)
	pdf.SetMargins(pageMargin, pageMargin, pageMargin)
	pdf.SetAutoPageBreak(true, pageMargin)
	pdf.AddPage()
	tr := pdf.UnicodeTranslatorFromDescriptor("")
	t := func(s string) string { return latin1(tr, s) }

	if o.Sandbox {
		drawWatermark(pdf)
	}
	drawLogo(pdf, o.Logo)
	drawHeader(pdf, t, inv, issued)
	drawParty(pdf, t, "From", inv.GetTenantName(), inv.GetSellerAddressLines())
	pdf.Ln(2)
	drawParty(pdf, t, "Bill to", inv.GetCustomerName(), inv.GetCustomerAddressLines())
	pdf.CellFormat(0, rowBody, t(inv.GetCustomerEmail()), "", 1, "L", false, 0, "")
	pdf.Ln(8)
	drawLines(pdf, t, inv, exp)
	drawTotals(pdf, t, inv, exp)
	if in := inv.GetPaymentInstructions(); in.GetIban() != "" {
		paymentInstructions(pdf, t, in)
	}

	var out bytes.Buffer
	if err := pdf.Output(&out); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return out.Bytes(), nil
}

func drawWatermark(pdf *fpdf.Fpdf) {
	pdf.SetFont(fontFamily, "B", sizeWatermark)
	pdf.SetTextColor(235, 200, 200)
	pdf.TransformBegin()
	pdf.TransformRotate(40, 105, 160)
	pdf.Text(30, 170, "SANDBOX - not a real invoice")
	pdf.TransformEnd()
	pdf.SetTextColor(0, 0, 0)
}

// drawLogo places the tenant's logo. A logo that cannot be decoded is skipped: it must not fail the invoice.
func drawLogo(pdf *fpdf.Fpdf, logo []byte) {
	if len(logo) == 0 {
		return
	}
	opt := fpdf.ImageOptions{ReadDpi: false, ImageType: "PNG"}
	if bytes.HasPrefix(logo, []byte{0xFF, 0xD8}) {
		opt.ImageType = "JPG"
	}
	pdf.RegisterImageOptionsReader("logo", opt, bytes.NewReader(logo))
	pdf.ImageOptions("logo", logoX, logoY, logoWidth, 0, false, opt, 0, "")
	if pdf.Error() != nil {
		pdf.ClearError()
	}
}

func drawHeader(pdf *fpdf.Fpdf, t func(string) string, inv *commandsv1.InvoiceSnapshot, issued time.Time) {
	pdf.SetFont(fontFamily, "B", sizeTitle)
	pdf.CellFormat(0, rowTitle, "INVOICE", "", 1, "L", false, 0, "")
	pdf.SetFont(fontFamily, "", sizeBody)
	pdf.CellFormat(0, rowBody, t("Number: "+inv.GetNumber()), "", 1, "L", false, 0, "")
	pdf.CellFormat(0, rowBody, t("Period: "+inv.GetPeriodId()), "", 1, "L", false, 0, "")
	pdf.CellFormat(0, rowBody, t("Issued: "+issued.Format("2006-01-02")), "", 1, "L", false, 0, "")
	pdf.Ln(4)
}

// drawParty prints a titled block: the name and the address lines.
func drawParty(pdf *fpdf.Fpdf, t func(string) string, title, name string, address []string) {
	pdf.SetFont(fontFamily, "B", sizeBody)
	pdf.CellFormat(0, rowBody, title, "", 1, "L", false, 0, "")
	pdf.SetFont(fontFamily, "", sizeBody)
	pdf.CellFormat(0, rowBody, t(name), "", 1, "L", false, 0, "")
	for _, l := range address {
		pdf.CellFormat(0, rowAddress, t(l), "", 1, "L", false, 0, "")
	}
}

func drawLines(pdf *fpdf.Fpdf, t func(string) string, inv *commandsv1.InvoiceSnapshot, exp int) {
	pdf.SetFont(fontFamily, "B", sizeTable)
	pdf.SetFillColor(240, 240, 240)
	pdf.CellFormat(colDescription, rowTable, "Description", "B", 0, "L", true, 0, "")
	pdf.CellFormat(colQuantity, rowTable, "Quantity", "B", 0, "R", true, 0, "")
	pdf.CellFormat(colAmount, rowTable, "Amount", "B", 1, "R", true, 0, "")
	pdf.SetFont(fontFamily, "", sizeTable)
	for _, l := range inv.GetLines() {
		qty := ""
		if l.GetQuantity() != "" {
			qty = strings.TrimSpace(l.GetQuantity() + " " + l.GetUnitLabel())
		}
		pdf.CellFormat(colDescription, rowTable, t(l.GetDescription()), "", 0, "L", false, 0, "")
		pdf.CellFormat(colQuantity, rowTable, t(qty), "", 0, "R", false, 0, "")
		pdf.CellFormat(colAmount, rowTable, t(money(l.GetAmountMinor(), exp, inv.GetCurrency())), "", 1, "R", false, 0, "")
	}
}

func drawTotals(pdf *fpdf.Fpdf, t func(string) string, inv *commandsv1.InvoiceSnapshot, exp int) {
	pdf.Ln(4)
	pdf.SetFont(fontFamily, "", sizeTable)
	pdf.CellFormat(colLabelSpan, rowTable, "Subtotal", "T", 0, "R", false, 0, "")
	pdf.CellFormat(colAmount, rowTable, t(money(inv.GetSubtotalMinor(), exp, inv.GetCurrency())), "T", 1, "R", false, 0, "")
	pdf.SetFont(fontFamily, "B", sizeTotal)
	pdf.CellFormat(colLabelSpan, rowTotal, "Total", "", 0, "R", false, 0, "")
	pdf.CellFormat(colAmount, rowTotal, t(money(inv.GetTotalMinor(), exp, inv.GetCurrency())), "", 1, "R", false, 0, "")
}
