// Package command is the write side of the assets module.
package command

import (
	"context"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"

	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/services/assets-svc/internal/apierr"
	quotacmd "github.com/0furkancolak/payrudder/services/assets-svc/internal/quota/command"
	"github.com/0furkancolak/payrudder/services/assets-svc/internal/store"
	"github.com/0furkancolak/payrudder/services/assets-svc/internal/store/sqlc"
)

// Commands are the state-changing operations on finished assets.
type Commands struct {
	db    store.DB
	blob  store.Blob
	quota *quotacmd.Commands
}

// New returns the asset commands.
func New(db store.DB, b store.Blob, quota *quotacmd.Commands) *Commands {
	return &Commands{db: db, blob: b, quota: quota}
}

// Delete removes a READY or CORRUPT asset's object, gives its bytes back to the quota and marks it DELETED. Deleting
// an asset twice returns the same asset.
func (c *Commands) Delete(ctx context.Context, id uuid.UUID) (sqlc.Asset, error) {
	var out sqlc.Asset
	err := c.db.Tx(ctx, func(q *sqlc.Queries) error {
		var err error
		out, err = c.deleteAsset(ctx, q, id)
		return err
	})
	if err != nil {
		return sqlc.Asset{}, apierr.Internal(ctx, err)
	}
	return out, nil
}

func (c *Commands) deleteAsset(ctx context.Context, q *sqlc.Queries, id uuid.UUID) (sqlc.Asset, error) {
	a, err := store.LockAsset(ctx, q, id)
	if err != nil {
		return a, err
	}
	if a.Status == store.AssetDeleted {
		return a, nil
	}
	if a.Status != store.AssetReady && a.Status != store.AssetCorrupt {
		return a, errkey.Newf(codes.FailedPrecondition, "assets.asset.cannot_abort", "the asset is %s; abort the upload instead", a.Status)
	}
	if err := c.blob.Delete(ctx, a.ObjectKey); err != nil {
		return a, errkey.New(codes.Unavailable, "assets.storage.unavailable", "object storage unavailable")
	}
	if a.SizeBytes != nil {
		if err := c.quota.Release(ctx, q, *a.SizeBytes); err != nil {
			return a, err
		}
	}
	if err := q.MarkAssetDeleted(ctx, id); err != nil {
		return a, err
	}
	return store.GetAsset(ctx, q, id)
}
