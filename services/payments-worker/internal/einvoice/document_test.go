package einvoice

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	commandsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/commands/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	pkg "github.com/0furkancolak/payrudder/pkg/einvoice"
	"github.com/0furkancolak/payrudder/pkg/workerx"
)

func command() *commandsv1.IssueEInvoice {
	return &commandsv1.IssueEInvoice{Action: "issue", DocumentId: "5f0c2b8e-0000-5000-8000-000000000001", Kind: "invoice", Document: &commandsv1.InvoiceSnapshot{
		Number: "INV-2026-000001", Currency: "TRY", CurrencyExponent: 2, TenantName: "Acme", CustomerName: "Cus", CustomerEmail: "c@example.com",
		IssuedAt: timestamppb.New(time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)),
		Seller:   &commandsv1.PartySnapshot{Country: "TR", TaxId: "1234567890"}, Buyer: &commandsv1.PartySnapshot{Country: "TR", EInvoiceRegistered: true},
		Lines: []*commandsv1.InvoiceLineSnapshot{
			{Kind: "BASE_FEE", Description: "Base fee", AmountMinor: 100000},
			{Kind: "USAGE", Description: "GB", Quantity: "2.500", UnitLabel: "GB", AmountMinor: 5000},
			{Kind: "DISCOUNT", Description: "Promo", AmountMinor: -5000},
			{Kind: "CORRECTION", Description: "Back", AmountMinor: -1000},
			{Kind: "CREDIT", Description: "Credit", AmountMinor: -2000},
			{Kind: "TAX", Description: "KDV 20%", AmountMinor: 20800},
		}}}
}

func TestDocumentMapsLinesWithPositiveAmounts(t *testing.T) {
	d := Document(command(), false)
	var kinds []pkg.LineKind
	for _, l := range d.Lines {
		kinds = append(kinds, l.Kind)
		if l.AmountMinor < 0 {
			t.Errorf("line %d is negative: %v", l.No, l)
		}
	}
	want := []pkg.LineKind{pkg.LineItem, pkg.LineItem, pkg.LineDiscount, pkg.LineDiscount, pkg.LineCredit, pkg.LineTax}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("kinds = %v, want %v", kinds, want)
		}
	}
	if d.Seller.Name != "Acme" || d.Buyer.Name != "Cus" || d.Buyer.Email != "c@example.com" || d.Reference != nil || d.Currency != "TRY" || d.Exponent != 2 {
		t.Errorf("document = %+v", d)
	}
	cn := command()
	cn.Kind, cn.OriginalNumber = "credit_note", "INV-2026-000001"
	if Document(cn, false).Reference == nil || Document(cn, false).Reference.Number != "INV-2026-000001" {
		t.Error("a credit note refers to its invoice")
	}
}

func TestChooseTypeFollowsCountryAndRegistration(t *testing.T) {
	tr := func(registered bool) pkg.Document {
		return pkg.Document{Seller: pkg.Party{Country: "TR"}, Buyer: pkg.Party{Country: "TR", EInvoiceRegistered: registered}}
	}
	if ChooseType(tr(true)) != pkg.TypeEFatura || ChooseType(tr(false)) != pkg.TypeEArsiv {
		t.Error("Turkey: e-fatura for a registered buyer, e-arşiv otherwise")
	}
	if ChooseType(pkg.Document{Seller: pkg.Party{Country: "DE"}, Buyer: pkg.Party{Country: "FR"}}) != pkg.TypePeppol {
		t.Error("elsewhere: Peppol")
	}
}

func TestSandboxDefaultsOnlyFillWhatIsMissing(t *testing.T) {
	d := pkg.Document{Seller: pkg.Party{Name: "Real", TaxID: "7777777777", Country: "DE"}, Buyer: pkg.Party{Country: "DE"}}
	ApplySandboxDefaults(&d)
	if d.Seller.Name != "Real" || d.Seller.TaxID != "7777777777" || d.Seller.Country != "DE" || d.Buyer.TaxID != "" || d.Buyer.Name == "" {
		t.Errorf("document = %+v", d)
	}
	empty := pkg.Document{}
	ApplySandboxDefaults(&empty)
	if empty.Seller.TaxID == "" || empty.Seller.Country != "TR" || empty.Buyer.TaxID != consumerTaxID {
		t.Errorf("empty = %+v", empty)
	}
}

func TestHandleRefusesWhatIsNotAnEInvoiceCommand(t *testing.T) {
	h := &Handler{}
	for _, cmd := range []*commandsv1.CommandEnvelope{
		{CommandId: "x", TenantId: "not-a-uuid", Command: &commandsv1.CommandEnvelope_IssueEInvoice{IssueEInvoice: command()}},
		{CommandId: "x", TenantId: "5f0c2b8e-0000-5000-8000-000000000002", Command: &commandsv1.CommandEnvelope_IssueEInvoice{IssueEInvoice: &commandsv1.IssueEInvoice{DocumentId: "bad"}}},
	} {
		if _, err := h.Handle(context.Background(), cmd); !errors.Is(err, workerx.ErrPoison) {
			t.Errorf("err = %v, want poison", err)
		}
	}
	unknown := command()
	unknown.Action = "explode"
	cmd := &commandsv1.CommandEnvelope{CommandId: "x", TenantId: "5f0c2b8e-0000-5000-8000-000000000002", Environment: plansv1.Environment_ENVIRONMENT_SANDBOX,
		Command: &commandsv1.CommandEnvelope_IssueEInvoice{IssueEInvoice: unknown}}
	if _, err := h.Handle(context.Background(), cmd); !errors.Is(err, workerx.ErrPoison) {
		t.Errorf("unknown action: %v", err)
	}
}
