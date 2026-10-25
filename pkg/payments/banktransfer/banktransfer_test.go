package banktransfer_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/0furkancolak/payrudder/pkg/payments"
	"github.com/0furkancolak/payrudder/pkg/payments/banktransfer"
)

func TestValidateIBAN(t *testing.T) {
	for _, ok := range []string{
		"TR33 0006 1005 1978 6457 8413 26", "tr330006100519786457841326", "DE89 3704 0044 0532 0130 00", "GB82-WEST-1234-5698-7654-32",
		"NL91 ABNA 0417 1643 00", "FR14 2004 1010 0505 0001 3M02 606",
	} {
		got, err := banktransfer.ValidateIBAN(ok)
		if err != nil {
			t.Errorf("%q: %v", ok, err)
		}
		if strings.ContainsAny(got, " -") || got != strings.ToUpper(got) {
			t.Errorf("%q normalised to %q", ok, got)
		}
	}
	for name, bad := range map[string]string{
		"wrong check digits":      "TR34 0006 1005 1978 6457 8413 26",
		"one digit changed":       "DE89 3704 0044 0532 0130 01",
		"too short for TR":        "TR33 0006 1005 1978 6457 8413",
		"empty":                   "",
		"too short at all":        "TR33",
		"no country":              "12 3456 7890 1234 5678",
		"letters in check digits": "DEAB 3704 0044 0532 0130 00",
		"punctuation":             "DE89 3704 0044 0532 0130 0!",
		"too long":                "DE89" + strings.Repeat("1", 40),
	} {
		if _, err := banktransfer.ValidateIBAN(bad); !errors.Is(err, banktransfer.ErrIBAN) {
			t.Errorf("%s: %q accepted (err %v)", name, bad, err)
		}
	}
}

func TestReference(t *testing.T) {
	const id = "0192f0b4-6f6e-7c3a-9a3e-6a1f2b3c4d5e"
	for name, tc := range map[string]struct{ template, number, want string }{
		"default is the invoice number": {"", "INV-2026-000123", "INV-2026-000123"},
		"prefix and number":             {"Acme {invoice_number}", "INV-2026-000123", "Acme INV-2026-000123"},
		"invoice id is shortened":       {"{invoice_id}", "INV-1", "0192f0b4"},
		"unusual characters dropped":    {"Fatura #{invoice_number} (TR)", "INV-1", "Fatura INV-1 TR"},
		"no number falls back to id":    {"{invoice_number}", "", "0192f0b4"},
		"cut to the bank limit":         {"{invoice_number} " + strings.Repeat("x", 60), "INV-1", "INV-1 " + strings.Repeat("x", banktransfer.MaxReferenceLen-6)},
	} {
		if got := banktransfer.Reference(tc.template, tc.number, id); got != tc.want {
			t.Errorf("%s: %q, want %q", name, got, tc.want)
		}
	}
	if err := banktransfer.ValidateTemplate("{customer}"); err == nil {
		t.Error("an unknown placeholder must be refused")
	}
	if err := banktransfer.ValidateTemplate("{invoice_number"); err == nil {
		t.Error("an unbalanced brace must be refused")
	}
	if err := banktransfer.ValidateTemplate("###"); err == nil {
		t.Error("a template that renders to nothing must be refused")
	}
	if err := banktransfer.ValidateTemplate("Acme {invoice_number}"); err != nil {
		t.Error(err)
	}
}

func valid() banktransfer.Settings {
	return banktransfer.Settings{BankName: "Example Bank", AccountHolder: "Acme Ltd", IBAN: "tr33 0006 1005 1978 6457 8413 26"}
}

