package apierr

import (
	"testing"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/services/assets-svc/internal/policy"
)

func TestFailureCarriesTheKeyAndKeepsTheReasonFirst(t *testing.T) {
	err := Failure(codes.FailedPrecondition, policy.StorageQuotaExceeded, "full")
	if got := errkey.Of(err); got != "assets.storage.quota_exceeded" {
		t.Fatalf("key = %q", got)
	}
	first, ok := status.Convert(err).Details()[0].(*errdetails.ErrorInfo)
	if !ok || first.GetReason() != policy.StorageQuotaExceeded {
		t.Fatalf("first detail = %v", status.Convert(err).Details()[0])
	}
	for reason, key := range violationKeys {
		if got := errkey.Of(Failure(codes.InvalidArgument, reason, "m")); got != key || key == "" {
			t.Errorf("%s: key %q", reason, got)
		}
	}
	if got := errkey.Of(Failure(codes.InvalidArgument, "SOMETHING_NEW", "m")); got != "common.invalid_argument" {
		t.Errorf("unknown reason: key %q", got)
	}
}
