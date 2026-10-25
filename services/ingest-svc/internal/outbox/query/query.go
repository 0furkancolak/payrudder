// Package query is the read side of the outbox module.
package query

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"

	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/outbox"
)

// Queries read the outbox.
type Queries struct{ system *pgxpool.Pool }

// New returns the outbox queries; system is the ingest_system pool (it sees every tenant's rows).
func New(system *pgxpool.Pool) *Queries { return &Queries{system: system} }

// Watermark is the ingest time of the oldest unpublished outbox row; ok is false when the outbox is empty.
func (q *Queries) Watermark(ctx context.Context) (oldest time.Time, ok bool, err error) {
	oldest, ok, err = outbox.Watermark(ctx, q.system)
	if err != nil {
		return time.Time{}, false, errkey.New(codes.Unavailable, "ingest.outbox.unavailable", "outbox unavailable")
	}
	return oldest, ok, nil
}
