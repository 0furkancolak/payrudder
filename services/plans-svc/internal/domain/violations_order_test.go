package domain

import (
	"strings"
	"testing"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
)

// An invalid request says the same thing every time: the first violation, the one the message names, is the first field in the order the fields are
// declared, not the first the map happened to yield.
func TestAddressViolationsComeInFieldOrder(t *testing.T) {
	long := strings.Repeat("x", 121)
	a := &plansv1.Address{Line1: long, Line2: "a\tb", City: long, Region: long, PostalCode: strings.Repeat("9", 21)}
	want := []string{"address.line1", "address.line2", "address.city", "address.region", "address.postal_code"}
	for range 200 {
		got := fields(t, ValidateAddress("address", a))
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("violations = %v, want %v", got, want)
		}
	}
}

func TestBankTransferViolationsComeInFieldOrder(t *testing.T) {
	d := &plansv1.BankTransferDetails{BankName: "", AccountHolder: "", Iban: "TR000000"}
	for range 200 {
		got := fields(t, ValidateBankTransfer("bank_transfer", d))
		if len(got) < 2 || got[0] != "bank_transfer.bank_name" || got[1] != "bank_transfer.account_holder" {
			t.Fatalf("violations = %v, want bank_name then account_holder first", got)
		}
	}
}
