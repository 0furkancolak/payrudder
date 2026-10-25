package command

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/types/known/timestamppb"

	commandsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/commands/v1"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/quantity"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/sagas/model"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store/sqlc"
)

func (e *Engine) snapshotLines(ctx context.Context, tx pgx.Tx, s *saga) ([]sqlc.ListInvoiceLinesForSnapshotRow, error) {
	return sqlc.New(tx).ListInvoiceLinesForSnapshot(ctx, sqlc.ListInvoiceLinesForSnapshotParams{TenantID: s.TenantID, InvoiceID: s.InvoiceID})
}

func (e *Engine) invoiceSnapshot(s *saga, lines []sqlc.ListInvoiceLinesForSnapshotRow) *commandsv1.InvoiceSnapshot {
	snap := &commandsv1.InvoiceSnapshot{
		InvoiceId: s.InvoiceID.String(), Version: int32(s.InvoiceVersion), Number: s.Number, PeriodId: s.PeriodID, Currency: s.Currency, //nolint:gosec // small
		CurrencyExponent: int32(s.Snap.CurrencyExponent), //nolint:gosec // 0..3
		TenantName:       s.Snap.TenantName, CustomerName: s.Snap.CustomerName, CustomerEmail: s.Snap.CustomerEmail,
		IssuedAt: timestamppb.New(s.FinalizedAt), TotalMinor: s.TotalMinor, LogoObjectKey: s.Snap.LogoObjectKey,
		SellerAddressLines: s.Snap.SellerAddress, CustomerAddressLines: s.Snap.CustomerAddress,
		Seller: s.Snap.Seller.Proto(), Buyer: s.Snap.Buyer.Proto(), EInvoiceOn: s.Snap.EInvoiceOn,
	}
	if in := s.Snap.Instructions(s.Number, s.InvoiceID); in != nil {
		snap.PaymentInstructions = &commandsv1.PaymentInstructions{BankName: in.BankName, AccountHolder: in.AccountHolder, Iban: in.IBAN, Bic: in.BIC, Reference: in.Reference}
	}
	var sub int64
	for _, l := range lines {
		ls := &commandsv1.InvoiceLineSnapshot{LineNo: l.LineNo, Kind: l.Kind, Description: l.Description, AmountMinor: l.AmountMinor}
		if l.Quantity != "" && l.QuantityScale != nil {
			if units, err := parseUnits(l.Quantity); err == nil {
				ls.Quantity = quantity.FormatUnits(units, int(*l.QuantityScale))
			}
		}
		if l.UnitLabel != nil {
			ls.UnitLabel = *l.UnitLabel
		}
		if l.Kind != "TAX" {
			sub += l.AmountMinor // the subtotal is before tax
		}
		snap.Lines = append(snap.Lines, ls)
	}
	snap.SubtotalMinor = sub
	return snap
}

func parseUnits(s string) (int64, error) {
	var v int64
	_, err := fmt.Sscan(s, &v)
	return v, err
}

// SweepResult counts what a sweep did.
type SweepResult struct {
	Reissued  int
	Exhausted int
	Dunned    int
	// Polled counts the pending payments (bank transfers) whose status was asked for again.
	Polled int
}

// Sweep handles one batch of sagas whose command went unanswered (re-issue up to MaxStepAttempts, then StepExhausted)
// and the dunning retries that are due. It uses FOR UPDATE SKIP LOCKED, so any number of pods can sweep.
func (e *Engine) Sweep(ctx context.Context, pool *pgxpool.Pool, limit int) (SweepResult, error) {
	var res SweepResult
	now := e.cfg.Now()
	if err := e.sweepDeadlines(ctx, pool, now, limit, &res); err != nil {
		return res, err
	}
	if err := e.sweepDunning(ctx, pool, now, limit, &res); err != nil {
		return res, err
	}
	err := e.sweepPolls(ctx, pool, now, limit, &res)
	return res, err
}

// dueSaga is a saga a sweep found due; the sweep locks it again before it acts.
type dueSaga struct{ tenantID, id uuid.UUID }

