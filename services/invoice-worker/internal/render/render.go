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

	commandsv1 "github.com/0furkancolak/quotify/gen/go/quotify/commands/v1"
	"github.com/0furkancolak/quotify/pkg/quantity"
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

// latin1 maps text to what the core PDF fonts can draw (ISO-8859-1 as cp1252): other runes become '?'.
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
	pdf.SetMargins(18, 18, 18)
	pdf.SetAutoPageBreak(true, 18)
	pdf.AddPage()
	tr := pdf.UnicodeTranslatorFromDescriptor("")
	t := func(s string) string { return latin1(tr, s) }

	if o.Sandbox {
		pdf.SetFont("Helvetica", "B", 44)
		pdf.SetTextColor(235, 200, 200)
		pdf.TransformBegin()
		pdf.TransformRotate(40, 105, 160)
		pdf.Text(30, 170, "SANDBOX - not a real invoice")
		pdf.TransformEnd()
		pdf.SetTextColor(0, 0, 0)
	}
	if len(o.Logo) > 0 {
		opt := fpdf.ImageOptions{ReadDpi: false}
		typ := "PNG"
		if bytes.HasPrefix(o.Logo, []byte{0xFF, 0xD8}) {
			typ = "JPG"
		}
		opt.ImageType = typ
		pdf.RegisterImageOptionsReader("logo", opt, bytes.NewReader(o.Logo))
		pdf.ImageOptions("logo", 150, 14, 40, 0, false, opt, 0, "")
		if pdf.Error() != nil {
			// A bad logo must not fail the invoice.
			pdf.ClearError()
		}
	}

	pdf.SetFont("Helvetica", "B", 22)
	pdf.CellFormat(0, 10, "INVOICE", "", 1, "L", false, 0, "")
	pdf.SetFont("Helvetica", "", 11)
	pdf.CellFormat(0, 6, t("Number: "+inv.GetNumber()), "", 1, "L", false, 0, "")
	pdf.CellFormat(0, 6, t("Period: "+inv.GetPeriodId()), "", 1, "L", false, 0, "")
	pdf.CellFormat(0, 6, t("Issued: "+issued.Format("2006-01-02")), "", 1, "L", false, 0, "")
	pdf.Ln(4)
	pdf.SetFont("Helvetica", "B", 11)
	pdf.CellFormat(0, 6, "From", "", 1, "L", false, 0, "")
	pdf.SetFont("Helvetica", "", 11)
	pdf.CellFormat(0, 6, t(inv.GetTenantName()), "", 1, "L", false, 0, "")
	for _, l := range inv.GetSellerAddressLines() {
		pdf.CellFormat(0, 5, t(l), "", 1, "L", false, 0, "")
	}
	pdf.Ln(2)
	pdf.SetFont("Helvetica", "B", 11)
	pdf.CellFormat(0, 6, "Bill to", "", 1, "L", false, 0, "")
	pdf.SetFont("Helvetica", "", 11)
	pdf.CellFormat(0, 6, t(inv.GetCustomerName()), "", 1, "L", false, 0, "")
	for _, l := range inv.GetCustomerAddressLines() {
		pdf.CellFormat(0, 5, t(l), "", 1, "L", false, 0, "")
	}
	pdf.CellFormat(0, 6, t(inv.GetCustomerEmail()), "", 1, "L", false, 0, "")
	pdf.Ln(8)

	pdf.SetFont("Helvetica", "B", 10)
	pdf.SetFillColor(240, 240, 240)
	pdf.CellFormat(110, 7, "Description", "B", 0, "L", true, 0, "")
	pdf.CellFormat(30, 7, "Quantity", "B", 0, "R", true, 0, "")
	pdf.CellFormat(32, 7, "Amount", "B", 1, "R", true, 0, "")
	pdf.SetFont("Helvetica", "", 10)
	for _, l := range inv.GetLines() {
		qty := ""
		if l.GetQuantity() != "" {
			qty = strings.TrimSpace(l.GetQuantity() + " " + l.GetUnitLabel())
		}
		pdf.CellFormat(110, 7, t(l.GetDescription()), "", 0, "L", false, 0, "")
		pdf.CellFormat(30, 7, t(qty), "", 0, "R", false, 0, "")
		pdf.CellFormat(32, 7, t(money(l.GetAmountMinor(), exp, inv.GetCurrency())), "", 1, "R", false, 0, "")
	}
	pdf.Ln(4)
	pdf.SetFont("Helvetica", "", 10)
	pdf.CellFormat(140, 7, "Subtotal", "T", 0, "R", false, 0, "")
	pdf.CellFormat(32, 7, t(money(inv.GetSubtotalMinor(), exp, inv.GetCurrency())), "T", 1, "R", false, 0, "")
	pdf.SetFont("Helvetica", "B", 12)
	pdf.CellFormat(140, 9, "Total", "", 0, "R", false, 0, "")
	pdf.CellFormat(32, 9, t(money(inv.GetTotalMinor(), exp, inv.GetCurrency())), "", 1, "R", false, 0, "")

	var out bytes.Buffer
	if err := pdf.Output(&out); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return out.Bytes(), nil
}
