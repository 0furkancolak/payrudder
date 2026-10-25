package command

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"

	assetsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/assets/v1"
	"github.com/0furkancolak/payrudder/pkg/blob"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/assets-svc/internal/apierr"
	"github.com/0furkancolak/payrudder/services/assets-svc/internal/policy"
	"github.com/0furkancolak/payrudder/services/assets-svc/internal/store"
	"github.com/0furkancolak/payrudder/services/assets-svc/internal/store/sqlc"
)

// reject marks an asset REJECTED after the caller removed what S3 held.
func reject(ctx context.Context, q *sqlc.Queries, id uuid.UUID, reason string) (sqlc.Asset, error) {
	if err := q.RejectAsset(ctx, sqlc.RejectAssetParams{ID: id, Reason: reason}); err != nil {
		return sqlc.Asset{}, err
	}
	return store.GetAsset(ctx, q, id)
}

// finish turns a complete S3 object into a READY asset: quota reservation and the status change in the caller's transaction.
func (c *Commands) finish(ctx context.Context, q *sqlc.Queries, ti tenant.Info, a sqlc.Asset, size int64, sha []byte) (sqlc.Asset, error) {
	if a.DeclaredSize != nil && size != *a.DeclaredSize {
		c.deleteObject(ctx, a.ObjectKey)
		return reject(ctx, q, a.ID, policy.SizeMismatch)
	}
	reserved, err := c.o.Quota.Reserve(ctx, q, ti, size)
	if err != nil {
		return sqlc.Asset{}, err
	}
	if !reserved {
		c.deleteObject(ctx, a.ObjectKey)
		return reject(ctx, q, a.ID, policy.StorageQuotaExceeded)
	}
	if len(sha) == 0 {
		sha = nil
	}
	if err := q.MarkAssetReady(ctx, sqlc.MarkAssetReadyParams{ID: a.ID, SizeBytes: size, CompletedAt: c.o.Now(), Sha256: sha}); err != nil {
		return sqlc.Asset{}, err
	}
	if a.SessionID != nil {
		if err := q.CompleteSessionIfDrained(ctx, *a.SessionID); err != nil {
			return sqlc.Asset{}, err
		}
	}
	return store.GetAsset(ctx, q, a.ID)
}

// CompleteFile assembles the file's multipart upload and makes it READY (or REJECTED). The asset row stays locked
// from the first check to the last write, so concurrent calls for one file queue up and the second one finds a READY asset.
func (c *Commands) CompleteFile(ctx context.Context, sessionID, assetID uuid.UUID, given []*assetsv1.PartETag) (sqlc.Asset, error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return sqlc.Asset{}, err
	}
	var out sqlc.Asset
	err = c.o.DB.Tx(ctx, func(q *sqlc.Queries) error {
		var err error
		out, err = c.completeFile(ctx, q, ti, sessionID, assetID, given)
		return err
	})
	if err != nil {
		return sqlc.Asset{}, apierr.Internal(ctx, err)
	}
	return out, nil
}

func (c *Commands) completeFile(ctx context.Context, q *sqlc.Queries, ti tenant.Info, sid, aid uuid.UUID, given []*assetsv1.PartETag) (sqlc.Asset, error) {
	a, err := store.LockAsset(ctx, q, aid)
	if err != nil {
		return a, err
	}
	if !store.InSession(a, sid) {
		return a, errkey.New(codes.NotFound, "assets.asset.not_in_session", "asset not found in this session")
	}
	switch a.Status {
	case store.AssetReady, store.AssetRejected:
		return a, nil // completing twice returns the same asset
	case store.AssetPending, store.AssetUploading:
	default:
		return a, errkey.Newf(codes.FailedPrecondition, "assets.asset.bad_status", "the file is %s", a.Status)
	}
	sess, err := store.GetSession(ctx, q, sid)
	if err != nil {
		return a, err
	}
	if !store.SessionIsOpen(sess, c.o.Now()) {
		return a, errkey.New(codes.FailedPrecondition, "assets.session.closed", "the upload session is no longer open")
	}

	parts, err := c.o.Blob.ListParts(ctx, a.ObjectKey, *a.S3UploadID)
	if errors.Is(err, blob.ErrNoSuchUpload) {
		// A previous CompleteFile assembled the object and crashed before recording it: finish from the object.
		info, herr := c.o.Blob.Head(ctx, a.ObjectKey)
		if herr != nil {
			return a, errkey.New(codes.FailedPrecondition, "assets.upload.multipart_gone", "the multipart upload no longer exists")
		}
		return c.finish(ctx, q, ti, a, info.Size, nil)
	}
	if err != nil {
		return a, errkey.New(codes.Unavailable, "assets.storage.unavailable", "object storage unavailable")
	}

	if reason := checkParts(a, given, parts); reason != "" {
		if err := c.o.Blob.AbortMultipart(ctx, a.ObjectKey, *a.S3UploadID); err != nil {
			return a, errkey.New(codes.Unavailable, "assets.storage.unavailable", "object storage unavailable")
		}
		return reject(ctx, q, a.ID, reason)
	}
	if err := c.o.Blob.CompleteMultipart(ctx, a.ObjectKey, *a.S3UploadID, parts); err != nil {
		if errors.Is(err, blob.ErrNoSuchUpload) {
			return a, errkey.New(codes.Aborted, "assets.upload.changed", "the upload changed concurrently; retry")
		}
		return a, errkey.New(codes.Unavailable, "assets.storage.unavailable", "object storage unavailable")
	}
	info, err := c.o.Blob.Head(ctx, a.ObjectKey)
	if err != nil {
		return a, errkey.New(codes.Unavailable, "assets.storage.unavailable", "object storage unavailable")
	}
	return c.finish(ctx, q, ti, a, info.Size, nil)
}

