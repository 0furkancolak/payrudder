package runs

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/saga"
)

// einvoiceSnapshot adds to a snapshot what an e-invoice needs about the seller and the buyer, as the tenant and the customer are now. With the setting off
// nothing is stored, so invoices of tenants that do not use e-invoicing keep their small snapshots.
func einvoiceSnapshot(s *saga.Snapshot, t *plansv1.Tenant, cu *plansv1.Customer) {
	on := t.GetEInvoiceOn()
	if on == "" || on == "off" {
		return
	}
	s.EInvoiceOn = on
	s.Seller, s.Buyer = EInvoiceParties(t, cu)
}

// EInvoiceParties are the seller and the buyer of an e-invoice from the tenant's and the customer's current data.
func EInvoiceParties(t *plansv1.Tenant, cu *plansv1.Customer) (seller, buyer *saga.Party) {
	l := t.GetLegal()
	name := l.GetLegalName()
	if name == "" {
		name = t.GetName()
	}
	seller = saga.PartyOf(name, t.GetAddress(), l.GetTaxId(), l.GetTaxOffice(), l.GetMersisNo(), l.GetTradeRegistryNo(), "", false)
	country := cu.GetAddress().GetCountry()
	if country == "" {
		country = cu.GetCountry()
	}
	addr := cu.GetAddress()
	if addr == nil {
		addr = &plansv1.Address{}
	}
	a := &plansv1.Address{Line1: addr.GetLine1(), Line2: addr.GetLine2(), City: addr.GetCity(), Region: addr.GetRegion(), PostalCode: addr.GetPostalCode(), Country: country}
	buyer = saga.PartyOf(cu.GetName(), a, cu.GetTaxId(), cu.GetTaxOffice(), "", "", cu.GetEmail(), cu.GetEInvoiceRegistered())
	return seller, buyer
}

// Parties reads the seller and buyer of an invoice's customer as they are now (the operator's retry of a failed e-document refreshes them).
func (m *Manager) Parties(ctx context.Context, tenantID uuid.UUID, customerExternalID string) (seller, buyer *saga.Party, err error) {
	tn, err := m.cfg.Admin.GetTenant(operatorCtx(ctx), &plansv1.GetTenantRequest{TenantId: tenantID.String()})
	if err != nil {
		return nil, nil, fmt.Errorf("get tenant: %w", err)
	}
	cu, err := m.cfg.Plans.GetCustomer(tenantCtx(ctx, tenantID), &plansv1.GetCustomerRequest{ExternalId: customerExternalID})
	if err != nil {
		return nil, nil, fmt.Errorf("get customer: %w", err)
	}
	seller, buyer = EInvoiceParties(tn.GetTenant(), cu.GetCustomer())
	return seller, buyer, nil
}
