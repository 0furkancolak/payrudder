package buyer

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
)

type reader struct {
	c   *plansv1.Customer
	err error
}

func (r reader) GetCustomer(context.Context, uuid.UUID, string) (*plansv1.Customer, error) {
	return r.c, r.err
}

func TestTheBuyerIsTheCustomerRecord(t *testing.T) {
	c := &plansv1.Customer{Id: "c1", Name: "Ada Lovelace", Email: "ada@example.com", Phone: "+905321234567", TaxId: "11111111110", Country: "DE",
		Address: &plansv1.Address{Line1: "Nisantasi 1", Line2: "Kat 3", City: "Istanbul", Region: "34", PostalCode: "34000", Country: "TR"}}
	b, err := Of(context.Background(), reader{c: c}, uuid.New(), "ext-1", "85.34.78.112")
	if err != nil || b == nil {
		t.Fatalf("= %+v, %v", b, err)
	}
	if b.ID != "c1" || b.Name != "Ada Lovelace" || b.Phone != "+905321234567" || b.TaxID != "11111111110" || b.Line2 != "Kat 3" || b.PostalCode != "34000" || b.IP != "85.34.78.112" {
		t.Errorf("buyer = %+v", b)
	}
	if b.Country != "TR" {
		t.Errorf("country = %q; the billing address's country is the customer's", b.Country)
	}
	if got := FromCustomer(&plansv1.Customer{Country: "DE"}, "").Country; got != "DE" {
		t.Errorf("a customer with no address falls back to their country: %q", got)
	}
}

func TestNoCustomerIsNoBuyerAndAFailedReadIsAnError(t *testing.T) {
	for name, r := range map[string]Reader{"no reader": nil, "gone": reader{err: status.Error(codes.NotFound, "no")}} {
		if b, err := Of(context.Background(), r, uuid.New(), "ext-1", ""); b != nil || err != nil {
			t.Errorf("%s: %+v, %v", name, b, err)
		}
	}
	if b, err := Of(context.Background(), reader{c: &plansv1.Customer{}}, uuid.New(), "", ""); b != nil || err != nil {
		t.Errorf("a guest has no external id: %+v, %v", b, err)
	}
	boom := errors.New("plans-svc is down")
	if _, err := Of(context.Background(), reader{err: boom}, uuid.New(), "ext-1", ""); !errors.Is(err, boom) {
		t.Errorf("err = %v; a payment is not refused for a record that could not be read", err)
	}
}
