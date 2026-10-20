package runs

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	commandsv1 "github.com/0furkancolak/quotify/gen/go/quotify/commands/v1"
	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/outbox"
	"github.com/0furkancolak/quotify/pkg/pgxx"
	"github.com/0furkancolak/quotify/pkg/rabbitx"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/saga"
)

// Refusals of CreateCreditNote.
var (
	ErrInvoiceNotFound = errors.New("credit note: no such invoice")
	// ErrNotPaid: a credit note corrects an invoice the customer has paid. An open invoice is voided instead; a declined one is dunned.
	ErrNotPaid     = errors.New("credit note: only a paid invoice can be credited")
	ErrBadAmount   = errors.New("credit note: the amount must be positive and at most what is left of the invoice")
	ErrNothingLeft = errors.New("credit note: the invoice has been credited in full")
	// ErrNoRefund: the invoice was not paid by a charge a provider can refund.
	ErrNoRefund = errors.New("credit note: the invoice has no card payment to refund")
	// ErrRefundNotFailed: only a refund that failed is retried.
	ErrRefundNotFailed = errors.New("credit note: the refund has not failed")
)

// Refund states of a credit note.
const (
	RefundNone      = "none"
	RefundRequested = "requested"
	RefundRefunded  = "refunded"
	RefundFailed    = "failed"
)

// Refund resends: the provider dedups on the idempotency key, so a resend never refunds twice.
const (
	refundMaxAttempts = 3
	refundReplyWait   = 5 * time.Minute
)

const noAnswer = "no answer from the payment provider"

var refundNS = uuid.MustParse("5e1f0a64-8c37-5d29-b0a1-3f6c2d9e7b48")

// CreditNote is a numbered correction of a paid invoice.
type CreditNote struct {
	ID, CustomerID, InvoiceID uuid.UUID
	Number, InvoiceNumber     string
	Currency, Reason          string
	TotalMinor                int64
	CreatedAt                 time.Time
	// Refund is true when the amount goes back to the card; RefundState and RefundError then say how that went.
	Refund                   bool
	RefundState, RefundError string
}

// CreditNoteNumber formats a credit note number: CN-2026-000007 for live tenants, SBX-CN-2026-000007 for sandbox ones.
func CreditNoteNumber(environment string, year int, seq int64) string {
	prefix := "CN"
	if environment == "sandbox" {
		prefix = "SBX-CN"
	}
	return fmt.Sprintf("%s-%d-%06d", prefix, year, seq)
}

