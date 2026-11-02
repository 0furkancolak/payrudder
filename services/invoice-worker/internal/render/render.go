// Package render draws an invoice PDF from the snapshot a RenderInvoicePdf command carries, in the customer's language (en, tr, de, fr, es; layouts.go) with numbers,
// money and dates written the way that language writes them (locale.go) and an embedded Noto Sans (fonts.go). The output is deterministic: the same snapshot yields the
// same bytes (fixed fonts, no wall-clock in the content or the document info), which is what makes re-rendering after a redelivery harmless.
package render

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/go-pdf/fpdf"

	commandsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/commands/v1"
	"github.com/0furkancolak/payrudder/pkg/taxid"
)

// Layout of the page. Lengths are millimetres, font sizes points.
const (
	fontFamily = "NotoSans"
	pageMargin = 18.0

	// Table columns: description, quantity, amount. The totals labels span the first two.
	colDescription = 100.0
	colQuantity    = 32.0
	colAmount      = 40.0
	colLabelSpan   = colDescription + colQuantity

	sizeTitle = 22
	sizeBody  = 11
	sizeTable = 10
	sizeTotal = 12
	sizeNote  = 9

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
	// PageSize is "A4" (the default) or "Letter".
	PageSize string
}

// ErrInvalid means the snapshot cannot be rendered; retrying will not help.
var ErrInvalid = errors.New("render: invalid invoice snapshot")

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
func paymentInstructions(pdf *fpdf.Fpdf, l *layout, in *commandsv1.PaymentInstructions) {
	pdf.Ln(8)
	pdf.SetFont(fontFamily, "B", sizeBody)
	pdf.CellFormat(0, rowBody, l.t(kPayByTransfer), "", 1, "L", false, 0, "")
	pdf.SetFont(fontFamily, "", sizeTable)
	rows := [][2]string{{l.t(kBank), in.GetBankName()}, {l.t(kAccountHolder), in.GetAccountHolder()}, {l.t(kIBAN), groupIBAN(in.GetIban())}, {l.t(kBIC), in.GetBic()},
		{l.t(kPaymentRef), in.GetReference()}}
	for _, r := range rows {
		if r[1] == "" {
			continue
		}
		pdf.CellFormat(45, rowBody, r[0], "", 0, "L", false, 0, "")
		pdf.CellFormat(0, rowBody, printable(r[1]), "", 1, "L", false, 0, "")
	}
	pdf.SetFont(fontFamily, "", sizeNote)
	pdf.MultiCell(0, rowBody, l.t(kTransferNote), "", "L", false)
}

// SandboxMarker is the /Subject of every sandbox invoice, in plain ASCII.
const SandboxMarker = "payrudder-sandbox"

// PDF renders the invoice.
func PDF(inv *commandsv1.InvoiceSnapshot, o Options) ([]byte, error) {
	if inv.GetInvoiceId() == "" || inv.GetNumber() == "" || inv.GetCurrency() == "" || inv.GetCurrencyExponent() < 0 || inv.GetCurrencyExponent() > 3 {
		return nil, fmt.Errorf("%w: id, number, currency and exponent are required", ErrInvalid)
	}
	exp := int(inv.GetCurrencyExponent())
	l, requested := choose(inv.GetLocale(), inv.GetDefaultLocale())
	pdf := fpdf.New("P", "mm", pageSize(o.PageSize), "")
	pdf.SetCompression(false)
	pdf.SetCatalogSort(true)
	issued := time.Unix(0, 0).UTC()
	if inv.GetIssuedAt() != nil {
		issued = inv.GetIssuedAt().AsTime().UTC()
	}
	pdf.SetCreationDate(issued)
	pdf.SetModificationDate(issued)
	pdf.SetTitle(printable(l.t(kInvoice)+" "+inv.GetNumber()), true)
	pdf.SetAuthor(printable(inv.GetTenantName()), true)
	// The language that was asked for and the layout that was drawn are both kept, because they differ for an Arabic customer.
	pdf.SetKeywords("locale="+requested+" layout="+l.code, true)
	pdf.AddUTF8FontFromBytes(fontFamily, "", notoRegular)
	pdf.AddUTF8FontFromBytes(fontFamily, "B", notoBold)
	pdf.SetMargins(pageMargin, pageMargin, pageMargin)
	pdf.SetAutoPageBreak(true, pageMargin)
	pdf.AddPage()

	if o.Sandbox {
		// The drawn mark is glyph ids of the embedded font, so nothing can find it by its text in the bytes; /Subject is a plain ASCII string (SandboxMarker) that a script can.
		pdf.SetSubject(SandboxMarker, false)
		drawWatermark(pdf, l.t(kSandbox))
	}
	drawLogo(pdf, o.Logo)
	drawHeader(pdf, l, inv, issued)
	drawParty(pdf, l.t(kFrom), inv.GetTenantName(), inv.GetSellerAddressLines(), taxLines(l, inv.GetSellerTaxId(), inv.GetSellerTaxOffice()))
	pdf.Ln(2)
	drawParty(pdf, l.t(kBillTo), inv.GetCustomerName(), inv.GetCustomerAddressLines(), taxLines(l, inv.GetCustomerTaxId(), inv.GetCustomerTaxOffice()))
	pdf.CellFormat(0, rowBody, printable(inv.GetCustomerEmail()), "", 1, "L", false, 0, "")
	pdf.Ln(8)
	drawLines(pdf, l, inv, exp)
	drawTotals(pdf, l, inv, exp)
	if in := inv.GetPaymentInstructions(); in.GetIban() != "" {
		paymentInstructions(pdf, l, in)
	}
	if eDocumentIssuedSeparately(inv) {
		pdf.Ln(6)
		pdf.SetFont(fontFamily, "", sizeNote)
		pdf.MultiCell(0, rowBody, l.t(kEDocumentNote), "", "L", false)
	}

	var out bytes.Buffer
	if err := pdf.Output(&out); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return out.Bytes(), nil
}

