// Package query is the read side of the quota module.
package query

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/assets-svc/internal/quota"
	"github.com/0furkancolak/payrudder/services/assets-svc/internal/store"
	"github.com/0furkancolak/payrudder/services/assets-svc/internal/store/sqlc"
)

// Queries read the storage quota.
type Queries struct {
	db     store.DB
	limits quota.Limits
}

// New returns the quota queries.
func New(db store.DB, limits quota.Limits) *Queries { return &Queries{db: db, limits: limits} }

// Limit is the storage limit of the tenant's tier, in bytes.
func (q *Queries) Limit(ti tenant.Info) int64 { return q.limits.For(ti) }

// Used is the tenant's storage in use (0 before its first completed upload).
func (q *Queries) Used(ctx context.Context) (int64, error) {
	var used int64
	err := q.db.Tx(ctx, func(tx *sqlc.Queries) error {
		var err error
		used, err = tx.GetBytesUsed(ctx)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	})
	return used, err
}
