// Package maintenance drops expired inbox partitions of processed_events (period end + 45 days).
package maintenance

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/0furkancolak/payrudder/services/aggregator/internal/store/sqlc"
)

// Once drops expired partitions and returns their names.
func Once(ctx context.Context, pool *pgxpool.Pool, now time.Time) ([]string, error) {
	return sqlc.New(pool).DropOldProcessedPartitions(ctx, sqlc.DropOldProcessedPartitionsParams{
		Now: now, Retention: pgtype.Interval{Days: 45, Valid: true},
	})
}

// Run calls Once every interval until ctx is done.
func Run(ctx context.Context, pool *pgxpool.Pool, interval time.Duration, log *slog.Logger) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		dropped, err := Once(ctx, pool, time.Now())
		switch {
		case err != nil && ctx.Err() == nil:
			log.ErrorContext(ctx, "inbox partition maintenance failed", "error", err)
		case len(dropped) > 0:
			log.InfoContext(ctx, "inbox partition maintenance", "dropped", dropped)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
