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
	"github.com/0furkancolak/quotify/services/billing-svc/internal/store/sqlc"
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
		q := m.q.WithTx(tx)
		inv, err := q.LockInvoiceForCreditNote(ctx, sqlc.LockInvoiceForCreditNoteParams{TenantID: tenantID, ID: invoiceID})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInvoiceNotFound
		}
		if err != nil {
			return err
		}
		if inv.Status != "paid" {
			return ErrNotPaid
		}
		if amount, err = creditableAmount(ctx, q, tenantID, invoiceID, inv.TotalMinor, amount); err != nil {
			return err
		}
		now := m.cfg.Now()
		year := int32(now.UTC().Year()) //nolint:gosec // a calendar year
		seq, err := nextCreditNoteSeq(ctx, q, tenantID, year)
		if err != nil {
			return err
		}
		cn := &CreditNote{ID: uuid.New(), CustomerID: inv.CustomerID, InvoiceID: invoiceID, Number: CreditNoteNumber(inv.Environment, int(year), seq), Currency: inv.Currency,
			Reason: reason, TotalMinor: amount, CreatedAt: now}
		if inv.Number != nil {
			cn.InvoiceNumber = *inv.Number
		}
		disposition := "credit"
		if refund {
			disposition = "refund"
		}
		if err := q.InsertCreditNote(ctx, sqlc.InsertCreditNoteParams{TenantID: tenantID, ID: cn.ID, CustomerID: inv.CustomerID, InvoiceID: invoiceID, Number: cn.Number,
			Currency: inv.Currency, TotalMinor: amount, Reason: reason, CreatedAt: now, Disposition: disposition}); err != nil {
			return err
		}
		if refund {
			cn.Refund = true
			err = m.requestRefund(ctx, tx, tenantID, cn, inv.Environment, 1, 0, 0)
		} else {
			err = q.InsertCreditBalanceForNote(ctx, sqlc.InsertCreditBalanceForNoteParams{TenantID: tenantID, CustomerID: inv.CustomerID, ID: uuid.New(), AmountMinor: amount,
				SourceCreditNoteID: cn.ID, Currency: inv.Currency})
		}
		if err != nil {
			return err
		}
		out = cn
		return creditNoteEvent(ctx, tx, tenantID, saga.EventCreditNoteCreated, cn.ID.String(), now, cn)
	})
	return out, err
}

// creditableAmount is the amount a new credit note takes: what was asked, or what is left of the invoice for 0.
func creditableAmount(ctx context.Context, q *sqlc.Queries, tenantID, invoiceID uuid.UUID, total, amount int64) (int64, error) {
	credited, err := q.SumCreditNotes(ctx, sqlc.SumCreditNotesParams{TenantID: tenantID, InvoiceID: invoiceID})
	if err != nil {
		return 0, err
	}
	left := total - credited
	switch {
	case left <= 0:
		return 0, ErrNothingLeft
	case amount == 0:
		return left, nil
	case amount < 0 || amount > left:
		return 0, ErrBadAmount
	}
	return amount, nil
}

// nextCreditNoteSeq takes the next gap-free number of the tenant and year.
func nextCreditNoteSeq(ctx context.Context, q *sqlc.Queries, tenantID uuid.UUID, year int32) (int64, error) {
	if err := q.EnsureCreditNoteSeq(ctx, sqlc.EnsureCreditNoteSeqParams{TenantID: tenantID, Year: year}); err != nil {
		return 0, err
	}
	seq, err := q.LockCreditNoteSeq(ctx, sqlc.LockCreditNoteSeqParams{TenantID: tenantID, Year: year})
	if err != nil {
		return 0, err
	}
	return seq, q.BumpCreditNoteSeq(ctx, sqlc.BumpCreditNoteSeqParams{TenantID: tenantID, Year: year})
}

