package command

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/0furkancolak/payrudder/pkg/env"
	invmodel "github.com/0furkancolak/payrudder/services/billing-svc/internal/invoices/model"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/sagas/model"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store/sqlc"
)

// step is what a transition writes to the saga row.
type step struct {
	attempt, stepAttempt, dunning int
	deadline, nextAttemptAt       *time.Time
	lastErr                       string
}

func (e *Engine) planStep(s *saga, ev model.Event, res model.Result, now time.Time) step {
	st := step{attempt: s.Attempt, dunning: s.DunningAttempt}
	switch res.Effect {
	case model.EffRender, model.EffCharge, model.EffChargeDunning, model.EffQueryStatus, model.EffRefund, model.EffNotify:
		st.attempt++
		st.stepAttempt = 1
		d := now.Add(e.cfg.StepTimeout)
		st.deadline = &d
		if res.Effect == model.EffChargeDunning {
			st.dunning++
		}
	case model.EffScheduleDunning:
		delays := e.dunningDelays(s)
		at := now.Add(delays[min(s.DunningAttempt, len(delays)-1)]) // the table only schedules while DunningLeft, so delays is not empty
		st.nextAttemptAt = &at
	case model.EffSchedulePoll, model.EffAwaitTransfer:
		// Nothing is outstanding while a payment is awaited: no step deadline, only the time of the next poll. The instructions mail is a command of its own
		// (a new attempt, so a new command id) whose reply nobody waits for.
		if res.Effect == model.EffAwaitTransfer {
			st.attempt++
		}
		at := now.Add(e.cfg.TransferPoll)
		st.nextAttemptAt = &at
	}
	if ev == model.EvExhausted {
		st.lastErr = "step timed out after " + fmt.Sprint(MaxStepAttempts) + " attempts"
		if res.To == model.PaymentUnknown && s.State == model.PaymentUnknown {
			// Parked for the operator: keep the attempt counters, drop the deadline.
			st.stepAttempt = s.StepAttempt
		}
	}
	return st
}

func (e *Engine) transition(ctx context.Context, tx pgx.Tx, s *saga, ev model.Event, res model.Result, cause string, commandID *uuid.UUID) error {
	now := e.cfg.Now()
	from, next := s.State, res.To
	causeKind := strings.SplitN(cause, ":", 2)[0]
	ctx, span := otel.Tracer("github.com/0furkancolak/payrudder/services/billing-svc/saga").Start(ctx, "saga.transition",
		trace.WithAttributes(attribute.String("saga.id", s.ID.String()), attribute.String("saga.from", string(from)),
			attribute.String("saga.to", string(next)), attribute.String("saga.cause", causeKind),
			attribute.String("payrudder.tenant_id", s.TenantID.String())))
	defer span.End()

	st := e.planStep(s, ev, res, now)
	if err := e.persistTransition(ctx, tx, s, next, st, cause, commandID, now); err != nil {
		return err
	}
	e.transitions.Add(ctx, 1, metric.WithAttributes(attribute.String("from", string(from)), attribute.String("to", string(next)), attribute.String("cause", causeKind)))
	s.State, s.Version, s.Attempt, s.StepAttempt, s.DunningAttempt = next, s.Version+1, st.attempt, st.stepAttempt, st.dunning

	if err := e.mirrorInvoiceStatus(ctx, tx, s, from, next); err != nil {
		return err
	}
	if res.Effect != model.EffNone && res.Effect != model.EffScheduleDunning && res.Effect != model.EffSchedulePoll {
		if err := e.issue(ctx, tx, s, res.Effect); err != nil {
			return err
		}
	}
	return e.announce(ctx, tx, s, next, cause, now)
}

