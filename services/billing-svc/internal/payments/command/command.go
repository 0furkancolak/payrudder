// Package command records money received outside a payment provider against an invoice. The rules live here; the invoice saga decides what a payment does to
// the invoice.
package command

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"

	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/apierr"
	invmodel "github.com/0furkancolak/payrudder/services/billing-svc/internal/invoices/model"
	sagacommand "github.com/0furkancolak/payrudder/services/billing-svc/internal/sagas/command"
	sagamodel "github.com/0furkancolak/payrudder/services/billing-svc/internal/sagas/model"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store/sqlc"
)

// Methods an operator can record a payment with.
var Methods = []string{"bank_transfer", "cash", "other"}

const (
	maxReferenceLen = 128
	maxNoteLen      = 500
	// clockSkew is how far into the future a received_at may be: operators' clocks are not exact.
	clockSkew = 5 * time.Minute
)

// Payment is a payment to record.
type Payment struct {
	Tenant, Invoice uuid.UUID
	Method          string
	AmountMinor     int64
	// ReceivedAt is when the money arrived; zero is now.
	ReceivedAt time.Time
	Reference  string
	Note       string
	// Actor is who records it.
	Actor string
}

// Result is the outcome of Record.
type Result struct {
	Payment sqlc.ListInvoicePaymentsRow
	// Duplicate: the same (invoice, method, reference) was recorded before; nothing was written.
	Duplicate bool
	// Paid is the sum of the payments recorded by hand, Outstanding what the invoice still needs, Settled whether the invoice is paid.
	Paid, Outstanding int64
	Settled           bool
}

// Record writes a payment. The invoice must be open or uncollectible and its saga in a state that can be settled; the payment must not take the sum past the
// invoice's total. When the payments add up to the total the saga is told (OfflineSettlement) in the same transaction, so the invoice is paid and the
// customer is mailed the receipt exactly once.
func Record(ctx context.Context, system *pgxpool.Pool, eng *sagacommand.Engine, in Payment) (Result, error) {
	ref, note := strings.TrimSpace(in.Reference), strings.TrimSpace(in.Note)
	if err := validate(in, ref, note, eng.Now()); err != nil {
		return Result{}, err
	}
	var out Result
	err := pgxx.InTx(ctx, system, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var err error
		out, err = record(ctx, tx, eng, in, ref, note)
		return err
	})
	if err != nil {
		return Result{}, apierr.Wrap(ctx, err)
	}
	return out, nil
}

func validate(in Payment, ref, note string, now time.Time) error {
	switch {
	case !slices.Contains(Methods, in.Method):
		return errkey.New(codes.InvalidArgument, "billing.payment.bad_method", "method is bank_transfer, cash or other")
	case in.AmountMinor <= 0:
		return errkey.New(codes.InvalidArgument, "billing.payment.bad_amount", "amount_minor must be positive")
	case !in.ReceivedAt.IsZero() && in.ReceivedAt.After(now.Add(clockSkew)):
		return errkey.New(codes.InvalidArgument, "billing.payment.bad_date", "received_at is in the future")
	case utf8.RuneCountInString(ref) > maxReferenceLen || strings.ContainsAny(ref, "\n\r\t\x00"):
		return errkey.New(codes.InvalidArgument, "billing.payment.bad_amount", "reference is one line of at most 128 characters")
	case utf8.RuneCountInString(note) > maxNoteLen:
		return errkey.New(codes.InvalidArgument, "billing.payment.bad_amount", "note is at most 500 characters")
	}
	return nil
}

