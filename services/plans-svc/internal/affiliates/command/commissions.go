package command

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/affiliates/query"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/convert"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// ReverseCommission is the owner's call: take a commission back for a chargeback (the provider took the money back from the owner) or for any other reason
// of their own. The same (invoice, reason, reference) takes back once.
func (c *Commands) ReverseCommission(ctx context.Context, req *plansv1.ReverseAffiliateCommissionRequest) (*plansv1.ReverseAffiliateCommissionResponse, error) {
	var v domain.Violations
	invoiceID, err := uuid.Parse(req.GetInvoiceId())
	if err != nil {
		v.Add("invoice_id", "the id of the invoice")
	}
	reason := req.GetReason()
	if reason != reversalChargeback && reason != reversalManual {
		v.Add("reason", "chargeback or manual")
	}
	ref := strings.TrimSpace(req.GetReference())
	if reason == reversalManual && ref == "" {
		v.Add("reference", "required for a manual reversal: it names this reversal, so that asking twice takes back once")
	}
	if len(ref) > 100 {
		v.Add("reference", "at most 100 characters")
	}
	if req.GetAmountMinor() < 0 {
		v.Add("amount_minor", "zero (all that is left) or more")
	}
	if len(req.GetNote()) > 500 {
		v.Add("note", "at most 500 characters")
	}
	if err := v.Err(); err != nil {
		return nil, err
	}
	var out *plansv1.ReverseAffiliateCommissionResponse
	err = c.db.Tx(ctx, func(q *sqlc.Queries) error {
		res, err := Reverse(ctx, q, ReverseInput{InvoiceID: invoiceID, Kind: reason, Key: fmt.Sprintf("%s:%s:%s", reason, invoiceID, ref), AmountMinor: req.GetAmountMinor(),
			Note: req.GetNote(), Now: c.now()})
		if err != nil {
			return err
		}
		if !res.Found {
			return errkey.New(codes.NotFound, "affiliate.commission.not_found", "this invoice earned no commission")
		}
		m, err := query.CommissionOfInvoice(ctx, q, invoiceID)
		if err != nil {
			return err
		}
		out = &plansv1.ReverseAffiliateCommissionResponse{ReversedMinor: res.Reversed, Commission: m}
		return nil
	})
	if err != nil {
		return nil, convert.Err(err, "commission")
	}
	return out, nil
}

// Approver makes the commissions whose hold is over approved. It runs on the system pool across tenants, and doing it twice, or on two instances at once, does
// no harm: the statement only moves pending to approved.
type Approver struct {
	pool  *pgxpool.Pool
	every time.Duration
	now   func() time.Time
	log   *slog.Logger
}

// NewApprover returns the job; every is how often it looks (the holds are days long, so an hour is plenty).
func NewApprover(systemPool *pgxpool.Pool, every time.Duration, log *slog.Logger) *Approver {
	return &Approver{pool: systemPool, every: every, now: time.Now, log: log}
}

// WithClock sets the clock the job reads (tests).
func (a *Approver) WithClock(now func() time.Time) *Approver {
	a.now = now
	return a
}

// Once approves what is due now and says how many.
func (a *Approver) Once(ctx context.Context) (int64, error) {
	return sqlc.New(a.pool).SystemApproveDueCommissions(ctx, a.now())
}

// Run looks now and then every interval until ctx ends.
func (a *Approver) Run(ctx context.Context) error {
	t := time.NewTicker(a.every)
	defer t.Stop()
	for {
		if n, err := a.Once(ctx); err != nil {
			a.log.ErrorContext(ctx, "affiliate commissions: approving the due ones failed", "error", err)
		} else if n > 0 {
			a.log.InfoContext(ctx, "affiliate commissions approved", "count", n)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}