func (e *Engine) persistTransition(ctx context.Context, tx pgx.Tx, s *saga, next model.State, st step, cause string, commandID *uuid.UUID, now time.Time) error {
	q := sqlc.New(tx)
	n, err := q.TransitionSaga(ctx, sqlc.TransitionSagaParams{
		State: string(next), Attempt: store.I32(st.attempt), StepAttempt: store.I32(st.stepAttempt), StepDeadline: st.deadline, DunningAttempt: store.I32(st.dunning),
		NextAttemptAt: st.nextAttemptAt, LastError: st.lastErr, UpdatedAt: now, TenantID: s.TenantID, ID: s.ID, Version: store.I32(s.Version), FromState: string(s.State),
	})
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("saga %s changed concurrently", s.ID)
	}
	return appendEvent(ctx, q, s, string(s.State), string(next), cause, commandID, now)
}

func appendEvent(ctx context.Context, q *sqlc.Queries, s *saga, from, to, cause string, commandID *uuid.UUID, now time.Time) error {
	seq, err := q.NextSagaEventSeq(ctx, sqlc.NextSagaEventSeqParams{TenantID: s.TenantID, SagaID: s.ID})
	if err != nil {
		return err
	}
	return q.InsertSagaEvent(ctx, sqlc.InsertSagaEventParams{TenantID: s.TenantID, SagaID: s.ID, Seq: seq, FromState: from, ToState: to, Cause: cause, CommandID: commandID, At: now})
}

// mirrorInvoiceStatus sets the invoice's coarse status from the saga's outcome.
func (e *Engine) mirrorInvoiceStatus(ctx context.Context, tx pgx.Tx, s *saga, from, next model.State) error {
	status := ""
	switch {
	case next == model.Paid, from == model.PdfRendered && next == model.NotifyRequested:
		status = invmodel.StatusPaid
	case next == model.Uncollectible:
		status = invmodel.StatusUncollectible
	case next == model.Voided:
		status = invmodel.StatusVoid
	}
	if status == "" {
		return nil
	}
	if err := sqlc.New(tx).SetInvoiceStatus(ctx, sqlc.SetInvoiceStatusParams{Status: status, TenantID: s.TenantID, ID: s.InvoiceID}); err != nil {
		return err
	}
	s.InvoiceStatus = status
	if next == model.Voided {
		if err := reopenCredits(ctx, tx, s); err != nil {
			return err
		}
		// An invoice that used a coupon gives its redemption back; plans-svc is told by the scheduler (the reversal is queued in this transaction).
		return sqlc.New(tx).QueuePromotionReversal(ctx, sqlc.QueuePromotionReversalParams{TenantID: s.TenantID, ID: s.InvoiceID})
	}
	return nil
}

// reopenCredits gives back the credit balances a voided invoice consumed when its draft was made, so the customer does not lose them.
func reopenCredits(ctx context.Context, tx pgx.Tx, s *saga) error {
	q := sqlc.New(tx)
	if err := q.DeleteCreditApplicationsOfInvoice(ctx, sqlc.DeleteCreditApplicationsOfInvoiceParams{TenantID: s.TenantID, InvoiceID: s.InvoiceID}); err != nil {
		return err
	}
	return q.ClearCreditsAppliedToInvoice(ctx, sqlc.ClearCreditsAppliedToInvoiceParams{TenantID: s.TenantID, InvoiceID: s.InvoiceID})
}

// dunningDelays are the delays before retry 1..n: the tenant's own when set (hours, or minutes in a sandbox tenant), else the defaults.
// A tenant that set none gives up on the first declined charge.
func (e *Engine) dunningDelays(s *saga) []time.Duration {
	if s.RetryHours != nil {
		unit := time.Hour
		if s.Snap.Environment == env.Sandbox.String() {
			unit = time.Minute
		}
		out := make([]time.Duration, len(s.RetryHours))
		for i, h := range s.RetryHours {
			out[i] = time.Duration(h) * unit
		}
		return out
	}
	if s.Snap.Environment == env.Sandbox.String() {
		return e.cfg.DunningSandbox
	}
	return e.cfg.DunningLive
}
