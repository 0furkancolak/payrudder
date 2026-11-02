package command

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	commandsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/commands/v1"
	"github.com/0furkancolak/payrudder/pkg/audit"
	"github.com/0furkancolak/payrudder/pkg/rabbitx"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/creditnotes/model"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store/sqlc"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/tenantdb"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/tenantevents"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/workercmd"
)

// Refund resends carry the same idempotency key. Stripe dedupes on it, so a resend never refunds twice there; iyzico and Paddle do not, so payments-worker answers a
// resend from its ledger and its attempts and never sends one again whose outcome it does not know (Capabilities.IdempotentRefunds, plan 22 G1).
const (
	refundMaxAttempts = 3
	refundReplyWait   = 5 * time.Minute
)

const noAnswer = "no answer from the payment provider"

// The codes of payments-worker's PaymentErrored that leave a refund's outcome unknown (its handler.CodeTimeout and handler.CodeRefundUnknown): the provider may have made
// the refund.
const (
	codeTimeout       = "TIMEOUT"
	codeRefundUnknown = "REFUND_OUTCOME_UNKNOWN"
)

// mayHaveHappened reports whether a failed refund's error leaves open that the provider made it. Asking again must then carry the same key: Stripe answers with the
// first result, and payments-worker refuses to send it again to a provider that does not dedupe, so the note stays failed for the operator to check at the provider.
// After a refusal the money did not move, and a new key lets it be asked again.
func mayHaveHappened(refundError string) bool {
	return strings.HasPrefix(refundError, noAnswer) || strings.HasPrefix(refundError, codeTimeout+":") || strings.HasPrefix(refundError, codeRefundUnknown+":")
}

var refundNS = uuid.MustParse("5e1f0a64-8c37-5d29-b0a1-3f6c2d9e7b48")

// requestRefund sends (or resends, with a higher attempt) the RefundPayment command of a credit note and marks the note requested. The command id changes
// with the attempt so the outbox does not swallow a resend; the idempotency key does not, so a provider that dedupes refunds once and payments-worker guards one that
// does not.
func (c *Commands) requestRefund(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, cn *model.CreditNote, environment string, attempt, runFrom, generation int) error {
	q := c.q.WithTx(tx)
	sagaID, err := q.GetSagaIDByInvoice(ctx, sqlc.GetSagaIDByInvoiceParams{TenantID: tenantID, InvoiceID: cn.InvoiceID})
	if errors.Is(err, pgx.ErrNoRows) {
		return model.ErrNoRefund
	}
	if err != nil {
		return err
	}
	cmdID := uuid.NewSHA1(refundNS, fmt.Appendf(nil, "%s|%d", cn.ID, attempt))
	now := c.cfg.Now()
	key := cn.InvoiceID.String() + ":cn:" + cn.ID.String() + ":refund"
	if generation > 0 {
		key += ":" + strconv.Itoa(generation)
	}
	envelope := &commandsv1.CommandEnvelope{
		Command: &commandsv1.CommandEnvelope_RefundPayment{RefundPayment: &commandsv1.RefundPayment{
			InvoiceId: cn.InvoiceID.String(), AmountMinor: cn.TotalMinor, Currency: cn.Currency, IdempotencyKey: key}},
	}
	if err := q.MarkRefundRequested(ctx, sqlc.MarkRefundRequestedParams{RefundAttempt: store.I32(attempt), RefundCommandID: cmdID, RefundRequestedAt: now,
		RefundRunFrom: store.I32(runFrom), RefundGeneration: store.I32(generation), TenantID: tenantID, ID: cn.ID}); err != nil {
		return err
	}
	cn.RefundState = model.RefundRequested
	return workercmd.Send(ctx, tx, workercmd.Header{
		CommandID: cmdID, SagaID: sagaID, TenantID: tenantID, Attempt: attempt, IssuedAt: now, Environment: environment, Subject: "refund of credit note " + cn.ID.String(),
	}, envelope, rabbitx.QueuePaymentRefund)
}

