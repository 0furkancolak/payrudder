package service

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"

	assetsv1 "github.com/0furkancolak/quotify/gen/go/quotify/assets/v1"
	"github.com/0furkancolak/quotify/pkg/blob"
	"github.com/0furkancolak/quotify/pkg/errkey"
	"github.com/0furkancolak/quotify/pkg/tenant"
	"github.com/0furkancolak/quotify/services/assets-svc/internal/policy"
	"github.com/0furkancolak/quotify/services/assets-svc/internal/store/sqlc"
)

// reject marks an asset REJECTED after the caller removed what S3 held.
func reject(ctx context.Context, q *sqlc.Queries, id uuid.UUID, reason string) (sqlc.Asset, error) {
	if err := q.RejectAsset(ctx, sqlc.RejectAssetParams{ID: id, Reason: reason}); err != nil {
		return sqlc.Asset{}, err
	}
	return loadAsset(ctx, q, id, false)
}

// finish turns a complete S3 object into a READY asset: quota update and the status change in the caller's transaction.
func (s *Service) finish(ctx context.Context, q *sqlc.Queries, ti tenant.Info, a sqlc.Asset, size int64, sha []byte) (sqlc.Asset, error) {
	if a.DeclaredSize != nil && size != *a.DeclaredSize {
		_ = s.blob.Delete(ctx, a.ObjectKey)
		return reject(ctx, q, a.ID, policy.SizeMismatch)
	}
	if err := q.EnsureStorageUsage(ctx); err != nil {
		return sqlc.Asset{}, err
	}
	reserved, err := q.ReserveStorage(ctx, sqlc.ReserveStorageParams{Size: size, Quota: s.quota(ti)})
	if err != nil {
		return sqlc.Asset{}, err
	}
	if reserved == 0 {
		_ = s.blob.Delete(ctx, a.ObjectKey)
		return reject(ctx, q, a.ID, policy.StorageQuotaExceeded)
	}
	if len(sha) == 0 {
		sha = nil
	}
	if err := q.MarkAssetReady(ctx, sqlc.MarkAssetReadyParams{ID: a.ID, SizeBytes: size, CompletedAt: s.now(), Sha256: sha}); err != nil {
		return sqlc.Asset{}, err
	}
	if a.SessionID != nil {
		if err := q.CompleteSessionIfDrained(ctx, *a.SessionID); err != nil {
			return sqlc.Asset{}, err
		}
	}
	return loadAsset(ctx, q, a.ID, false)
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
	var out sqlc.Asset
	err = s.tx(ctx, func(q *sqlc.Queries) error {
		var err error
		out, err = s.completeFile(ctx, q, ti, sid, aid, req.GetParts())
		return err
	})
	if err != nil {
		return nil, internal(err)
	}
	return &assetsv1.CompleteFileResponse{Asset: assetProto(out)}, nil
}

func (s *Service) completeFile(ctx context.Context, q *sqlc.Queries, ti tenant.Info, sid, aid uuid.UUID, given []*assetsv1.PartETag) (sqlc.Asset, error) {
	a, err := loadAsset(ctx, q, aid, true)
	if err != nil {
		return a, err
	}
	if !inSession(a, sid) {
		return a, errkey.New(codes.NotFound, "assets.asset.not_in_session", "asset not found in this session")
	}
	switch a.Status {
	case "READY", "REJECTED":
		return a, nil // completing twice returns the same asset
	case "PENDING", "UPLOADING":
	default:
		return a, errkey.Newf(codes.FailedPrecondition, "assets.asset.bad_status", "the file is %s", a.Status)
	}
	sess, err := loadSession(ctx, q, sid, false)
	if err != nil {
		return a, err
	}
	if !s.sessionOpen(sess) {
		return a, errkey.New(codes.FailedPrecondition, "assets.session.closed", "the upload session is no longer open")
	}

	parts, err := s.blob.ListParts(ctx, a.ObjectKey, *a.S3UploadID)
	if errors.Is(err, blob.ErrNoSuchUpload) {
		// A previous CompleteFile assembled the object and crashed before recording it: finish from the object.
		info, herr := s.blob.Head(ctx, a.ObjectKey)
		if herr != nil {
			return a, errkey.New(codes.FailedPrecondition, "assets.upload.multipart_gone", "the multipart upload no longer exists")
		}
		return s.finish(ctx, q, ti, a, info.Size, nil)
	}
	if err != nil {
		return a, errkey.New(codes.Unavailable, "assets.storage.unavailable", "object storage unavailable")
	}

	if reason := checkParts(a, given, parts); reason != "" {
		if err := s.blob.AbortMultipart(ctx, a.ObjectKey, *a.S3UploadID); err != nil {
			return a, errkey.New(codes.Unavailable, "assets.storage.unavailable", "object storage unavailable")
		}
		return reject(ctx, q, a.ID, reason)
	}
	if err := s.blob.CompleteMultipart(ctx, a.ObjectKey, *a.S3UploadID, parts); err != nil {
		if errors.Is(err, blob.ErrNoSuchUpload) {
			return a, errkey.New(codes.Aborted, "assets.upload.changed", "the upload changed concurrently; retry")
		}
		return a, errkey.New(codes.Unavailable, "assets.storage.unavailable", "object storage unavailable")
	}
	info, err := s.blob.Head(ctx, a.ObjectKey)
	if err != nil {
		return a, errkey.New(codes.Unavailable, "assets.storage.unavailable", "object storage unavailable")
	}
	return s.finish(ctx, q, ti, a, info.Size, nil)
}

