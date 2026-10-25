// Package convert maps asset and upload rows to their protobuf messages.
package convert

import (
	"encoding/hex"

	"google.golang.org/protobuf/types/known/timestamppb"

	assetsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/assets/v1"
	"github.com/0furkancolak/payrudder/services/assets-svc/internal/policy"
	"github.com/0furkancolak/payrudder/services/assets-svc/internal/store"
	"github.com/0furkancolak/payrudder/services/assets-svc/internal/store/sqlc"
)

// Asset is the protobuf form of an asset row.
func Asset(a sqlc.Asset) *assetsv1.Asset {
	out := &assetsv1.Asset{
		Id: a.ID.String(), Purpose: policy.PurposeOf(a.Purpose), Filename: a.Filename, ContentType: a.ContentType, Status: Status(a.Status),
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

// Status is the protobuf form of an asset status.
func Status(s string) assetsv1.AssetStatus {
	switch s {
	case store.AssetPending:
		return assetsv1.AssetStatus_ASSET_STATUS_PENDING
	case store.AssetUploading:
		return assetsv1.AssetStatus_ASSET_STATUS_UPLOADING
	case store.AssetScanning:
		return assetsv1.AssetStatus_ASSET_STATUS_SCANNING
	case store.AssetReady:
		return assetsv1.AssetStatus_ASSET_STATUS_READY
	case store.AssetRejected:
		return assetsv1.AssetStatus_ASSET_STATUS_REJECTED
	case store.AssetAborted:
		return assetsv1.AssetStatus_ASSET_STATUS_ABORTED
	case store.AssetCorrupt:
		return assetsv1.AssetStatus_ASSET_STATUS_CORRUPT
	case store.AssetDeleted:
		return assetsv1.AssetStatus_ASSET_STATUS_DELETED
	}
	return assetsv1.AssetStatus_ASSET_STATUS_UNSPECIFIED
}

// UploadFile is the protobuf form of an asset of an upload session (without presigned or uploaded parts).
func UploadFile(a sqlc.Asset) *assetsv1.UploadFile {
	f := &assetsv1.UploadFile{AssetId: a.ID.String(), Filename: a.Filename, Status: Status(a.Status)}
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
