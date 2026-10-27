// Package query reads the tenant directory (ADR 0018) on the system pool.
package query

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/page"
	"github.com/0furkancolak/payrudder/pkg/tenantdir"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/convert"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// Default and largest page of ListPlacements.
const (
	defaultPageSize = 500
	maxPageSize     = 1000
)

// Queries reads the placement columns of tenants.
type Queries struct{ pool *pgxpool.Pool }

// New returns the directory queries on the system pool (role plans_system).
func New(systemPool *pgxpool.Pool) *Queries { return &Queries{pool: systemPool} }

// Get returns where one tenant lives.
func (q *Queries) Get(ctx context.Context, id uuid.UUID) (*plansv1.Placement, error) {
	r, err := sqlc.New(q.pool).GetPlacement(ctx, id)
	if err != nil {
		return nil, convert.Err(err, "tenant")
	}
	return placement(r.ID, r.Cell, r.Region, r.PlacementStatus), nil
}

// List returns a page of the tenants placed in a cell, in id order. The token is the id of the last tenant of the previous page.
func (q *Queries) List(ctx context.Context, cell string, pageSize int32, pageToken string) ([]*plansv1.Placement, string, error) {
	size, ok := page.Size(pageSize, defaultPageSize, maxPageSize)
	if !ok {
		return nil, "", domain.Invalid("page_size", "must be 1..1000")
	}
	after := uuid.Nil
	if pageToken != "" {
		var err error
		if after, err = uuid.Parse(pageToken); err != nil {
			return nil, "", domain.Invalid("page_token", "invalid")
		}
	}
	rows, err := sqlc.New(q.pool).ListPlacements(ctx, sqlc.ListPlacementsParams{Cell: cell, After: after, PageSize: size + 1})
	if err != nil {
		return nil, "", convert.Err(err, "placements")
	}
	rows, more := page.Trim(rows, int(size))
	out := make([]*plansv1.Placement, len(rows))
	for i, r := range rows {
		out[i] = placement(r.ID, r.Cell, r.Region, r.PlacementStatus)
	}
	next := ""
	if more {
		next = rows[len(rows)-1].ID.String()
	}
	return out, next, nil
}

func placement(id uuid.UUID, cell, region, status string) *plansv1.Placement {
	return tenantdir.ToProto(tenantdir.Placement{TenantID: id, Cell: cell, Region: region, Status: tenantdir.Status(status)})
}
