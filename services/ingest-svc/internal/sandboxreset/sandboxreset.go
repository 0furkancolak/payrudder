// Package sandboxreset deletes a sandbox tenant's dedup keys and unpublished events (phase 45 E). Events already on the topic stay there: the aggregator has read
// them (or reads them, and a reset can be repeated).
package sandboxreset

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/services/ingest-svc/internal/store/sqlc"
)

// Tables are the tables a reset deletes from; Kept are the tables with tenant rows it leaves alone (none: ingest-svc keeps no configuration).
var (
	Tables = []string{"idempotency_keys", "outbox"}
	Kept   []string
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
		keys, err := q.ResetIdempotencyKeys(ctx, tenantID)
		if err != nil {
			return err
		}
		queued, err := q.ResetOutbox(ctx, tenantID)
		if err != nil {
			return err
		}
		for table, n := range map[string]int64{"idempotency_keys": keys, "outbox": queued} {
			if n > 0 {
				out[table] = n
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
