package command

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	commandsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/commands/v1"
	"github.com/0furkancolak/payrudder/pkg/rabbitx"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/sagas/model"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store/sqlc"
)

// issue writes the outbox row of the command an effect asks for. Its message id is the command id, so issuing the same
// (saga, step, attempt) twice is a no-op.
func (e *Engine) issue(ctx context.Context, tx pgx.Tx, s *saga, eff model.Effect) error {
	step, route := "", ""
	env := &commandsv1.CommandEnvelope{}
	switch eff {
	case model.EffRender:
		step, route = "render", rabbitx.QueueInvoiceRender
		lines, err := e.snapshotLines(ctx, tx, s)
		if err != nil {
			return err
		}
		env.Command = &commandsv1.CommandEnvelope_RenderInvoicePdf{RenderInvoicePdf: &commandsv1.RenderInvoicePdf{Invoice: e.invoiceSnapshot(s, lines)}}
	case model.EffCharge, model.EffChargeDunning:
		step, route = "charge", rabbitx.QueuePaymentCharge
		// A part payment recorded by hand is not charged again: the card is asked for what is still outstanding.
		paid, err := sqlc.New(tx).SumInvoicePayments(ctx, sqlc.SumInvoicePaymentsParams{TenantID: s.TenantID, InvoiceID: s.InvoiceID})
		if err != nil {
			return err
		}
		if s.TotalMinor-paid <= 0 {
			return fmt.Errorf("saga %s: invoice %s has nothing outstanding to charge", s.ID, s.InvoiceID)
		}
		env.Command = &commandsv1.CommandEnvelope_ChargePayment{ChargePayment: &commandsv1.ChargePayment{
			InvoiceId: s.InvoiceID.String(), InvoiceNumber: s.Number, AmountMinor: s.TotalMinor - paid, Currency: s.Currency, PaymentMethod: s.Snap.PaymentMethod, Provider: s.Snap.Provider,
			DunningAttempt: int32(s.DunningAttempt), IdempotencyKey: ChargeKey(s.InvoiceID, s.DunningAttempt), //nolint:gosec // 0..3
		}}
	case model.EffQueryStatus:
		step, route = "status", rabbitx.QueuePaymentStatus
		env.Command = &commandsv1.CommandEnvelope_QueryPaymentStatus{QueryPaymentStatus: &commandsv1.QueryPaymentStatus{
			InvoiceId: s.InvoiceID.String(), IdempotencyKey: ChargeKey(s.InvoiceID, s.DunningAttempt)}}
	case model.EffRefund:
		step, route = "refund", rabbitx.QueuePaymentRefund
		// Credit notes already gave part of the invoice back (to the card, or as a credit balance).
		credited, err := sqlc.New(tx).SumCreditNotes(ctx, sqlc.SumCreditNotesParams{TenantID: s.TenantID, InvoiceID: s.InvoiceID})
		if err != nil {
			return err
		}
		if s.TotalMinor-credited <= 0 {
			return fmt.Errorf("saga %s: invoice %s is credited in full, there is nothing to refund", s.ID, s.InvoiceID)
		}
		env.Command = &commandsv1.CommandEnvelope_RefundPayment{RefundPayment: &commandsv1.RefundPayment{
			InvoiceId: s.InvoiceID.String(), AmountMinor: s.TotalMinor - credited, Currency: s.Currency,
			IdempotencyKey: s.InvoiceID.String() + ":refund"}}
	case model.EffNotify:
		step, route = "notify", rabbitx.QueueNotificationSend
		env.Command = &commandsv1.CommandEnvelope_SendNotification{SendNotification: e.notification(s)}
	case model.EffAwaitTransfer:
		step, route = "notify", rabbitx.QueueNotificationSend
		env.Command = &commandsv1.CommandEnvelope_SendNotification{SendNotification: e.transferNotification(s)}
	default:
		return fmt.Errorf("saga: unknown effect %q", eff)
	}
	return e.publishCommand(ctx, tx, s, env, step, route)
}
