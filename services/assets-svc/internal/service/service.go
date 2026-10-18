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
	"github.com/0furkancolak/quotify/pkg/idgen"
	"github.com/0furkancolak/quotify/pkg/pgxx"
	"github.com/0furkancolak/quotify/pkg/tenant"
	"github.com/0furkancolak/quotify/services/assets-svc/internal/policy"
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

// failure builds a gRPC error that also carries an application error code (ErrorInfo.Reason), which the gateway puts in
// the `code` of its error envelope.
func failure(c codes.Code, reason, msg string) error {
	st := status.New(c, msg)
	if d, err := st.WithDetails(&errdetails.ErrorInfo{Reason: reason, Domain: "quotify.assets"}); err == nil {
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

func (s *Service) tx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return err
	}
	return pgxx.InTenantTx(ctx, s.pool, ti.ID, fn)
}

func internal(err error) error {
	if _, ok := status.FromError(err); ok {
		return err
	}
	slog.Error("assets-svc internal error", "error", err)
	return status.Error(codes.Internal, "internal error")
}

// asset is an assets row.
type asset struct {
	TenantID, ID uuid.UUID
	SessionID    *uuid.UUID
	Purpose      string
	Filename     string
	ContentType  string
	DeclaredSize *int64
	SizeBytes    *int64
	SHA256       []byte
	ObjectKey    string
	UploadID     *string
	PartSize     *int64
	PartCount    *int32
	Status       string
	RejectReason *string
	CreatedAt    time.Time
	CompletedAt  *time.Time
}

const assetColumns = `tenant_id, id, session_id, purpose, filename, content_type, declared_size, size_bytes, sha256, object_key, s3_upload_id,
	part_size, part_count, status, reject_reason, created_at, completed_at`

func scanAsset(row pgx.Row) (*asset, error) {
	var a asset
	err := row.Scan(&a.TenantID, &a.ID, &a.SessionID, &a.Purpose, &a.Filename, &a.ContentType, &a.DeclaredSize, &a.SizeBytes, &a.SHA256, &a.ObjectKey,
		&a.UploadID, &a.PartSize, &a.PartCount, &a.Status, &a.RejectReason, &a.CreatedAt, &a.CompletedAt)
	return &a, err
}

func (a *asset) proto() *assetsv1.Asset {
	out := &assetsv1.Asset{
		Id: a.ID.String(), Purpose: policy.PurposeOf(a.Purpose), Filename: a.Filename, ContentType: a.ContentType, Status: statusOf(a.Status),
		CreateTime: timestamppb.New(a.CreatedAt), Sha256: hex.EncodeToString(a.SHA256),
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
		return uuid.Nil, status.Errorf(codes.InvalidArgument, "%s must be a UUID", field)
	}
	return id, nil
}

type session struct {
	ID        uuid.UUID
	Status    string
	ExpiresAt time.Time
}

func loadSession(ctx context.Context, tx pgx.Tx, id uuid.UUID, forUpdate bool) (*session, error) {
	q := `SELECT id, status, expires_at FROM upload_sessions WHERE id = $1`
	if forUpdate {
		q += ` FOR UPDATE`
	}
	var s session
	err := tx.QueryRow(ctx, q, id).Scan(&s.ID, &s.Status, &s.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, status.Error(codes.NotFound, "upload session not found")
	}
	return &s, err
}

