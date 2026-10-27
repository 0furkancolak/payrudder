// Package maintenance drops expired inbox partitions of processed_events (period end + 45 days) and the hour totals older than domain.WindowRetention.
package maintenance

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/0furkancolak/payrudder/services/aggregator/internal/domain"
	"github.com/0furkancolak/payrudder/services/aggregator/internal/store/sqlc"
)

// Once drops expired partitions and returns their names.
func Once(ctx context.Context, pool *pgxpool.Pool, now time.Time) ([]string, error) {
	return sqlc.New(pool).DropOldProcessedPartitions(ctx, sqlc.DropOldProcessedPartitionsParams{
		Now: now, Retention: pgtype.Interval{Days: 45, Valid: true},
	})
}

// PruneWindows deletes the hour totals and hour value sets that ended before now − domain.WindowRetention, of every tenant and generation, and returns how many. Hour ids are the
// wall-clock hours of each tenant, so the cut is taken a day further back than the UTC hour of the retention: no zone's hour is deleted early.
func PruneWindows(ctx context.Context, pool *pgxpool.Pool, now time.Time) (int64, error) {
	oldest := now.UTC().Add(-domain.WindowRetention - 24*time.Hour).Format("2006-01-02T15")
	q := sqlc.New(pool)
	n, err := q.PruneUsageWindowAggregates(ctx, oldest)
	if err != nil {
		return n, err
	}
	sets, err := q.PruneUsageDistinctHours(ctx, oldest)
	return n + sets, err
}

// Run calls Once and PruneWindows every interval until ctx is done.
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
		pruned, err := PruneWindows(ctx, pool, time.Now())
		switch {
		case err != nil && ctx.Err() == nil:
			log.ErrorContext(ctx, "hour totals maintenance failed", "error", err)
		case pruned > 0:
			log.InfoContext(ctx, "hour totals maintenance", "deleted", pruned)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
