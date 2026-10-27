// Package query is the read side of the outbox module.
package query

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"

	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/outbox"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
)

// Queries read the outbox.
type Queries struct {
	system *pgxpool.Pool
	cells  *pgxx.Router
}

// New returns the outbox queries; system is the ingest_system pool (it sees every tenant's rows). cells, when not nil, is the system-role router of the cells
// mode (ADR 0018): the outbox of every tenant placed in a cell is read too, in its schema.
func New(system *pgxpool.Pool, cells *pgxx.Router) *Queries {
	return &Queries{system: system, cells: cells}
}

// Watermark is the ingest time of the oldest unpublished outbox row over the pooled table and every cell tenant; ok is false when all are empty. It is the
// answer a consumer closes a period on, so the tenants placed in cells are listed afresh.
func (q *Queries) Watermark(ctx context.Context) (oldest time.Time, ok bool, err error) {
	return q.WatermarkWithin(ctx, 0)
}

// WatermarkWithin is Watermark for a number that is only watched: the list of placed tenants may be maxAge old.
func (q *Queries) WatermarkWithin(ctx context.Context, maxAge time.Duration) (oldest time.Time, ok bool, err error) {
	oldest, ok, err = outbox.Watermark(ctx, q.system)
	if err != nil {
		return time.Time{}, false, errkey.New(codes.Unavailable, "ingest.outbox.unavailable", "outbox unavailable")
	}
	if q.cells == nil {
		return oldest, ok, nil
	}
	cellOldest, cellOK, err := outbox.CellsWatermark(ctx, q.cells, maxAge)
	if err != nil {
		return time.Time{}, false, errkey.New(codes.Unavailable, "ingest.outbox.unavailable", "outbox unavailable")
	}
	if cellOK && (!ok || cellOldest.Before(oldest)) {
		oldest, ok = cellOldest, true
	}
	return oldest, ok, nil
}
