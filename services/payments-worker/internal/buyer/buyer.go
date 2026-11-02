// Package buyer builds the payments.Buyer of a charge or a checkout from the customer record, for the providers that vet the payer (Capabilities.NeedsBuyer).
package buyer

import (
	"context"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/payments"
)

// Reader reads a tenant's customer, as the tenant. plans-svc's client satisfies it.
type Reader interface {
	GetCustomer(ctx context.Context, tenant uuid.UUID, externalID string) (*plansv1.Customer, error)
}

// Of reads the customer and builds their buyer; ip is the payer's address when a person is at the other end (a hosted page), else empty. A customer that does not
// exist, or no external id (a guest), is no buyer: nil, and the adapter says what it lacks. Any other failure of the read is returned, because a payment must not be
// refused for a customer record that could not be read.
func Of(ctx context.Context, r Reader, tenant uuid.UUID, externalID, ip string) (*payments.Buyer, error) {
	if r == nil || externalID == "" {
		return nil, nil
	}
	c, err := r.GetCustomer(ctx, tenant, externalID)
	if status.Code(err) == codes.NotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return FromCustomer(c, ip), nil
}

// FromCustomer is the buyer a customer record makes: the address of the record's billing address (its country is the customer's country, else the customer's own).
func FromCustomer(c *plansv1.Customer, ip string) *payments.Buyer {
	a := c.GetAddress()
	country := a.GetCountry()
	if country == "" {
		country = c.GetCountry()
	}
	return &payments.Buyer{ID: c.GetId(), Name: c.GetName(), Email: c.GetEmail(), Phone: c.GetPhone(), TaxID: c.GetTaxId(),
		Line1: a.GetLine1(), Line2: a.GetLine2(), City: a.GetCity(), Region: a.GetRegion(), PostalCode: a.GetPostalCode(), Country: country, IP: ip}
}
