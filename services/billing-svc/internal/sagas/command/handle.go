package command

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/0furkancolak/payrudder/pkg/paymentgate"
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

// held asks the payment gate whether the charge this event would send is refused now. Only the first charge of an invoice is asked (the saga leaves PDF_RENDERED for it,
// or PAYMENT_HELD is asked again): a decline's retry is held by the sweep, and a charge in flight is not recalled.
func (e *Engine) held(ctx context.Context, s *saga, ev model.Event) (bool, error) {
	first := s.State == model.PdfRendered && ev == model.EvContinue || s.State == model.PaymentHeld && (ev == model.EvDunningDue || ev == model.EvRetry)
	if !first || e.cfg.Gate == nil {
		return false, nil
	}
	if s.State == model.PdfRendered && (s.TotalMinor <= 0 || s.Snap.Collection == model.CollectionLink) {
		return false, nil // nothing to charge, or the customer pays: the table does not look at Held
	}
	v, err := e.cfg.Gate(ctx, s.TenantID, s.Snap.CustomerExternalID)
	if err != nil {
		return false, fmt.Errorf("may the first charge of invoice %s go out: %w", s.InvoiceID, err)
	}
	return v != paymentgate.Allowed, nil
}

func (e *Engine) guardCtx(s *saga, held bool) model.Ctx {
	var age time.Duration
	if !s.ChargeKeyStart.IsZero() {
		age = e.cfg.Now().Sub(s.ChargeKeyStart)
	}
	return model.Ctx{TotalMinor: s.TotalMinor, CreditedMinor: s.CreditedMinor, ByHandMinor: s.ByHandMinor, Paid: s.InvoiceStatus == invmodel.StatusPaid, DunningAttempt: s.DunningAttempt, MaxDunning: len(e.dunningDelays(s)),
		ProviderMails: s.Snap.ProviderMails || s.Snap.SkipMail, Link: s.Snap.Collection == model.CollectionLink, Held: held,
		ChargeAge: age, NotFoundGrace: e.notFoundGrace(s), KeyHorizon: e.cfg.KeyHorizon}
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
		held, err := e.held(ctx, s, ev)
		if err != nil {
			return out, err
		}
		res, ok := model.Next(s.State, ev, e.guardCtx(s, held))
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
