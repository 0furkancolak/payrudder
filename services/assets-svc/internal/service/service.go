// Package service implements AssetsService: upload sessions with presigned multipart uploads, proxied streaming uploads,
// asset metadata and the quota (docs/03-services/assets-svc.md).
package service

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	assetsv1 "github.com/0furkancolak/quotify/gen/go/quotify/assets/v1"
	"github.com/0furkancolak/quotify/pkg/blob"
	"github.com/0furkancolak/quotify/pkg/errkey"
	"github.com/0furkancolak/quotify/pkg/pgxx"
	"github.com/0furkancolak/quotify/pkg/tenant"
	"github.com/0furkancolak/quotify/services/assets-svc/internal/policy"
	"github.com/0furkancolak/quotify/services/assets-svc/internal/store/sqlc"
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

// Config tunes the service.
type Config struct {
	// Quotas are the storage limits in bytes by tenant tier ("free", "standard", "enterprise").
	Quotas map[string]int64
	// DefaultQuota applies to an unknown tier.
	DefaultQuota int64
	SessionTTL   time.Duration // default 24 h
	PresignTTL   time.Duration // default 15 min
	DownloadTTL  time.Duration // default 5 min
	Log          *slog.Logger
}

// DefaultQuotas are the per-tier storage quotas.
var DefaultQuotas = map[string]int64{"free": 100 * policy.MiB, "standard": 10 * policy.GiB, "enterprise": 1024 * policy.GiB}

const (
	maxFiles     = 50
	presignBatch = 100
)

// Service implements assetsv1.AssetsServiceServer.
type Service struct {
	assetsv1.UnimplementedAssetsServiceServer
	pool *pgxpool.Pool
	blob Blob
	pol  policy.Policy
	cfg  Config
	log  *slog.Logger
	now  func() time.Time
}

// New returns the service over the app pool (assets_app, RLS enforced).
func New(pool *pgxpool.Pool, b Blob, pol policy.Policy, cfg Config) *Service {
	if cfg.SessionTTL <= 0 {
		cfg.SessionTTL = 24 * time.Hour
	}
	if cfg.PresignTTL <= 0 {
		cfg.PresignTTL = 15 * time.Minute
	}
	if cfg.DownloadTTL <= 0 {
		cfg.DownloadTTL = 5 * time.Minute
	}
	if cfg.Quotas == nil {
		cfg.Quotas = DefaultQuotas
	}
	if cfg.DefaultQuota <= 0 {
		cfg.DefaultQuota = cfg.Quotas["free"]
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	return &Service{pool: pool, blob: b, pol: pol, cfg: cfg, log: cfg.Log, now: time.Now}
}

// violationKeys are the error keys of the application error codes (ErrorInfo.Reason) that policy violations carry.
var violationKeys = map[string]string{
	policy.UnsupportedContentType: errkey.Key("assets.upload.unsupported_content_type"),
	policy.ContentTypeMismatch:    errkey.Key("assets.upload.content_type_mismatch"),
	policy.FileTooLarge:           errkey.Key("assets.upload.file_too_large"),
	policy.StorageQuotaExceeded:   errkey.Key("assets.storage.quota_exceeded"),
	policy.InvalidFilename:        errkey.Key("assets.upload.invalid_filename"),
	policy.PartMismatch:           errkey.Key("assets.upload.part_mismatch"),
	policy.SizeMismatch:           errkey.Key("assets.upload.size_mismatch"),
}

// failure builds a gRPC error that carries an error key (the console translates it) and also an application error code (ErrorInfo.Reason), which the
// gateway puts in the `code` of its error envelope.
func failure(c codes.Code, reason, msg string) error {
	key, ok := violationKeys[reason]
	if !ok {
		key = errkey.Default(c)
	}
	// The application code comes first: clients that read the first ErrorInfo keep getting it.
	st := status.New(c, msg)
	if d, err := st.WithDetails(&errdetails.ErrorInfo{Reason: reason, Domain: "quotify.assets"},
		&errdetails.ErrorInfo{Domain: errkey.Domain, Metadata: map[string]string{errkey.MetaKey: key}}); err == nil {
		st = d
	}
	return st.Err()
}

func fromViolation(v *policy.Violation) error {
	c := codes.InvalidArgument
	if v.Code == policy.StorageQuotaExceeded {
		c = codes.FailedPrecondition
	}
	return failure(c, v.Code, v.Message)
}

func (s *Service) quota(ti tenant.Info) int64 {
	if q, ok := s.cfg.Quotas[ti.Tier]; ok {
		return q
	}
	return s.cfg.DefaultQuota
}

func (s *Service) tx(ctx context.Context, fn func(q *sqlc.Queries) error) error {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return err
	}
	return pgxx.InTenantTx(ctx, s.pool, ti.ID, func(tx pgx.Tx) error { return fn(sqlc.New(tx)) })
}

