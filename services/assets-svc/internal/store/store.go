// Package store is what the modules share about the database and the object storage: the tenant transaction, the row
// loaders that turn "no rows" into a not-found error, the status names and the object key format. The statements
// themselves are in queries/ and generated into sqlc/.
package store

import (
	"context"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"

	"github.com/0furkancolak/payrudder/pkg/blob"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/assets-svc/internal/store/sqlc"
)

// Asset statuses.
const (
	AssetPending   = "PENDING"
	AssetUploading = "UPLOADING"
	AssetScanning  = "SCANNING"
	AssetReady     = "READY"
	AssetRejected  = "REJECTED"
	AssetAborted   = "ABORTED"
	AssetCorrupt   = "CORRUPT"
	AssetDeleted   = "DELETED"
)

// Upload session statuses.
const (
	SessionOpen      = "OPEN"
	SessionCompleted = "COMPLETED"
	SessionAborted   = "ABORTED"
)

// Blob is the object storage the service needs (*blob.Store).
type Blob interface {
	CreateMultipart(ctx context.Context, key string, o blob.PutOptions) (string, error)
	PresignPart(ctx context.Context, key, uploadID string, part int32, ttl time.Duration, checksumSHA256 string) (string, error)
	ListParts(ctx context.Context, key, uploadID string) ([]blob.Part, error)
	CompleteMultipart(ctx context.Context, key, uploadID string, parts []blob.Part) error
	AbortMultipart(ctx context.Context, key, uploadID string) error
	Head(ctx context.Context, key string) (blob.Info, error)
	Delete(ctx context.Context, key string) error
	PutStream(ctx context.Context, key string, body io.Reader, o blob.StreamOptions) error
	PresignDownload(ctx context.Context, key, filename string, ttl time.Duration) (string, error)
}

// DB runs statements as the tenant of the request (assets_app, RLS enforced).
type DB struct{ pool *pgxpool.Pool }

// NewDB returns the tenant transaction runner over the app pool.
func NewDB(pool *pgxpool.Pool) DB { return DB{pool: pool} }

// Tx runs fn in a transaction of the caller's tenant.
func (d DB) Tx(ctx context.Context, fn func(q *sqlc.Queries) error) error {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return err
	}
	return pgxx.InTenantTx(ctx, d.pool, ti.ID, func(tx pgx.Tx) error { return fn(sqlc.New(tx)) })
}

func sessionNotFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return errkey.New(codes.NotFound, "assets.session.not_found", "upload session not found")
	}
	return err
}

func assetNotFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return errkey.New(codes.NotFound, "assets.asset.not_found", "asset not found")
	}
	return err
}

// GetSession reads an upload session.
func GetSession(ctx context.Context, q *sqlc.Queries, id uuid.UUID) (sqlc.UploadSession, error) {
	sess, err := q.GetUploadSession(ctx, id)
	return sess, sessionNotFound(err)
}

// LockSession reads an upload session and locks its row.
func LockSession(ctx context.Context, q *sqlc.Queries, id uuid.UUID) (sqlc.UploadSession, error) {
	sess, err := q.GetUploadSessionForUpdate(ctx, id)
	return sess, sessionNotFound(err)
}

// GetAsset reads an asset.
func GetAsset(ctx context.Context, q *sqlc.Queries, id uuid.UUID) (sqlc.Asset, error) {
	a, err := q.GetAsset(ctx, id)
	return a, assetNotFound(err)
}

// LockAsset reads an asset and locks its row.
func LockAsset(ctx context.Context, q *sqlc.Queries, id uuid.UUID) (sqlc.Asset, error) {
	a, err := q.GetAssetForUpdate(ctx, id)
	return a, assetNotFound(err)
}

// InSession reports whether the asset belongs to the session.
func InSession(a sqlc.Asset, sessionID uuid.UUID) bool {
	return a.SessionID != nil && *a.SessionID == sessionID
}

// Uploadable reports whether bytes can still be uploaded to the asset.
func Uploadable(a sqlc.Asset) bool { return a.Status == AssetPending || a.Status == AssetUploading }

// SessionIsOpen reports whether the session is OPEN and not yet expired at now.
func SessionIsOpen(sess sqlc.UploadSession, now time.Time) bool {
	return sess.Status == SessionOpen && sess.ExpiresAt.After(now)
}

// ObjectKey is where an asset's bytes live: {tenant}/assets/{asset}.
func ObjectKey(tenantID, assetID uuid.UUID) string {
	return tenantID.String() + "/assets/" + assetID.String()
}

// AssetIDOfKey is the inverse of ObjectKey for the asset id; ok is false for a key that is not an asset object.
func AssetIDOfKey(key string) (id uuid.UUID, ok bool) {
	parts := strings.Split(key, "/")
	if len(parts) != 3 || parts[1] != "assets" {
		return uuid.Nil, false
	}
	id, err := uuid.Parse(parts[2])
	return id, err == nil
}

// TrimETag removes the quotes S3 puts around an ETag.
func TrimETag(s string) string { return strings.Trim(s, `"`) }
