package outbox

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/0furkancolak/payrudder/pkg/backoff"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/tenantdir"
)

// Cells mode of the relay (ADR 0018). A tenant placed in a cell has its own outbox table in its schema, so the relay cannot claim from one shared table: it visits
// the tenants placed in cells and relays each one's outbox in a transaction whose search path is that schema, exactly as RelayOnce does for the pooled table (same
// claim, same publish, same delete in the same transaction: at-least-once, per tenant in order, which is the order Kafka keeps by partition key).
//
// A tenant is visited when its writer says so, and otherwise on a sweep. The writer's function sends pg_notify(NotifyChannel, '<tenant id>') at commit; the
// payload names the schema to visit, and an empty payload means "everyone". A tenant that is moving is not visited. LISTEN needs a session, so behind a
// transaction-mode pooler the notification never arrives and the sweep carries the load (a cell's system pool should reach the cell directly).

// WithCells makes the relay serve the tenants placed in the cells of router (the system-role router of the service) as well as the pooled outbox. It must be called
// before Run. A router with no cells changes nothing.
func (r *Relay) WithCells(router *pgxx.Router) *Relay {
	if router != nil && len(router.CellNames()) > 0 {
		r.cells = &cellRelay{r: r, router: router, dirty: map[uuid.UUID]struct{}{}, wake: make(chan struct{}, 1)}
	}
	return r
}

// RelayTenantOnce is RelayOnce for the outbox in a placed tenant's schema.
func (r *Relay) RelayTenantOnce(ctx context.Context, t pgxx.PlacedTenant) (int, error) {
	tx, err := pgxx.BeginTenantSchema(ctx, t.Pool, t.TenantID)
	if err != nil {
		return 0, fmt.Errorf("outbox: begin: %w", err)
	}
	return r.relay(ctx, tx, t.TenantID.String())
}

type cellRelay struct {
	r      *Relay
	router *pgxx.Router
	wake   chan struct{}

	mu    sync.Mutex
	dirty map[uuid.UUID]struct{}
	all   bool
}

