package httpapi

import (
	"context"
	"net/http"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
)

func (f *fakePlans) GetPlanVersion(_ context.Context, req *plansv1.GetPlanVersionRequest) (*plansv1.GetPlanVersionResponse, error) {
	if req.GetVersion() == 99 {
		return nil, status.Error(codes.NotFound, "no such plan version")
	}
	return &plansv1.GetPlanVersionResponse{PlanVersion: &plansv1.PlanVersion{PlanCode: req.GetPlanCode(), Version: req.GetVersion()}}, nil
}

// A published plan version never changes, so its answer may be kept for five minutes; the "not found" for a version that is not published yet may not: it
// would stay in the cache of whatever sits in front of the gateway after the version has been published.
func TestOnlyAFoundPlanVersionMayBeCached(t *testing.T) {
	ts, _ := newTestServer(t)
	resp, _ := do(t, ts, "GET", "/v1/plans/basic/versions/5", "live-key", "")
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Cache-Control") != "private, max-age=300" {
		t.Fatalf("a published version: %d, Cache-Control %q", resp.StatusCode, resp.Header.Get("Cache-Control"))
	}
	resp, _ = do(t, ts, "GET", "/v1/plans/basic/versions/99", "live-key", "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("an unpublished version: %d", resp.StatusCode)
	}
	if cc := resp.Header.Get("Cache-Control"); cc == "private, max-age=300" {
		t.Errorf("a 404 was marked cacheable for five minutes: %q", cc)
	}
}
