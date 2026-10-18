package saga

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/types/known/timestamppb"

	commandsv1 "github.com/0furkancolak/quotify/gen/go/quotify/commands/v1"
	"github.com/0furkancolak/quotify/pkg/pgxx"
	"github.com/0furkancolak/quotify/pkg/quantity"
	"github.com/0furkancolak/quotify/pkg/rabbitx"
)

type lineRow struct {
	No          int32
	Kind        string
	Description string
	Meter       *string
	Qty         *string
	Scale       *int16
	UnitLabel   *string
	Amount      int64
}

func (e *Engine) snapshotLines(ctx context.Context, tx pgx.Tx, s *saga) ([]lineRow, error) {
	rows, err := tx.Query(ctx, `
		SELECT line_no, kind, description, meter_code, quantity::text, quantity_scale, unit_label, amount_minor
		FROM invoice_lines WHERE tenant_id = $1 AND invoice_id = $2 ORDER BY line_no`, s.TenantID, s.InvoiceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []lineRow
	for rows.Next() {
		var l lineRow
		if err := rows.Scan(&l.No, &l.Kind, &l.Description, &l.Meter, &l.Qty, &l.Scale, &l.UnitLabel, &l.Amount); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (e *Engine) invoiceSnapshot(s *saga, lines []lineRow) *commandsv1.InvoiceSnapshot {
	snap := &commandsv1.InvoiceSnapshot{
		InvoiceId: s.InvoiceID.String(), Version: int32(s.InvoiceVersion), Number: s.Number, PeriodId: s.PeriodID, Currency: s.Currency, //nolint:gosec // small
		CurrencyExponent: int32(s.Snap.CurrencyExponent), //nolint:gosec // 0..3
		TenantName:       s.Snap.TenantName, CustomerName: s.Snap.CustomerName, CustomerEmail: s.Snap.CustomerEmail,
		IssuedAt: timestamppb.New(s.FinalizedAt), TotalMinor: s.TotalMinor, LogoObjectKey: s.Snap.LogoObjectKey,
	}
	var sub int64
	for _, l := range lines {
		ls := &commandsv1.InvoiceLineSnapshot{LineNo: l.No, Kind: l.Kind, Description: l.Description, AmountMinor: l.Amount}
		if l.Qty != nil && l.Scale != nil {
			if units, err := parseUnits(*l.Qty); err == nil {
				ls.Quantity = quantity.FormatUnits(units, int(*l.Scale))
			}
		}
		if l.UnitLabel != nil {
			ls.UnitLabel = *l.UnitLabel
		}
		sub += l.Amount
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

// Due is a saga whose step deadline or dunning time has passed.
type Due struct {
	TenantID, SagaID uuid.UUID
}

// SweepResult counts what a sweep did.
type SweepResult struct {
	Reissued  int
	Exhausted int
	Dunned    int
}

// Sweep handles one batch of sagas whose command went unanswered (re-issue up to MaxStepAttempts, then StepExhausted)
// and the dunning retries that are due. It uses FOR UPDATE SKIP LOCKED, so any number of pods can sweep.
func (e *Engine) Sweep(ctx context.Context, pool *pgxpool.Pool, limit int) (SweepResult, error) {
	var res SweepResult
	now := e.cfg.Now()
	deadlines, err := e.due(ctx, pool, `SELECT tenant_id, id FROM invoice_sagas WHERE step_deadline IS NOT NULL AND step_deadline < $1
		ORDER BY step_deadline LIMIT $2`, now, limit)
	if err != nil {
		return res, err
	}
	for _, d := range deadlines {
		err := pgxx.InTx(ctx, pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
			s, err := e.lockDue(ctx, tx, d, `step_deadline IS NOT NULL AND step_deadline < $3`, now)
			if err != nil || s == nil {
				return err
			}
			if s.StepAttempt >= MaxStepAttempts {
				if _, err := e.handleLoaded(ctx, tx, s, EvExhausted, "sweeper", nil); err != nil {
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
		if err != nil {
			return res, err
		}
	}
	dunning, err := e.due(ctx, pool, `SELECT tenant_id, id FROM invoice_sagas WHERE state = 'PAYMENT_DECLINED' AND next_attempt_at <= $1
		ORDER BY next_attempt_at LIMIT $2`, now, limit)
	if err != nil {
		return res, err
	}
	for _, d := range dunning {
		err := pgxx.InTx(ctx, pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
			s, err := e.lockDue(ctx, tx, d, `state = 'PAYMENT_DECLINED' AND next_attempt_at <= $3`, now)
			if err != nil || s == nil {
				return err
			}
			out, err := e.handleLoaded(ctx, tx, s, EvDunningDue, "dunning", nil)
			if out.Applied {
				res.Dunned++
			}
			return err
		})
		if err != nil {
			return res, err
		}
	}
	return res, nil
}

func (e *Engine) due(ctx context.Context, pool *pgxpool.Pool, q string, now time.Time, limit int) ([]Due, error) {
	rows, err := pool.Query(ctx, q, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Due
	for rows.Next() {
		var d Due
		if err := rows.Scan(&d.TenantID, &d.SagaID); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// lockDue re-reads a due saga under FOR UPDATE SKIP LOCKED, re-checking the condition: another pod may have handled it.
func (e *Engine) lockDue(ctx context.Context, tx pgx.Tx, d Due, cond string, now time.Time) (*saga, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx, `SELECT id FROM invoice_sagas WHERE tenant_id = $1 AND id = $2 AND `+cond+` FOR UPDATE SKIP LOCKED`, d.TenantID, d.SagaID, now).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return e.load(ctx, tx, d.TenantID, d.SagaID)
}

// reissue publishes the command of the saga's current step again with a fresh attempt (a fresh command id; the PSP
// idempotency key of a charge stays the same).
func (e *Engine) reissue(ctx context.Context, tx pgx.Tx, s *saga, now time.Time) error {
	var eff Effect
	switch s.State {
	case PdfRequested:
		eff = EffRender
	case PaymentRequested:
		eff = EffCharge
	case PaymentUnknown:
		eff = EffQueryStatus
	case NotifyRequested:
		eff = EffNotify
	case RefundRequested:
		eff = EffRefund
	default:
		return fmt.Errorf("saga %s in %s has a deadline but no command", s.ID, s.State)
	}
	s.Attempt++
	s.StepAttempt++
	deadline := now.Add(e.cfg.StepTimeout)
	if _, err := tx.Exec(ctx, `UPDATE invoice_sagas SET attempt = $1, step_attempt = $2, step_deadline = $3, version = version + 1, updated_at = $4
		WHERE tenant_id = $5 AND id = $6`, s.Attempt, s.StepAttempt, deadline, now, s.TenantID, s.ID); err != nil {
		return err
	}
	s.Version++
	if err := e.issue(ctx, tx, s, eff); err != nil {
		return err
	}
	var seq int
	if err := tx.QueryRow(ctx, `SELECT COALESCE(max(seq), 0) + 1 FROM saga_events WHERE tenant_id = $1 AND saga_id = $2`, s.TenantID, s.ID).Scan(&seq); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO saga_events (tenant_id, saga_id, seq, from_state, to_state, cause, at) VALUES ($1,$2,$3,$4,$4,'timeout:reissue',$5)`,
		s.TenantID, s.ID, seq, string(s.State), now)
	return err
}

// Start creates the saga of a finalized invoice and issues its first command (the PDF render) in tx.
func (e *Engine) Start(ctx context.Context, tx pgx.Tx, tenantID, invoiceID uuid.UUID) (uuid.UUID, error) {
	sagaID := uuid.NewSHA1(commandNS, fmt.Appendf(nil, "saga|%s", invoiceID))
	tag, err := tx.Exec(ctx, `INSERT INTO invoice_sagas (tenant_id, id, invoice_id, state, version) VALUES ($1,$2,$3,'FINALIZED',1)
		ON CONFLICT (tenant_id, invoice_id) DO NOTHING`, tenantID, sagaID, invoiceID)
	if err != nil {
		return uuid.Nil, err
	}
	if tag.RowsAffected() == 0 {
		return sagaID, nil // already started: idempotent
	}
	if _, err := e.Handle(ctx, tx, tenantID, sagaID, EvStart, "finalize", nil); err != nil {
		return uuid.Nil, err
	}
	return sagaID, nil
}

// ReplyEvents maps a worker reply to the saga event it triggers.
func ReplyEvent(r *commandsv1.ReplyEnvelope) (Event, string, bool) {
	switch x := r.GetReply().(type) {
	case *commandsv1.ReplyEnvelope_PdfRendered:
		return EvPdfRendered, "reply:PdfRendered", true
	case *commandsv1.ReplyEnvelope_PdfFailed:
		return EvPdfFailed, "reply:PdfFailed", true
	case *commandsv1.ReplyEnvelope_PaymentSucceeded:
		return EvPaymentOK, "reply:PaymentSucceeded", true
	case *commandsv1.ReplyEnvelope_PaymentDeclined:
		return EvPaymentDeclined, "reply:PaymentDeclined", true
	case *commandsv1.ReplyEnvelope_PaymentErrored:
		return EvPaymentErrored, "reply:PaymentErrored", true
	case *commandsv1.ReplyEnvelope_PaymentRefunded:
		return EvRefunded, "reply:PaymentRefunded", true
	case *commandsv1.ReplyEnvelope_NotificationSent:
		return EvNotified, "reply:NotificationSent", true
	case *commandsv1.ReplyEnvelope_PaymentStatus:
		switch x.PaymentStatus.GetState() {
		case commandsv1.PaymentStatus_STATE_SUCCEEDED:
			return EvStatusSucceeded, "reply:PaymentStatus(succeeded)", true
		case commandsv1.PaymentStatus_STATE_DECLINED:
			return EvStatusDeclined, "reply:PaymentStatus(declined)", true
		case commandsv1.PaymentStatus_STATE_NOT_FOUND:
			return EvStatusNotFound, "reply:PaymentStatus(not_found)", true
		}
	}
	return "", "", false
}

var _ = rabbitx.ReplyQueue
