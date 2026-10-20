package runs

import "github.com/0furkancolak/quotify/pkg/payments/providers"

// providerMails reports whether the named provider mails the customer its own receipt for what it charges (Paddle does as merchant of record; iyzico does
// not). An empty or unknown name is the tenant's default provider account, which sends nothing Quotify can rely on.
func providerMails(name string) bool {
	c, ok := providers.Lookup(name)
	return ok && c.SendsOwnReceipts
}
