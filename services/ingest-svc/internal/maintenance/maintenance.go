// Package maintenance keeps the monthly partitions of idempotency_keys: the next two months always exist, and
// partitions older than the 45-day retention are dropped.
package maintenance

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Once creates missing partitions and drops expired ones as of now, and reports what it did.
func Once(ctx context.Context, pool *pgxpool.Pool, now time.Time) (created, dropped []string, err error) {
	err = pool.QueryRow(ctx,
		`SELECT created, dropped FROM maintain_idempotency_partitions($1::timestamptz, 2, interval '45 days')`, now).
		Scan(&created, &dropped)
	if err != nil {
		return nil, nil, fmt.Errorf("maintenance: %w", err)
	}
	return created, dropped, nil
}

// Run calls Once immediately and then every interval until ctx is done. A failed run is logged and retried on the next tick.
func Run(ctx context.Context, pool *pgxpool.Pool, interval time.Duration, log *slog.Logger) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		created, dropped, err := Once(ctx, pool, time.Now())
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
