// Package maintenance drops expired inbox partitions of processed_events (period end + 45 days) and the hour totals older than domain.WindowRetention. In cells mode
// (ADR 0018) every tenant placed in a cell has its own inbox and its own hour tables, kept by the same functions and statements in its own schema.
package maintenance

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/services/aggregator/internal/domain"
	"github.com/0furkancolak/payrudder/services/aggregator/internal/store/sqlc"
)

// Once drops expired partitions and returns their names. db is the pooled pool, or a transaction in a tenant's schema.
func Once(ctx context.Context, db sqlc.DBTX, now time.Time) ([]string, error) {
	return sqlc.New(db).DropOldProcessedPartitions(ctx, sqlc.DropOldProcessedPartitionsParams{
		Now: now, Retention: pgtype.Interval{Days: 45, Valid: true},
	})
}

// PruneWindows deletes the hour totals and hour value sets that ended before now − domain.WindowRetention, of every tenant and generation, and returns how many. Hour ids are the
// wall-clock hours of each tenant, so the cut is taken a day further back than the UTC hour of the retention: no zone's hour is deleted early.
func PruneWindows(ctx context.Context, db sqlc.DBTX, now time.Time) (int64, error) {
	oldest := now.UTC().Add(-domain.WindowRetention - 24*time.Hour).Format("2006-01-02T15")
	q := sqlc.New(db)
	n, err := q.PruneUsageWindowAggregates(ctx, oldest)
	if err != nil {
		return n, err
	}
	sets, err := q.PruneUsageDistinctHours(ctx, oldest)
	return n + sets, err
}

// OnceCells does both for the schema of every tenant placed in a cell that is serving, in one transaction each (a tenant that is moving is skipped, one whose run
// fails does not stop the others; the errors come back joined). It returns the partitions dropped, as "<tenant>.<partition>", and the rows of hour totals deleted.
func OnceCells(ctx context.Context, cells *pgxx.Router, now time.Time) (dropped []string, pruned int64, err error) {
	err = cells.EachReady(ctx, func(ctx context.Context, t pgxx.PlacedTenant) error {
		var names []string
		var n int64
		err := pgxx.InTenantSchemaTx(ctx, t.Pool, t.TenantID, func(tx pgx.Tx) (err error) {
			if names, err = Once(ctx, tx, now); err != nil {
				return fmt.Errorf("inbox partitions: %w", err)
			}
			if n, err = PruneWindows(ctx, tx, now); err != nil {
				return fmt.Errorf("hour totals: %w", err)
			}
			return nil
		})
		if err != nil {
			return err
		}
		for _, name := range names {
			dropped = append(dropped, t.TenantID.String()+"."+name)
		}
		pruned += n
		return nil
	})
	return dropped, pruned, err
}

// Run calls Once and PruneWindows on the pooled database (and OnceCells when cells is not nil) every interval until ctx is done.
func Run(ctx context.Context, pool sqlc.DBTX, cells *pgxx.Router, interval time.Duration, log *slog.Logger) {
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
		if cells != nil {
			dropped, pruned, err := OnceCells(ctx, cells, time.Now())
			switch {
			case err != nil && ctx.Err() == nil:
				log.ErrorContext(ctx, "cell maintenance failed", "error", err)
			case len(dropped) > 0 || pruned > 0:
				log.InfoContext(ctx, "cell maintenance", "dropped", dropped, "deleted", pruned)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
