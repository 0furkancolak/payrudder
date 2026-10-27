package command

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/cockroachdb/apd/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	invmodel "github.com/0furkancolak/payrudder/services/billing-svc/internal/invoices/model"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/rating"
	sagamodel "github.com/0furkancolak/payrudder/services/billing-svc/internal/sagas/model"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store/sqlc"
)

// RegularDraft is the regular invoice of one customer for a billing run, about to be inserted.
type RegularDraft struct {
	// Provider and Country are what reports split by: the subscription's payment provider and the customer's country.
	Provider, Country string
	Currency          string
	CustomerID        uuid.UUID
	PlanVersion       uuid.UUID
	Lines             []rating.Line
	LineScales        []int
	Total             int64
	Snapshot          sagamodel.Snapshot
}

// InsertRegular inserts the regular drafts of a billing run in one transaction and returns how many were new: a customer that already has its invoice for the
// period (the partial unique index one_regular_invoice) keeps it, so a retried run converges.
func (c *Commands) InsertRegular(ctx context.Context, tenantID uuid.UUID, periodID string, drafts []RegularDraft) (int, error) {
	created := 0
	err := c.cfg.DB.InTx(ctx, tenantID, pgx.TxOptions{}, func(tx pgx.Tx) error {
		created = 0
		q := c.q.WithTx(tx)
		for _, d := range drafts {
			inserted, err := insertRegular(ctx, q, tenantID, periodID, d)
			if err != nil {
				return err
			}
			if inserted {
				created++
			}
		}
		return nil
	})
	return created, err
}

// insertRegular writes one draft invoice and its lines; false means the customer already has its invoice for the period.
func insertRegular(ctx context.Context, q *sqlc.Queries, tenantID uuid.UUID, periodID string, d RegularDraft) (bool, error) {
	snap, err := json.Marshal(d.Snapshot)
	if err != nil {
		return false, err
	}
	id := uuid.New()
	subtotal := rating.Subtotal(d.Lines) // before tax
	_, err = q.InsertDraftInvoice(ctx, sqlc.InsertDraftInvoiceParams{
		TenantID: tenantID, ID: id, CustomerID: d.CustomerID, PeriodID: periodID, Kind: invmodel.KindRegular, PlanVersionID: d.PlanVersion, Currency: d.Currency,
		SubtotalMinor: subtotal, TotalMinor: d.Total, Snapshot: snap, Provider: d.Provider, Country: store.NilIfEmpty(d.Country),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	// Credits from re-rating are applied to the first regular invoice that can use them.
	lines, scales, err := ApplyCredits(ctx, q, tenantID, d.CustomerID, id, d.Currency, subtotal, d.Total, d.Lines, d.LineScales, "Credit from a price correction of an earlier period")
	if err != nil {
		return false, err
	}
	return true, InsertLines(ctx, q, tenantID, id, lines, scales)
}

// ApplyCredits consumes the customer's unapplied credit balances against the invoice (oldest first, up to its total), records the applications and, when any
// was used, returns the lines with a CREDIT line added and lowers the invoice's totals. A credit larger than what is left to pay stays partly unapplied for the
// next invoice. subtotal is the invoice's total before tax and total what it stands at now.
func ApplyCredits(ctx context.Context, q *sqlc.Queries, tenantID, customerID, invoiceID uuid.UUID, currency string, subtotal, total int64,
	lines []rating.Line, scales []int, description string) ([]rating.Line, []int, error) {
	credit, err := applyCredits(ctx, q, tenantID, customerID, invoiceID, currency, total)
	if err != nil || credit == 0 {
		return lines, scales, err
	}
	lines = append(append([]rating.Line(nil), lines...), rating.Line{Kind: rating.KindCredit, AmountMinor: -credit, Description: description})
	scales = append(append([]int(nil), scales...), 0)
	err = q.SetInvoiceTotals(ctx, sqlc.SetInvoiceTotalsParams{SubtotalMinor: subtotal - credit, TotalMinor: total - credit, TenantID: tenantID, ID: invoiceID})
	return lines, scales, err
}

// applyCredits consumes the customer's unapplied credit balances, oldest first, up to max minor units, records the
// applications against the invoice and returns the amount applied.
func applyCredits(ctx context.Context, q *sqlc.Queries, tenantID, customerID, invoiceID uuid.UUID, currency string, max int64) (int64, error) {
	if max <= 0 {
		return 0, nil
	}
	credits, err := q.ListUnappliedCredits(ctx, sqlc.ListUnappliedCreditsParams{TenantID: tenantID, CustomerID: customerID, Currency: currency})
	if err != nil {
		return 0, err
	}
	var applied int64
	for _, c := range credits {
		if applied >= max {
			break
		}
		use := min(c.Remaining, max-applied)
		if use <= 0 {
			continue
		}
		if err := q.InsertCreditApplication(ctx, sqlc.InsertCreditApplicationParams{TenantID: tenantID, CreditID: c.ID, InvoiceID: invoiceID, AmountMinor: use}); err != nil {
			return 0, err
		}
		if use == c.Remaining {
			if err := q.MarkCreditApplied(ctx, sqlc.MarkCreditAppliedParams{AppliedInvoiceID: invoiceID, TenantID: tenantID, ID: c.ID}); err != nil {
				return 0, err
			}
		}
		applied += use
	}
	return applied, nil
}

// InsertLines writes the lines of an invoice, numbered from 1; scales[i] is the scale of lines[i]'s quantity.
func InsertLines(ctx context.Context, q *sqlc.Queries, tenantID, invoiceID uuid.UUID, lines []rating.Line, scales []int) error {
	for i, l := range lines {
		p := sqlc.InsertInvoiceLineParams{
			TenantID: tenantID, InvoiceID: invoiceID, LineNo: store.I32(i + 1), Kind: string(l.Kind), MeterCode: store.NilIfEmpty(l.MeterCode),
			OriginPeriodID: store.NilIfEmpty(l.OriginPeriod), UnitPrice: store.NilIfEmpty(l.UnitPrice), AmountMinor: l.AmountMinor, Description: l.Description,
			ServiceFrom: store.NilIfZero(l.ServiceFrom), ServiceTo: store.NilIfZero(l.ServiceTo), CouponCode: store.NilIfEmpty(l.CouponCode),
		}
		if l.Quantity != nil {
			units, err := unitsOf(l.Quantity, scales[i])
			if err != nil {
				return err
			}
			p.Quantity, p.QuantityScale, p.UnitLabel = &units, store.Ptr(int16(scales[i])), &l.UnitLabel //nolint:gosec // 0..6
		}
		if err := q.InsertInvoiceLine(ctx, p); err != nil {
			return err
		}
	}
	return nil
}

var unitsCtx = apd.BaseContext.WithPrecision(80)

// unitsOf renders a decimal quantity as scaled integer units with the given scale (the stored form).
func unitsOf(q *apd.Decimal, scale int) (string, error) {
	var r apd.Decimal
	if _, err := unitsCtx.Quantize(&r, q, -int32(scale)); err != nil { //nolint:gosec // scale 0..6
		return "", err
	}
	s := r.Text('f')
	if scale == 0 {
		return strings.SplitN(s, ".", 2)[0], nil
	}
	return strings.Replace(s, ".", "", 1), nil
}
