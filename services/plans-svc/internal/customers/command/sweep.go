package command

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/placement"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// Sweeper clears the blocks that ran out by itself (blocked_until has passed). Every check already reads the end, so nothing waits for it; the sweep makes the row say
// what is true, writes the lift to the customer's history (actor "system") and sends customer.unblocked, which a block that ends by itself otherwise never did.
//
// Finding the customers reads across tenants on the system pool; each lift happens on the app pool in the tenant's own transaction, after the row is locked and read
// again. It is safe to run twice and on several instances: the second finds the block gone. With cells on (ADR 0018) each tenant placed in a cell is listed in its own
// schema, as the trial reminder does.
type Sweeper struct {
	system  *pgxpool.Pool
	app     pgxx.Beginner
	places  *placement.Places
	every   time.Duration
	batch   int
	now     func() time.Time
	log     *slog.Logger
	visited func(uuid.UUID) // tests: sees every tenant in a cell the sweep visits
}

// NewSweeper returns the sweep; every is how often it looks (default 5 minutes). appDB is the app pool, or the app-role router (placement.AppDB) when cells are on.
func NewSweeper(systemPool *pgxpool.Pool, appDB pgxx.Beginner, places *placement.Places, every time.Duration, log *slog.Logger) *Sweeper {
	if every <= 0 {
		every = 5 * time.Minute
	}
	return &Sweeper{system: systemPool, app: appDB, places: places, every: every, batch: 200, now: time.Now, log: log}
}

// WithClock sets the clock the sweep reads, and WithVisits makes it report each tenant in a cell it visits to fn (tests).
func (s *Sweeper) WithClock(now func() time.Time) *Sweeper { s.now = now; return s }

// WithVisits: see WithClock.
func (s *Sweeper) WithVisits(fn func(uuid.UUID)) *Sweeper { s.visited = fn; return s }

// Once lifts the blocks that have run out at now, at most one batch per list, and says how many it lifted and how many it looked at. A customer that fails is logged
// and left for the next pass.
func (s *Sweeper) Once(ctx context.Context) (lifted, looked int, err error) {
	now := s.now()
	due, err := sqlc.New(s.system).SystemListEndedBlocks(ctx, sqlc.SystemListEndedBlocksParams{Now: now, Batch: int32(s.batch)}) //nolint:gosec // a small constant
	if err != nil {
		return 0, 0, err
	}
	lifted, looked = s.liftAll(ctx, now, due)
	err = s.places.EachCell(ctx, func(ctx context.Context, t pgxx.PlacedTenant) error {
		if s.visited != nil {
			s.visited(t.TenantID)
		}
		var due []sqlc.SystemListEndedBlocksRow
		err := s.places.CellTx(ctx, t, func(tx pgx.Tx) (err error) {
			due, err = sqlc.New(tx).SystemListEndedBlocks(ctx, sqlc.SystemListEndedBlocksParams{Now: now, Batch: int32(s.batch)}) //nolint:gosec // a small constant
			return err
		})
		if err != nil {
			return err
		}
		for _, d := range due {
			if d.TenantID != t.TenantID {
				return fmt.Errorf("block sweep: customer %s of tenant %s is in the schema of tenant %s", d.ID, d.TenantID, t.TenantID)
			}
		}
		l, n := s.liftAll(ctx, now, due)
		lifted, looked = lifted+l, looked+n
		return nil
	})
	return lifted, looked, err
}

func (s *Sweeper) liftAll(ctx context.Context, now time.Time, due []sqlc.SystemListEndedBlocksRow) (lifted, looked int) {
	for _, d := range due {
		ok, err := s.lift(ctx, now, d)
		if err != nil {
			s.log.WarnContext(ctx, "block sweep: could not clear the block", "tenant_id", d.TenantID, "customer_id", d.ID, "error", err)
			continue
		}
		if ok {
			lifted++
		}
	}
	return lifted, len(due)
}

// lift clears one block in its tenant's transaction, or does nothing when it was lifted, replaced or extended since the list was read.
func (s *Sweeper) lift(ctx context.Context, now time.Time, d sqlc.SystemListEndedBlocksRow) (bool, error) {
	done := false
	err := pgxx.InTenantTx(ctx, s.app, d.TenantID, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		cur, err := q.LockCustomerByExternalID(ctx, d.ExternalID)
		if err != nil {
			return err
		}
		if cur.BlockedAt == nil || cur.BlockedUntil == nil || cur.BlockedUntil.After(now) {
			return nil
		}
		if _, err := liftBlock(ctx, q, tx, cur, now, "system", "the block ended on the customer "); err != nil {
			return err
		}
		done = true
		return nil
	})
	return done && err == nil, err
}

// Run sweeps now and then every interval until ctx ends. A pass that fills a whole batch runs again at once.
func (s *Sweeper) Run(ctx context.Context) error {
	t := time.NewTicker(s.every)
	defer t.Stop()
	for {
		for {
			n, looked, err := s.Once(ctx)
			if err != nil {
				s.log.ErrorContext(ctx, "block sweep: listing the customers failed", "error", err)
			} else if n > 0 {
				s.log.InfoContext(ctx, "blocks that ended were cleared", "count", n)
			}
			// Another pass only when this one made progress through a full batch: a customer that keeps failing must not make it spin.
			if err != nil || n == 0 || looked < s.batch || ctx.Err() != nil {
				break
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}
