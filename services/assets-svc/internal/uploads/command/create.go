package command

import (
	"context"
	"encoding/hex"
	"fmt"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"

	assetsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/assets/v1"
	"github.com/0furkancolak/payrudder/pkg/blob"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/idgen"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/assets-svc/internal/apierr"
	"github.com/0furkancolak/payrudder/services/assets-svc/internal/policy"
	"github.com/0furkancolak/payrudder/services/assets-svc/internal/store"
	"github.com/0furkancolak/payrudder/services/assets-svc/internal/store/sqlc"
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
func (c *Commands) planFiles(req *assetsv1.CreateUploadSessionRequest) ([]*plannedFile, int64, error) {
	n := len(req.GetFiles())
	if n < 1 || n > maxFiles {
		return nil, 0, errkey.Newf(codes.InvalidArgument, "assets.session.bad_file_count", "a session takes 1..%d files, got %d", maxFiles, n)
	}
	files := make([]*plannedFile, n)
	var total int64
	for i, f := range req.GetFiles() {
		p, err := c.planFile(i, f)
		if err != nil {
			return nil, 0, err
		}
		total += p.size
		files[i] = p
	}
	return files, total, nil
}

func (c *Commands) planFile(i int, f *assetsv1.FileSpec) (*plannedFile, error) {
	name, err := policy.SanitizeFilename(f.GetFilename())
	if err != nil {
		return nil, apierr.Failure(codes.InvalidArgument, policy.InvalidFilename, fmt.Sprintf("files[%d].filename: unusable", i))
	}
	if f.GetSize() <= 0 {
		return nil, errkey.Newf(codes.InvalidArgument, "assets.upload.size_required", "files[%d].size is required for direct uploads", i)
	}
	ct, v := c.o.Policy.Check(f.GetPurpose(), f.GetContentType(), f.GetSize())
	if v != nil {
		v.Message = fmt.Sprintf("files[%d]: %s", i, v.Message)
		return nil, apierr.FromViolation(v)
	}
	p := &plannedFile{id: idgen.NewUUIDv7(), filename: name, contentType: ct, size: f.GetSize(), purpose: f.GetPurpose()}
	if h := f.GetSha256(); h != "" {
		if p.sha, err = hex.DecodeString(h); err != nil || len(p.sha) != sha256Len {
			return nil, errkey.Newf(codes.InvalidArgument, "assets.upload.bad_sha256", "files[%d].sha256 must be 64 hex characters", i)
		}
	}
	p.partSize = policy.PartSize(p.size)
	p.partCount = policy.PartCount(p.size, p.partSize)
	return p, nil
}

// startMultipart opens the S3 multipart upload of every file. On a failure it aborts the ones already open.
func (c *Commands) startMultipart(ctx context.Context, ti tenant.Info, files []*plannedFile) error {
	for _, p := range files {
		var err error
		p.uploadID, err = c.o.Blob.CreateMultipart(ctx, store.ObjectKey(ti.ID, p.id), blob.PutOptions{ContentType: p.contentType,
			Tags: map[string]string{"purpose": policy.PurposeName(p.purpose)}})
		if err != nil {
			c.abortMultipart(ctx, ti, files)
			return errkey.New(codes.Unavailable, "assets.storage.unavailable", "object storage unavailable")
		}
	}
	return nil
}

// abortMultipart aborts the open multipart uploads of files. A failure is logged: S3 keeps the parts until its
// lifecycle rule removes the incomplete upload.
func (c *Commands) abortMultipart(ctx context.Context, ti tenant.Info, files []*plannedFile) {
	actx := context.WithoutCancel(ctx)
	for _, p := range files {
		if p.uploadID == "" {
			continue
		}
		if err := c.o.Blob.AbortMultipart(actx, store.ObjectKey(ti.ID, p.id), p.uploadID); err != nil {
			c.log.WarnContext(ctx, "assets: could not abort a multipart upload", "asset_id", p.id, "error", err)
		}
	}
}

// recordSession stores the session and its assets. If a concurrent request with the same idempotency key won, it
// returns that session and aborts this request's multipart uploads.
func (c *Commands) recordSession(ctx context.Context, ti tenant.Info, key string, files []*plannedFile) (*assetsv1.UploadSession, error) {
	sessionID := idgen.NewUUIDv7()
	var idem *string
	if key != "" {
		idem = &key
	}
	var out *assetsv1.UploadSession
	err := c.o.DB.Tx(ctx, func(q *sqlc.Queries) error {
		inserted, err := q.InsertUploadSession(ctx, sqlc.InsertUploadSessionParams{ID: sessionID, IdempotencyKey: idem, ExpiresAt: c.o.Now().Add(c.o.SessionTTL)})
		if err != nil {
			return err
		}
		if inserted == 0 {
			id, err := q.GetSessionIDByIdempotencyKey(ctx, key)
			if err != nil {
				return err
			}
			c.abortMultipart(ctx, ti, files)
			out, err = c.o.Sessions.SessionView(ctx, q, id)
			return err
		}
		for _, p := range files {
			if err := q.InsertSessionAsset(ctx, newSessionAsset(ti, sessionID, p)); err != nil {
				return err
			}
		}
		out, err = c.o.Sessions.SessionView(ctx, q, sessionID)
		return err
	})
	return out, err
}

func newSessionAsset(ti tenant.Info, sessionID uuid.UUID, p *plannedFile) sqlc.InsertSessionAssetParams {
	return sqlc.InsertSessionAssetParams{
		ID: p.id, SessionID: &sessionID, Purpose: policy.PurposeName(p.purpose), Filename: p.filename, ContentType: p.contentType,
		DeclaredSize: &p.size, Sha256: p.sha, ObjectKey: store.ObjectKey(ti.ID, p.id), S3UploadID: &p.uploadID, PartSize: &p.partSize, PartCount: &p.partCount,
	}
}

// CreateSession validates the files, checks the quota, opens their multipart uploads and records the session. A retry
// with the same idempotency key gets the session the first call made.
func (c *Commands) CreateSession(ctx context.Context, req *assetsv1.CreateUploadSessionRequest) (*assetsv1.UploadSession, error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	files, total, err := c.planFiles(req)
	if err != nil {
		return nil, err
	}

	// An idempotent retry gets the same session.
	if key := req.GetIdempotencyKey(); key != "" {
		existing, err := c.o.Sessions.SessionByKey(ctx, key)
		if err != nil {
			return nil, apierr.Internal(ctx, err)
		}
		if existing != nil {
			return existing, nil
		}
	}

	// Quota is checked now (cheap early answer) and again, authoritatively, when each file completes.
	used, err := c.o.Usage.Used(ctx)
	if err != nil {
		return nil, apierr.Internal(ctx, err)
	}
	if used+total > c.o.Usage.Limit(ti) {
		return nil, apierr.Failure(codes.FailedPrecondition, policy.StorageQuotaExceeded, "the files do not fit in the tenant's storage quota")
	}

	// Start the S3 multipart uploads, then record the session; undo S3 if recording fails.
	if err := c.startMultipart(ctx, ti, files); err != nil {
		return nil, err
	}
	out, err := c.recordSession(ctx, ti, req.GetIdempotencyKey(), files)
	if err != nil {
		c.abortMultipart(ctx, ti, files)
		return nil, apierr.Internal(ctx, err)
	}
	return out, nil
}
