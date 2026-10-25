package httpapi

import (
	"context"
	"net/http"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	aggregatorv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/aggregator/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/period"
)

type fakeUsage struct {
	aggregatorv1.UnimplementedUsageQueryServiceServer
}

func (fakeUsage) GetCustomerUsage(_ context.Context, req *aggregatorv1.GetCustomerUsageRequest) (*aggregatorv1.GetCustomerUsageResponse, error) {
	if req.GetCustomerId() != "cust-uuid" {
		return nil, status.Error(codes.Internal, "gateway must pass the internal customer id")
	}
	return &aggregatorv1.GetCustomerUsageResponse{Usage: []*aggregatorv1.UsageAggregate{
		{CustomerId: "cust-uuid", MeterCode: "gb_hours", PeriodId: req.GetPeriodId(), Quantity: "1.500", QuantityScale: 3, EventCount: 3},
	}}, nil
}

func (f *fakePlans) ResolveCustomer(_ context.Context, req *plansv1.ResolveCustomerRequest) (*plansv1.ResolveCustomerResponse, error) {
	if req.GetExternalId() != "cus_1" {
		return nil, status.Error(codes.NotFound, "no customer")
	}
	return &plansv1.ResolveCustomerResponse{Customer: &plansv1.Customer{Id: "cust-uuid", ExternalId: "cus_1"}}, nil
}

func TestGetCustomerUsage(t *testing.T) {
	ts, _ := newTestServer(t)
	resp, m := do(t, ts, "GET", "/v1/customers/cus_1/usage?period=2026-11", "live-key", "")
	if resp.StatusCode != http.StatusOK || m["period_id"] != "2026-11" || m["customer_external_id"] != "cus_1" {
		t.Fatalf("usage: %d %v", resp.StatusCode, m)
	}
	row := m["usage"].([]any)[0].(map[string]any)
	if row["meter_code"] != "gb_hours" || row["quantity"] != "1.500" || row["event_count"] != float64(3) {
		t.Fatalf("row = %v", row)
	}

	// Without a period: the current one (UTC when the principal carries no time zone).
	_, m = do(t, ts, "GET", "/v1/customers/cus_1/usage", "live-key", "")
	if want := period.PeriodOf(time.Now(), time.UTC); m["period_id"] != want {
		t.Fatalf("default period = %v, want %s", m["period_id"], want)
	}
	if resp, m := do(t, ts, "GET", "/v1/customers/cus_1/usage?period=2026-13", "live-key", ""); resp.StatusCode != 400 || errCode(m) != "INVALID_ARGUMENT" {
		t.Fatalf("bad period: %d %v", resp.StatusCode, m)
	}
	if resp, m := do(t, ts, "GET", "/v1/customers/nobody/usage?period=2026-11", "live-key", ""); resp.StatusCode != 404 || errCode(m) != "NOT_FOUND" {
		t.Fatalf("unknown customer: %d %v", resp.StatusCode, m)
	}
	if resp, _ := do(t, ts, "GET", "/v1/customers/cus_1/usage", "", ""); resp.StatusCode != 401 {
		t.Fatalf("no key: %d", resp.StatusCode)
	}
}
