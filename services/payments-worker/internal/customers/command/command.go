// Package command makes a tenant's customer known to a payment provider, and opens the provider's page where the customer manages payment methods.
package command

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/payments"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/refuse"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/target"
)

// Plans is what the commands ask of plans-svc, as the tenant.
type Plans interface {
	GetCustomer(ctx context.Context, tenant uuid.UUID, externalID string) (*plansv1.Customer, error)
}

// Deps is what the commands need. Plans nil means plans-svc is not configured, and customers cannot be looked up.
type Deps struct {
	Plans   Plans
	Objects store.Objects
	Log     *slog.Logger
}

// Ensure finds or creates the customer at the provider (and its address for its country), and remembers the provider's ids.
func Ensure(ctx context.Context, d Deps, tg target.Target, api payments.CustomerAPI, externalID string) (payments.CustomerRefs, error) {
	if d.Plans == nil {
		return payments.CustomerRefs{}, refuse.New(http.StatusServiceUnavailable, "payments.customer.not_ready", "plans-svc is not configured")
	}
	c, err := d.Plans.GetCustomer(ctx, tg.Tenant, externalID)
	if err != nil {
		return payments.CustomerRefs{}, refuse.New(http.StatusNotFound, "payments.customer.not_ready", "no such customer")
	}
	refs, err := api.EnsureCustomer(ctx, payments.CustomerSpec{PayrudderCustomerID: c.GetId(), Email: c.GetEmail(), Name: c.GetName(), Country: c.GetCountry()})
	if err != nil {
		return refs, &refuse.Provider{What: "ensure customer", Err: err}
	}
	for _, o := range []struct{ kind, ref string }{{"customer", refs.CustomerRef}, {"address", refs.AddressRef}, {"business", refs.BusinessRef}} {
		if o.ref == "" {
			continue
		}
		if err := d.Objects.Put(ctx, tg.Tenant, tg.Provider, tg.Env, o.kind, "cus:"+c.GetId(), o.ref); err != nil {
			d.Log.ErrorContext(ctx, "could not remember a customer created at the provider", "provider", tg.Provider, "ref", o.ref, "err", err)
		}
	}
	return refs, nil
}

// PortalLink returns the provider's page where the customer manages payment methods and sees invoices. The customer is made known to the provider first.
func PortalLink(ctx context.Context, d Deps, tg target.Target, api payments.CustomerAPI, externalID string) (string, error) {
	refs, err := Ensure(ctx, d, tg, api, externalID)
	if err != nil {
		return "", err
	}
	url, err := api.PortalLink(ctx, refs.CustomerRef, nil)
	if err != nil {
		return "", &refuse.Provider{What: "portal link", Err: err}
	}
	return url, nil
}
