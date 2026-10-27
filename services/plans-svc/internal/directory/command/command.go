// Package command changes where a tenant lives (ADR 0018): the three steps of a move, each a transition of tenants.placement_status that is checked in the
// database under a row lock, so two operators or a retry cannot fence, finish or abort a move twice.
//
// These calls are the operator tool's (payrudderctl cells), never a console's, so a refusal is a plain status whose message starts with a reason word
// (cells_not_ready, move_in_progress, same_place, not_moving, try_again) and not an error key with translations.
package command

import (
	"context"
	"log/slog"
	"regexp"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/tenantdir"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/convert"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// name is the same pattern the cell and region columns check.
var name = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,30}$`)

// Commands change placements on the system pool.
type Commands struct {
	pool *pgxpool.Pool
	// ready is nil when every job is converted to run per tenant schema, and otherwise says what is left: the check behind the refusal to place a tenant in a cell.
	ready func() error
	log   *slog.Logger
}

// New returns the placement commands. ready is tenantdir.CellsReady in the service; a test passes its own inventory.
func New(systemPool *pgxpool.Pool, ready func() error, log *slog.Logger) *Commands {
	if ready == nil {
		ready = tenantdir.CellsReady
	}
	return &Commands{pool: systemPool, ready: ready, log: log}
}

// BeginMove fences the tenant: ready to moving. Going to a cell is refused until every job is converted; so is a move to where the tenant already is.
func (c *Commands) BeginMove(ctx context.Context, tenantID, toCell string) (*plansv1.Placement, error) {
	id, err := domain.ParseID("tenant_id", tenantID)
	if err != nil {
		return nil, err
	}
	if toCell != "" && !name.MatchString(toCell) {
		return nil, domain.Invalid("to_cell", "must be empty (the pooled database) or a cell name: lower-case letters, digits and hyphens, at most 31 characters")
	}
	if toCell != "" {
		if err := c.ready(); err != nil {
			return nil, status.Errorf(codes.FailedPrecondition, "cells_not_ready: no tenant can be placed in a cell yet: %v", err)
		}
	}
	return c.transition(ctx, id, func(q *sqlc.Queries, cur sqlc.GetPlacementRow) (int64, error) {
		if cur.PlacementStatus != string(tenantdir.StatusReady) {
			return 0, status.Error(codes.FailedPrecondition, "move_in_progress: the tenant is already being moved")
		}
		if cur.Cell == toCell {
			return 0, status.Error(codes.FailedPrecondition, "same_place: the tenant is already there")
		}
		return q.BeginMove(ctx, id)
	})
}

// FinishMove ends a move in cell/region ("" for the pooled database).
func (c *Commands) FinishMove(ctx context.Context, tenantID, cell, region string) (*plansv1.Placement, error) {
	id, err := domain.ParseID("tenant_id", tenantID)
	if err != nil {
		return nil, err
	}
	if cell != "" && !name.MatchString(cell) {
		return nil, domain.Invalid("cell", "must be empty (the pooled database) or a cell name: lower-case letters, digits and hyphens, at most 31 characters")
	}
	if region != "" && !name.MatchString(region) {
		return nil, domain.Invalid("region", "must be empty or a region name: lower-case letters, digits and hyphens, at most 31 characters")
	}
	if cell == "" && region != "" {
		return nil, domain.Invalid("region", "a tenant in the pooled database has no region")
	}
	if cell != "" {
		if err := c.ready(); err != nil {
			return nil, status.Errorf(codes.FailedPrecondition, "cells_not_ready: no tenant can be placed in a cell yet: %v", err)
		}
	}
	return c.transition(ctx, id, func(q *sqlc.Queries, cur sqlc.GetPlacementRow) (int64, error) {
		if cur.PlacementStatus != string(tenantdir.StatusMoving) {
			return 0, status.Error(codes.FailedPrecondition, "not_moving: the tenant is not being moved")
		}
		return q.FinishMove(ctx, sqlc.FinishMoveParams{Cell: cell, Region: region, ID: id})
	})
}

// AbortMove ends a move where it began.
func (c *Commands) AbortMove(ctx context.Context, tenantID string) (*plansv1.Placement, error) {
	id, err := domain.ParseID("tenant_id", tenantID)
	if err != nil {
		return nil, err
	}
	return c.transition(ctx, id, func(q *sqlc.Queries, cur sqlc.GetPlacementRow) (int64, error) {
		if cur.PlacementStatus != string(tenantdir.StatusMoving) {
			return 0, status.Error(codes.FailedPrecondition, "not_moving: the tenant is not being moved")
		}
		return q.AbortMove(ctx, id)
	})
}

// transition runs step on the tenant's row under a lock and returns the placement it leaves.
func (c *Commands) transition(ctx context.Context, id uuid.UUID, step func(q *sqlc.Queries, cur sqlc.GetPlacementRow) (int64, error)) (*plansv1.Placement, error) {
	var out sqlc.GetPlacementRow
	err := pgxx.InTx(ctx, c.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		cur, err := q.LockPlacement(ctx, id)
		if err != nil {
			return convert.Err(err, "tenant")
		}
		n, err := step(q, sqlc.GetPlacementRow(cur))
		if err != nil {
			return err
		}
		if n != 1 {
			return status.Error(codes.Aborted, "try_again: the placement changed under the call; read it and try again")
		}
		out, err = q.GetPlacement(ctx, id)
		return convert.Err(err, "tenant")
	})
	if err != nil {
		return nil, err
	}
	if c.log != nil {
		c.log.InfoContext(ctx, "placement changed", "tenant_id", id, "cell", out.Cell, "region", out.Region, "placement_status", out.PlacementStatus)
	}
	return tenantdir.ToProto(tenantdir.Placement{TenantID: out.ID, Cell: out.Cell, Region: out.Region, Status: tenantdir.Status(out.PlacementStatus)}), nil
}