// eDocumentIssuedSeparately: a Turkish seller whose tenant e-invoices has its legal e-Fatura or e-Arşiv document made by the integrator after this PDF, with a number
// this PDF cannot know, so the PDF says so. The wording is UNVERIFIED (no accountant has read it). Other countries print no such line: Peppol and the EU have no
// wording in this renderer (docs/plans/05-customer-locale-tax-documents.md).
func eDocumentIssuedSeparately(inv *commandsv1.InvoiceSnapshot) bool {
	on := inv.GetEInvoiceOn()
	return inv.GetSellerCountry() == "TR" && (on == "finalized" || on == "paid")
}

// taxLines are the lines that identify a party for the tax: the id, labelled by what it is (a Turkish VKN or TCKN, a European VAT id, else a plain tax id), and the tax
// office when there is one. Nothing is printed for a party without either.
func taxLines(l *layout, id, office string) []string {
	var out []string
	if id != "" {
		label := kTaxID
		switch k, _ := taxid.Detect(id); k {
		case taxid.TCKN:
			label = kTCKN
		case taxid.VKN:
			label = kVKN
		case taxid.EUVAT:
			label = kVATID
		}
		out = append(out, l.t(label)+": "+id)
	}
	if office != "" {
		out = append(out, l.t(kTaxOffice)+": "+office)
	}
	return out
}

// pageSize maps an Options page size to a fpdf one; anything but "Letter" is A4.
func pageSize(s string) string {
	if strings.EqualFold(s, "letter") {
		return "Letter"
	}
	return "A4"
}

const (
	watermarkAngle  = 40.0 // degrees, counter-clockwise
	watermarkMargin = 12.0 // mm kept free around the rotated text
	watermarkMaxPt  = 60.0
)

// watermarkFit is the font size (points) at which the rotated watermark of a text width w1 (mm at 1 pt) and a page w x h still lies inside the page margins, and
// the half extents of its rotated bounding box.
func watermarkFit(w1, pageW, pageH float64) (size, halfW, halfH float64) {
	a := watermarkAngle * math.Pi / 180
	cos, sin := math.Cos(a), math.Sin(a)
	const ptMM = 25.4 / 72 // one point in millimetres; the text height is about 0.75 of the size
	h1 := ptMM * 0.75
	// The box of a text of size s: width s*(w1*cos + h1*sin), height s*(w1*sin + h1*cos).
	bw, bh := w1*cos+h1*sin, w1*sin+h1*cos
	size = math.Min((pageW-2*watermarkMargin)/bw, (pageH-2*watermarkMargin)/bh)
	size = math.Min(size, watermarkMaxPt)
	return size, size * bw / 2, size * bh / 2
}

// watermarkPlacement is the font size (points), the text width (mm) and the page centre the watermark is drawn with.
func watermarkPlacement(pdf *fpdf.Fpdf, text string) (size, width, cx, cy float64) {
	pageW, pageH := pdf.GetPageSize()
	pdf.SetFont(fontFamily, "B", 10)
	w1 := pdf.GetStringWidth(text) / 10
	size, _, _ = watermarkFit(w1, pageW, pageH)
	pdf.SetFont(fontFamily, "B", size)
	return size, pdf.GetStringWidth(text), pageW / 2, pageH / 2
}