func loadAsset(ctx context.Context, tx pgx.Tx, id uuid.UUID, forUpdate bool) (*asset, error) {
	q := `SELECT ` + assetColumns + ` FROM assets WHERE id = $1`
	if forUpdate {
		q += ` FOR UPDATE`
	}
	a, err := scanAsset(tx.QueryRow(ctx, q, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, status.Error(codes.NotFound, "asset not found")
	}
	return a, err
}

func etagOf(s string) string { return strings.Trim(s, `"`) }

// CreateUploadSession implements assetsv1.AssetsServiceServer.
func (s *Service) CreateUploadSession(ctx context.Context, req *assetsv1.CreateUploadSessionRequest) (*assetsv1.CreateUploadSessionResponse, error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	n := len(req.GetFiles())
	if n < 1 || n > maxFiles {
		return nil, status.Errorf(codes.InvalidArgument, "a session takes 1..%d files, got %d", maxFiles, n)
	}
	type planned struct {
		id          uuid.UUID
		filename    string
		contentType string
		size        int64
		purpose     assetsv1.Purpose
		sha         []byte
		partSize    int64
		partCount   int32
		uploadID    string
	}
	files := make([]*planned, n)
	var total int64
	for i, f := range req.GetFiles() {
		name, err := policy.SanitizeFilename(f.GetFilename())
		if err != nil {
			return nil, failure(codes.InvalidArgument, policy.InvalidFilename, fmt.Sprintf("files[%d].filename: unusable", i))
		}
		if f.GetSize() <= 0 {
			return nil, status.Errorf(codes.InvalidArgument, "files[%d].size is required for direct uploads", i)
		}
		ct, v := s.pol.Check(f.GetPurpose(), f.GetContentType(), f.GetSize())
		if v != nil {
			v.Message = fmt.Sprintf("files[%d]: %s", i, v.Message)
			return nil, fromViolation(v)
		}
		p := &planned{id: idgen.NewUUIDv7(), filename: name, contentType: ct, size: f.GetSize(), purpose: f.GetPurpose()}
		if h := f.GetSha256(); h != "" {
			if p.sha, err = hex.DecodeString(h); err != nil || len(p.sha) != 32 {
				return nil, status.Errorf(codes.InvalidArgument, "files[%d].sha256 must be 64 hex characters", i)
			}
		}
		p.partSize = policy.PartSize(p.size)
		p.partCount = policy.PartCount(p.size, p.partSize)
		total += p.size
		files[i] = p
	}

	// An idempotent retry gets the same session.
	if key := req.GetIdempotencyKey(); key != "" {
		var existing *assetsv1.UploadSession
		err := s.tx(ctx, func(tx pgx.Tx) error {
			var id uuid.UUID
			err := tx.QueryRow(ctx, `SELECT id FROM upload_sessions WHERE idempotency_key = $1`, key).Scan(&id)
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			if err != nil {
				return err
			}
			existing, err = s.sessionView(ctx, tx, id)
			return err
		})
		if err != nil {
			return nil, internal(err)
		}
		if existing != nil {
			return &assetsv1.CreateUploadSessionResponse{Session: existing}, nil
		}
	}

	// Quota is checked now (cheap early answer) and again, authoritatively, when each file completes.
	var used int64
	if err := s.tx(ctx, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `SELECT bytes_used FROM storage_usage WHERE tenant_id = app_tenant_id()`).Scan(&used)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}); err != nil {
		return nil, internal(err)
	}
	if used+total > s.quota(ti) {
		return nil, failure(codes.FailedPrecondition, policy.StorageQuotaExceeded, "the files do not fit in the tenant's storage quota")
	}

	// Start the S3 multipart uploads, then record the session; undo S3 if recording fails.
	abortAll := func() {
		actx := context.WithoutCancel(ctx)
		for _, p := range files {
			if p.uploadID != "" {
				_ = s.blob.AbortMultipart(actx, objectKey(ti.ID, p.id), p.uploadID)
			}
		}
	}
	for _, p := range files {
		p.uploadID, err = s.blob.CreateMultipart(ctx, objectKey(ti.ID, p.id), blob.PutOptions{ContentType: p.contentType,
			Tags: map[string]string{"purpose": policy.PurposeName(p.purpose)}})
		if err != nil {
			abortAll()
			return nil, status.Error(codes.Unavailable, "object storage unavailable")
		}
	}
	sessionID := idgen.NewUUIDv7()
	var idem *string
	if k := req.GetIdempotencyKey(); k != "" {
		idem = &k
	}
	var out *assetsv1.UploadSession
	err = s.tx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `INSERT INTO upload_sessions (tenant_id, id, idempotency_key, status, expires_at) VALUES (app_tenant_id(), $1, $2, 'OPEN', $3)
			ON CONFLICT DO NOTHING`, sessionID, idem, s.now().Add(s.cfg.SessionTTL))
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 { // a concurrent request with the same idempotency key won
			var id uuid.UUID
			if err := tx.QueryRow(ctx, `SELECT id FROM upload_sessions WHERE idempotency_key = $1`, idem).Scan(&id); err != nil {
				return err
			}
			abortAll()
			out, err = s.sessionView(ctx, tx, id)
			return err
		}
		for _, p := range files {
			var sha any
			if p.sha != nil {
				sha = p.sha
			}
			if _, err := tx.Exec(ctx, `INSERT INTO assets (tenant_id, id, session_id, purpose, filename, content_type, declared_size, sha256, object_key, s3_upload_id, part_size, part_count, status)
				VALUES (app_tenant_id(), $1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,'PENDING')`,
				p.id, sessionID, policy.PurposeName(p.purpose), p.filename, p.contentType, p.size, sha, objectKey(ti.ID, p.id), p.uploadID, p.partSize, p.partCount); err != nil {
				return err
			}
		}
		out, err = s.sessionView(ctx, tx, sessionID)
		return err
	})
	if err != nil {
		abortAll()
		return nil, internal(err)
	}
	return &assetsv1.CreateUploadSessionResponse{Session: out}, nil
}

