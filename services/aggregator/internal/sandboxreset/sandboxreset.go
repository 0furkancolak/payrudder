// Package sandboxreset deletes a sandbox tenant's usage (phase 45 E). The generations and replay jobs stay: they are the aggregator's own bookkeeping for the tenant,
// not usage, and the aggregator expects a generation to exist.
package sandboxreset

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/services/aggregator/internal/store/sqlc"
)

// Tables are the tables a reset deletes from; Kept are the tables with tenant rows it leaves alone on purpose.
var (
	Tables = []string{"usage_aggregates", "usage_window_aggregates", "usage_distinct_values", "usage_distinct_hours", "late_usage", "processed_events", "period_settings"}
	Kept   = []string{"aggregate_generations", "replay_jobs"}
)

// Resetter deletes on the system pool, which sees every tenant: each statement names the tenant.
type Resetter struct {
	pool  *pgxpool.Pool
	cells *pgxx.Router
}

// New returns a Resetter.
func New(systemPool *pgxpool.Pool) *Resetter { return &Resetter{pool: systemPool} }

// WithCells makes the Resetter reset a tenant placed in a cell in its schema (cells is the system-role router of ADR 0018). Tenants in the pooled database are
// reset as before.
func (r *Resetter) WithCells(cells *pgxx.Router) *Resetter {
	r.cells = cells
	return r
}

// Reset deletes the tenant's rows and returns how many, by table (tables with none are left out).
func (r *Resetter) Reset(ctx context.Context, tenantID uuid.UUID) (map[string]int64, error) {
	out := map[string]int64{}
	reset := func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		for _, s := range []struct {
			table string
			run   func(context.Context, uuid.UUID) (int64, error)
		}{
			{"usage_aggregates", q.ResetUsageAggregates}, {"usage_window_aggregates", q.ResetUsageWindowAggregates}, {"usage_distinct_values", q.ResetUsageDistinctValues}, {"usage_distinct_hours", q.ResetUsageDistinctHours}, {"late_usage", q.ResetLateUsage}, {"processed_events", q.ResetProcessedEvents}, {"period_settings", q.ResetPeriodSettings},
		} {
			n, err := s.run(ctx, tenantID)
			if err != nil {
				return err
			}
			if n > 0 {
				out[s.table] = n
			}
		}
		return nil
	}
	var err error
	if r.cells != nil {
		err = pgxx.InTenantTx(ctx, r.cells, tenantID, reset)
	} else {
		err = pgxx.InTx(ctx, r.pool, pgx.TxOptions{}, reset)
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}
