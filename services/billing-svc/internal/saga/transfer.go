package saga

import (
	"fmt"

	commandsv1 "github.com/0furkancolak/quotify/gen/go/quotify/commands/v1"
	"github.com/0furkancolak/quotify/pkg/env"
	"github.com/0furkancolak/quotify/pkg/quantity"
)

// instructionData adds the bank details and the payment reference to the data of a mail, when the invoice is paid by bank transfer.
func instructionData(s *saga, data map[string]string) {
	if in := s.Snap.Instructions(s.Number, s.InvoiceID); in != nil {
		data["bank_name"], data["account_holder"], data["iban"], data["bic"], data["reference"] = in.BankName, in.AccountHolder, in.IBAN, in.BIC, in.Reference
	}
}

// transferNotification is the mail that tells the customer how to pay an invoice by bank transfer: where, how much and with which reference. It goes out when the
// saga starts to wait for the money, and again for every dunning retry (a retry opens a new payment window; the mail doubles as the reminder).
func (e *Engine) transferNotification(s *saga) *commandsv1.SendNotification {
	data := map[string]string{
		"invoice_number": s.Number, "invoice_id": s.InvoiceID.String(), "period": s.PeriodID, "amount": quantity.FormatUnits(s.TotalMinor, s.Snap.CurrencyExponent),
		"currency": s.Currency, "customer_name": s.Snap.CustomerName, "tenant_name": s.Snap.TenantName, env.Sandbox.String(): fmt.Sprint(s.Snap.Environment == env.Sandbox.String()),
	}
	instructionData(s, data)
	return &commandsv1.SendNotification{Template: "invoice_transfer.v1", To: s.Snap.CustomerEmail, Data: data}
}
