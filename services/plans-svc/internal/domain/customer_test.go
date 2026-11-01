package domain

import (
	"testing"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
)

func TestCustomerPhoneIsOptionalAndInternational(t *testing.T) {
	for _, ok := range []string{"", "+905321234567", "+90 532 123 45 67", "0049 30 123456"} {
		if err := ValidateCustomerContact("Ada", "ada@example.com", "", "", ok); err != nil {
			t.Errorf("phone %q refused: %v", ok, err)
		}
		if err := ValidateCustomer("live", &plansv1.CreateCustomerRequest{ExternalId: "c", Name: "Ada", Email: "ada@example.com", Phone: ok}); err != nil {
			t.Errorf("create with phone %q refused: %v", ok, err)
		}
	}
	for _, bad := range []string{"05321234567", "532 123 45 67", "+12", "+90 532 abc 45 67"} {
		if err := ValidateCustomerContact("Ada", "ada@example.com", "", "", bad); err == nil {
			t.Errorf("phone %q accepted", bad)
		}
		if err := ValidateCustomer("live", &plansv1.CreateCustomerRequest{ExternalId: "c", Name: "Ada", Email: "ada@example.com", Phone: bad}); err == nil {
			t.Errorf("create with phone %q accepted", bad)
		}
	}
}
