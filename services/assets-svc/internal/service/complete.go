package service

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	assetsv1 "github.com/0furkancolak/quotify/gen/go/quotify/assets/v1"
	"github.com/0furkancolak/quotify/pkg/blob"
	"github.com/0furkancolak/quotify/pkg/tenant"
	"github.com/0furkancolak/quotify/services/assets-svc/internal/policy"
)

// reject marks an asset REJECTED after the caller removed what S3 held.
func reject(ctx context.Context, tx pgx.Tx, id uuid.UUID, reason string) (*asset, error) {
	if _, err := tx.Exec(ctx, `UPDATE assets SET status = 'REJECTED', reject_reason = $2, s3_upload_id = NULL WHERE id = $1`, id, reason); err != nil {
		return nil, err
	}
	return loadAsset(ctx, tx, id, false)
}

// finish turns a complete S3 object into a READY asset: quota update and the status change in the caller's transaction.
func (s *Service) finish(ctx context.Context, tx pgx.Tx, ti tenant.Info, a *asset, size int64, sha []byte) (*asset, error) {
	if a.DeclaredSize != nil && size != *a.DeclaredSize {
		_ = s.blob.Delete(ctx, a.ObjectKey)
		return reject(ctx, tx, a.ID, policy.SizeMismatch)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO storage_usage (tenant_id, bytes_used) VALUES (app_tenant_id(), 0) ON CONFLICT DO NOTHING`); err != nil {
		return nil, err
	}
	tag, err := tx.Exec(ctx, `UPDATE storage_usage SET bytes_used = bytes_used + $1 WHERE tenant_id = app_tenant_id() AND bytes_used + $1 <= $2`, size, s.quota(ti))
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		_ = s.blob.Delete(ctx, a.ObjectKey)
		return reject(ctx, tx, a.ID, policy.StorageQuotaExceeded)
	}
	var shaArg any
	if len(sha) > 0 {
		shaArg = sha
	}
	if _, err := tx.Exec(ctx, `UPDATE assets SET status = 'READY', size_bytes = $2, completed_at = $3, s3_upload_id = NULL, sha256 = COALESCE($4, sha256) WHERE id = $1`,
		a.ID, size, s.now(), shaArg); err != nil {
		return nil, err
	}
	if a.SessionID != nil {
		if _, err := tx.Exec(ctx, `UPDATE upload_sessions SET status = 'COMPLETED' WHERE id = $1 AND status = 'OPEN'
			AND NOT EXISTS (SELECT 1 FROM assets WHERE session_id = $1 AND status IN ('PENDING','UPLOADING'))`, *a.SessionID); err != nil {
			return nil, err
		}
	}
	return loadAsset(ctx, tx, a.ID, false)
}

// CompleteFile implements assetsv1.AssetsServiceServer. The asset row stays locked from the first check to the last
// write, so concurrent calls for one file queue up and the second one finds a READY asset.
func (s *Service) CompleteFile(ctx context.Context, req *assetsv1.CompleteFileRequest) (*assetsv1.CompleteFileResponse, error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	sid, err := parseID("session_id", req.GetSessionId())
	if err != nil {
		return nil, err
	}
	aid, err := parseID("asset_id", req.GetAssetId())
	if err != nil {
		return nil, err
	}
	var out *asset
	err = s.tx(ctx, func(tx pgx.Tx) error {
		a, err := loadAsset(ctx, tx, aid, true)
		if err != nil {
			return err
		}
		if a.SessionID == nil || *a.SessionID != sid {
			return status.Error(codes.NotFound, "asset not found in this session")
		}
		switch a.Status {
		case "READY", "REJECTED":
			out = a // completing twice returns the same asset
			return nil
		case "PENDING", "UPLOADING":
		default:
			return status.Errorf(codes.FailedPrecondition, "the file is %s", a.Status)
		}
		sess, err := loadSession(ctx, tx, sid, false)
		if err != nil {
			return err
		}
		if sess.Status != "OPEN" || !sess.ExpiresAt.After(s.now()) {
			return status.Error(codes.FailedPrecondition, "the upload session is no longer open")
		}

		parts, err := s.blob.ListParts(ctx, a.ObjectKey, *a.UploadID)
		if errors.Is(err, blob.ErrNoSuchUpload) {
			// A previous CompleteFile assembled the object and crashed before recording it: finish from the object.
			info, herr := s.blob.Head(ctx, a.ObjectKey)
			if herr != nil {
				return status.Error(codes.FailedPrecondition, "the multipart upload no longer exists")
			}
			out, err = s.finish(ctx, tx, ti, a, info.Size, nil)
			return err
		}
		if err != nil {
			return status.Error(codes.Unavailable, "object storage unavailable")
		}

		rejectUpload := func(reason string) error {
			if err := s.blob.AbortMultipart(ctx, a.ObjectKey, *a.UploadID); err != nil {
				return status.Error(codes.Unavailable, "object storage unavailable")
			}
			out, err = reject(ctx, tx, a.ID, reason)
			return err
		}
		given := make(map[int32]string, len(req.GetParts()))
		for _, p := range req.GetParts() {
			given[p.GetPartNumber()] = etagOf(p.GetEtag())
		}
		if len(given) != len(req.GetParts()) || int32(len(parts)) != *a.PartCount || len(given) != len(parts) { //nolint:gosec // ≤ 10 000
			return rejectUpload(policy.PartMismatch)
		}
		var sum int64
		for i, p := range parts {
			if p.Number != int32(i+1) || given[p.Number] != etagOf(p.ETag) { //nolint:gosec // ≤ 10 000
				return rejectUpload(policy.PartMismatch)
			}
			if i < len(parts)-1 && p.Size != *a.PartSize {
				return rejectUpload(policy.SizeMismatch)
			}
			sum += p.Size
		}
		// The sizes S3 reports decide, never what the client claims.
		if a.DeclaredSize != nil && sum != *a.DeclaredSize {
			return rejectUpload(policy.SizeMismatch)
		}
		if err := s.blob.CompleteMultipart(ctx, a.ObjectKey, *a.UploadID, parts); err != nil {
			if errors.Is(err, blob.ErrNoSuchUpload) {
				return status.Error(codes.Aborted, "the upload changed concurrently; retry")
			}
			return status.Error(codes.Unavailable, "object storage unavailable")
		}
		info, err := s.blob.Head(ctx, a.ObjectKey)
		if err != nil {
			return status.Error(codes.Unavailable, "object storage unavailable")
		}
		out, err = s.finish(ctx, tx, ti, a, info.Size, nil)
		return err
	})
	if err != nil {
		return nil, internal(err)
	}
	return &assetsv1.CompleteFileResponse{Asset: out.proto()}, nil
}

// AbortUpload implements assetsv1.AssetsServiceServer.
func (s *Service) AbortUpload(ctx context.Context, req *assetsv1.AbortUploadRequest) (*assetsv1.AbortUploadResponse, error) {
	sid, err := parseID("session_id", req.GetSessionId())
	if err != nil {
		return nil, err
	}
	var aid *uuid.UUID
	if req.GetAssetId() != "" {
		id, err := parseID("asset_id", req.GetAssetId())
		if err != nil {
			return nil, err
		}
		aid = &id
	}
	var out *assetsv1.UploadSession
	err = s.tx(ctx, func(tx pgx.Tx) error {
		if _, err := loadSession(ctx, tx, sid, true); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT `+assetColumns+` FROM assets WHERE session_id = $1 AND status IN ('PENDING','UPLOADING') AND ($2::uuid IS NULL OR id = $2) FOR UPDATE`, sid, aid)
		if err != nil {
			return err
		}
		var as []*asset
		for rows.Next() {
			a, err := scanAsset(rows)
			if err != nil {
				rows.Close()
				return err
			}
			as = append(as, a)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if aid != nil && len(as) == 0 {
			if _, err := loadAsset(ctx, tx, *aid, false); err != nil {
				return err
			}
		}
		for _, a := range as {
			if a.UploadID != nil {
				if err := s.blob.AbortMultipart(ctx, a.ObjectKey, *a.UploadID); err != nil {
					return status.Error(codes.Unavailable, "object storage unavailable")
				}
			}
			if _, err := tx.Exec(ctx, `UPDATE assets SET status = 'ABORTED', s3_upload_id = NULL WHERE id = $1`, a.ID); err != nil {
				return err
			}
		}
		next := "COMPLETED"
		if aid == nil {
			next = "ABORTED"
		}
		if _, err := tx.Exec(ctx, `UPDATE upload_sessions SET status = $2 WHERE id = $1 AND status = 'OPEN'
			AND ($3 OR NOT EXISTS (SELECT 1 FROM assets WHERE session_id = $1 AND status IN ('PENDING','UPLOADING')))`, sid, next, aid == nil); err != nil {
			return err
		}
		out, err = s.sessionView(ctx, tx, sid)
		return err
	})
	if err != nil {
		return nil, internal(err)
	}
	return &assetsv1.AbortUploadResponse{Session: out}, nil
}

