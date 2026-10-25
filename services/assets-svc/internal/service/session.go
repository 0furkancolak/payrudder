package service

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"

	assetsv1 "github.com/0furkancolak/quotify/gen/go/quotify/assets/v1"
	"github.com/0furkancolak/quotify/pkg/blob"
	"github.com/0furkancolak/quotify/pkg/errkey"
	"github.com/0furkancolak/quotify/pkg/idgen"
	"github.com/0furkancolak/quotify/pkg/tenant"
	"github.com/0furkancolak/quotify/services/assets-svc/internal/policy"
	"github.com/0furkancolak/quotify/services/assets-svc/internal/store/sqlc"
)

// plannedFile is one validated file of a CreateUploadSession request.
type plannedFile struct {
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

// planFiles validates the request's files and returns them with the total size.
func (s *Service) planFiles(req *assetsv1.CreateUploadSessionRequest) ([]*plannedFile, int64, error) {
	n := len(req.GetFiles())
	if n < 1 || n > maxFiles {
		return nil, 0, errkey.Newf(codes.InvalidArgument, "assets.session.bad_file_count", "a session takes 1..%d files, got %d", maxFiles, n)
	}
	files := make([]*plannedFile, n)
	var total int64
	for i, f := range req.GetFiles() {
		p, err := s.planFile(i, f)
		if err != nil {
			return nil, 0, err
		}
		total += p.size
		files[i] = p
	}
	return files, total, nil
}

func (s *Service) planFile(i int, f *assetsv1.FileSpec) (*plannedFile, error) {
	name, err := policy.SanitizeFilename(f.GetFilename())
	if err != nil {
		return nil, failure(codes.InvalidArgument, policy.InvalidFilename, fmt.Sprintf("files[%d].filename: unusable", i))
	}
	if f.GetSize() <= 0 {
		return nil, errkey.Newf(codes.InvalidArgument, "assets.upload.size_required", "files[%d].size is required for direct uploads", i)
	}
	ct, v := s.pol.Check(f.GetPurpose(), f.GetContentType(), f.GetSize())
	if v != nil {
		v.Message = fmt.Sprintf("files[%d]: %s", i, v.Message)
		return nil, fromViolation(v)
	}
	p := &plannedFile{id: idgen.NewUUIDv7(), filename: name, contentType: ct, size: f.GetSize(), purpose: f.GetPurpose()}
	if h := f.GetSha256(); h != "" {
		if p.sha, err = hex.DecodeString(h); err != nil || len(p.sha) != 32 {
			return nil, errkey.Newf(codes.InvalidArgument, "assets.upload.bad_sha256", "files[%d].sha256 must be 64 hex characters", i)
		}
	}
	p.partSize = policy.PartSize(p.size)
	p.partCount = policy.PartCount(p.size, p.partSize)
	return p, nil
}

// existingSession returns the session an idempotent retry already created, or nil.
func (s *Service) existingSession(ctx context.Context, key string) (*assetsv1.UploadSession, error) {
	var existing *assetsv1.UploadSession
	err := s.tx(ctx, func(q *sqlc.Queries) error {
		id, err := q.GetSessionIDByIdempotencyKey(ctx, key)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		existing, err = s.sessionView(ctx, q, id)
		return err
	})
	return existing, err
}

// bytesUsed is the tenant's storage in use (0 before its first completed upload).
func (s *Service) bytesUsed(ctx context.Context) (int64, error) {
	var used int64
	err := s.tx(ctx, func(q *sqlc.Queries) error {
		var err error
		used, err = q.GetBytesUsed(ctx)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	})
	return used, err
}

// startMultipart opens the S3 multipart upload of every file. On a failure it aborts the ones already open.
func (s *Service) startMultipart(ctx context.Context, ti tenant.Info, files []*plannedFile) error {
	for _, p := range files {
		var err error
		p.uploadID, err = s.blob.CreateMultipart(ctx, objectKey(ti.ID, p.id), blob.PutOptions{ContentType: p.contentType,
			Tags: map[string]string{"purpose": policy.PurposeName(p.purpose)}})
		if err != nil {
			s.abortMultipart(ctx, ti, files)
			return errkey.New(codes.Unavailable, "assets.storage.unavailable", "object storage unavailable")
		}
	}
	return nil
}

func (s *Service) abortMultipart(ctx context.Context, ti tenant.Info, files []*plannedFile) {
	actx := context.WithoutCancel(ctx)
	for _, p := range files {
		if p.uploadID != "" {
			_ = s.blob.AbortMultipart(actx, objectKey(ti.ID, p.id), p.uploadID)
		}
	}
}

// recordSession stores the session and its assets. If a concurrent request with the same idempotency key won, it
// returns that session and aborts this request's multipart uploads.
func (s *Service) recordSession(ctx context.Context, ti tenant.Info, key string, files []*plannedFile) (*assetsv1.UploadSession, error) {
	sessionID := idgen.NewUUIDv7()
	var idem *string
	if key != "" {
		idem = &key
	}
	var out *assetsv1.UploadSession
	err := s.tx(ctx, func(q *sqlc.Queries) error {
		inserted, err := q.InsertUploadSession(ctx, sqlc.InsertUploadSessionParams{ID: sessionID, IdempotencyKey: idem, ExpiresAt: s.now().Add(s.cfg.SessionTTL)})
		if err != nil {
			return err
		}
		if inserted == 0 {
			id, err := q.GetSessionIDByIdempotencyKey(ctx, key)
			if err != nil {
				return err
			}
			s.abortMultipart(ctx, ti, files)
			out, err = s.sessionView(ctx, q, id)
			return err
		}
		for _, p := range files {
			if err := q.InsertSessionAsset(ctx, newSessionAsset(ti, sessionID, p)); err != nil {
				return err
			}
		}
		out, err = s.sessionView(ctx, q, sessionID)
		return err
	})
	return out, err
}

func newSessionAsset(ti tenant.Info, sessionID uuid.UUID, p *plannedFile) sqlc.InsertSessionAssetParams {
	return sqlc.InsertSessionAssetParams{
		ID: p.id, SessionID: &sessionID, Purpose: policy.PurposeName(p.purpose), Filename: p.filename, ContentType: p.contentType,
		DeclaredSize: &p.size, Sha256: p.sha, ObjectKey: objectKey(ti.ID, p.id), S3UploadID: &p.uploadID, PartSize: &p.partSize, PartCount: &p.partCount,
	}
}

// CreateUploadSession implements assetsv1.AssetsServiceServer.
func (s *Service) CreateUploadSession(ctx context.Context, req *assetsv1.CreateUploadSessionRequest) (*assetsv1.CreateUploadSessionResponse, error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	files, total, err := s.planFiles(req)
	if err != nil {
		return nil, err
	}

	// An idempotent retry gets the same session.
	if key := req.GetIdempotencyKey(); key != "" {
		existing, err := s.existingSession(ctx, key)
		if err != nil {
			return nil, internal(err)
		}
		if existing != nil {
			return &assetsv1.CreateUploadSessionResponse{Session: existing}, nil
		}
	}

	// Quota is checked now (cheap early answer) and again, authoritatively, when each file completes.
	used, err := s.bytesUsed(ctx)
	if err != nil {
		return nil, internal(err)
	}
	if used+total > s.quota(ti) {
		return nil, failure(codes.FailedPrecondition, policy.StorageQuotaExceeded, "the files do not fit in the tenant's storage quota")
	}

	// Start the S3 multipart uploads, then record the session; undo S3 if recording fails.
	if err := s.startMultipart(ctx, ti, files); err != nil {
		return nil, err
	}
	out, err := s.recordSession(ctx, ti, req.GetIdempotencyKey(), files)
	if err != nil {
		s.abortMultipart(ctx, ti, files)
		return nil, internal(err)
	}
	return &assetsv1.CreateUploadSessionResponse{Session: out}, nil
}

// sessionView renders a session with the first presigned parts of each file that can still be uploaded.
func (s *Service) sessionView(ctx context.Context, q *sqlc.Queries, id uuid.UUID) (*assetsv1.UploadSession, error) {
	sess, err := loadSession(ctx, q, id, false)
	if err != nil {
		return nil, err
	}
	out := &assetsv1.UploadSession{Id: sess.ID.String(), Status: sess.Status, ExpireTime: timestamppb.New(sess.ExpiresAt)}
	as, err := q.ListSessionAssets(ctx, id)
	if err != nil {
		return nil, err
	}
	for _, a := range as {
		f := fileView(a)
		if sess.Status == "OPEN" && uploadable(a) && a.S3UploadID != nil {
			count := min(int(*a.PartCount), presignBatch)
			if f.PresignedParts, err = s.presign(ctx, a, 1, count); err != nil {
				return nil, errkey.New(codes.Unavailable, "assets.storage.sign_failed", "could not sign part URLs")
			}
		}
		out.Files = append(out.Files, f)
	}
	return out, nil
}

func fileView(a sqlc.Asset) *assetsv1.UploadFile {
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

func (s *Service) presign(ctx context.Context, a sqlc.Asset, from, count int) ([]*assetsv1.PresignedPart, error) {
	var out []*assetsv1.PresignedPart
	var checksum string
	for p := from; p < from+count && p <= int(*a.PartCount); p++ {
		u, err := s.blob.PresignPart(ctx, a.ObjectKey, *a.S3UploadID, int32(p), s.cfg.PresignTTL, checksum) //nolint:gosec // ≤ 10 000
		if err != nil {
			return nil, err
		}
		out = append(out, &assetsv1.PresignedPart{PartNumber: int32(p), Url: u}) //nolint:gosec // ≤ 10 000
	}
	return out, nil
}

// openForUpload loads an asset of an OPEN, unexpired session that is still uploadable.
func (s *Service) openForUpload(ctx context.Context, q *sqlc.Queries, sessionID, assetID uuid.UUID, forUpdate bool) (sqlc.Asset, error) {
	sess, err := loadSession(ctx, q, sessionID, false)
	if err != nil {
		return sqlc.Asset{}, err
	}
	a, err := loadAsset(ctx, q, assetID, forUpdate)
	if err != nil {
		return a, err
	}
	if !inSession(a, sessionID) {
		return a, errkey.New(codes.NotFound, "assets.asset.not_in_session", "asset not found in this session")
	}
	if !s.sessionOpen(sess) {
		return a, errkey.New(codes.FailedPrecondition, "assets.session.closed", "the upload session is no longer open")
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
		return nil, errkey.Newf(codes.InvalidArgument, "assets.upload.bad_part_count", "count must be 1..%d", presignBatch)
	}
	var parts []*assetsv1.PresignedPart
	err = s.tx(ctx, func(q *sqlc.Queries) error {
		a, err := s.openForUpload(ctx, q, sid, aid, false)
		if err != nil {
			return err
		}
		if !uploadable(a) {
			return errkey.New(codes.FailedPrecondition, "assets.asset.not_uploadable", "the file is no longer uploadable")
		}
		if from > int(*a.PartCount) {
			return errkey.Newf(codes.InvalidArgument, "assets.upload.bad_from_part", "from_part is beyond the %d parts of this file", *a.PartCount)
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
	err = s.tx(ctx, func(q *sqlc.Queries) error {
		a, err := loadAsset(ctx, q, aid, true)
		if err != nil {
			return err
		}
		if !inSession(a, sid) {
			return errkey.New(codes.NotFound, "assets.asset.not_in_session", "asset not found in this session")
		}
		out = fileView(a)
		if uploadable(a) && a.S3UploadID != nil {
			return s.addUploadedParts(ctx, q, a, out)
		}
		return nil
	})
	if err != nil {
		return nil, internal(err)
	}
	return &assetsv1.GetUploadFileResponse{File: out}, nil
}

// addUploadedParts lists what S3 holds for a file and moves a PENDING file with parts to UPLOADING.
func (s *Service) addUploadedParts(ctx context.Context, q *sqlc.Queries, a sqlc.Asset, out *assetsv1.UploadFile) error {
	parts, err := s.blob.ListParts(ctx, a.ObjectKey, *a.S3UploadID)
	if err != nil && !errors.Is(err, blob.ErrNoSuchUpload) {
		return errkey.New(codes.Unavailable, "assets.storage.unavailable", "object storage unavailable")
	}
	for _, p := range parts {
		out.UploadedParts = append(out.UploadedParts, &assetsv1.UploadedPart{PartNumber: p.Number, Size: p.Size, Etag: etagOf(p.ETag)})
	}
	if len(parts) > 0 && a.Status == "PENDING" {
		if err := q.MarkAssetUploading(ctx, a.ID); err != nil {
			return err
		}
		out.Status = assetsv1.AssetStatus_ASSET_STATUS_UPLOADING
	}
	return nil
}
