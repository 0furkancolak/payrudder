// Package query is the read side of the uploads module: the view of a session and of its files, and the presigned part URLs.
package query

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"

	assetsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/assets/v1"
	"github.com/0furkancolak/payrudder/pkg/blob"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/services/assets-svc/internal/apierr"
	"github.com/0furkancolak/payrudder/services/assets-svc/internal/convert"
	"github.com/0furkancolak/payrudder/services/assets-svc/internal/store"
	"github.com/0furkancolak/payrudder/services/assets-svc/internal/store/sqlc"
)

// PresignBatch is the most part URLs one call signs.
const PresignBatch = 100

// Queries read upload sessions.
type Queries struct {
	db         store.DB
	blob       store.Blob
	presignTTL time.Duration
	now        func() time.Time
}

// New returns the upload queries; presignTTL is how long a part URL stays valid.
func New(db store.DB, b store.Blob, presignTTL time.Duration, now func() time.Time) *Queries {
	return &Queries{db: db, blob: b, presignTTL: presignTTL, now: now}
}

// SessionByKey returns the session an idempotent retry already created, or nil.
func (q *Queries) SessionByKey(ctx context.Context, key string) (*assetsv1.UploadSession, error) {
	var existing *assetsv1.UploadSession
	err := q.db.Tx(ctx, func(tx *sqlc.Queries) error {
		id, err := tx.GetSessionIDByIdempotencyKey(ctx, key)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		existing, err = q.SessionView(ctx, tx, id)
		return err
	})
	return existing, err
}

// SessionView renders a session with the first presigned parts of each file that can still be uploaded.
func (q *Queries) SessionView(ctx context.Context, tx *sqlc.Queries, id uuid.UUID) (*assetsv1.UploadSession, error) {
	sess, err := store.GetSession(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	out := &assetsv1.UploadSession{Id: sess.ID.String(), Status: sess.Status, ExpireTime: timestamppb.New(sess.ExpiresAt)}
	as, err := tx.ListSessionAssets(ctx, id)
	if err != nil {
		return nil, err
	}
	for _, a := range as {
		f := convert.UploadFile(a)
		if sess.Status == store.SessionOpen && store.Uploadable(a) && a.S3UploadID != nil {
			count := min(int(*a.PartCount), PresignBatch)
			if f.PresignedParts, err = q.presign(ctx, a, 1, count); err != nil {
				return nil, errkey.New(codes.Unavailable, "assets.storage.sign_failed", "could not sign part URLs")
			}
		}
		out.Files = append(out.Files, f)
	}
	return out, nil
}

func (q *Queries) presign(ctx context.Context, a sqlc.Asset, from, count int) ([]*assetsv1.PresignedPart, error) {
	var out []*assetsv1.PresignedPart
	for p := from; p < from+count && p <= int(*a.PartCount); p++ {
		u, err := q.blob.PresignPart(ctx, a.ObjectKey, *a.S3UploadID, int32(p), q.presignTTL, "") //nolint:gosec // ≤ 10 000
		if err != nil {
			return nil, err
		}
		out = append(out, &assetsv1.PresignedPart{PartNumber: int32(p), Url: u}) //nolint:gosec // ≤ 10 000
	}
	return out, nil
}

// openForUpload loads an asset of an OPEN, unexpired session that is still uploadable.
func (q *Queries) openForUpload(ctx context.Context, tx *sqlc.Queries, sessionID, assetID uuid.UUID) (sqlc.Asset, error) {
	sess, err := store.GetSession(ctx, tx, sessionID)
	if err != nil {
		return sqlc.Asset{}, err
	}
	a, err := store.GetAsset(ctx, tx, assetID)
	if err != nil {
		return a, err
	}
	if !store.InSession(a, sessionID) {
		return a, errkey.New(codes.NotFound, "assets.asset.not_in_session", "asset not found in this session")
	}
	if !store.SessionIsOpen(sess, q.now()) {
		return a, errkey.New(codes.FailedPrecondition, "assets.session.closed", "the upload session is no longer open")
	}
	return a, nil
}

// PresignParts signs count part URLs of an uploadable file, starting at part from.
func (q *Queries) PresignParts(ctx context.Context, sessionID, assetID uuid.UUID, from, count int) ([]*assetsv1.PresignedPart, error) {
	if from < 1 {
		from = 1
	}
	if count < 1 || count > PresignBatch {
		return nil, errkey.Newf(codes.InvalidArgument, "assets.upload.bad_part_count", "count must be 1..%d", PresignBatch)
	}
	var parts []*assetsv1.PresignedPart
	err := q.db.Tx(ctx, func(tx *sqlc.Queries) error {
		a, err := q.openForUpload(ctx, tx, sessionID, assetID)
		if err != nil {
			return err
		}
		if !store.Uploadable(a) {
			return errkey.New(codes.FailedPrecondition, "assets.asset.not_uploadable", "the file is no longer uploadable")
		}
		if from > int(*a.PartCount) {
			return errkey.Newf(codes.InvalidArgument, "assets.upload.bad_from_part", "from_part is beyond the %d parts of this file", *a.PartCount)
		}
		parts, err = q.presign(ctx, a, from, count)
		return err
	})
	if err != nil {
		return nil, apierr.Internal(ctx, err)
	}
	return parts, nil
}

// File is one file of a session with the parts S3 already holds. Reading it also moves a PENDING file that has parts to
// UPLOADING, so it takes the asset's row lock; that bookkeeping is idempotent and is the only write a query here makes.
func (q *Queries) File(ctx context.Context, sessionID, assetID uuid.UUID) (*assetsv1.UploadFile, error) {
	var out *assetsv1.UploadFile
	err := q.db.Tx(ctx, func(tx *sqlc.Queries) error {
		a, err := store.LockAsset(ctx, tx, assetID)
		if err != nil {
			return err
		}
		if !store.InSession(a, sessionID) {
			return errkey.New(codes.NotFound, "assets.asset.not_in_session", "asset not found in this session")
		}
		out = convert.UploadFile(a)
		if store.Uploadable(a) && a.S3UploadID != nil {
			return q.addUploadedParts(ctx, tx, a, out)
		}
		return nil
	})
	if err != nil {
		return nil, apierr.Internal(ctx, err)
	}
	return out, nil
}

// addUploadedParts lists what S3 holds for a file and moves a PENDING file with parts to UPLOADING.
func (q *Queries) addUploadedParts(ctx context.Context, tx *sqlc.Queries, a sqlc.Asset, out *assetsv1.UploadFile) error {
	parts, err := q.blob.ListParts(ctx, a.ObjectKey, *a.S3UploadID)
	if err != nil && !errors.Is(err, blob.ErrNoSuchUpload) {
		return errkey.New(codes.Unavailable, "assets.storage.unavailable", "object storage unavailable")
	}
	for _, p := range parts {
		out.UploadedParts = append(out.UploadedParts, &assetsv1.UploadedPart{PartNumber: p.Number, Size: p.Size, Etag: store.TrimETag(p.ETag)})
	}
	if len(parts) > 0 && a.Status == store.AssetPending {
		if err := tx.MarkAssetUploading(ctx, a.ID); err != nil {
			return err
		}
		out.Status = assetsv1.AssetStatus_ASSET_STATUS_UPLOADING
	}
	return nil
}
