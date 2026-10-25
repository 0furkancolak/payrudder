package replay

import (
	"context"
	"fmt"

	"github.com/0furkancolak/payrudder/services/aggregator/internal/store/sqlc"
)

// Verification is the difference between the live generation and the rebuilt one, over the job's periods.
type Verification struct {
	RowsCompared  int64  `json:"rows_compared"`
	RowsChanged   int64  `json:"rows_changed"`
	RowsOnlyInOld int64  `json:"rows_only_in_old"`
	RowsOnlyInNew int64  `json:"rows_only_in_new"`
	Diffs         []Diff `json:"diffs,omitempty"`
}

// Diff is one aggregate that differs.
type Diff struct {
	Kind          string `json:"kind"` // usage | late
	Customer      string `json:"customer_id"`
	Meter         string `json:"meter_code"`
	Period        string `json:"period_id"`
	OriginPeriod  string `json:"origin_period_id,omitempty"`
	OldQuantity   string `json:"old_quantity"`
	NewQuantity   string `json:"new_quantity"`
	OldEventCount int64  `json:"old_event_count"`
	NewEventCount int64  `json:"new_event_count"`
}

const maxDiffs = 100

// verify compares generation source and target for the tenant's periods [from, to], for on-time aggregates and late usage.
func (m *Manager) verify(ctx context.Context, job *Job) (*Verification, error) {
	q := sqlc.New(m.cfg.Pool)
	out := &Verification{}
	usage, err := q.CompareUsage(ctx, sqlc.CompareUsageParams{
		TenantID: job.TenantID, Source: job.SourceGeneration, Target: job.TargetGeneration, FromPeriod: job.From, ToPeriod: job.To,
	})
	if err != nil {
		return nil, fmt.Errorf("verify usage: %w", err)
	}
	for _, r := range usage {
		out.record("usage", r)
	}
	late, err := q.CompareLateUsage(ctx, sqlc.CompareLateUsageParams{
		TenantID: job.TenantID, Source: job.SourceGeneration, Target: job.TargetGeneration, FromPeriod: job.From, ToPeriod: job.To,
	})
	if err != nil {
		return nil, fmt.Errorf("verify late: %w", err)
	}
	for _, r := range late {
		out.record("late", sqlc.CompareUsageRow(r)) // both queries return the same columns
	}
	return out, nil
}

// record counts one compared row and keeps it, up to maxDiffs, when the generations differ.
func (v *Verification) record(kind string, r sqlc.CompareUsageRow) {
	v.RowsCompared++
	switch {
	case r.InOld && r.InNew && r.OldQuantity == r.NewQuantity && r.OldEventCount == r.NewEventCount:
		return
	case !r.InOld:
		v.RowsOnlyInNew++
	case !r.InNew:
		v.RowsOnlyInOld++
	default:
		v.RowsChanged++
	}
	if len(v.Diffs) < maxDiffs {
		v.Diffs = append(v.Diffs, Diff{
			Kind: kind, Customer: r.CustomerID, Meter: r.MeterCode, Period: r.PeriodID, OriginPeriod: r.OriginPeriodID,
			OldQuantity: r.OldQuantity, NewQuantity: r.NewQuantity, OldEventCount: r.OldEventCount, NewEventCount: r.NewEventCount,
		})
	}
}