// SettleRefund applies a worker's reply to the credit note whose refund command it answers. handled is false when no credit note sent that command.
// A refund that was not accepted for a reason that will not change fails the note; one that may succeed later stays requested for the resend.
func SettleRefund(ctx context.Context, tx pgx.Tx, now time.Time, tenantID, commandID uuid.UUID, reply *commandsv1.ReplyEnvelope) (handled bool, err error) {
	q := sqlc.New(tx)
	row, err := q.LockCreditNoteByRefundCommand(ctx, sqlc.LockCreditNoteByRefundCommandParams{TenantID: tenantID, RefundCommandID: commandID})
	if errors.Is(err, pgx.ErrNoRows) {
		// A late duplicate for a note that is already settled is still handled.
		return q.RefundCommandExists(ctx, sqlc.RefundCommandExistsParams{TenantID: tenantID, RefundCommandID: commandID})
	}
	if err != nil {
		return false, err
	}
	cn := &model.CreditNote{Refund: true, ID: row.ID, CustomerID: row.CustomerID, InvoiceID: row.InvoiceID, Number: row.Number, Currency: row.Currency, TotalMinor: row.TotalMinor,
		TaxMinor: row.TaxMinor, Reason: row.Reason, CreatedAt: row.CreatedAt}
	switch r := reply.GetReply().(type) {
	case *commandsv1.ReplyEnvelope_PaymentRefunded:
		if err := q.MarkRefundRefunded(ctx, sqlc.MarkRefundRefundedParams{RefundRef: r.PaymentRefunded.GetPspRefundId(), TenantID: tenantID, ID: cn.ID}); err != nil {
			return true, err
		}
		cn.RefundState = model.RefundRefunded
		return true, creditNoteEvent(ctx, tx, tenantID, tenantevents.EventCreditNoteRefunded, commandID.String(), now, cn)
	case *commandsv1.ReplyEnvelope_PaymentErrored:
		if r.PaymentErrored.GetRetryable() {
			return true, nil
		}
		cn.RefundState, cn.RefundError = model.RefundFailed, r.PaymentErrored.GetCode()+": "+r.PaymentErrored.GetMessage()
		if err := q.MarkRefundFailed(ctx, sqlc.MarkRefundFailedParams{RefundError: cn.RefundError, TenantID: tenantID, ID: cn.ID}); err != nil {
			return true, err
		}
		return true, creditNoteEvent(ctx, tx, tenantID, tenantevents.EventCreditNoteRefundFailed, commandID.String(), now, cn)
	}
	return true, nil
}

// pendingRefund is a refund whose command nobody answered for a while.
type pendingRefund struct {
	tenant                       uuid.UUID
	cn                           model.CreditNote
	env                          string
	attempt, runFrom, generation int
}

// SweepRefunds resends the refunds nobody answered for a while, and fails a note whose refund went unanswered refundMaxAttempts times. It looks in every place a
// tenant's rows can be (the pooled database, each tenant placed in a cell, ADR 0018), and acts on a note only in the place it was found.
func (c *Commands) SweepRefunds(ctx context.Context) error {
	return c.cfg.DB.Each(ctx, c.sweepRefunds)
}

func (c *Commands) sweepRefunds(ctx context.Context, s tenantdb.Scope) error {
	rows, err := s.Scan().ListRefundsAwaitingReply(ctx, c.cfg.Now().Add(-refundReplyWait))
	if err != nil {
		return err
	}
	for _, r := range rows {
		p := pendingRefund{tenant: r.TenantID, cn: model.CreditNote{ID: r.ID, InvoiceID: r.InvoiceID, Currency: r.Currency, TotalMinor: r.TotalMinor, TaxMinor: r.TaxMinor}, env: r.Environment,
			attempt: int(r.RefundAttempt), runFrom: int(r.RefundRunFrom), generation: int(r.RefundGeneration)}
		err := s.InTx(ctx, p.tenant, pgx.TxOptions{}, func(tx pgx.Tx) error { return c.resendRefund(ctx, tx, &p) })
		if tenantdb.Skippable(err) {
			continue // the tenant is moving: the next pass finds the note again
		}
		if err != nil {
			c.cfg.Log.WarnContext(ctx, "credit note refund not resent", "credit_note", p.cn.ID, "error", err)
		}
	}
	return nil
}