// GetAsset implements assetsv1.AssetsServiceServer.
func (s *Service) GetAsset(ctx context.Context, req *assetsv1.GetAssetRequest) (*assetsv1.GetAssetResponse, error) {
	id, err := parseID("asset_id", req.GetAssetId())
	if err != nil {
		return nil, err
	}
	var a *asset
	if err := s.tx(ctx, func(tx pgx.Tx) error {
		var err error
		a, err = loadAsset(ctx, tx, id, false)
		return err
	}); err != nil {
		return nil, internal(err)
	}
	return &assetsv1.GetAssetResponse{Asset: a.proto()}, nil
}

// ListAssets implements assetsv1.AssetsServiceServer.
func (s *Service) ListAssets(ctx context.Context, req *assetsv1.ListAssetsRequest) (*assetsv1.ListAssetsResponse, error) {
	size := int(req.GetPageSize())
	if size <= 0 {
		size = 100
	}
	if size > 500 {
		return nil, status.Error(codes.InvalidArgument, "page_size must be 1..500")
	}
	after := uuid.Nil
	if t := req.GetPageToken(); t != "" {
		var err error
		if after, err = uuid.Parse(t); err != nil {
			return nil, status.Error(codes.InvalidArgument, "page_token is invalid")
		}
	}
	purpose := policy.PurposeName(req.GetPurpose())
	resp := &assetsv1.ListAssetsResponse{}
	err := s.tx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+assetColumns+` FROM assets WHERE status = 'READY' AND ($1 = '' OR purpose = $1) AND id > $2 ORDER BY id LIMIT $3`, purpose, after, size+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			a, err := scanAsset(rows)
			if err != nil {
				return err
			}
			if len(resp.Assets) == size {
				resp.NextPageToken = resp.Assets[size-1].Id
				break
			}
			resp.Assets = append(resp.Assets, a.proto())
		}
		return rows.Err()
	})
	if err != nil {
		return nil, internal(err)
	}
	return resp, nil
}