// checkParts compares the parts the client lists with what S3 holds and returns the rejection reason, or "" when they
// agree. The sizes S3 reports decide, never what the client claims.
func checkParts(a sqlc.Asset, given []*assetsv1.PartETag, parts []blob.Part) string {
	etags := make(map[int32]string, len(given))
	for _, p := range given {
		etags[p.GetPartNumber()] = store.TrimETag(p.GetEtag())
	}
	if len(etags) != len(given) || int32(len(parts)) != *a.PartCount || len(etags) != len(parts) { //nolint:gosec // ≤ 10 000
		return policy.PartMismatch
	}
	var sum int64
	for i, p := range parts {
		if p.Number != int32(i+1) || etags[p.Number] != store.TrimETag(p.ETag) { //nolint:gosec // ≤ 10 000
			return policy.PartMismatch
		}
		if i < len(parts)-1 && p.Size != *a.PartSize {
			return policy.SizeMismatch
		}
		sum += p.Size
	}
	if a.DeclaredSize != nil && sum != *a.DeclaredSize {
		return policy.SizeMismatch
	}
	return ""
}

// Abort aborts the uploadable files of a session, or just one file when assetID is set, and settles the session.
func (c *Commands) Abort(ctx context.Context, sessionID uuid.UUID, assetID *uuid.UUID) (*assetsv1.UploadSession, error) {
	var out *assetsv1.UploadSession
	err := c.o.DB.Tx(ctx, func(q *sqlc.Queries) error {
		if _, err := store.LockSession(ctx, q, sessionID); err != nil {
			return err
		}
		if err := c.abortAssets(ctx, q, sessionID, assetID); err != nil {
			return err
		}
		next := store.SessionCompleted
		if assetID == nil {
			next = store.SessionAborted
		}
		if err := q.SettleSession(ctx, sqlc.SettleSessionParams{ID: sessionID, Status: next, Force: assetID == nil}); err != nil {
			return err
		}
		var err error
		out, err = c.o.Sessions.SessionView(ctx, q, sessionID)
		return err
	})
	if err != nil {
		return nil, apierr.Internal(ctx, err)
	}
	return out, nil
}

// abortAssets aborts the uploadable assets of a session, or just the one asset when assetID is set.
func (c *Commands) abortAssets(ctx context.Context, q *sqlc.Queries, sessionID uuid.UUID, assetID *uuid.UUID) error {
	as, err := q.LockUploadableSessionAssets(ctx, sqlc.LockUploadableSessionAssetsParams{SessionID: sessionID, AssetID: assetID})
	if err != nil {
		return err
	}
	if assetID != nil && len(as) == 0 {
		if _, err := store.GetAsset(ctx, q, *assetID); err != nil {
			return err
		}
	}
	for _, a := range as {
		if a.S3UploadID != nil {
			if err := c.o.Blob.AbortMultipart(ctx, a.ObjectKey, *a.S3UploadID); err != nil {
				return errkey.New(codes.Unavailable, "assets.storage.unavailable", "object storage unavailable")
			}
		}
		if err := q.MarkAssetAborted(ctx, a.ID); err != nil {
			return err
		}
	}
	return nil
}