// checkParts compares the parts the client lists with what S3 holds and returns the rejection reason, or "" when they
// agree. The sizes S3 reports decide, never what the client claims.
func checkParts(a sqlc.Asset, given []*assetsv1.PartETag, parts []blob.Part) string {
	etags := make(map[int32]string, len(given))
	for _, p := range given {
		etags[p.GetPartNumber()] = etagOf(p.GetEtag())
	}
	if len(etags) != len(given) || int32(len(parts)) != *a.PartCount || len(etags) != len(parts) { //nolint:gosec // ≤ 10 000
		return policy.PartMismatch
	}
	var sum int64
	for i, p := range parts {
		if p.Number != int32(i+1) || etags[p.Number] != etagOf(p.ETag) { //nolint:gosec // ≤ 10 000
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
	err = s.tx(ctx, func(q *sqlc.Queries) error {
		if _, err := loadSession(ctx, q, sid, true); err != nil {
			return err
		}
		if err := s.abortAssets(ctx, q, sid, aid); err != nil {
			return err
		}
		next := "COMPLETED"
		if aid == nil {
			next = "ABORTED"
		}
		if err := q.SettleSession(ctx, sqlc.SettleSessionParams{ID: sid, Status: next, Force: aid == nil}); err != nil {
			return err
		}
		out, err = s.sessionView(ctx, q, sid)
		return err
	})
	if err != nil {
		return nil, internal(err)
	}
	return &assetsv1.AbortUploadResponse{Session: out}, nil
}

// abortAssets aborts the uploadable assets of a session, or just the one asset when assetID is set.
func (s *Service) abortAssets(ctx context.Context, q *sqlc.Queries, sessionID uuid.UUID, assetID *uuid.UUID) error {
	as, err := q.LockUploadableSessionAssets(ctx, sqlc.LockUploadableSessionAssetsParams{SessionID: sessionID, AssetID: assetID})
	if err != nil {
		return err
	}
	if assetID != nil && len(as) == 0 {
		if _, err := loadAsset(ctx, q, *assetID, false); err != nil {
			return err
		}
	}
	for _, a := range as {
		if a.S3UploadID != nil {
			if err := s.blob.AbortMultipart(ctx, a.ObjectKey, *a.S3UploadID); err != nil {
				return errkey.New(codes.Unavailable, "assets.storage.unavailable", "object storage unavailable")
			}
		}
		if err := q.MarkAssetAborted(ctx, a.ID); err != nil {
			return err
		}
	}
	return nil
}

// GetAsset implements assetsv1.AssetsServiceServer.
func (s *Service) GetAsset(ctx context.Context, req *assetsv1.GetAssetRequest) (*assetsv1.GetAssetResponse, error) {
	id, err := parseID("asset_id", req.GetAssetId())
	if err != nil {
		return nil, err
	}
	a, err := s.readAsset(ctx, id)
	if err != nil {
		return nil, internal(err)
	}
	return &assetsv1.GetAssetResponse{Asset: assetProto(a)}, nil
}

func (s *Service) readAsset(ctx context.Context, id uuid.UUID) (sqlc.Asset, error) {
	var a sqlc.Asset
	err := s.tx(ctx, func(q *sqlc.Queries) error {
		var err error
		a, err = loadAsset(ctx, q, id, false)
		return err
	})
	return a, err
}

// ListAssets implements assetsv1.AssetsServiceServer.
func (s *Service) ListAssets(ctx context.Context, req *assetsv1.ListAssetsRequest) (*assetsv1.ListAssetsResponse, error) {
	size := int(req.GetPageSize())
	if size <= 0 {
		size = 100
	}
	if size > 500 {
		return nil, errkey.New(codes.InvalidArgument, "assets.list.bad_page_size", "page_size must be 1..500")
	}
	after := uuid.Nil
	if t := req.GetPageToken(); t != "" {
		var err error
		if after, err = uuid.Parse(t); err != nil {
			return nil, errkey.New(codes.InvalidArgument, "assets.list.bad_page_token", "page_token is invalid")
		}
	}
	var rows []sqlc.Asset
	err := s.tx(ctx, func(q *sqlc.Queries) error {
		var err error
		rows, err = q.ListReadyAssets(ctx, sqlc.ListReadyAssetsParams{Purpose: policy.PurposeName(req.GetPurpose()), After: after, PageSize: int32(size + 1)}) //nolint:gosec // ≤ 501
		return err
	})
	if err != nil {
		return nil, internal(err)
	}
	resp := &assetsv1.ListAssetsResponse{}
	for _, a := range rows {
		if len(resp.Assets) == size {
			resp.NextPageToken = resp.Assets[size-1].Id
			break
		}
		resp.Assets = append(resp.Assets, assetProto(a))
	}
	return resp, nil
}

// GetDownloadUrl implements assetsv1.AssetsServiceServer.
func (s *Service) GetDownloadUrl(ctx context.Context, req *assetsv1.GetDownloadUrlRequest) (*assetsv1.GetDownloadUrlResponse, error) {
	id, err := parseID("asset_id", req.GetAssetId())
	if err != nil {
		return nil, err
	}
	a, err := s.readAsset(ctx, id)
	if err != nil {
		return nil, internal(err)
	}
	if a.Status != "READY" {
		return nil, errkey.Newf(codes.FailedPrecondition, "assets.download.not_ready", "the asset is %s, only READY assets can be downloaded", a.Status)
	}
	u, err := s.blob.PresignDownload(ctx, a.ObjectKey, a.Filename, s.cfg.DownloadTTL)
	if err != nil {
		return nil, errkey.New(codes.Unavailable, "assets.download.sign_failed", "could not sign the download link")
	}
	return &assetsv1.GetDownloadUrlResponse{Url: u, ExpireTime: timestamppb.New(s.now().Add(s.cfg.DownloadTTL))}, nil
}

// DeleteAsset implements assetsv1.AssetsServiceServer.
func (s *Service) DeleteAsset(ctx context.Context, req *assetsv1.DeleteAssetRequest) (*assetsv1.DeleteAssetResponse, error) {
	id, err := parseID("asset_id", req.GetAssetId())
	if err != nil {
		return nil, err
	}
	var out sqlc.Asset
	err = s.tx(ctx, func(q *sqlc.Queries) error {
		var err error
		out, err = s.deleteAsset(ctx, q, id)
		return err
	})
	if err != nil {
		return nil, internal(err)
	}
	return &assetsv1.DeleteAssetResponse{Asset: assetProto(out)}, nil
}

func (s *Service) deleteAsset(ctx context.Context, q *sqlc.Queries, id uuid.UUID) (sqlc.Asset, error) {
	a, err := loadAsset(ctx, q, id, true)
	if err != nil {
		return a, err
	}
	if a.Status == "DELETED" {
		return a, nil
	}
	if a.Status != "READY" && a.Status != "CORRUPT" {
		return a, errkey.Newf(codes.FailedPrecondition, "assets.asset.cannot_abort", "the asset is %s; abort the upload instead", a.Status)
	}
	if err := s.blob.Delete(ctx, a.ObjectKey); err != nil {
		return a, errkey.New(codes.Unavailable, "assets.storage.unavailable", "object storage unavailable")
	}
	if a.SizeBytes != nil {
		if err := q.ReleaseStorage(ctx, *a.SizeBytes); err != nil {
			return a, err
		}
	}
	if err := q.MarkAssetDeleted(ctx, id); err != nil {
		return a, err
	}
	return loadAsset(ctx, q, id, false)
}
