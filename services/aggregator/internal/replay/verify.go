package replay

import (
	"context"
	"fmt"
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
	Kind           string `json:"kind"` // usage | late
	Customer       string `json:"customer_id"`
	Meter          string `json:"meter_code"`
	Period         string `json:"period_id"`
	OriginPeriod   string `json:"origin_period_id,omitempty"`
	OldQuantity    string `json:"old_quantity"`
	NewQuantity    string `json:"new_quantity"`
	OldEventCount  int64  `json:"old_event_count"`
	NewEventCount  int64  `json:"new_event_count"`
	OldQuantityNil bool   `json:"-"`
}

const maxDiffs = 100

// verify compares generation source and target for the tenant's periods [from, to], for on-time aggregates and late usage.
func (m *Manager) verify(ctx context.Context, job *Job) (*Verification, error) {
	out := &Verification{}
	// quantity is numeric(38,0); NULL on a side means the row does not exist in that generation.
	type spec struct {
		kind, query string
	}
	for _, s := range []spec{
		{"usage", `
			SELECT COALESCE(a.period_id, b.period_id), COALESCE(a.customer_id, b.customer_id)::text, COALESCE(a.meter_code, b.meter_code), '',
			       a.quantity::text, b.quantity::text, COALESCE(a.event_count, 0), COALESCE(b.event_count, 0)
			FROM (SELECT period_id, customer_id, meter_code, quantity, event_count FROM usage_aggregates
			      WHERE tenant_id = $1 AND generation = $2 AND period_id BETWEEN $4 AND $5) a
			FULL OUTER JOIN (SELECT period_id, customer_id, meter_code, quantity, event_count FROM usage_aggregates
			      WHERE tenant_id = $1 AND generation = $3 AND period_id BETWEEN $4 AND $5) b
			  USING (period_id, customer_id, meter_code)
			ORDER BY 1, 2, 3`},
		{"late", `
			SELECT COALESCE(a.billed_period_id, b.billed_period_id), COALESCE(a.customer_id, b.customer_id)::text, COALESCE(a.meter_code, b.meter_code),
			       COALESCE(a.origin_period_id, b.origin_period_id),
			       a.quantity::text, b.quantity::text, COALESCE(a.event_count, 0), COALESCE(b.event_count, 0)
			FROM (SELECT billed_period_id, customer_id, meter_code, origin_period_id, quantity, event_count FROM late_usage
			      WHERE tenant_id = $1 AND generation = $2 AND origin_period_id BETWEEN $4 AND $5) a
			FULL OUTER JOIN (SELECT billed_period_id, customer_id, meter_code, origin_period_id, quantity, event_count FROM late_usage
			      WHERE tenant_id = $1 AND generation = $3 AND origin_period_id BETWEEN $4 AND $5) b
			  USING (billed_period_id, customer_id, meter_code, origin_period_id)
			ORDER BY 1, 2, 3, 4`},
	} {
		rows, err := m.cfg.Pool.Query(ctx, s.query, job.TenantID, job.SourceGeneration, job.TargetGeneration, job.From, job.To)
		if err != nil {
			return nil, fmt.Errorf("verify %s: %w", s.kind, err)
		}
		for rows.Next() {
			var d Diff
			var oldQ, newQ *string
			if err := rows.Scan(&d.Period, &d.Customer, &d.Meter, &d.OriginPeriod, &oldQ, &newQ, &d.OldEventCount, &d.NewEventCount); err != nil {
				rows.Close()
				return nil, err
			}
			d.Kind = s.kind
			out.RowsCompared++
			switch {
			case oldQ != nil && newQ != nil && *oldQ == *newQ && d.OldEventCount == d.NewEventCount:
				continue
			case oldQ == nil:
				out.RowsOnlyInNew++
			case newQ == nil:
				out.RowsOnlyInOld++
			default:
				out.RowsChanged++
			}
			if oldQ != nil {
				d.OldQuantity = *oldQ
			}
			if newQ != nil {
				d.NewQuantity = *newQ
			}
			if len(out.Diffs) < maxDiffs {
				out.Diffs = append(out.Diffs, d)
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return out, nil
}
