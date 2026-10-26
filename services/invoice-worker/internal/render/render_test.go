package render

import (
	"bytes"
	"math"
	"testing"
	"time"

	"github.com/go-pdf/fpdf"
	"google.golang.org/protobuf/types/known/timestamppb"

	commandsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/commands/v1"
)

func snapshot() *commandsv1.InvoiceSnapshot {
	return &commandsv1.InvoiceSnapshot{
		InvoiceId: "0192f0b4-6f6e-7c3a-9a3e-6a1f2b3c4d5e", Version: 1, Number: "INV-2026-000001", PeriodId: "2026-10", Currency: "USD", CurrencyExponent: 2,
		TenantName: "Acme", CustomerName: "Ada Lovelace", CustomerEmail: "ada@example.com", IssuedAt: timestamppb.New(time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)),
		Lines: []*commandsv1.InvoiceLineSnapshot{
			{LineNo: 1, Kind: "BASE_FEE", Description: "Base fee", AmountMinor: 1000},
			{LineNo: 2, Kind: "USAGE", Description: "Usage — gb_hours", Quantity: "1.250", UnitLabel: "GB-h", AmountMinor: 16},
		},
		SubtotalMinor: 1016, TotalMinor: 1016,
	}
}

func TestPDFIsDeterministic(t *testing.T) {
	a, err := PDF(snapshot(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	b, err := PDF(snapshot(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatal("rendering the same snapshot twice gave different bytes")
	}
	if !bytes.HasPrefix(a, []byte("%PDF-")) || !bytes.Contains(a, []byte("10.16 USD")) || !bytes.Contains(a, []byte("INV-2026-000001")) {
		t.Fatalf("the PDF lacks the expected content (%d bytes)", len(a))
	}
	// Another snapshot differs.
	s := snapshot()
	s.TotalMinor = 999
	c, _ := PDF(s, Options{})
	if bytes.Equal(a, c) {
		t.Fatal("a different invoice rendered identically")
	}
}

func TestBankDetailsAndReferenceArePrinted(t *testing.T) {
	plain, _ := PDF(snapshot(), Options{})
	if bytes.Contains(plain, []byte("bank transfer")) {
		t.Fatal("an invoice that is not paid by transfer has no bank block")
	}
	s := snapshot()
	s.PaymentInstructions = &commandsv1.PaymentInstructions{BankName: "Example Bank", AccountHolder: "Acme Ltd", Iban: "TR330006100519786457841326", Bic: "TCZBTR2A", Reference: "INV-2026-000001"}
	pdf, err := PDF(s, Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Pay by bank transfer", "Example Bank", "Acme Ltd", "TR33 0006 1005 1978 6457 8413 26", "TCZBTR2A", "Payment reference", "INV-2026-000001"} {
		if !bytes.Contains(pdf, []byte(want)) {
			t.Errorf("the PDF lacks %q", want)
		}
	}
}

func TestSandboxWatermark(t *testing.T) {
	live, _ := PDF(snapshot(), Options{})
	sb, err := PDF(snapshot(), Options{Sandbox: true})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(live, []byte("SANDBOX")) || !bytes.Contains(sb, []byte("SANDBOX")) {
		t.Fatal("the SANDBOX watermark must appear on sandbox invoices only")
	}
}

func TestZeroExponentCurrency(t *testing.T) {
	s := snapshot()
	s.Currency, s.CurrencyExponent, s.TotalMinor, s.SubtotalMinor = "JPY", 0, 1016, 1016
	pdf, err := PDF(s, Options{})
	if err != nil || !bytes.Contains(pdf, []byte("1016 JPY")) {
		t.Fatalf("JPY: %v", err)
	}
}

func TestInvalidSnapshot(t *testing.T) {
	s := snapshot()
	s.Number = ""
	if _, err := PDF(s, Options{}); err == nil {
		t.Fatal("a snapshot without a number must be rejected")
	}
}

func TestNonLatinTextDoesNotFail(t *testing.T) {
	s := snapshot()
	s.CustomerName = "Şükrü Çağlar 日本"
	if _, err := PDF(s, Options{}); err != nil {
		t.Fatalf("non-latin names must not fail the invoice: %v", err)
	}
}

func TestAddressesArePrinted(t *testing.T) {
	s := snapshot()
	s.SellerAddressLines = []string{"1 Market St", "London", "GB"}
	s.CustomerAddressLines = []string{"Hauptstr. 5", "10115 Berlin", "DE"}
	withAddr, err := PDF(s, Options{})
	if err != nil {
		t.Fatal(err)
	}
	without, _ := PDF(snapshot(), Options{})
	if bytes.Equal(withAddr, without) || len(withAddr) <= len(without) {
		t.Fatal("the addresses changed nothing in the PDF")
	}
}

// The rotated watermark lies inside the page, with its margin, on A4 and on Letter (it used to run off the right edge).
func TestSandboxWatermarkFitsThePage(t *testing.T) {
	for _, size := range []string{"A4", "Letter"} {
		t.Run(size, func(t *testing.T) {
			pdf := fpdf.New("P", "mm", pageSize(size), "")
			pdf.AddPage()
			fs, w, cx, cy := watermarkPlacement(pdf)
			pageW, pageH := pdf.GetPageSize()
			h := fs * 25.4 / 72 * 0.75
			a := watermarkAngle * math.Pi / 180
			for _, c := range [][2]float64{{-w / 2, -h / 2}, {w / 2, -h / 2}, {w / 2, h / 2}, {-w / 2, h / 2}} {
				x := cx + c[0]*math.Cos(a) - c[1]*math.Sin(a)
				y := cy + c[0]*math.Sin(a) + c[1]*math.Cos(a)
				if x < 0 || y < 0 || x > pageW || y > pageH {
					t.Fatalf("watermark corner (%.1f, %.1f) is outside the %s page %.0fx%.0f", x, y, size, pageW, pageH)
				}
			}
			if fs < 20 {
				t.Fatalf("watermark font %.1f pt is too small to read", fs)
			}
			out, err := PDF(snapshot(), Options{Sandbox: true, PageSize: size})
			if err != nil || !bytes.Contains(out, []byte("SANDBOX")) {
				t.Fatalf("render on %s: %v", size, err)
			}
			if size == "Letter" && !bytes.Contains(out, []byte("612")) {
				t.Fatal("a Letter render must have a Letter media box")
			}
		})
	}
}