func (c *Commands) resendRefund(ctx context.Context, tx pgx.Tx, p *pendingRefund) error {
	cur, err := c.q.WithTx(tx).LockCreditNoteRefundState(ctx, sqlc.LockCreditNoteRefundStateParams{TenantID: p.tenant, ID: p.cn.ID})
	if err != nil {
		return err
	}
	attempt := int(cur.RefundAttempt)
	if cur.RefundState != model.RefundRequested || attempt != p.attempt {
		return nil // answered or resent meanwhile
	}
	if attempt-p.runFrom >= refundMaxAttempts {
		return c.failUnanswered(ctx, tx, p, attempt)
	}
	return c.requestRefund(ctx, tx, p.tenant, &p.cn, p.env, attempt+1, p.runFrom, p.generation)
}

func (c *Commands) failUnanswered(ctx context.Context, tx pgx.Tx, p *pendingRefund, attempt int) error {
	q := c.q.WithTx(tx)
	p.cn.RefundError = fmt.Sprintf("%s after %d attempts", noAnswer, attempt-p.runFrom)
	if err := q.MarkRefundFailed(ctx, sqlc.MarkRefundFailedParams{RefundError: p.cn.RefundError, TenantID: p.tenant, ID: p.cn.ID}); err != nil {
		return err
	}
	row, err := q.GetCreditNoteForEvent(ctx, sqlc.GetCreditNoteForEventParams{TenantID: p.tenant, ID: p.cn.ID})
	if err != nil {
		return err
	}
	p.cn.Number, p.cn.Reason, p.cn.CustomerID, p.cn.Refund, p.cn.RefundState = row.Number, row.Reason, row.CustomerID, true, model.RefundFailed
	return creditNoteEvent(ctx, tx, p.tenant, tenantevents.EventCreditNoteRefundFailed, fmt.Sprintf("%s|gen%d|%d", p.cn.ID, p.generation, attempt), c.cfg.Now(), &p.cn)
}

// retryRefund asks again for a refund that failed: a new run of attempts.
func (c *Commands) retryRefund(ctx context.Context, tenantID, creditNoteID uuid.UUID) (*model.CreditNote, error) {
	var out *model.CreditNote
	err := c.cfg.DB.InTx(ctx, tenantID, pgx.TxOptions{}, func(tx pgx.Tx) error {
		row, err := c.q.WithTx(tx).LockRefundForRetry(ctx, sqlc.LockRefundForRetryParams{TenantID: tenantID, ID: creditNoteID})
		if errors.Is(err, pgx.ErrNoRows) {
			return model.ErrNoteNotFound
		}
		if err != nil {
			return err
		}
		if row.RefundState != model.RefundFailed {
			return model.ErrRefundNotFailed
		}
		cn := &model.CreditNote{ID: creditNoteID, Refund: true, InvoiceID: row.InvoiceID, CustomerID: row.CustomerID, Number: row.Number, Currency: row.Currency,
			TotalMinor: row.TotalMinor, TaxMinor: row.TaxMinor, Reason: row.Reason, CreatedAt: row.CreatedAt}
		generation := int(row.RefundGeneration)
		if !mayHaveHappened(row.RefundError) {
			generation++
		}
		attempt := int(row.RefundAttempt)
		if err := c.requestRefund(ctx, tx, tenantID, cn, row.Environment, attempt+1, attempt, generation); err != nil {
			return err
		}
		out = cn
		return audit.Record(ctx, tx, audit.Event{TenantID: tenantID, Action: "credit_note.refund_retried", TargetType: "credit_note", TargetID: creditNoteID.String(),
			Summary: fmt.Sprintf("asked again for the refund of the credit note %s (%d %s)", cn.Number, cn.TotalMinor, cn.Currency),
			Detail:  map[string]any{"number": cn.Number, "amount_minor": cn.TotalMinor, "currency": cn.Currency, "same_key": generation == int(row.RefundGeneration)}})
	})
	return out, err
}
