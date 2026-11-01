package convert

import (
	"testing"
	"time"

	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// The warning is worked out from what is stored, so it follows a change of country or kind; it never hides the id.
func TestCustomerCarriesPhoneValidatedTimeAndWarning(t *testing.T) {
	de, tr := "DE", "TR"
	at := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	c := Customer(sqlc.Customer{TaxID: "DE12", Country: &de, CustomerType: "business", Phone: "+4930123456", TaxIDValidatedAt: &at})
	if c.GetTaxIdWarning() != "eu_vat" || c.GetPhone() != "+4930123456" || !c.GetTaxIdValidatedAt().AsTime().Equal(at) || c.GetTaxId() != "DE12" {
		t.Fatalf("customer = %v", c)
	}
	if w := Customer(sqlc.Customer{TaxID: "DE136695976", Country: &de, CustomerType: "business"}).GetTaxIdWarning(); w != "" {
		t.Errorf("a well-formed VAT id warns %q", w)
	}
	if got := Customer(sqlc.Customer{TaxID: "1234567890", Country: &tr, CustomerType: "individual"}); got.GetTaxIdWarning() != "tr_is_vkn" || got.GetTaxIdValidatedAt() != nil {
		t.Errorf("a VKN for a Turkish individual: warning %q, validated %v", got.GetTaxIdWarning(), got.GetTaxIdValidatedAt())
	}
	if w := Customer(sqlc.Customer{TaxID: "12345"}).GetTaxIdWarning(); w != "" {
		t.Errorf("a customer without a country warns %q", w)
	}
}
