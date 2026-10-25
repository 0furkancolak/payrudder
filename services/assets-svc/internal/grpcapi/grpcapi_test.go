package grpcapi

import (
	"context"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	assetsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/assets/v1"
	"github.com/0furkancolak/payrudder/services/assets-svc/internal/policy"
)

type noTenantStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s noTenantStream) Context() context.Context { return s.ctx }
func (noTenantStream) Recv() (*assetsv1.UploadAssetRequest, error) {
	return nil, status.Error(codes.Canceled, "no message")
}
func (noTenantStream) SendAndClose(*assetsv1.UploadAssetResponse) error { return nil }

// Every method of AssetsService must be served by a module, not by the Unimplemented fallback. A request that fails
// before it reaches the database (no tenant, malformed id) is enough to tell the two apart.
func TestEveryMethodIsServed(t *testing.T) {
	s := New(nil, nil, policy.New(nil), Config{})
	ctx := context.Background()
	calls := map[string]error{}
	_, calls["CreateUploadSession"] = s.CreateUploadSession(ctx, &assetsv1.CreateUploadSessionRequest{})
	_, calls["PresignParts"] = s.PresignParts(ctx, &assetsv1.PresignPartsRequest{})
	_, calls["GetUploadFile"] = s.GetUploadFile(ctx, &assetsv1.GetUploadFileRequest{})
	_, calls["CompleteFile"] = s.CompleteFile(ctx, &assetsv1.CompleteFileRequest{})
	_, calls["AbortUpload"] = s.AbortUpload(ctx, &assetsv1.AbortUploadRequest{})
	calls["UploadAsset"] = s.UploadAsset(noTenantStream{ctx: ctx})
	_, calls["GetAsset"] = s.GetAsset(ctx, &assetsv1.GetAssetRequest{})
	_, calls["ListAssets"] = s.ListAssets(ctx, &assetsv1.ListAssetsRequest{})
	_, calls["GetDownloadUrl"] = s.GetDownloadUrl(ctx, &assetsv1.GetDownloadUrlRequest{})
	_, calls["DeleteAsset"] = s.DeleteAsset(ctx, &assetsv1.DeleteAssetRequest{})
	for name, err := range calls {
		if err == nil || status.Code(err) == codes.Unimplemented {
			t.Errorf("%s: %v (not served by a module)", name, err)
		}
	}
	desc := assetsv1.AssetsService_ServiceDesc
	if n := len(desc.Methods) + len(desc.Streams); n != len(calls) {
		t.Errorf("the service has %d methods, this test covers %d", n, len(calls))
	}
}
