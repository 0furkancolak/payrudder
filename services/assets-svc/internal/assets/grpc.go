// Package assets registers the asset handlers of AssetsService: request to command or query, result to response.
package assets

import (
	"context"

	"google.golang.org/protobuf/types/known/timestamppb"

	assetsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/assets/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/services/assets-svc/internal/assets/command"
	"github.com/0furkancolak/payrudder/services/assets-svc/internal/assets/query"
	"github.com/0furkancolak/payrudder/services/assets-svc/internal/convert"
)

// Handler serves the asset methods of AssetsService.
type Handler struct {
	cmd *command.Commands
	qry *query.Queries
}

// New returns the asset handlers.
func New(cmd *command.Commands, qry *query.Queries) *Handler { return &Handler{cmd: cmd, qry: qry} }

// GetAsset implements assetsv1.AssetsServiceServer.
func (h *Handler) GetAsset(ctx context.Context, req *assetsv1.GetAssetRequest) (*assetsv1.GetAssetResponse, error) {
	id, err := errkey.ParseUUID("assets.request.bad_id", "asset_id", req.GetAssetId())
	if err != nil {
		return nil, err
	}
	a, err := h.qry.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	return &assetsv1.GetAssetResponse{Asset: a}, nil
}

// ListAssets implements assetsv1.AssetsServiceServer.
func (h *Handler) ListAssets(ctx context.Context, req *assetsv1.ListAssetsRequest) (*assetsv1.ListAssetsResponse, error) {
	return h.qry.List(ctx, req)
}

// GetDownloadUrl implements assetsv1.AssetsServiceServer.
func (h *Handler) GetDownloadUrl(ctx context.Context, req *assetsv1.GetDownloadUrlRequest) (*assetsv1.GetDownloadUrlResponse, error) {
	id, err := errkey.ParseUUID("assets.request.bad_id", "asset_id", req.GetAssetId())
	if err != nil {
		return nil, err
	}
	u, expires, err := h.qry.DownloadURL(ctx, id)
	if err != nil {
		return nil, err
	}
	return &assetsv1.GetDownloadUrlResponse{Url: u, ExpireTime: timestamppb.New(expires)}, nil
}

// DeleteAsset implements assetsv1.AssetsServiceServer.
func (h *Handler) DeleteAsset(ctx context.Context, req *assetsv1.DeleteAssetRequest) (*assetsv1.DeleteAssetResponse, error) {
	id, err := errkey.ParseUUID("assets.request.bad_id", "asset_id", req.GetAssetId())
	if err != nil {
		return nil, err
	}
	a, err := h.cmd.Delete(ctx, id)
	if err != nil {
		return nil, err
	}
	return &assetsv1.DeleteAssetResponse{Asset: convert.Asset(a)}, nil
}
