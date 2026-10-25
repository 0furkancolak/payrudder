package httpapi

import (
	"context"
	"net/http"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	analyticsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/analytics/v1"
)

type fakeAnalytics struct {
	analyticsv1.UnimplementedAnalyticsServiceServer
}

func (fakeAnalytics) QueryUsage(_ context.Context, req *analyticsv1.QueryUsageRequest) (*analyticsv1.QueryUsageResponse, error) {
	if req.GetCustomerId() != "" && req.GetCustomerId() != "cust-uuid" {
		return nil, status.Error(codes.Internal, "gateway must pass the internal customer id")
	}
	prec := "estimated"
	if req.GetExact() {
		prec = "exact"
	}
	return &analyticsv1.QueryUsageResponse{Precision: prec, QuantityScale: 3, Points: []*analyticsv1.UsagePoint{
		{Start: timestamppb.New(req.GetFrom().AsTime()), Quantity: "1.500", EventCount: 3}}}, nil
}

func (fakeAnalytics) TopCustomers(_ context.Context, req *analyticsv1.TopCustomersRequest) (*analyticsv1.TopCustomersResponse, error) {
	return &analyticsv1.TopCustomersResponse{Precision: "exact", Customers: []*analyticsv1.CustomerUsage{{CustomerId: "c1", CustomerExternalId: "cus_1", Quantity: "9.000", EventCount: 9}}}, nil
}

func (fakeAnalytics) GetIngestStats(_ context.Context, req *analyticsv1.GetIngestStatsRequest) (*analyticsv1.GetIngestStatsResponse, error) {
	return &analyticsv1.GetIngestStatsResponse{Tenants: []*analyticsv1.TenantIngestStats{{TenantId: req.GetTenantId(), Events: 10, EventsPerSecond: 2}}}, nil
}

func TestAnalyticsUsage(t *testing.T) {
	ts, _ := newTestServer(t)
	path := "/v1/analytics/usage?meter=gb&from=2026-10-01T00:00:00Z&to=2026-10-02T00:00:00Z&granularity=day&customer=cus_1"
	resp, m := do(t, ts, "GET", path, "live-key", "")
	pts, _ := m["points"].([]any)
	if resp.StatusCode != http.StatusOK || m["precision"] != "estimated" || len(pts) != 1 || pts[0].(map[string]any)["quantity"] != "1.500" {
		t.Fatalf("usage: %d %v", resp.StatusCode, m)
	}
	if _, m := do(t, ts, "GET", path+"&exact=true", "live-key", ""); m["precision"] != "exact" {
		t.Fatalf("exact: %v", m)
	}
	for name, p := range map[string]string{
		"no meter":     "/v1/analytics/usage?from=2026-10-01T00:00:00Z&to=2026-10-02T00:00:00Z",
		"no range":     "/v1/analytics/usage?meter=gb",
		"bad gran":     "/v1/analytics/usage?meter=gb&from=2026-10-01T00:00:00Z&to=2026-10-02T00:00:00Z&granularity=week",
		"bad exact":    "/v1/analytics/usage?meter=gb&from=2026-10-01T00:00:00Z&to=2026-10-02T00:00:00Z&exact=maybe",
		"top no meter": "/v1/analytics/top-customers?period=2026-10",
		"top bad per":  "/v1/analytics/top-customers?meter=gb&period=2026-13",
	} {
		if resp, m := do(t, ts, "GET", p, "live-key", ""); resp.StatusCode != 400 || errCode(m) != "INVALID_ARGUMENT" {
			t.Errorf("%s: %d %v", name, resp.StatusCode, m)
		}
	}
	if resp, _ := do(t, ts, "GET", "/v1/analytics/usage?meter=gb&from=2026-10-01T00:00:00Z&to=2026-10-02T00:00:00Z&customer=nobody", "live-key", ""); resp.StatusCode != 404 {
		t.Fatalf("unknown customer: %d", resp.StatusCode)
	}
	resp, m = do(t, ts, "GET", "/v1/analytics/top-customers?meter=gb&period=2026-10&limit=5", "live-key", "")
	if resp.StatusCode != 200 || len(m["customers"].([]any)) != 1 {
		t.Fatalf("top: %d %v", resp.StatusCode, m)
	}
	if resp, _ := do(t, ts, "GET", "/admin/v1/analytics/ingest-stats", "live-key", ""); resp.StatusCode != 401 {
		t.Fatalf("tenant key on the operator route: %d", resp.StatusCode)
	}
	resp, m = do(t, ts, "GET", "/admin/v1/analytics/ingest-stats?window_seconds=60", operatorToken, "")
	if resp.StatusCode != 200 || len(m["tenants"].([]any)) != 1 {
		t.Fatalf("stats: %d %v", resp.StatusCode, m)
	}
}
