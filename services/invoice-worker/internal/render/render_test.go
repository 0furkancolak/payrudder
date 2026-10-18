package render

import (
	"bytes"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	commandsv1 "github.com/0furkancolak/quotify/gen/go/quotify/commands/v1"
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
