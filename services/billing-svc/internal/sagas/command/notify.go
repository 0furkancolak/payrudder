package command

import (
	"fmt"

	commandsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/commands/v1"
	"github.com/0furkancolak/payrudder/pkg/env"
	"github.com/0furkancolak/payrudder/pkg/quantity"
	invmodel "github.com/0furkancolak/payrudder/services/billing-svc/internal/invoices/model"
)

// mailData is what every mail about an invoice carries.
func mailData(s *saga) map[string]string {
	return map[string]string{
		"invoice_number": s.Number, "invoice_id": s.InvoiceID.String(), "period": s.PeriodID, "amount": quantity.FormatUnits(s.TotalMinor, s.Snap.CurrencyExponent),
		"currency": s.Currency, "customer_name": s.Snap.CustomerName, "tenant_name": s.Snap.TenantName,
		env.Sandbox.String(): fmt.Sprint(s.Snap.Environment == env.Sandbox.String()),
	}
}

// instructionData adds the bank details and the payment reference to the data of a mail, when the invoice is paid by bank transfer.
func instructionData(s *saga, data map[string]string) {
	if in := s.Snap.Instructions(s.Number, s.InvoiceID); in != nil {
		data["bank_name"], data["account_holder"], data["iban"], data["bic"], data["reference"] = in.BankName, in.AccountHolder, in.IBAN, in.BIC, in.Reference
	}
}

// notification is the mail about the outcome of an invoice: the receipt of a paid one, the failure of an uncollectible one.
func (e *Engine) notification(s *saga) *commandsv1.SendNotification {
	tmpl := "invoice_paid.v1"
	if s.InvoiceStatus == invmodel.StatusUncollectible {
		tmpl = "payment_failed.v1"
	}
	data := mailData(s)
	if tmpl == "payment_failed.v1" {
		instructionData(s, data) // a customer who did not pay by transfer is told once more where to
	}
	return &commandsv1.SendNotification{Template: tmpl, To: s.Snap.CustomerEmail, Data: data}
}

// transferNotification is the mail that tells the customer how to pay an invoice by bank transfer: where, how much and with which reference. It goes out when the
// saga starts to wait for the money, and again for every dunning retry (a retry opens a new payment window; the mail doubles as the reminder).
func (e *Engine) transferNotification(s *saga) *commandsv1.SendNotification {
	data := mailData(s)
	instructionData(s, data)
	return &commandsv1.SendNotification{Template: "invoice_transfer.v1", To: s.Snap.CustomerEmail, Data: data}
}