// CreateCreditNote issues a credit note against a paid invoice and adds its amount to the customer's credit balance, or, with refund, asks the invoice's
// payment provider to return it to the card; either way in one transaction: a rollback leaves neither a number consumed nor a credit or refund behind.
// amount 0 means whatever is left of the invoice. The notes of an invoice never exceed it.
func (m *Manager) CreateCreditNote(ctx context.Context, tenantID, invoiceID uuid.UUID, amount int64, reason string, refund bool) (*CreditNote, error) {
	var out *CreditNote
	err := pgxx.InTx(ctx, m.cfg.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var (
			status, currency, env string
			number                *string
			total                 int64
			customer              uuid.UUID
		)
		err := tx.QueryRow(ctx, `SELECT status, currency, COALESCE(snapshot->>'environment', 'live'), number, total_minor, customer_id FROM invoices
			WHERE tenant_id = $1 AND id = $2 FOR UPDATE`, tenantID, invoiceID).Scan(&status, &currency, &env, &number, &total, &customer)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInvoiceNotFound
		}
		if err != nil {
			return err
		}
		if status != "paid" {
			return ErrNotPaid
		}
		var credited int64
		if err := tx.QueryRow(ctx, `SELECT COALESCE(sum(total_minor), 0) FROM credit_notes WHERE tenant_id = $1 AND invoice_id = $2`, tenantID, invoiceID).Scan(&credited); err != nil {
			return err
		}
		left := total - credited
		switch {
		case left <= 0:
			return ErrNothingLeft
		case amount == 0:
			amount = left
		case amount < 0 || amount > left:
			return ErrBadAmount
		}
		now := m.cfg.Now()
		year := now.UTC().Year()
		if _, err := tx.Exec(ctx, `INSERT INTO credit_note_seq (tenant_id, year, next_value) VALUES ($1,$2,1) ON CONFLICT DO NOTHING`, tenantID, year); err != nil {
			return err
		}
		var seq int64
		if err := tx.QueryRow(ctx, `SELECT next_value FROM credit_note_seq WHERE tenant_id = $1 AND year = $2 FOR UPDATE`, tenantID, year).Scan(&seq); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE credit_note_seq SET next_value = next_value + 1 WHERE tenant_id = $1 AND year = $2`, tenantID, year); err != nil {
			return err
		}
		id := uuid.New()
		cn := &CreditNote{ID: id, CustomerID: customer, InvoiceID: invoiceID, Number: CreditNoteNumber(env, year, seq), Currency: currency, Reason: reason, TotalMinor: amount, CreatedAt: now}
		if number != nil {
			cn.InvoiceNumber = *number
		}
		disposition := "credit"
		if refund {
			disposition = "refund"
		}
		if _, err := tx.Exec(ctx, `INSERT INTO credit_notes (tenant_id, id, customer_id, invoice_id, number, currency, total_minor, reason, created_at, disposition) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
			tenantID, id, customer, invoiceID, cn.Number, currency, amount, reason, now, disposition); err != nil {
			return err
		}
		if refund {
			cn.Refund = true
			if err := m.requestRefund(ctx, tx, tenantID, cn, env, 1, 0, 0); err != nil {
				return err
			}
			out = cn
			return creditNoteEvent(ctx, tx, tenantID, saga.EventCreditNoteCreated, cn.ID.String(), now, cn)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO credit_balances (tenant_id, customer_id, id, amount_minor, source_credit_note_id, currency) VALUES ($1,$2,$3,$4,$5,$6)`,
			tenantID, customer, uuid.New(), amount, id, currency); err != nil {
			return err
		}
		out = cn
		return creditNoteEvent(ctx, tx, tenantID, saga.EventCreditNoteCreated, cn.ID.String(), now, cn)
	})
	return out, err
}

// requestRefund sends (or resends, with a higher attempt) the RefundPayment command of a credit note and marks the note requested. The command id changes
// with the attempt so the outbox does not swallow a resend; the idempotency key does not, so the provider refunds once.
func (m *Manager) requestRefund(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, cn *CreditNote, environment string, attempt, runFrom, generation int) error {
	var sagaID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT id FROM invoice_sagas WHERE tenant_id = $1 AND invoice_id = $2`, tenantID, cn.InvoiceID).Scan(&sagaID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNoRefund
		}
		return err
	}
	cmdID := uuid.NewSHA1(refundNS, fmt.Appendf(nil, "%s|%d", cn.ID, attempt))
	now := m.cfg.Now()
	key := cn.InvoiceID.String() + ":cn:" + cn.ID.String() + ":refund"
	if generation > 0 {
		key += ":" + strconv.Itoa(generation)
	}
	env := &commandsv1.CommandEnvelope{
		CommandId: cmdID.String(), SagaId: sagaID.String(), TenantId: tenantID.String(), Attempt: int32(attempt), //nolint:gosec // 1..3
		IssuedAt: timestamppb.New(now), Environment: plansv1.Environment_ENVIRONMENT_LIVE,
		Command: &commandsv1.CommandEnvelope_RefundPayment{RefundPayment: &commandsv1.RefundPayment{
			InvoiceId: cn.InvoiceID.String(), AmountMinor: cn.TotalMinor, Currency: cn.Currency, IdempotencyKey: key}},
	}
	if environment == "sandbox" {
		env.Environment = plansv1.Environment_ENVIRONMENT_SANDBOX
	}
	body, err := proto.Marshal(env)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE credit_notes SET refund_state = 'requested', refund_attempt = $3, refund_command_id = $4, refund_requested_at = $5, refund_error = NULL,
		refund_run_from = $6, refund_generation = $7 WHERE tenant_id = $1 AND id = $2`, tenantID, cn.ID, attempt, cmdID, now, runFrom, generation); err != nil {
		return err
	}
	cn.RefundState = RefundRequested
	return saga.EnqueueOutbox(ctx, tx, tenantID, cmdID, rabbitx.ExchangeCommands, rabbitx.QueuePaymentRefund, outbox.DestRabbitMQ, body)
}

// SettleRefund applies a worker's reply to the credit note whose refund command it answers. handled is false when no credit note sent that command.
// A refund that was not accepted for a reason that will not change fails the note; one that may succeed later stays requested for the resend.
func SettleRefund(ctx context.Context, tx pgx.Tx, tenantID, commandID uuid.UUID, reply *commandsv1.ReplyEnvelope) (handled bool, err error) {
	cn := &CreditNote{Refund: true}
	err = tx.QueryRow(ctx, `SELECT id, customer_id, invoice_id, number, currency, total_minor, COALESCE(reason, ''), created_at FROM credit_notes
		WHERE tenant_id = $1 AND refund_command_id = $2 AND refund_state = 'requested' FOR UPDATE`, tenantID, commandID).Scan(
		&cn.ID, &cn.CustomerID, &cn.InvoiceID, &cn.Number, &cn.Currency, &cn.TotalMinor, &cn.Reason, &cn.CreatedAt)
	id := cn.ID
	if errors.Is(err, pgx.ErrNoRows) {
		var any bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM credit_notes WHERE tenant_id = $1 AND refund_command_id = $2)`, tenantID, commandID).Scan(&any); err != nil {
			return false, err
		}
		return any, nil // a late duplicate for a note that is already settled
	}
	if err != nil {
		return false, err
	}
	switch r := reply.GetReply().(type) {
	case *commandsv1.ReplyEnvelope_PaymentRefunded:
		if _, err = tx.Exec(ctx, `UPDATE credit_notes SET refund_state = 'refunded', refund_ref = $3, refund_error = NULL WHERE tenant_id = $1 AND id = $2`, tenantID, id, r.PaymentRefunded.GetPspRefundId()); err != nil {
			return true, err
		}
		cn.RefundState = RefundRefunded
		return true, creditNoteEvent(ctx, tx, tenantID, saga.EventCreditNoteRefunded, commandID.String(), time.Now(), cn)
	case *commandsv1.ReplyEnvelope_PaymentErrored:
		if r.PaymentErrored.GetRetryable() {
			return true, nil
		}
		cn.RefundState, cn.RefundError = RefundFailed, r.PaymentErrored.GetCode()+": "+r.PaymentErrored.GetMessage()
		if _, err = tx.Exec(ctx, `UPDATE credit_notes SET refund_state = 'failed', refund_error = $3 WHERE tenant_id = $1 AND id = $2`, tenantID, id, cn.RefundError); err != nil {
			return true, err
		}
		return true, creditNoteEvent(ctx, tx, tenantID, saga.EventCreditNoteRefundFailed, commandID.String(), time.Now(), cn)
	}
	return true, nil
}

