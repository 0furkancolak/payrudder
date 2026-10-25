// Package command changes credit notes: it creates them, sends and re-sends their refunds, settles what the payment worker answers and retries failed refunds.
// The rules live here.
package command

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"

	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/apierr"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/creditnotes/model"
	sagacommand "github.com/0furkancolak/payrudder/services/billing-svc/internal/sagas/command"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store/sqlc"
)

const maxReasonLen = 500

// Config is what the commands run on.
type Config struct {
	Pool   *pgxpool.Pool // billing_system
	Engine *sagacommand.Engine
	Now    func() time.Time
	Log    *slog.Logger
}

// Commands are the credit-note commands.
type Commands struct {
	cfg Config
	q   *sqlc.Queries
}

// New returns the commands; a missing clock is the wall clock and a missing logger the default one.
func New(cfg Config) *Commands {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	return &Commands{cfg: cfg, q: sqlc.New(cfg.Pool)}
}

// Create writes a credit note for a paid invoice, optionally refunding it to the card.
func (c *Commands) Create(ctx context.Context, tenantID, invoiceID uuid.UUID, amountMinor int64, reason string, refundToCard bool) (*model.CreditNote, error) {
	if len(reason) > maxReasonLen {
		return nil, errkey.New(codes.InvalidArgument, "billing.credit_note.reason_too_long", "reason is at most 500 characters")
	}
	cn, err := c.create(ctx, tenantID, invoiceID, amountMinor, strings.TrimSpace(reason), refundToCard)
	switch {
	case errors.Is(err, model.ErrInvoiceNotFound):
		return nil, errkey.New(codes.NotFound, "billing.invoice.not_found", "invoice not found")
	case errors.Is(err, model.ErrNotPaid):
		return nil, errkey.New(codes.FailedPrecondition, "billing.credit_note.not_paid", err.Error())
	case errors.Is(err, model.ErrNothingLeft):
		return nil, errkey.New(codes.FailedPrecondition, "billing.credit_note.fully_credited", err.Error())
	case errors.Is(err, model.ErrNoRefund):
		return nil, errkey.New(codes.FailedPrecondition, "billing.credit_note.nothing_to_refund", err.Error())
	case errors.Is(err, model.ErrBadAmount):
		return nil, errkey.New(codes.InvalidArgument, "billing.credit_note.bad_amount", err.Error())
	case err != nil:
		return nil, apierr.Internal(ctx, err)
	}
	return cn, nil
}

// RetryRefund asks again for the refund of a credit note whose refund failed.
func (c *Commands) RetryRefund(ctx context.Context, tenantID, creditNoteID uuid.UUID) (*model.CreditNote, error) {
	cn, err := c.retryRefund(ctx, tenantID, creditNoteID)
	switch {
	case errors.Is(err, model.ErrNoteNotFound):
		return nil, errkey.New(codes.NotFound, "billing.credit_note.not_found", "credit note not found")
	case errors.Is(err, model.ErrRefundNotFailed):
		return nil, errkey.New(codes.FailedPrecondition, "billing.credit_note.refund_not_failed", err.Error())
	case err != nil:
		return nil, apierr.Internal(ctx, err)
	}
	return cn, nil
}
