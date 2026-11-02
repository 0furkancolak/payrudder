package command

import (
	"cmp"
	"context"
	"fmt"

	commandsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/commands/v1"
	"github.com/0furkancolak/payrudder/pkg/env"
	"github.com/0furkancolak/payrudder/pkg/localefmt"
	invmodel "github.com/0furkancolak/payrudder/services/billing-svc/internal/invoices/model"
)

// mailData is what every mail about an invoice carries. The amount and the period are written the way the mail's language writes them (pkg/localefmt, the table the
// invoice PDF and the hosted pages read): the customer's own language, else the tenant's default one, else English.
func mailData(s *saga) map[string]string {
	locale := cmp.Or(s.Snap.CustomerLocale, s.Snap.DefaultLocale)
	f := localefmt.For(locale)
	return map[string]string{
		"invoice_number": s.Number, "invoice_id": s.InvoiceID.String(), "period": f.Period(s.PeriodID), "amount": f.Amount(s.TotalMinor, s.Snap.CurrencyExponent),
		"currency": s.Currency, "customer_name": s.Snap.CustomerName, "tenant_name": s.Snap.TenantName, "locale": locale,
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

// actionNotification is the mail that asks the customer to come back and confirm a payment their bank wants them present for (3-D Secure): the amount, the invoice and a link
// to the invoice's page, where the Pay button starts the provider's own hosted checkout. A link that cannot be made is left out and the mail says so in words: it must go
// out, because the customer is the only one who can finish the payment.
func (e *Engine) actionNotification(ctx context.Context, s *saga) *commandsv1.SendNotification {
	data := mailData(s)
	if e.cfg.PayLink != nil {
		link, err := e.cfg.PayLink(ctx, s.TenantID, s.InvoiceID)
		if err != nil {
			e.cfg.Log.WarnContext(ctx, "the link to the invoice page could not be made for the mail asking the customer to confirm the payment", "tenant", s.TenantID, "invoice", s.InvoiceID, "err", err)
		} else if link != "" {
			data["pay_url"] = link
		}
	}
	return &commandsv1.SendNotification{Template: "payment_action_required.v1", To: s.Snap.CustomerEmail, Data: data}
}