// SweepRefunds resends the refunds nobody answered for a while, and fails a note whose refund went unanswered refundMaxAttempts times.
func (m *Manager) SweepRefunds(ctx context.Context) error {
	type pending struct {
		tenant                       uuid.UUID
		cn                           CreditNote
		env                          string
		attempt, runFrom, generation int
	}
	rows, err := m.cfg.Pool.Query(ctx, `SELECT c.tenant_id, c.id, c.invoice_id, c.currency, c.total_minor, c.refund_attempt, c.refund_run_from, c.refund_generation, COALESCE(i.snapshot->>'environment', 'live')
		FROM credit_notes c JOIN invoices i ON i.tenant_id = c.tenant_id AND i.id = c.invoice_id
		WHERE c.refund_state = 'requested' AND c.refund_requested_at <= $1 ORDER BY c.refund_requested_at LIMIT 100`, m.cfg.Now().Add(-refundReplyWait))
	if err != nil {
		return err
	}
	var due []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.tenant, &p.cn.ID, &p.cn.InvoiceID, &p.cn.Currency, &p.cn.TotalMinor, &p.attempt, &p.runFrom, &p.generation, &p.env); err != nil {
			rows.Close()
			return err
		}
		due = append(due, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, p := range due {
		err := pgxx.InTx(ctx, m.cfg.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
			var state string
			var attempt int
			if err := tx.QueryRow(ctx, `SELECT refund_state, refund_attempt FROM credit_notes WHERE tenant_id = $1 AND id = $2 FOR UPDATE`, p.tenant, p.cn.ID).Scan(&state, &attempt); err != nil {
				return err
			}
			if state != RefundRequested || attempt != p.attempt {
				return nil // answered or resent meanwhile
			}
			if attempt-p.runFrom >= refundMaxAttempts {
				p.cn.RefundError = fmt.Sprintf("%s after %d attempts", noAnswer, attempt-p.runFrom)
				if _, err := tx.Exec(ctx, `UPDATE credit_notes SET refund_state = 'failed', refund_error = $3 WHERE tenant_id = $1 AND id = $2`, p.tenant, p.cn.ID, p.cn.RefundError); err != nil {
					return err
				}
				var number, reason string
				if err := tx.QueryRow(ctx, `SELECT number, COALESCE(reason, ''), customer_id FROM credit_notes WHERE tenant_id = $1 AND id = $2`, p.tenant, p.cn.ID).Scan(&number, &reason, &p.cn.CustomerID); err != nil {
					return err
				}
				p.cn.Number, p.cn.Reason, p.cn.Refund, p.cn.RefundState = number, reason, true, RefundFailed
				return creditNoteEvent(ctx, tx, p.tenant, saga.EventCreditNoteRefundFailed, fmt.Sprintf("%s|gen%d|%d", p.cn.ID, p.generation, attempt), m.cfg.Now(), &p.cn)
			}
			return m.requestRefund(ctx, tx, p.tenant, &p.cn, p.env, attempt+1, p.runFrom, p.generation)
		})
		if err != nil {
			m.log.WarnContext(ctx, "credit note refund not resent", "credit_note", p.cn.ID, "error", err)
		}
	}
	return nil
}

