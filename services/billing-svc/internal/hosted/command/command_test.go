package command

import (
	"testing"

	"github.com/google/uuid"

	"github.com/0furkancolak/payrudder/pkg/errkey"
)

func TestCreditPackInputIsNormalizedAndValidated(t *testing.T) {
	pack := uuid.New()
	ok := CreateInput{PackID: pack, CustomerExternalID: "cus_1"}
	if err := ok.normalize(); err != nil {
		t.Fatal(err)
	}
	if ok.Kind != KindCreditPack || ok.Quantity != 1 || ok.days() != DefaultAdhocDays || !ok.singleUse() {
		t.Errorf("a pack link defaults to kind %q, quantity %d, %d days, single use %v", ok.Kind, ok.Quantity, ok.days(), ok.singleUse())
	}
	for name, tc := range map[string]struct {
		in  CreateInput
		key string
	}{
		"no pack":                  {CreateInput{Kind: KindCreditPack, CustomerExternalID: "c"}, "billing.payment_link.pack_required"},
		"no customer":              {CreateInput{Kind: KindCreditPack, PackID: pack}, "billing.payment_link.customer_required"},
		"a price from the client":  {CreateInput{PackID: pack, CustomerExternalID: "c", AmountMinor: 1}, "billing.payment_link.bad_kind"},
		"a currency":               {CreateInput{PackID: pack, CustomerExternalID: "c", Currency: "USD"}, "billing.payment_link.bad_kind"},
		"a description":            {CreateInput{PackID: pack, CustomerExternalID: "c", Description: "free"}, "billing.payment_link.bad_kind"},
		"an invoice":               {CreateInput{Kind: KindCreditPack, PackID: pack, CustomerExternalID: "c", InvoiceID: uuid.New()}, "billing.payment_link.bad_kind"},
		"too many":                 {CreateInput{PackID: pack, CustomerExternalID: "c", Quantity: MaxQuantity + 1}, "billing.credits.bad_quantity"},
		"negative":                 {CreateInput{PackID: pack, CustomerExternalID: "c", Quantity: -1}, "billing.credits.bad_quantity"},
		"a pack on an ad-hoc":      {CreateInput{Kind: KindAdhoc, PackID: pack, AmountMinor: 100, Description: "x"}, "billing.payment_link.bad_kind"},
		"a quantity on an invoice": {CreateInput{Kind: KindInvoice, InvoiceID: uuid.New(), Quantity: 2}, "billing.payment_link.bad_kind"},
	} {
		in := tc.in
		if got := errkey.Of(in.normalize()); got != tc.key {
			t.Errorf("%s: key %q, want %q", name, got, tc.key)
		}
	}
}

func TestIdempotencyFingerprintCoversThePackAndQuantity(t *testing.T) {
	pack := uuid.New()
	base := CreateInput{PackID: pack, CustomerExternalID: "c", Quantity: 1}
	for _, in := range []*CreateInput{&base} {
		if err := in.normalize(); err != nil {
			t.Fatal(err)
		}
	}
	other := CreateInput{PackID: uuid.New(), CustomerExternalID: "c", Quantity: 1}
	more := CreateInput{PackID: pack, CustomerExternalID: "c", Quantity: 2}
	same := CreateInput{PackID: pack, CustomerExternalID: "c"}
	for _, in := range []*CreateInput{&other, &more, &same} {
		if err := in.normalize(); err != nil {
			t.Fatal(err)
		}
	}
	if string(base.fingerprint()) == string(other.fingerprint()) || string(base.fingerprint()) == string(more.fingerprint()) {
		t.Error("another pack or quantity must not be a repeat of the same key")
	}
	if string(base.fingerprint()) != string(same.fingerprint()) {
		t.Error("an omitted quantity is 1: the same request")
	}
}

func TestFactoryKinds(t *testing.T) {
	for kind, want := range map[string]bool{KindAdhoc: true, KindCreditPack: true, KindInvoice: false, "": false} {
		if factory(kind) != want {
			t.Errorf("factory(%q) = %v", kind, !want)
		}
	}
}
