// Package command changes the storage a tenant uses: Reserve when a file becomes READY, Release when it is deleted.
// Both run in the caller's transaction, so the quota and the asset status change together.
package command

import (
	"context"

	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/assets-svc/internal/quota"
	"github.com/0furkancolak/payrudder/services/assets-svc/internal/store/sqlc"
)

// Commands change the storage usage.
type Commands struct{ limits quota.Limits }

// New returns the quota commands.
func New(limits quota.Limits) *Commands { return &Commands{limits: limits} }

// Reserve adds size bytes to the tenant's usage unless that would pass the limit of its tier; it reports whether it did.
// This is the authoritative check, the one in the queries is only an early answer.
func (c *Commands) Reserve(ctx context.Context, q *sqlc.Queries, ti tenant.Info, size int64) (bool, error) {
	if err := q.EnsureStorageUsage(ctx); err != nil {
		return false, err
	}
	reserved, err := q.ReserveStorage(ctx, sqlc.ReserveStorageParams{Size: size, Quota: c.limits.For(ti)})
	if err != nil {
		return false, err
	}
	return reserved != 0, nil
}

// Release gives size bytes back.
func (c *Commands) Release(ctx context.Context, q *sqlc.Queries, size int64) error {
	return q.ReleaseStorage(ctx, size)
}
