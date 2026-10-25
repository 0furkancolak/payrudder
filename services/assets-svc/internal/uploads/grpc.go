// Package uploads registers the upload handlers of AssetsService: request to command or query, result to response.
package uploads

import (
	"context"

	"github.com/google/uuid"
	"google.golang.org/grpc"

	assetsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/assets/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/services/assets-svc/internal/convert"
	"github.com/0furkancolak/payrudder/services/assets-svc/internal/uploads/command"
	"github.com/0furkancolak/payrudder/services/assets-svc/internal/uploads/query"
)

// Handler serves the upload methods of AssetsService.
type Handler struct {
	cmd *command.Commands
	qry *query.Queries
}

// New returns the upload handlers.
func New(cmd *command.Commands, qry *query.Queries) *Handler { return &Handler{cmd: cmd, qry: qry} }

const badID = "assets.request.bad_id"

// ids parses the session and asset ids of a request.
func ids(sessionID, assetID string) (sid, aid uuid.UUID, err error) {
	if sid, err = errkey.ParseUUID(badID, "session_id", sessionID); err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	if aid, err = errkey.ParseUUID(badID, "asset_id", assetID); err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	return sid, aid, nil
}

// CreateUploadSession implements assetsv1.AssetsServiceServer.
func (h *Handler) CreateUploadSession(ctx context.Context, req *assetsv1.CreateUploadSessionRequest) (*assetsv1.CreateUploadSessionResponse, error) {
	sess, err := h.cmd.CreateSession(ctx, req)
	if err != nil {
		return nil, err
	}
	return &assetsv1.CreateUploadSessionResponse{Session: sess}, nil
}

// PresignParts implements assetsv1.AssetsServiceServer.
func (h *Handler) PresignParts(ctx context.Context, req *assetsv1.PresignPartsRequest) (*assetsv1.PresignPartsResponse, error) {
	sid, aid, err := ids(req.GetSessionId(), req.GetAssetId())
	if err != nil {
		return nil, err
	}
	parts, err := h.qry.PresignParts(ctx, sid, aid, int(req.GetFromPart()), int(req.GetCount()))
	if err != nil {
		return nil, err
	}
	return &assetsv1.PresignPartsResponse{Parts: parts}, nil
}

// GetUploadFile implements assetsv1.AssetsServiceServer.
func (h *Handler) GetUploadFile(ctx context.Context, req *assetsv1.GetUploadFileRequest) (*assetsv1.GetUploadFileResponse, error) {
	sid, aid, err := ids(req.GetSessionId(), req.GetAssetId())
	if err != nil {
		return nil, err
	}
	f, err := h.qry.File(ctx, sid, aid)
	if err != nil {
		return nil, err
	}
	return &assetsv1.GetUploadFileResponse{File: f}, nil
}

// CompleteFile implements assetsv1.AssetsServiceServer.
func (h *Handler) CompleteFile(ctx context.Context, req *assetsv1.CompleteFileRequest) (*assetsv1.CompleteFileResponse, error) {
	sid, aid, err := ids(req.GetSessionId(), req.GetAssetId())
	if err != nil {
		return nil, err
	}
	a, err := h.cmd.CompleteFile(ctx, sid, aid, req.GetParts())
	if err != nil {
		return nil, err
	}
	return &assetsv1.CompleteFileResponse{Asset: convert.Asset(a)}, nil
}

// AbortUpload implements assetsv1.AssetsServiceServer.
func (h *Handler) AbortUpload(ctx context.Context, req *assetsv1.AbortUploadRequest) (*assetsv1.AbortUploadResponse, error) {
	sid, err := errkey.ParseUUID(badID, "session_id", req.GetSessionId())
	if err != nil {
		return nil, err
	}
	var aid *uuid.UUID
	if req.GetAssetId() != "" {
		id, err := errkey.ParseUUID(badID, "asset_id", req.GetAssetId())
		if err != nil {
			return nil, err
		}
		aid = &id
	}
	sess, err := h.cmd.Abort(ctx, sid, aid)
	if err != nil {
		return nil, err
	}
	return &assetsv1.AbortUploadResponse{Session: sess}, nil
}

// UploadAsset implements assetsv1.AssetsServiceServer.
func (h *Handler) UploadAsset(stream grpc.ClientStreamingServer[assetsv1.UploadAssetRequest, assetsv1.UploadAssetResponse]) error {
	return h.cmd.Upload(stream)
}
