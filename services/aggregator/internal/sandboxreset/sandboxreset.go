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
	Tables = []string{"usage_aggregates", "late_usage", "processed_events", "period_settings"}
	Kept   = []string{"aggregate_generations", "replay_jobs"}
)

// Resetter deletes on the system pool, which sees every tenant: each statement names the tenant.
type Resetter struct{ pool *pgxpool.Pool }

// New returns a Resetter.
func New(systemPool *pgxpool.Pool) *Resetter { return &Resetter{pool: systemPool} }

// Reset deletes the tenant's rows and returns how many, by table (tables with none are left out).
func (r *Resetter) Reset(ctx context.Context, tenantID uuid.UUID) (map[string]int64, error) {
	out := map[string]int64{}
	err := pgxx.InTx(ctx, r.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		for _, s := range []struct {
			table string
			run   func(context.Context, uuid.UUID) (int64, error)
		}{
			{"usage_aggregates", q.ResetUsageAggregates}, {"late_usage", q.ResetLateUsage}, {"processed_events", q.ResetProcessedEvents}, {"period_settings", q.ResetPeriodSettings},
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
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
