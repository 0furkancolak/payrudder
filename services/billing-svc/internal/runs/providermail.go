package runs

import (
	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/address"
	"github.com/0furkancolak/quotify/pkg/payments/providers"
)

// providerMails reports whether the named provider mails the customer its own receipt for what it charges (Paddle does as merchant of record; iyzico does
// not). An empty or unknown name is the tenant's default provider account, which sends nothing Quotify can rely on.
func providerMails(name string) bool {
	c, ok := providers.Lookup(name)
	return ok && c.SendsOwnReceipts
}

// addressLines is a postal address as the lines printed on an invoice.
func addressLines(a *plansv1.Address) []string {
	return address.Lines(a.GetLine1(), a.GetLine2(), a.GetCity(), a.GetRegion(), a.GetPostalCode(), a.GetCountry())
}