// sweepEach handles every due saga in a transaction of its own: lock locks the saga under FOR UPDATE SKIP LOCKED and re-checks the condition of the sweep (it is
// nil when another pod got there first), handle acts on it.
func (e *Engine) sweepEach(ctx context.Context, pool *pgxpool.Pool, due []dueSaga, lock func(q *sqlc.Queries, d dueSaga) (uuid.UUID, error), handle func(tx pgx.Tx, s *saga) error) error {
	for _, d := range due {
		err := pgxx.InTx(ctx, pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
			s, err := e.lockDue(ctx, tx, d.tenantID, func(q *sqlc.Queries) (uuid.UUID, error) { return lock(q, d) })
			if err != nil || s == nil {
				return err
			}
			return handle(tx, s)
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// sweepPolls asks again for the status of the payments that are awaited (a bank transfer whose money has not been confirmed yet) when their poll is due.
// The answer is in the ledger, so a poll costs one query; the payment window runs out in the answer, not here.
func (e *Engine) sweepPolls(ctx context.Context, pool *pgxpool.Pool, now time.Time, limit int, res *SweepResult) error {
	rows, err := sqlc.New(pool).ListSagasDueForPoll(ctx, sqlc.ListSagasDueForPollParams{Now: now, RowLimit: store.I32(limit)})
	if err != nil {
		return err
	}
	due := make([]dueSaga, len(rows))
	for i, r := range rows {
		due[i] = dueSaga{r.TenantID, r.ID}
	}
	return e.sweepEach(ctx, pool, due,
		func(q *sqlc.Queries, d dueSaga) (uuid.UUID, error) {
			return q.LockSagaDueForPoll(ctx, sqlc.LockSagaDueForPollParams{TenantID: d.tenantID, ID: d.id, Now: now})
		},
		func(tx pgx.Tx, s *saga) error {
			out, err := e.handleLoaded(ctx, tx, s, model.EvPollDue, "poll", nil)
			if out.Applied {
				res.Polled++
			}
			return err
		})
}

func (e *Engine) sweepDeadlines(ctx context.Context, pool *pgxpool.Pool, now time.Time, limit int, res *SweepResult) error {
	rows, err := sqlc.New(pool).ListSagasPastDeadline(ctx, sqlc.ListSagasPastDeadlineParams{Now: now, RowLimit: store.I32(limit)})
	if err != nil {
		return err
	}
	due := make([]dueSaga, len(rows))
	for i, r := range rows {
		due[i] = dueSaga{r.TenantID, r.ID}
	}
	return e.sweepEach(ctx, pool, due,
		func(q *sqlc.Queries, d dueSaga) (uuid.UUID, error) {
			return q.LockSagaPastDeadline(ctx, sqlc.LockSagaPastDeadlineParams{TenantID: d.tenantID, ID: d.id, Now: now})
		},
		func(tx pgx.Tx, s *saga) error {
			if s.StepAttempt >= MaxStepAttempts {
				if _, err := e.handleLoaded(ctx, tx, s, model.EvExhausted, "sweeper", nil); err != nil {
					return err
				}
				res.Exhausted++
				return nil
			}
			if err := e.reissue(ctx, tx, s, now); err != nil {
				return err
			}
			res.Reissued++
			return nil
		})
}

func (e *Engine) sweepDunning(ctx context.Context, pool *pgxpool.Pool, now time.Time, limit int, res *SweepResult) error {
	rows, err := sqlc.New(pool).ListSagasDueForDunning(ctx, sqlc.ListSagasDueForDunningParams{Now: now, RowLimit: store.I32(limit)})
	if err != nil {
		return err
	}
	due := make([]dueSaga, len(rows))
	for i, r := range rows {
		due[i] = dueSaga{r.TenantID, r.ID}
	}
	return e.sweepEach(ctx, pool, due,
		func(q *sqlc.Queries, d dueSaga) (uuid.UUID, error) {
			return q.LockSagaDueForDunning(ctx, sqlc.LockSagaDueForDunningParams{TenantID: d.tenantID, ID: d.id, Now: now})
		},
		func(tx pgx.Tx, s *saga) error {
			out, err := e.handleLoaded(ctx, tx, s, model.EvDunningDue, "dunning", nil)
			if out.Applied {
				res.Dunned++
			}
			return err
		})
}

// lockDue re-reads a due saga under FOR UPDATE SKIP LOCKED, re-checking the condition the lock query carries: another pod may have handled it.
func (e *Engine) lockDue(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, lock func(*sqlc.Queries) (uuid.UUID, error)) (*saga, error) {
	id, err := lock(sqlc.New(tx))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return e.load(ctx, tx, tenantID, id)
}

// reissue publishes the command of the saga's current step again with a fresh attempt (a fresh command id; the PSP
// idempotency key of a charge stays the same).
func (e *Engine) reissue(ctx context.Context, tx pgx.Tx, s *saga, now time.Time) error {
	var eff model.Effect
	switch s.State {
	case model.PdfRequested:
		eff = model.EffRender
	case model.PaymentRequested:
		eff = model.EffCharge
	case model.PaymentUnknown, model.PaymentPending:
		eff = model.EffQueryStatus
	case model.NotifyRequested:
		eff = model.EffNotify
	case model.RefundRequested:
		eff = model.EffRefund
	default:
		return fmt.Errorf("saga %s in %s has a deadline but no command", s.ID, s.State)
	}
	s.Attempt++
	s.StepAttempt++
	deadline := now.Add(e.cfg.StepTimeout)
	q := sqlc.New(tx)
	if err := q.ReissueSaga(ctx, sqlc.ReissueSagaParams{Attempt: store.I32(s.Attempt), StepAttempt: store.I32(s.StepAttempt), StepDeadline: deadline, UpdatedAt: now, TenantID: s.TenantID, ID: s.ID}); err != nil {
		return err
	}
	s.Version++
	if err := e.issue(ctx, tx, s, eff); err != nil {
		return err
	}
	seq, err := q.NextSagaEventSeq(ctx, sqlc.NextSagaEventSeqParams{TenantID: s.TenantID, SagaID: s.ID})
	if err != nil {
		return err
	}
	return q.InsertSagaReissueEvent(ctx, sqlc.InsertSagaReissueEventParams{TenantID: s.TenantID, SagaID: s.ID, Seq: seq, State: string(s.State), At: now})
}

// Start creates the saga of a finalized invoice and issues its first command (the PDF render) in tx.
func (e *Engine) Start(ctx context.Context, tx pgx.Tx, tenantID, invoiceID uuid.UUID) (uuid.UUID, error) {
	sagaID := uuid.NewSHA1(commandNS, fmt.Appendf(nil, "saga|%s", invoiceID))
	n, err := sqlc.New(tx).InsertSaga(ctx, sqlc.InsertSagaParams{TenantID: tenantID, ID: sagaID, InvoiceID: invoiceID})
	if err != nil {
		return uuid.Nil, err
	}
	if n == 0 {
		return sagaID, nil // already started: idempotent
	}
	if _, err := e.Handle(ctx, tx, tenantID, sagaID, model.EvStart, "finalize", nil); err != nil {
		return uuid.Nil, err
	}
	return sagaID, nil
}

// ReplyEvents maps a worker reply to the saga event it triggers.
func ReplyEvent(r *commandsv1.ReplyEnvelope) (model.Event, string, bool) {
	switch x := r.GetReply().(type) {
	case *commandsv1.ReplyEnvelope_PdfRendered:
		return model.EvPdfRendered, "reply:PdfRendered", true
	case *commandsv1.ReplyEnvelope_PdfFailed:
		return model.EvPdfFailed, "reply:PdfFailed", true
	case *commandsv1.ReplyEnvelope_PaymentSucceeded:
		return model.EvPaymentOK, "reply:PaymentSucceeded", true
	case *commandsv1.ReplyEnvelope_PaymentDeclined:
		return model.EvPaymentDeclined, "reply:PaymentDeclined", true
	case *commandsv1.ReplyEnvelope_PaymentErrored:
		return model.EvPaymentErrored, "reply:PaymentErrored", true
	case *commandsv1.ReplyEnvelope_PaymentRefunded:
		return model.EvRefunded, "reply:PaymentRefunded", true
	case *commandsv1.ReplyEnvelope_NotificationSent:
		return model.EvNotified, "reply:NotificationSent", true
	case *commandsv1.ReplyEnvelope_PaymentStatus:
		switch x.PaymentStatus.GetState() {
		case commandsv1.PaymentStatus_STATE_SUCCEEDED:
			return model.EvStatusSucceeded, "reply:PaymentStatus(succeeded)", true
		case commandsv1.PaymentStatus_STATE_DECLINED:
			return model.EvStatusDeclined, "reply:PaymentStatus(declined)", true
		case commandsv1.PaymentStatus_STATE_PENDING:
			return model.EvStatusPending, "reply:PaymentStatus(pending)", true
		case commandsv1.PaymentStatus_STATE_NOT_FOUND:
			return model.EvStatusNotFound, "reply:PaymentStatus(not_found)", true
		}
	}
	return "", "", false
}