func (c *cellRelay) start(ctx context.Context, wg *sync.WaitGroup) {
	for _, name := range c.router.CellNames() {
		pool, err := c.router.Cell(name)
		if err != nil {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			retry := backoff.Retry{Base: c.r.cfg.ErrorBackoff}
			for ctx.Err() == nil {
				if err := c.listenOnce(ctx, pool); err != nil && ctx.Err() == nil {
					retry.Fail(ctx, c.r.log, "outbox: cell listener lost, sweeping only until it reconnects", err, "cell", name)
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		c.work(ctx)
	}()
}

func (c *cellRelay) listenOnce(ctx context.Context, pool *pgxpool.Pool) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "LISTEN "+NotifyChannel); err != nil {
		return err
	}
	for {
		n, err := conn.Conn().WaitForNotification(ctx)
		if err != nil {
			_ = conn.Conn().Close(context.WithoutCancel(ctx))
			return err
		}
		c.mark(n.Payload)
	}
}

// mark records a notification: a tenant id means that tenant, anything else everyone.
func (c *cellRelay) mark(payload string) {
	c.mu.Lock()
	if id, err := uuid.Parse(strings.TrimSpace(payload)); err == nil {
		c.dirty[id] = struct{}{}
	} else {
		c.all = true
	}
	c.mu.Unlock()
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

func (c *cellRelay) work(ctx context.Context) {
	log := c.r.log.With("relay_worker", "cells")
	sweep := time.NewTimer(c.r.cfg.CellSweep)
	defer sweep.Stop()
	for ctx.Err() == nil {
		select {
		case <-ctx.Done():
			return
		case <-c.wake:
		case <-sweep.C:
			c.mu.Lock()
			c.all = true
			c.mu.Unlock()
			resetTimer(sweep, c.r.cfg.CellSweep)
		}
		for _, t := range c.due(ctx, log) {
			c.drain(ctx, log, t)
		}
	}
}

// due takes what was marked since the last time: the tenants to visit now.
func (c *cellRelay) due(ctx context.Context, log *slog.Logger) []pgxx.PlacedTenant {
	c.mu.Lock()
	all, dirty := c.all, c.dirty
	c.all, c.dirty = false, map[uuid.UUID]struct{}{}
	c.mu.Unlock()
	if !all && len(dirty) == 0 {
		return nil
	}

	// A sweep visits what the directory says now: a tenant placed a moment ago must not wait for an old list. A notification names its tenant, which the list of the
	// last sweep usually knows.
	placed := c.list(ctx, log, all)
	if all {
		return placed
	}
	var out []pgxx.PlacedTenant
	for _, t := range placed {
		if _, ok := dirty[t.TenantID]; ok {
			out = append(out, t)
			delete(dirty, t.TenantID)
		}
	}
	if len(dirty) > 0 { // a tenant placed since the list was fetched: ask again
		for _, t := range c.list(ctx, log, true) {
			if _, ok := dirty[t.TenantID]; ok {
				out = append(out, t)
			}
		}
	}
	return out
}

// list is the directory's list of placed tenants: a new one when force is set, otherwise the one the last sweep fetched (less than a sweep ago). A list that
// cannot be had leaves the last one in use.
func (c *cellRelay) list(ctx context.Context, log *slog.Logger, force bool) []pgxx.PlacedTenant {
	maxAge := c.r.cfg.CellSweep
	if force {
		maxAge = 0
	}
	placed, err := c.router.PlacedWithin(ctx, maxAge)
	if err != nil {
		log.WarnContext(ctx, "outbox: placed tenants incomplete or unavailable", "error", err)
	}
	return placed
}

// drain relays a tenant's outbox until a batch comes back short.
func (c *cellRelay) drain(ctx context.Context, log *slog.Logger, t pgxx.PlacedTenant) {
	if !t.Serving() {
		return
	}
	for ctx.Err() == nil {
		n, err := c.r.RelayTenantOnce(ctx, t)
		if err != nil {
			if ctx.Err() == nil {
				c.r.failures.Add(ctx, 1)
				log.WarnContext(ctx, "outbox: tenant batch failed, will retry on the next sweep", "tenant_id", t.TenantID, "cell", t.Cell, "error", err)
			}
			return
		}
		if n < c.r.cfg.BatchSize {
			return
		}
	}
}

// StatsPlacementAge is how old the list of placed tenants may be for the numbers that are only watched (the depth, the age gauge), which are read every second. A
// tenant fenced for a move since then fails its read (the fence takes the roles' access to the schema away), and the read is an error, not a smaller number.
const StatsPlacementAge = 5 * time.Second

// CellsDepth is the sum of the outbox depth estimates (Depth) of every tenant placed in a cell.
func CellsDepth(ctx context.Context, router *pgxx.Router) (int64, error) {
	var total int64
	placed, err := router.PlacedWithin(ctx, StatsPlacementAge)
	if err != nil {
		return 0, err
	}
	for _, t := range placed {
		err := pgxx.InTenantSchemaTx(ctx, t.Pool, t.TenantID, func(tx pgx.Tx) error {
			d, err := Depth(ctx, tx)
			total += d
			return err
		})
		if err != nil {
			return 0, fmt.Errorf("outbox: depth of tenant %s: %w", t.TenantID, err)
		}
	}
	return total, nil
}

// CellsWatermark is the oldest unpublished ingested_at over the outboxes of the tenants placed in cells; ok is false when they are all empty. While a tenant is
// moving its rows are in neither place, so the answer is unknown and an error that matches tenantdir.ErrNotServing: a watermark that skipped it could let a
// consumer close a period over events that are still unpublished.
//
// maxAge is how old the list of placed tenants may be. A tenant placed after the list was made is not in it, and a consumer must never be told that everything
// before the watermark is published while that tenant has rows older than it: an answer that decides something (the RPC) passes 0, and an answer that is only
// watched (a gauge) may pass a few seconds.
func CellsWatermark(ctx context.Context, router *pgxx.Router, maxAge time.Duration) (oldest time.Time, ok bool, err error) {
	placed, err := router.PlacedWithin(ctx, maxAge)
	if err != nil {
		return time.Time{}, false, err
	}
	for _, t := range placed {
		if !t.Serving() {
			return time.Time{}, false, fmt.Errorf("outbox: tenant %s is %s: %w", t.TenantID, t.Status, tenantdir.ErrNotServing)
		}
		err := pgxx.InTenantSchemaTx(ctx, t.Pool, t.TenantID, func(tx pgx.Tx) error {
			o, has, err := Watermark(ctx, tx)
			if err != nil || !has {
				return err
			}
			if !ok || o.Before(oldest) {
				oldest, ok = o, true
			}
			return nil
		})
		if err != nil {
			return time.Time{}, false, fmt.Errorf("outbox: watermark of tenant %s: %w", t.TenantID, err)
		}
	}
	return oldest, ok, nil
}