// sessionView renders a session with the first presigned parts of each file that can still be uploaded.
func (s *Service) sessionView(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*assetsv1.UploadSession, error) {
	sess, err := loadSession(ctx, tx, id, false)
	if err != nil {
		return nil, err
	}
	out := &assetsv1.UploadSession{Id: sess.ID.String(), Status: sess.Status, ExpireTime: timestamppb.New(sess.ExpiresAt)}
	rows, err := tx.Query(ctx, `SELECT `+assetColumns+` FROM assets WHERE session_id = $1 ORDER BY created_at, id`, id)
	if err != nil {
		return nil, err
	}
	var as []*asset
	for rows.Next() {
		a, err := scanAsset(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		as = append(as, a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, a := range as {
		f := fileView(a)
		if sess.Status == "OPEN" && (a.Status == "PENDING" || a.Status == "UPLOADING") && a.UploadID != nil {
			count := min(int(*a.PartCount), presignBatch)
			if f.PresignedParts, err = s.presign(ctx, a, 1, count); err != nil {
				return nil, status.Error(codes.Unavailable, "could not sign part URLs")
			}
		}
		out.Files = append(out.Files, f)
	}
	return out, nil
}

func fileView(a *asset) *assetsv1.UploadFile {
	f := &assetsv1.UploadFile{AssetId: a.ID.String(), Filename: a.Filename, Status: statusOf(a.Status)}
	if a.RejectReason != nil {
		f.RejectReason = *a.RejectReason
	}
	if a.DeclaredSize != nil {
		f.Size = *a.DeclaredSize
	}
	if a.PartSize != nil {
		f.PartSize = *a.PartSize
	}
	if a.PartCount != nil {
		f.PartCount = *a.PartCount
	}
	return f
}

func (s *Service) presign(ctx context.Context, a *asset, from, count int) ([]*assetsv1.PresignedPart, error) {
	var out []*assetsv1.PresignedPart
	var checksum string
	for p := from; p < from+count && p <= int(*a.PartCount); p++ {
		u, err := s.blob.PresignPart(ctx, a.ObjectKey, *a.UploadID, int32(p), s.cfg.PresignTTL, checksum) //nolint:gosec // ≤ 10 000
		if err != nil {
			return nil, err
		}
		out = append(out, &assetsv1.PresignedPart{PartNumber: int32(p), Url: u}) //nolint:gosec // ≤ 10 000
	}
	return out, nil
}

// openForUpload loads an asset of an OPEN, unexpired session that is still uploadable.
func (s *Service) openForUpload(ctx context.Context, tx pgx.Tx, sessionID, assetID uuid.UUID, forUpdate bool) (*asset, error) {
	sess, err := loadSession(ctx, tx, sessionID, false)
	if err != nil {
		return nil, err
	}
	a, err := loadAsset(ctx, tx, assetID, forUpdate)
	if err != nil {
		return nil, err
	}
	if a.SessionID == nil || *a.SessionID != sessionID {
		return nil, status.Error(codes.NotFound, "asset not found in this session")
	}
	if sess.Status != "OPEN" || !sess.ExpiresAt.After(s.now()) {
		return a, status.Error(codes.FailedPrecondition, "the upload session is no longer open")
	}
	return a, nil
}

// PresignParts implements assetsv1.AssetsServiceServer.
func (s *Service) PresignParts(ctx context.Context, req *assetsv1.PresignPartsRequest) (*assetsv1.PresignPartsResponse, error) {
	sid, err := parseID("session_id", req.GetSessionId())
	if err != nil {
		return nil, err
	}
	aid, err := parseID("asset_id", req.GetAssetId())
	if err != nil {
		return nil, err
	}
	from, count := int(req.GetFromPart()), int(req.GetCount())
	if from < 1 {
		from = 1
	}
	if count < 1 || count > presignBatch {
		return nil, status.Errorf(codes.InvalidArgument, "count must be 1..%d", presignBatch)
	}
	var parts []*assetsv1.PresignedPart
	err = s.tx(ctx, func(tx pgx.Tx) error {
		a, err := s.openForUpload(ctx, tx, sid, aid, false)
		if err != nil {
			return err
		}
		if a.Status != "PENDING" && a.Status != "UPLOADING" {
			return status.Error(codes.FailedPrecondition, "the file is no longer uploadable")
		}
		if from > int(*a.PartCount) {
			return status.Errorf(codes.InvalidArgument, "from_part is beyond the %d parts of this file", *a.PartCount)
		}
		parts, err = s.presign(ctx, a, from, count)
		return err
	})
	if err != nil {
		return nil, internal(err)
	}
	return &assetsv1.PresignPartsResponse{Parts: parts}, nil
}

// GetUploadFile implements assetsv1.AssetsServiceServer.
func (s *Service) GetUploadFile(ctx context.Context, req *assetsv1.GetUploadFileRequest) (*assetsv1.GetUploadFileResponse, error) {
	sid, err := parseID("session_id", req.GetSessionId())
	if err != nil {
		return nil, err
	}
	aid, err := parseID("asset_id", req.GetAssetId())
	if err != nil {
		return nil, err
	}
	var out *assetsv1.UploadFile
	err = s.tx(ctx, func(tx pgx.Tx) error {
		a, err := loadAsset(ctx, tx, aid, true)
		if err != nil {
			return err
		}
		if a.SessionID == nil || *a.SessionID != sid {
			return status.Error(codes.NotFound, "asset not found in this session")
		}
		out = fileView(a)
		if (a.Status == "PENDING" || a.Status == "UPLOADING") && a.UploadID != nil {
			parts, err := s.blob.ListParts(ctx, a.ObjectKey, *a.UploadID)
			if err != nil && !errors.Is(err, blob.ErrNoSuchUpload) {
				return status.Error(codes.Unavailable, "object storage unavailable")
			}
			for _, p := range parts {
				out.UploadedParts = append(out.UploadedParts, &assetsv1.UploadedPart{PartNumber: p.Number, Size: p.Size, Etag: etagOf(p.ETag)})
			}
			if len(parts) > 0 && a.Status == "PENDING" {
				if _, err := tx.Exec(ctx, `UPDATE assets SET status = 'UPLOADING' WHERE id = $1`, aid); err != nil {
					return err
				}
				out.Status = assetsv1.AssetStatus_ASSET_STATUS_UPLOADING
			}
		}
		return nil
	})
	if err != nil {
		return nil, internal(err)
	}
	return &assetsv1.GetUploadFileResponse{File: out}, nil
}
