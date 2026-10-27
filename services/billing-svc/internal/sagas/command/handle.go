package command

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	invmodel "github.com/0furkancolak/payrudder/services/billing-svc/internal/invoices/model"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/sagas/model"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store/sqlc"
)

// ErrNotFound means the saga does not exist.
var ErrNotFound = errors.New("saga: not found")

// Outcome of Handle.
type Outcome struct {
	// Applied is false when the event was invalid or stale for the saga's state and was ignored.
	Applied bool
	From    model.State
	To      model.State
}

// Handle applies an event to a saga: it locks the row, follows the table (including the automatic continuations),
// persists the new state, the audit rows and the outbox commands, all in tx. An invalid pair is ignored (Applied=false).
func (e *Engine) Handle(ctx context.Context, tx pgx.Tx, tenantID, sagaID uuid.UUID, ev model.Event, cause string, commandID *uuid.UUID) (Outcome, error) {
	s, err := e.load(ctx, tx, tenantID, sagaID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Outcome{}, ErrNotFound
	}
	if err != nil {
		return Outcome{}, err
	}
	return e.handleLoaded(ctx, tx, s, ev, cause, commandID)
}

func (e *Engine) guardCtx(s *saga) model.Ctx {
	return model.Ctx{TotalMinor: s.TotalMinor, CreditedMinor: s.CreditedMinor, ByHandMinor: s.ByHandMinor, Paid: s.InvoiceStatus == invmodel.StatusPaid, DunningAttempt: s.DunningAttempt, MaxDunning: len(e.dunningDelays(s)),
		ProviderMails: s.Snap.ProviderMails || s.Snap.SkipMail, Link: s.Snap.Collection == model.CollectionLink}
}

func (e *Engine) handleLoaded(ctx context.Context, tx pgx.Tx, s *saga, ev model.Event, cause string, commandID *uuid.UUID) (Outcome, error) {
	out := Outcome{From: s.State}
	if ev == model.EvVoid {
		// What a void refunds is what the credit notes left, less the money received by hand (it becomes a credit balance, not a refund): an invoice credited in
		// full, or paid in full by hand, has no card payment to give back.
		q := sqlc.New(tx)
		credited, err := q.SumCreditNotes(ctx, sqlc.SumCreditNotesParams{TenantID: s.TenantID, InvoiceID: s.InvoiceID})
		if err != nil {
			return out, err
		}
		byHand, err := q.SumInvoicePayments(ctx, sqlc.SumInvoicePaymentsParams{TenantID: s.TenantID, InvoiceID: s.InvoiceID})
		if err != nil {
			return out, err
		}
		s.CreditedMinor, s.ByHandMinor = credited, byHand
	}
	for {
		res, ok := model.Next(s.State, ev, e.guardCtx(s))
		if !ok {
			if !out.Applied {
				e.ignored.Add(ctx, 1, metric.WithAttributes(attribute.String("state", string(s.State)), attribute.String("event", string(ev))))
			}
			out.To = s.State
			return out, nil
		}
		if err := e.transition(ctx, tx, s, ev, res, cause, commandID); err != nil {
			return out, err
		}
		out.Applied, out.To = true, s.State
		switch s.State {
		case model.PdfRendered, model.Paid, model.Uncollectible, model.Refunded:
			ev, cause, commandID = model.EvContinue, "auto", nil
		default:
			return out, nil
		}
	}
}