func TestSettings(t *testing.T) {
	s := valid()
	s.BIC, s.Currencies, s.PaymentWindowDays = "tczb tr2a", []string{"try", " eur", "TRY"}, 10
	got, err := s.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	if got.IBAN != "TR330006100519786457841326" || got.BIC != "TCZBTR2A" || !slices.Equal(got.Currencies, []string{"TRY", "EUR"}) {
		t.Errorf("not normalised: %+v", got)
	}
	for name, mutate := range map[string]func(*banktransfer.Settings){
		"no bank":          func(s *banktransfer.Settings) { s.BankName = " " },
		"no holder":        func(s *banktransfer.Settings) { s.AccountHolder = "" },
		"bad iban":         func(s *banktransfer.Settings) { s.IBAN = "TR00 0000" },
		"bad bic":          func(s *banktransfer.Settings) { s.BIC = "ABC" },
		"bad template":     func(s *banktransfer.Settings) { s.ReferenceTemplate = "{nope}" },
		"bad currency":     func(s *banktransfer.Settings) { s.Currencies = []string{"TL"} },
		"window too long":  func(s *banktransfer.Settings) { s.PaymentWindowDays = banktransfer.MaxPaymentWindowDays + 1 },
		"negative window":  func(s *banktransfer.Settings) { s.PaymentWindowDays = -1 },
		"multi-line bank":  func(s *banktransfer.Settings) { s.BankName = "a\nb" },
		"very long holder": func(s *banktransfer.Settings) { s.AccountHolder = strings.Repeat("a", 101) },
	} {
		s := valid()
		mutate(&s)
		if _, err := s.Normalize(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := banktransfer.Factory([]byte(`{"bank_name":"B"}`)); err == nil {
		t.Error("the factory must validate")
	}
	if _, err := banktransfer.Factory([]byte(`nope`)); err == nil {
		t.Error("the factory must refuse garbage")
	}
}

func TestChargeIsPendingAndCallsNothing(t *testing.T) {
	s := valid()
	s.Currencies, s.PaymentWindowDays, s.ReferenceTemplate = []string{"TRY"}, 7, "Acme {invoice_number}"
	p, err := banktransfer.New(s)
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Charge(context.Background(), payments.ChargeRequest{IdempotencyKey: "k", InvoiceID: "0192f0b4-6f6e", InvoiceNumber: "INV-2026-000123", AmountMinor: 150000, Currency: "TRY"})
	var pending *payments.PendingError
	if !errors.As(err, &pending) || pending.Ref != "Acme INV-2026-000123" {
		t.Fatalf("Charge = %v, want a PendingError carrying the reference", err)
	}
	if _, err := p.Charge(context.Background(), payments.ChargeRequest{IdempotencyKey: "k", AmountMinor: 100, Currency: "USD"}); err == nil || errors.As(err, &pending) {
		t.Errorf("a currency the account does not take: %v", err)
	}
	if _, err := p.Charge(context.Background(), payments.ChargeRequest{AmountMinor: 100, Currency: "TRY"}); err == nil {
		t.Error("the idempotency key is mandatory")
	}
	if _, err := p.Charge(context.Background(), payments.ChargeRequest{IdempotencyKey: "k", Currency: "TRY"}); err == nil {
		t.Error("the amount must be positive")
	}
	if _, err := p.Refund(context.Background(), payments.RefundRequest{}); !errors.Is(err, payments.ErrUnsupported) {
		t.Errorf("Refund = %v, want ErrUnsupported", err)
	}
	if _, err := p.Status(context.Background(), "k"); !errors.As(err, &pending) {
		t.Errorf("Status = %v, want pending", err)
	}
	if p.PaymentWindow().Hours() != 7*24 {
		t.Errorf("window %v", p.PaymentWindow())
	}
}

func TestCapabilities(t *testing.T) {
	p, _ := banktransfer.New(valid())
	c := p.Capabilities()
	if c.Name != "banktransfer" || c.Owner() != payments.OwnerPayrudder || c.Refunds || c.HostedCheckout || c.IdempotentCharges || c.OwnsRenewal || c.OwnsDunning {
		t.Errorf("capabilities = %+v", c)
	}
	if !c.CanCharge("JPY") {
		t.Error("no currency list means any currency")
	}
	var _ payments.ManualCollector = p
}

func TestRegistry(t *testing.T) {
	r := payments.NewRegistry()
	banktransfer.Register(r)
	if _, err := r.Build("banktransfer", []byte(`{"bank_name":"B","account_holder":"H","iban":"DE89370400440532013000"}`)); err != nil {
		t.Fatal(err)
	}
}
