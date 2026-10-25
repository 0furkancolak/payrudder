// Package command changes customers: creates them and sets their address and payment method.
package command

import (
	"context"

	"google.golang.org/grpc/codes"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/errkey"
	"github.com/0furkancolak/quotify/pkg/idgen"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/convert"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/domain"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/store"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/store/sqlc"
	tenantsqry "github.com/0furkancolak/quotify/services/plans-svc/internal/tenants/query"
)

// Commands changes customers.
type Commands struct{ db *store.DB }

// New returns the customer commands.
func New(db *store.DB) *Commands { return &Commands{db: db} }

// Create adds the customer the request describes, within the tier's customer quota.
func (c *Commands) Create(ctx context.Context, req *plansv1.CreateCustomerRequest) (*plansv1.Customer, error) {
	var out sqlc.Customer
	err := c.db.Tx(ctx, func(q *sqlc.Queries) error {
		t, err := tenantsqry.Current(ctx, q)
		if err != nil {
			return err
		}
		if err := domain.ValidateCustomer(t.Environment, req); err != nil {
			return err
		}
		if quota := domain.QuotasFor(t.Tier).MaxCustomers; quota > 0 {
			n, err := q.CountCustomers(ctx)
			if err != nil {
				return err
			}
			if n >= quota {
				return errkey.Newf(codes.ResourceExhausted, "plans.customer.quota_reached", "customer quota of %d reached for tier %s", quota, t.Tier)
			}
		}
		var pm *string
		if req.GetPaymentMethod() != "" {
			pm = &req.PaymentMethod
		}
		var country, billing *string
		if req.GetCountry() != "" {
			country = &req.Country
		}
		if req.GetBillingCurrency() != "" {
			billing = &req.BillingCurrency
		}
		if c := req.GetAddress().GetCountry(); c != "" {
			country = &c
		}
		out, err = q.CreateCustomer(ctx, sqlc.CreateCustomerParams{
			ID: idgen.NewUUIDv7(), ExternalID: req.GetExternalId(), Name: req.GetName(), Email: req.GetEmail(), PaymentMethod: pm,
			Country: country, BillingCurrency: billing, Address: convert.AddressJSON(req.GetAddress()),
		})
		return err
	})
	if err != nil {
		return nil, convert.Err(err, "customer")
	}
	return convert.Customer(out), nil
}

// SetAddress replaces the customer's address.
func (c *Commands) SetAddress(ctx context.Context, req *plansv1.SetCustomerAddressRequest) (*plansv1.Customer, error) {
	if req.GetExternalId() == "" {
		return nil, domain.Invalid("external_id", "required")
	}
	if err := domain.ValidateAddress("address", req.GetAddress()); err != nil {
		return nil, err
	}
	params := sqlc.SetCustomerAddressParams{ExternalID: req.GetExternalId(), Address: convert.AddressJSON(req.GetAddress())}
	if c := req.GetAddress().GetCountry(); c != "" {
		params.Country = &c
	}
	var out sqlc.Customer
	err := c.db.Tx(ctx, func(q *sqlc.Queries) error {
		var err error
		out, err = q.SetCustomerAddress(ctx, params)
		return err
	})
	if err != nil {
		return nil, convert.Err(err, "customer")
	}
	return convert.Customer(out), nil
}

// SetTax replaces the customer's tax id, tax office and e-fatura registration flag.
func (c *Commands) SetTax(ctx context.Context, req *plansv1.SetCustomerTaxRequest) (*plansv1.Customer, error) {
	if req.GetExternalId() == "" {
		return nil, domain.Invalid("external_id", "required")
	}
	if err := domain.ValidateCustomerTax(req); err != nil {
		return nil, err
	}
	var out sqlc.Customer
	err := c.db.Tx(ctx, func(q *sqlc.Queries) error {
		var err error
		out, err = q.SetCustomerTax(ctx, sqlc.SetCustomerTaxParams{ExternalID: req.GetExternalId(), TaxID: req.GetTaxId(), TaxOffice: req.GetTaxOffice(),
			EInvoiceRegistered: req.GetEInvoiceRegistered()})
		return err
	})
	if err != nil {
		return nil, convert.Err(err, "customer")
	}
	return convert.Customer(out), nil
}

// SetPaymentMethod replaces the customer's payment method.
func (c *Commands) SetPaymentMethod(ctx context.Context, req *plansv1.SetPaymentMethodRequest) (*plansv1.Customer, error) {
	var out sqlc.Customer
	err := c.db.Tx(ctx, func(q *sqlc.Queries) error {
		t, err := tenantsqry.Current(ctx, q)
		if err != nil {
			return err
		}
		if req.GetPaymentMethod() == "" {
			return domain.Invalid("payment_method", "required")
		}
		if err := domain.ValidatePaymentMethod(t.Environment, req.GetPaymentMethod()); err != nil {
			return domain.Invalid("payment_method", err.Error())
		}
		out, err = q.SetPaymentMethod(ctx, sqlc.SetPaymentMethodParams{ExternalID: req.GetExternalId(), PaymentMethod: &req.PaymentMethod})
		return err
	})
	if err != nil {
		return nil, convert.Err(err, "customer")
	}
	return convert.Customer(out), nil
}
