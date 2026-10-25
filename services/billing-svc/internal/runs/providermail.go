package runs

import (
	"slices"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/address"
	"github.com/0furkancolak/payrudder/pkg/payments/banktransfer"
	"github.com/0furkancolak/payrudder/pkg/payments/providers"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/saga"
)

// providerMails reports whether the named provider mails the customer its own receipt for what it charges (Paddle does as merchant of record; iyzico does
// not). An empty or unknown name is the tenant's default provider account, which sends nothing Payrudder can rely on.
func providerMails(name string) bool {
	c, ok := providers.Lookup(name)
	return ok && c.SendsOwnReceipts
}

// bankTransfer returns the tenant's bank details for an invoice in currency whose provider is provider, or nil when it is not paid by bank transfer: the provider is
// the bank transfer account, or none is named (the tenant's default account) and the bank transfer account is that default, and the account takes the currency.
func bankTransfer(t *plansv1.Tenant, provider, currency string) *saga.BankTransfer {
	d := t.GetBankTransfer()
	if d.GetIban() == "" || (provider != banktransfer.Name && (provider != "" || !d.GetIsDefault())) {
		return nil
	}
	if cs := d.GetCurrencies(); len(cs) > 0 && !slices.Contains(cs, currency) {
		return nil
	}
	return &saga.BankTransfer{BankName: d.GetBankName(), AccountHolder: d.GetAccountHolder(), IBAN: d.GetIban(), BIC: d.GetBic(), ReferenceTemplate: d.GetReferenceTemplate()}
}

// addressLines is a postal address as the lines printed on an invoice.
func addressLines(a *plansv1.Address) []string {
	return address.Lines(a.GetLine1(), a.GetLine2(), a.GetCity(), a.GetRegion(), a.GetPostalCode(), a.GetCountry())
}