func record(ctx context.Context, tx pgx.Tx, eng *sagacommand.Engine, in Payment, ref, note string) (Result, error) {
	q := sqlc.New(tx)
	inv, err := q.LockInvoiceForPayment(ctx, sqlc.LockInvoiceForPaymentParams{TenantID: in.Tenant, ID: in.Invoice})
	if errors.Is(err, pgx.ErrNoRows) {
		return Result{}, errkey.New(codes.NotFound, "billing.invoice.not_found", "invoice not found")
	}
	if err != nil {
		return Result{}, err
	}
	paid, err := q.SumInvoicePayments(ctx, sqlc.SumInvoicePaymentsParams{TenantID: in.Tenant, InvoiceID: in.Invoice})
	if err != nil {
		return Result{}, err
	}
	if ref != "" {
		prev, err := q.GetInvoicePaymentByReference(ctx, sqlc.GetInvoicePaymentByReferenceParams{TenantID: in.Tenant, InvoiceID: in.Invoice, Method: in.Method, Reference: ref})
		if err == nil {
			if prev.AmountMinor != in.AmountMinor {
				return Result{}, errkey.New(codes.FailedPrecondition, "billing.payment.reference_reused", "that reference was recorded before with another amount")
			}
			return Result{Payment: sqlc.ListInvoicePaymentsRow(prev), Duplicate: true, Paid: paid, Outstanding: max(inv.TotalMinor-paid, 0), Settled: inv.Status == invmodel.StatusPaid}, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return Result{}, err
		}
	}
	sagaID, err := checkPayable(ctx, q, in, inv, paid)
	if err != nil {
		return Result{}, err
	}
	now := eng.Now()
	if in.ReceivedAt.IsZero() {
		in.ReceivedAt = now
	}
	actor := strings.TrimSpace(in.Actor)
	if actor == "" {
		actor = "operator"
	}
	row := sqlc.ListInvoicePaymentsRow{ID: uuid.New(), InvoiceID: in.Invoice, Method: in.Method, AmountMinor: in.AmountMinor, Currency: inv.Currency, ReceivedAt: in.ReceivedAt.UTC(),
		Reference: ref, Note: note, RecordedBy: actor, RecordedAt: now}
	if err := q.InsertInvoicePayment(ctx, sqlc.InsertInvoicePaymentParams{TenantID: in.Tenant, ID: row.ID, InvoiceID: in.Invoice, Method: in.Method, AmountMinor: in.AmountMinor,
		Currency: inv.Currency, ReceivedAt: row.ReceivedAt, Reference: ref, Note: note, RecordedBy: actor, RecordedAt: now}); err != nil {
		return Result{}, err
	}
	res := Result{Payment: row, Paid: paid + in.AmountMinor, Outstanding: inv.TotalMinor - paid - in.AmountMinor}
	if res.Outstanding > 0 {
		return res, nil
	}
	out, err := eng.Handle(ctx, tx, in.Tenant, sagaID, sagamodel.EvSettled, "operator:payment:"+in.Method, nil)
	if err != nil {
		return Result{}, err
	}
	if !out.Applied {
		return Result{}, errkey.New(codes.FailedPrecondition, "billing.payment.not_payable", "the invoice's payment cannot be settled in the state it is in")
	}
	res.Settled = true
	return res, nil
}

// checkPayable refuses a payment the invoice cannot take, and returns the saga to settle.
func checkPayable(ctx context.Context, q *sqlc.Queries, in Payment, inv sqlc.LockInvoiceForPaymentRow, paid int64) (uuid.UUID, error) {
	switch inv.Status {
	case invmodel.StatusPaid:
		return uuid.Nil, errkey.New(codes.FailedPrecondition, "billing.payment.already_paid", "the invoice is paid")
	case invmodel.StatusOpen, invmodel.StatusUncollectible:
	default:
		return uuid.Nil, errkey.Newf(codes.FailedPrecondition, "billing.payment.invoice_not_payable", "a %s invoice takes no payment", inv.Status)
	}
	s, err := q.GetSagaStateByInvoice(ctx, sqlc.GetSagaStateByInvoiceParams{TenantID: in.Tenant, InvoiceID: in.Invoice})
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, errkey.New(codes.FailedPrecondition, "billing.payment.invoice_not_payable", "the invoice is not finalized")
	}
	if err != nil {
		return uuid.Nil, err
	}
	if _, ok := sagamodel.Next(sagamodel.State(s.State), sagamodel.EvSettled, sagamodel.Ctx{TotalMinor: inv.TotalMinor}); !ok {
		return uuid.Nil, errkey.Newf(codes.FailedPrecondition, "billing.payment.not_payable",
			"the invoice's payment is in progress (%s); record the payment when it has settled or failed", s.State)
	}
	if outstanding := inv.TotalMinor - paid; in.AmountMinor > outstanding {
		return uuid.Nil, errkey.Newf(codes.FailedPrecondition, "billing.payment.overpayment",
			"%d is more than is outstanding (%d %s, minor units); record the outstanding amount and settle the rest with the customer outside Payrudder", in.AmountMinor, outstanding, inv.Currency)
	}
	return s.ID, nil
}