// drawWatermark draws the sandbox mark centred on the page, rotated and scaled so that all of it is inside the page, on A4 and on Letter alike.
func drawWatermark(pdf *fpdf.Fpdf, text string) {
	size, w, cx, cy := watermarkPlacement(pdf, text)
	pdf.SetTextColor(235, 200, 200)
	pdf.TransformBegin()
	pdf.TransformRotate(watermarkAngle, cx, cy)
	// The baseline sits at the text's vertical middle, so the box is centred on the page.
	pdf.Text(cx-w/2, cy+size*25.4/72*0.75/2, text)
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

func drawHeader(pdf *fpdf.Fpdf, l *layout, inv *commandsv1.InvoiceSnapshot, issued time.Time) {
	pdf.SetFont(fontFamily, "B", sizeTitle)
	pdf.CellFormat(0, rowTitle, l.t(kInvoice), "", 1, "L", false, 0, "")
	pdf.SetFont(fontFamily, "", sizeBody)
	pdf.CellFormat(0, rowBody, printable(l.t(kNumber)+": "+inv.GetNumber()), "", 1, "L", false, 0, "")
	pdf.CellFormat(0, rowBody, printable(l.t(kPeriod)+": "+l.Period(inv.GetPeriodId())), "", 1, "L", false, 0, "")
	pdf.CellFormat(0, rowBody, printable(l.t(kIssued)+": "+l.Date(issued)), "", 1, "L", false, 0, "")
	pdf.Ln(4)
}

// drawParty prints a titled block: the name, the address lines and the lines that identify the party for the tax.
func drawParty(pdf *fpdf.Fpdf, title, name string, address, tax []string) {
	pdf.SetFont(fontFamily, "B", sizeBody)
	pdf.CellFormat(0, rowBody, title, "", 1, "L", false, 0, "")
	pdf.SetFont(fontFamily, "", sizeBody)
	pdf.CellFormat(0, rowBody, printable(name), "", 1, "L", false, 0, "")
	for _, l := range slices.Concat(address, tax) {
		pdf.CellFormat(0, rowAddress, printable(l), "", 1, "L", false, 0, "")
	}
}

// fit shortens s with an ellipsis to the width w (mm) in the current font, so that a long description does not run into the next column.
func fit(pdf *fpdf.Fpdf, s string, w float64) string {
	const margins = 2.0 // the cell margin on both sides
	if pdf.GetStringWidth(s) <= w-margins {
		return s
	}
	r := []rune(s)
	for len(r) > 0 && pdf.GetStringWidth(string(r)+"…") > w-margins {
		r = r[:len(r)-1]
	}
	return string(r) + "…"
}

func drawLines(pdf *fpdf.Fpdf, l *layout, inv *commandsv1.InvoiceSnapshot, exp int) {
	pdf.SetFont(fontFamily, "B", sizeTable)
	pdf.SetFillColor(240, 240, 240)
	pdf.CellFormat(colDescription, rowTable, l.t(kDescription), "B", 0, "L", true, 0, "")
	pdf.CellFormat(colQuantity, rowTable, l.t(kQuantity), "B", 0, "R", true, 0, "")
	pdf.CellFormat(colAmount, rowTable, l.t(kAmount), "B", 1, "R", true, 0, "")
	pdf.SetFont(fontFamily, "", sizeTable)
	for _, ln := range inv.GetLines() {
		qty := ""
		if ln.GetQuantity() != "" {
			qty = strings.TrimSpace(l.Number(ln.GetQuantity()) + " " + ln.GetUnitLabel())
		}
		pdf.CellFormat(colDescription, rowTable, fit(pdf, printable(ln.GetDescription()), colDescription), "", 0, "L", false, 0, "")
		pdf.CellFormat(colQuantity, rowTable, fit(pdf, printable(qty), colQuantity), "", 0, "R", false, 0, "")
		pdf.CellFormat(colAmount, rowTable, printable(l.Money(ln.GetAmountMinor(), exp, inv.GetCurrency())), "", 1, "R", false, 0, "")
	}
}

func drawTotals(pdf *fpdf.Fpdf, l *layout, inv *commandsv1.InvoiceSnapshot, exp int) {
	pdf.Ln(4)
	pdf.SetFont(fontFamily, "", sizeTable)
	pdf.CellFormat(colLabelSpan, rowTable, l.t(kSubtotal), "T", 0, "R", false, 0, "")
	pdf.CellFormat(colAmount, rowTable, printable(l.Money(inv.GetSubtotalMinor(), exp, inv.GetCurrency())), "T", 1, "R", false, 0, "")
	pdf.SetFont(fontFamily, "B", sizeTotal)
	pdf.CellFormat(colLabelSpan, rowTotal, l.t(kTotal), "", 0, "R", false, 0, "")
	pdf.CellFormat(colAmount, rowTotal, printable(l.Money(inv.GetTotalMinor(), exp, inv.GetCurrency())), "", 1, "R", false, 0, "")
}
