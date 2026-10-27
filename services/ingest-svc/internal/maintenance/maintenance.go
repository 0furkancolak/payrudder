// Package maintenance keeps the monthly partitions of idempotency_keys: the next two months always exist, and
// partitions older than the 45-day retention are dropped. In cells mode (ADR 0018) every tenant placed in a cell has its own partitioned table, kept by
// the same function in its own schema.
package maintenance

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/services/ingest-svc/internal/store/sqlc"
)

// Once creates missing partitions and drops expired ones as of now in the pooled table, and reports what it did.
func Once(ctx context.Context, pool *pgxpool.Pool, now time.Time) (created, dropped []string, err error) {
	row, err := sqlc.New(pool).MaintainIdempotencyPartitions(ctx, now)
	if err != nil {
		return nil, nil, fmt.Errorf("maintenance: %w", err)
	}
	return row.Created, row.Dropped, nil
}

// OnceCells does the same in the schema of every tenant placed in a cell that is serving. A tenant whose maintenance fails does not stop the others; the
// errors come back joined. It returns what was created and dropped, as "<schema>.<partition>".
func OnceCells(ctx context.Context, cells *pgxx.Router, now time.Time) (created, dropped []string, err error) {
	err = cells.EachReady(ctx, func(ctx context.Context, t pgxx.PlacedTenant) error {
		return pgxx.InTenantSchemaTx(ctx, t.Pool, t.TenantID, func(tx pgx.Tx) error {
			row, err := sqlc.New(tx).MaintainIdempotencyPartitions(ctx, now)
			if err != nil {
				return fmt.Errorf("maintenance: %w", err)
			}
			for _, p := range row.Created {
				created = append(created, t.TenantID.String()+"."+p)
			}
			for _, p := range row.Dropped {
				dropped = append(dropped, t.TenantID.String()+"."+p)
			}
			return nil
		})
	})
	return created, dropped, err
}

// Run calls Once (and OnceCells when cells is not nil) immediately and then every interval until ctx is done. A failed run is logged and retried on the next tick.
func Run(ctx context.Context, pool *pgxpool.Pool, cells *pgxx.Router, interval time.Duration, log *slog.Logger) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		now := time.Now()
		created, dropped, err := Once(ctx, pool, now)
		if cells != nil {
			cc, cd, cerr := OnceCells(ctx, cells, now)
			created, dropped = append(created, cc...), append(dropped, cd...)
			if err == nil {
				err = cerr
			}
		}
		switch {
		case err != nil && ctx.Err() == nil:
			log.ErrorContext(ctx, "partition maintenance failed", "error", err)
		case len(created) > 0 || len(dropped) > 0:
			log.InfoContext(ctx, "partition maintenance", "created", created, "dropped", dropped)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