// requestRefund sends (or resends, with a higher attempt) the RefundPayment command of a credit note and marks the note requested. The command id changes
// with the attempt so the outbox does not swallow a resend; the idempotency key does not, so the provider refunds once.
func (m *Manager) requestRefund(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, cn *CreditNote, environment string, attempt, runFrom, generation int) error {
	q := m.q.WithTx(tx)
	sagaID, err := q.GetSagaIDByInvoice(ctx, sqlc.GetSagaIDByInvoiceParams{TenantID: tenantID, InvoiceID: cn.InvoiceID})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNoRefund
	}
	if err != nil {
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
	if err := q.MarkRefundRequested(ctx, sqlc.MarkRefundRequestedParams{RefundAttempt: i32(attempt), RefundCommandID: cmdID, RefundRequestedAt: now,
		RefundRunFrom: i32(runFrom), RefundGeneration: i32(generation), TenantID: tenantID, ID: cn.ID}); err != nil {
		return err
	}
	cn.RefundState = RefundRequested
	return saga.EnqueueOutbox(ctx, tx, tenantID, cmdID, rabbitx.ExchangeCommands, rabbitx.QueuePaymentRefund, outbox.DestRabbitMQ, body)
}

// SettleRefund applies a worker's reply to the credit note whose refund command it answers. handled is false when no credit note sent that command.
// A refund that was not accepted for a reason that will not change fails the note; one that may succeed later stays requested for the resend.
func SettleRefund(ctx context.Context, tx pgx.Tx, tenantID, commandID uuid.UUID, reply *commandsv1.ReplyEnvelope) (handled bool, err error) {
	q := sqlc.New(tx)
	row, err := q.LockCreditNoteByRefundCommand(ctx, sqlc.LockCreditNoteByRefundCommandParams{TenantID: tenantID, RefundCommandID: commandID})
	if errors.Is(err, pgx.ErrNoRows) {
		// A late duplicate for a note that is already settled is still handled.
		return q.RefundCommandExists(ctx, sqlc.RefundCommandExistsParams{TenantID: tenantID, RefundCommandID: commandID})
	}
	if err != nil {
		return false, err
	}
	cn := &CreditNote{Refund: true, ID: row.ID, CustomerID: row.CustomerID, InvoiceID: row.InvoiceID, Number: row.Number, Currency: row.Currency, TotalMinor: row.TotalMinor,
		Reason: row.Reason, CreatedAt: row.CreatedAt}
	switch r := reply.GetReply().(type) {
	case *commandsv1.ReplyEnvelope_PaymentRefunded:
		if err := q.MarkRefundRefunded(ctx, sqlc.MarkRefundRefundedParams{RefundRef: r.PaymentRefunded.GetPspRefundId(), TenantID: tenantID, ID: cn.ID}); err != nil {
			return true, err
		}
		cn.RefundState = RefundRefunded
		return true, creditNoteEvent(ctx, tx, tenantID, saga.EventCreditNoteRefunded, commandID.String(), time.Now(), cn)
	case *commandsv1.ReplyEnvelope_PaymentErrored:
		if r.PaymentErrored.GetRetryable() {
			return true, nil
		}
		cn.RefundState, cn.RefundError = RefundFailed, r.PaymentErrored.GetCode()+": "+r.PaymentErrored.GetMessage()
		if err := q.MarkRefundFailed(ctx, sqlc.MarkRefundFailedParams{RefundError: cn.RefundError, TenantID: tenantID, ID: cn.ID}); err != nil {
			return true, err
		}
		return true, creditNoteEvent(ctx, tx, tenantID, saga.EventCreditNoteRefundFailed, commandID.String(), time.Now(), cn)
	}
	return true, nil
}

// pendingRefund is a refund whose command nobody answered for a while.
type pendingRefund struct {
	tenant                       uuid.UUID
	cn                           CreditNote
	env                          string
	attempt, runFrom, generation int
}

// SweepRefunds resends the refunds nobody answered for a while, and fails a note whose refund went unanswered refundMaxAttempts times.
func (m *Manager) SweepRefunds(ctx context.Context) error {
	rows, err := m.q.ListRefundsAwaitingReply(ctx, m.cfg.Now().Add(-refundReplyWait))
	if err != nil {
		return err
	}
	for _, r := range rows {
		p := pendingRefund{tenant: r.TenantID, cn: CreditNote{ID: r.ID, InvoiceID: r.InvoiceID, Currency: r.Currency, TotalMinor: r.TotalMinor}, env: r.Environment,
			attempt: int(r.RefundAttempt), runFrom: int(r.RefundRunFrom), generation: int(r.RefundGeneration)}
		err := pgxx.InTx(ctx, m.cfg.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error { return m.resendRefund(ctx, tx, &p) })
		if err != nil {
			m.log.WarnContext(ctx, "credit note refund not resent", "credit_note", p.cn.ID, "error", err)
		}
	}
	return nil
}

