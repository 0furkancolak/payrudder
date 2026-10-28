package inputs

import (
	"context"
	"fmt"
	"slices"

	"github.com/google/uuid"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/address"
	"github.com/0furkancolak/payrudder/pkg/payments/banktransfer"
	"github.com/0furkancolak/payrudder/pkg/payments/providers"
	einvoicemodel "github.com/0furkancolak/payrudder/services/billing-svc/internal/einvoices/model"
	sagamodel "github.com/0furkancolak/payrudder/services/billing-svc/internal/sagas/model"
)

// SnapshotOf is what an invoice freezes about its addressees when its draft is made.
type SnapshotOf struct {
	TenantID uuid.UUID
	Tenant   *plansv1.Tenant
	Customer *plansv1.Customer
	// External is the customer's external id the invoice is out to (the one of the subscription, which late usage can take from an earlier period).
	External string
	// Provider is the payment provider of the subscription ("" for the tenant's default); PaymentMethod what the saga charges.
	Provider, PaymentMethod string
	Currency                string
	CurrencyExponent        int
}

// Snapshot freezes the invoice's addressees: who is billed, who bills, their addresses, the bank details and the e-invoice parties.
func (s SnapshotOf) Snapshot() sagamodel.Snapshot {
	t, cu := s.Tenant, s.Customer
	snap := sagamodel.Snapshot{
		CustomerExternalID: s.External, CustomerName: cu.GetName(), CustomerEmail: cu.GetEmail(), CustomerLocale: cu.GetLocale(), CustomerType: cu.GetCustomerType(),
		PaymentMethod: s.PaymentMethod, Provider: s.Provider, TenantName: t.GetName(), Environment: Environment(t), CurrencyExponent: s.CurrencyExponent,
		LogoObjectKey: LogoKey(s.TenantID, t), ProviderMails: ProviderMails(s.Provider),
		SellerAddress: AddressLines(t.GetAddress()), CustomerAddress: AddressLines(cu.GetAddress()),
		BankTransfer: BankTransfer(t, s.Provider, s.Currency),
	}
	EInvoiceSnapshot(&snap, t, cu)
	return snap
}

// ProviderMails reports whether the named provider mails the customer its own receipt for what it charges (Paddle does as merchant of record; iyzico does
// not). An empty or unknown name is the tenant's default provider account, which sends nothing Payrudder can rely on.
func ProviderMails(name string) bool {
	c, ok := providers.Lookup(name)
	return ok && c.SendsOwnReceipts
}

// BankTransfer returns the tenant's bank details for an invoice in currency whose provider is provider, or nil when it is not paid by bank transfer: the provider is
// the bank transfer account, or none is named (the tenant's default account) and the bank transfer account is that default, and the account takes the currency.
func BankTransfer(t *plansv1.Tenant, provider, currency string) *sagamodel.BankTransfer {
	d := t.GetBankTransfer()
	if d.GetIban() == "" || (provider != banktransfer.Name && (provider != "" || !d.GetIsDefault())) {
		return nil
	}
	if cs := d.GetCurrencies(); len(cs) > 0 && !slices.Contains(cs, currency) {
		return nil
	}
	return &sagamodel.BankTransfer{BankName: d.GetBankName(), AccountHolder: d.GetAccountHolder(), IBAN: d.GetIban(), BIC: d.GetBic(), ReferenceTemplate: d.GetReferenceTemplate()}
}

// AddressLines is a postal address as the lines printed on an invoice.
func AddressLines(a *plansv1.Address) []string {
	return address.Lines(a.GetLine1(), a.GetLine2(), a.GetCity(), a.GetRegion(), a.GetPostalCode(), a.GetCountry())
}

// EInvoiceSnapshot adds to a snapshot what an e-invoice needs about the seller and the buyer, as the tenant and the customer are now. With the setting off
// nothing is stored, so invoices of tenants that do not use e-invoicing keep their small snapshots.
func EInvoiceSnapshot(s *sagamodel.Snapshot, t *plansv1.Tenant, cu *plansv1.Customer) {
	on := t.GetEInvoiceOn()
	if !einvoicemodel.Enabled(on) {
		return
	}
	s.EInvoiceOn = on
	s.Seller, s.Buyer = eInvoiceParties(t, cu)
}

// eInvoiceParties are the seller and the buyer of an e-invoice from the tenant's and the customer's current data.
func eInvoiceParties(t *plansv1.Tenant, cu *plansv1.Customer) (seller, buyer *sagamodel.Party) {
	l := t.GetLegal()
	name := l.GetLegalName()
	if name == "" {
		name = t.GetName()
	}
	seller = sagamodel.PartyOf(name, t.GetAddress(), l.GetTaxId(), l.GetTaxOffice(), l.GetMersisNo(), l.GetTradeRegistryNo(), "", false)
	country := cu.GetAddress().GetCountry()
	if country == "" {
		country = cu.GetCountry()
	}
	addr := cu.GetAddress()
	if addr == nil {
		addr = &plansv1.Address{}
	}
	a := &plansv1.Address{Line1: addr.GetLine1(), Line2: addr.GetLine2(), City: addr.GetCity(), Region: addr.GetRegion(), PostalCode: addr.GetPostalCode(), Country: country}
	buyer = sagamodel.PartyOf(cu.GetName(), a, cu.GetTaxId(), cu.GetTaxOffice(), "", "", cu.GetEmail(), cu.GetEInvoiceRegistered())
	return seller, buyer
}

// Parties reads the seller and buyer of an invoice's customer as they are now (the operator's retry of a failed e-document refreshes them).
func (in *Inputs) Parties(ctx context.Context, tenantID uuid.UUID, customerExternalID string) (seller, buyer *sagamodel.Party, err error) {
	t, err := in.Tenant(ctx, tenantID)
	if err != nil {
		return nil, nil, err
	}
	resp, err := in.Plans.GetCustomer(TenantCtx(ctx, tenantID), &plansv1.GetCustomerRequest{ExternalId: customerExternalID})
	if err != nil {
		return nil, nil, fmt.Errorf("get customer: %w", err)
	}
	seller, buyer = eInvoiceParties(t, resp.GetCustomer())
	return seller, buyer, nil
}
