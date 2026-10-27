package command

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/cockroachdb/apd/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/0furkancolak/payrudder/pkg/pgxx"
	invoicecmd "github.com/0furkancolak/payrudder/services/billing-svc/internal/invoices/command"
	invmodel "github.com/0furkancolak/payrudder/services/billing-svc/internal/invoices/model"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/rating"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/rating/inputs"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/rerating/model"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store/sqlc"
)

// billedComponents sums what a customer was already billed for the period's own usage: base fee, usage lines and the
// lines of earlier corrections, per meter ("" is the base fee). Late-usage and credit lines are other things.
func (c *Commands) billedComponents(ctx context.Context, tenantID uuid.UUID, periodID string) (map[string]map[string]int64, map[string]json.RawMessage, error) {
	rows, err := c.q.BilledComponents(ctx, sqlc.BilledComponentsParams{TenantID: tenantID, PeriodID: periodID})
	if err != nil {
		return nil, nil, err
	}
	billed := map[string]map[string]int64{}
	for _, r := range rows {
		if billed[r.CustomerID] == nil {
			billed[r.CustomerID] = map[string]int64{}
		}
		billed[r.CustomerID][r.MeterCode] = r.AmountMinor
	}
	// The snapshot of each customer's regular invoice (a correction repeats who it is addressed to).
	srows, err := c.q.ListRegularInvoiceSnapshots(ctx, sqlc.ListRegularInvoiceSnapshotsParams{TenantID: tenantID, PeriodID: periodID})
	if err != nil {
		return nil, nil, err
	}
	snaps := make(map[string]json.RawMessage, len(srows))
	for _, r := range srows {
		snaps[r.CustomerID] = r.Snapshot
	}
	return billed, snaps, nil
}

// correctionDeltas prices usage under target and returns, per component (a meter, "" for the base fee), the lines that bring what was billed to the new price
// and their sum: positive when the customer owes more, negative when it is owed a credit.
//
// feeWaived: the customer's fixed fee is not on the regular invoice (a trial, an advance fee, a provider that bills it), so the target's fixed fee is not owed either.
func correctionDeltas(target rating.PlanVersion, usage []rating.Usage, billed map[string]int64, meters map[string]inputs.MeterInfo, feeWaived bool) ([]rating.Line, int64, error) {
	lines, err := rating.Rate(target, usage)
	if err != nil {
		return nil, 0, inputs.Permanent(err)
	}
	newAmount := map[string]int64{}
	newLine := map[string]rating.Line{}
	for _, l := range lines {
		if feeWaived && l.Kind == rating.KindBaseFee {
			continue
		}
		newAmount[l.MeterCode] += l.AmountMinor
		newLine[l.MeterCode] = l
	}
	components := map[string]bool{}
	for k := range newAmount {
		components[k] = true
	}
	for k := range billed {
		components[k] = true
	}
	keys := make([]string, 0, len(components))
	for k := range components {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var deltas []rating.Line
	var total int64
	for _, k := range keys {
		d := newAmount[k] - billed[k]
		if d == 0 {
			continue
		}
		total += d
		l := rating.Line{Kind: rating.KindCorrection, MeterCode: k, AmountMinor: d, Description: "Base fee: price change"}
		if k != "" {
			l.Description = fmt.Sprintf("%s: price change", k)
			l.UnitLabel = meters[k].Unit
			if nl, ok := newLine[k]; ok {
				l.Quantity = nl.Quantity
			} else if q := quantityOf(usage, k); q != nil {
				l.Quantity = q
			}
		}
		deltas = append(deltas, l)
	}
	return deltas, total, nil
}

func quantityOf(us []rating.Usage, meter string) *apd.Decimal {
	for _, u := range us {
		if u.MeterCode == meter {
			return u.Quantity
		}
	}
	return nil
}

// finish finalizes the correction invoices (number, open, saga; finalizing twice is a no-op) and completes the job.
func (c *Commands) finish(ctx context.Context, job *model.Job, rated int) error {
	ids, err := c.q.ListDraftInvoiceIDsOfRerating(ctx, sqlc.ListDraftInvoiceIDsOfReratingParams{TenantID: job.TenantID, ReratingJobID: job.ID})
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := c.cfg.Invoices.Finalize(ctx, job.TenantID, id); err != nil {
			return err
		}
	}
	return c.q.CompleteReratingJob(ctx, sqlc.CompleteReratingJobParams{CompletedAt: c.cfg.Now(), CustomersRated: store.I32(rated), TenantID: job.TenantID, ID: job.ID})
}

// taxRate is a tax name and its rate in basis points; the zero value is no tax.
type taxRate struct {
	name string
	bps  int32
}

// record writes one customer's outcome: a CORRECTION draft when the new price is higher, a credit balance when
// it is lower. Both are guarded by unique indexes on the job, so a rerun does nothing. The invoices of the period carried tax, so a correction does too: the
// draft gets a TAX line on what it adds, and a credit includes the tax that was paid on what is no longer owed.
func (c *Commands) record(ctx context.Context, job *model.Job, customer uuid.UUID, snapshot json.RawMessage, currency string,
	deltas []rating.Line, total int64, meters map[string]inputs.MeterInfo, tax taxRate) error {
	return pgxx.InTx(ctx, c.cfg.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := c.q.WithTx(tx)
		if total < 0 {
			return q.InsertCreditBalanceForRerating(ctx, sqlc.InsertCreditBalanceForReratingParams{TenantID: job.TenantID, CustomerID: customer, ID: uuid.New(),
				AmountMinor: -total + rating.TaxOn(-total, tax.bps), SourceReratingJobID: job.ID, Currency: currency})
		}
		id := uuid.New()
		taxLine, taxed := rating.TaxLine(deltas, tax.name, tax.bps)
		invoiceTotal := total
		if taxed {
			invoiceTotal += taxLine.AmountMinor
		}
		_, err := q.InsertDraftInvoice(ctx, sqlc.InsertDraftInvoiceParams{
			TenantID: job.TenantID, ID: id, CustomerID: customer, PeriodID: job.PeriodID, Kind: invmodel.KindCorrection, PlanVersionID: job.PlanVersionID, Currency: currency,
			SubtotalMinor: total, TotalMinor: invoiceTotal, Snapshot: snapshot, ReratingJobID: &job.ID,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // this job already corrected this customer
		}
		if err != nil {
			return err
		}
		lines, scales := make([]rating.Line, len(deltas)), make([]int, len(deltas))
		for i, l := range deltas {
			l.Description += " (" + job.PeriodID + ")"
			lines[i], scales[i] = l, meters[l.MeterCode].Scale
		}
		if taxed {
			lines, scales = append(lines, taxLine), append(scales, 0)
		}
		return invoicecmd.InsertLines(ctx, q, job.TenantID, id, lines, scales)
	})
}