// RetryRefund asks again for a refund that failed: a new run of attempts.
func (m *Manager) RetryRefund(ctx context.Context, tenantID, creditNoteID uuid.UUID) (*CreditNote, error) {
	var out *CreditNote
	err := pgxx.InTx(ctx, m.cfg.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		cn := &CreditNote{ID: creditNoteID, Refund: true}
		var env, state, lastErr string
		var attempt, generation int
		err := tx.QueryRow(ctx, `SELECT c.invoice_id, c.customer_id, c.number, c.currency, c.total_minor, c.reason, c.created_at, c.refund_state, c.refund_attempt, c.refund_generation, COALESCE(c.refund_error, ''), COALESCE(i.snapshot->>'environment', 'live')
			FROM credit_notes c JOIN invoices i ON i.tenant_id = c.tenant_id AND i.id = c.invoice_id
			WHERE c.tenant_id = $1 AND c.id = $2 AND c.disposition = 'refund' FOR UPDATE OF c`, tenantID, creditNoteID).Scan(
			&cn.InvoiceID, &cn.CustomerID, &cn.Number, &cn.Currency, &cn.TotalMinor, &cn.Reason, &cn.CreatedAt, &state, &attempt, &generation, &lastErr, &env)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInvoiceNotFound
		}
		if err != nil {
			return err
		}
		if state != RefundFailed {
			return ErrRefundNotFailed
		}
		// A refund nobody answered may have gone through: asking again must carry the same key. One the provider rejected did not, and a new key lets it be asked again.
		if !strings.HasPrefix(lastErr, noAnswer) {
			generation++
		}
		if err := m.requestRefund(ctx, tx, tenantID, cn, env, attempt+1, attempt, generation); err != nil {
			return err
		}
		out = cn
		return nil
	})
	return out, err
}

// creditNoteEvent announces a credit note (or the outcome of its refund) to the tenant's endpoints.
func creditNoteEvent(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, typ, key string, at time.Time, cn *CreditNote) error {
	var external string
	if err := tx.QueryRow(ctx, `SELECT COALESCE(snapshot->>'customer_external_id', '') FROM invoices WHERE tenant_id = $1 AND id = $2`, tenantID, cn.InvoiceID).Scan(&external); err != nil {
		return err
	}
	disposition := "credit"
	if cn.Refund {
		disposition = "refund"
	}
	data := map[string]any{
		"credit_note_id": cn.ID.String(), "number": cn.Number, "invoice_id": cn.InvoiceID.String(), "invoice_number": cn.InvoiceNumber, "customer_id": cn.CustomerID.String(),
		"customer_external_id": external, "currency": cn.Currency, "total_minor": fmt.Sprint(cn.TotalMinor), "reason": cn.Reason, "disposition": disposition,
	}
	if cn.Refund {
		data["refund_state"] = cn.RefundState
		if cn.RefundError != "" {
			data["refund_error"] = cn.RefundError
		}
	}
	return saga.TenantEvent(ctx, tx, tenantID, typ, key, at, data)
}