func (m *Manager) resendRefund(ctx context.Context, tx pgx.Tx, p *pendingRefund) error {
	cur, err := m.q.WithTx(tx).LockCreditNoteRefundState(ctx, sqlc.LockCreditNoteRefundStateParams{TenantID: p.tenant, ID: p.cn.ID})
	if err != nil {
		return err
	}
	attempt := int(cur.RefundAttempt)
	if cur.RefundState != RefundRequested || attempt != p.attempt {
		return nil // answered or resent meanwhile
	}
	if attempt-p.runFrom >= refundMaxAttempts {
		return m.failUnanswered(ctx, tx, p, attempt)
	}
	return m.requestRefund(ctx, tx, p.tenant, &p.cn, p.env, attempt+1, p.runFrom, p.generation)
}

func (m *Manager) failUnanswered(ctx context.Context, tx pgx.Tx, p *pendingRefund, attempt int) error {
	q := m.q.WithTx(tx)
	p.cn.RefundError = fmt.Sprintf("%s after %d attempts", noAnswer, attempt-p.runFrom)
	if err := q.MarkRefundFailed(ctx, sqlc.MarkRefundFailedParams{RefundError: p.cn.RefundError, TenantID: p.tenant, ID: p.cn.ID}); err != nil {
		return err
	}
	row, err := q.GetCreditNoteForEvent(ctx, sqlc.GetCreditNoteForEventParams{TenantID: p.tenant, ID: p.cn.ID})
	if err != nil {
		return err
	}
	p.cn.Number, p.cn.Reason, p.cn.CustomerID, p.cn.Refund, p.cn.RefundState = row.Number, row.Reason, row.CustomerID, true, RefundFailed
	return creditNoteEvent(ctx, tx, p.tenant, saga.EventCreditNoteRefundFailed, fmt.Sprintf("%s|gen%d|%d", p.cn.ID, p.generation, attempt), m.cfg.Now(), &p.cn)
}

// RetryRefund asks again for a refund that failed: a new run of attempts.
func (m *Manager) RetryRefund(ctx context.Context, tenantID, creditNoteID uuid.UUID) (*CreditNote, error) {
	var out *CreditNote
	err := pgxx.InTx(ctx, m.cfg.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		row, err := m.q.WithTx(tx).LockRefundForRetry(ctx, sqlc.LockRefundForRetryParams{TenantID: tenantID, ID: creditNoteID})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInvoiceNotFound
		}
		if err != nil {
			return err
		}
		if row.RefundState != RefundFailed {
			return ErrRefundNotFailed
		}
		cn := &CreditNote{ID: creditNoteID, Refund: true, InvoiceID: row.InvoiceID, CustomerID: row.CustomerID, Number: row.Number, Currency: row.Currency,
			TotalMinor: row.TotalMinor, Reason: row.Reason, CreatedAt: row.CreatedAt}
		generation := int(row.RefundGeneration)
		// A refund nobody answered may have gone through: asking again must carry the same key. One the provider rejected did not, and a new key lets it be asked again.
		if !strings.HasPrefix(row.RefundError, noAnswer) {
			generation++
		}
		attempt := int(row.RefundAttempt)
		if err := m.requestRefund(ctx, tx, tenantID, cn, row.Environment, attempt+1, attempt, generation); err != nil {
			return err
		}
		out = cn
		return nil
	})
	return out, err
}

// creditNoteEvent announces a credit note (or the outcome of its refund) to the tenant's endpoints.
func creditNoteEvent(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, typ, key string, at time.Time, cn *CreditNote) error {
	external, err := sqlc.New(tx).GetInvoiceCustomerExternalID(ctx, sqlc.GetInvoiceCustomerExternalIDParams{TenantID: tenantID, ID: cn.InvoiceID})
	if err != nil {
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