func internal(err error) error {
	if _, ok := status.FromError(err); ok {
		return err
	}
	slog.Error("assets-svc internal error", "error", err)
	return status.Error(codes.Internal, "internal error")
}

func assetProto(a sqlc.Asset) *assetsv1.Asset {
	out := &assetsv1.Asset{
		Id: a.ID.String(), Purpose: policy.PurposeOf(a.Purpose), Filename: a.Filename, ContentType: a.ContentType, Status: statusOf(a.Status),
		CreateTime: timestamppb.New(a.CreatedAt), Sha256: hex.EncodeToString(a.Sha256),
	}
	if a.SessionID != nil {
		out.SessionId = a.SessionID.String()
	}
	switch {
	case a.SizeBytes != nil:
		out.SizeBytes = *a.SizeBytes
	case a.DeclaredSize != nil:
		out.SizeBytes = *a.DeclaredSize
	}
	if a.RejectReason != nil {
		out.RejectReason = *a.RejectReason
	}
	if a.CompletedAt != nil {
		out.CompleteTime = timestamppb.New(*a.CompletedAt)
	}
	return out
}

func statusOf(s string) assetsv1.AssetStatus {
	switch s {
	case "PENDING":
		return assetsv1.AssetStatus_ASSET_STATUS_PENDING
	case "UPLOADING":
		return assetsv1.AssetStatus_ASSET_STATUS_UPLOADING
	case "SCANNING":
		return assetsv1.AssetStatus_ASSET_STATUS_SCANNING
	case "READY":
		return assetsv1.AssetStatus_ASSET_STATUS_READY
	case "REJECTED":
		return assetsv1.AssetStatus_ASSET_STATUS_REJECTED
	case "ABORTED":
		return assetsv1.AssetStatus_ASSET_STATUS_ABORTED
	case "CORRUPT":
		return assetsv1.AssetStatus_ASSET_STATUS_CORRUPT
	case "DELETED":
		return assetsv1.AssetStatus_ASSET_STATUS_DELETED
	}
	return assetsv1.AssetStatus_ASSET_STATUS_UNSPECIFIED
}

func objectKey(tenantID, assetID uuid.UUID) string {
	return fmt.Sprintf("%s/assets/%s", tenantID, assetID)
}

func parseID(field, v string) (uuid.UUID, error) {
	id, err := uuid.Parse(v)
	if err != nil {
		return uuid.Nil, errkey.Newf(codes.InvalidArgument, "assets.request.bad_id", "%s must be a UUID", field)
	}
	return id, nil
}

func loadSession(ctx context.Context, q *sqlc.Queries, id uuid.UUID, forUpdate bool) (sqlc.UploadSession, error) {
	var sess sqlc.UploadSession
	var err error
	if forUpdate {
		sess, err = q.GetUploadSessionForUpdate(ctx, id)
	} else {
		sess, err = q.GetUploadSession(ctx, id)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return sess, errkey.New(codes.NotFound, "assets.session.not_found", "upload session not found")
	}
	return sess, err
}

func loadAsset(ctx context.Context, q *sqlc.Queries, id uuid.UUID, forUpdate bool) (sqlc.Asset, error) {
	var a sqlc.Asset
	var err error
	if forUpdate {
		a, err = q.GetAssetForUpdate(ctx, id)
	} else {
		a, err = q.GetAsset(ctx, id)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return a, errkey.New(codes.NotFound, "assets.asset.not_found", "asset not found")
	}
	return a, err
}

func etagOf(s string) string { return strings.Trim(s, `"`) }

func inSession(a sqlc.Asset, sessionID uuid.UUID) bool {
	return a.SessionID != nil && *a.SessionID == sessionID
}

func (s *Service) sessionOpen(sess sqlc.UploadSession) bool {
	return sess.Status == "OPEN" && sess.ExpiresAt.After(s.now())
}

func uploadable(a sqlc.Asset) bool { return a.Status == "PENDING" || a.Status == "UPLOADING" }