// GetDownloadUrl implements assetsv1.AssetsServiceServer.
func (s *Service) GetDownloadUrl(ctx context.Context, req *assetsv1.GetDownloadUrlRequest) (*assetsv1.GetDownloadUrlResponse, error) {
	id, err := parseID("asset_id", req.GetAssetId())
	if err != nil {
		return nil, err
	}
	var a *asset
	if err := s.tx(ctx, func(tx pgx.Tx) error {
		var err error
		a, err = loadAsset(ctx, tx, id, false)
		return err
	}); err != nil {
		return nil, internal(err)
	}
	if a.Status != "READY" {
		return nil, status.Errorf(codes.FailedPrecondition, "the asset is %s, only READY assets can be downloaded", a.Status)
	}
	u, err := s.blob.PresignDownload(ctx, a.ObjectKey, a.Filename, s.cfg.DownloadTTL)
	if err != nil {
		return nil, status.Error(codes.Unavailable, "could not sign the download link")
	}
	return &assetsv1.GetDownloadUrlResponse{Url: u, ExpireTime: timestamppb.New(s.now().Add(s.cfg.DownloadTTL))}, nil
}

// DeleteAsset implements assetsv1.AssetsServiceServer.
func (s *Service) DeleteAsset(ctx context.Context, req *assetsv1.DeleteAssetRequest) (*assetsv1.DeleteAssetResponse, error) {
	id, err := parseID("asset_id", req.GetAssetId())
	if err != nil {
		return nil, err
	}
	var out *asset
	err = s.tx(ctx, func(tx pgx.Tx) error {
		a, err := loadAsset(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if a.Status == "DELETED" {
			out = a
			return nil
		}
		if a.Status != "READY" && a.Status != "CORRUPT" {
			return status.Errorf(codes.FailedPrecondition, "the asset is %s; abort the upload instead", a.Status)
		}
		if err := s.blob.Delete(ctx, a.ObjectKey); err != nil {
			return status.Error(codes.Unavailable, "object storage unavailable")
		}
		if a.SizeBytes != nil {
			if _, err := tx.Exec(ctx, `UPDATE storage_usage SET bytes_used = GREATEST(bytes_used - $1, 0) WHERE tenant_id = app_tenant_id()`, *a.SizeBytes); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE assets SET status = 'DELETED' WHERE id = $1`, id); err != nil {
			return err
		}
		out, err = loadAsset(ctx, tx, id, false)
		return err
	})
	if err != nil {
		return nil, internal(err)
	}
	return &assetsv1.DeleteAssetResponse{Asset: out.proto()}, nil
}
