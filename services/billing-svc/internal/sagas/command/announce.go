package command

import (
	"context"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	eventsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/events/v1"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/sagas/model"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store/sqlc"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/tenantevents"
)

// announce tells the tenant's endpoints and the invoice topic about the states that are outcomes.
func (e *Engine) announce(ctx context.Context, tx pgx.Tx, s *saga, next model.State, cause string, now time.Time) error {
	if typ := map[model.State]string{model.Paid: tenantevents.EventInvoicePaid, model.Voided: tenantevents.EventInvoiceVoided, model.Uncollectible: tenantevents.EventInvoiceUncollectible}[next]; typ != "" {
		// Reaching the state again after a retry is the same occurrence: the key is the invoice.
		data := tenantevents.InvoiceData(s.InvoiceID, s.CustomerID, s.Snap.CustomerExternalID, s.Number, s.PeriodID, s.Currency, s.TotalMinor, s.InvoiceStatus)
		// The environment of the invoice and, for a paid one, how much of the total was tax: plans-svc reads the partner commission off the net amount
		// and refuses an event that disagrees with the environment of its tenant.
		data["environment"] = s.Snap.Environment
		if next == model.Paid {
			tax, err := sqlc.New(tx).SumInvoiceTax(ctx, sqlc.SumInvoiceTaxParams{TenantID: s.TenantID, InvoiceID: s.InvoiceID})
			if err != nil {
				return err
			}
			data["tax_minor"], data["net_minor"] = strconv.FormatInt(tax, 10), strconv.FormatInt(max(s.TotalMinor-tax, 0), 10)
		}
		if err := tenantevents.Write(ctx, tx, s.TenantID, typ, s.InvoiceID.String(), now, data); err != nil {
			return err
		}
	}
	switch next {
	case model.Paid:
		if err := e.invoiceEInvoice(ctx, tx, s, "paid"); err != nil {
			return err
		}
		if err := e.linksPaid(ctx, tx, s, now); err != nil {
			return err
		}
		return e.fact(ctx, tx, s, FactPaid, func(f *eventsv1.InvoiceFact) {
			f.Kind = &eventsv1.InvoiceFact_Paid_{Paid: &eventsv1.InvoiceFact_Paid{}}
		})
	case model.Voided:
		return e.fact(ctx, tx, s, FactVoided, func(f *eventsv1.InvoiceFact) {
			f.Kind = &eventsv1.InvoiceFact_Voided_{Voided: &eventsv1.InvoiceFact_Voided{Reason: cause}}
		})
	}
	return nil
}
