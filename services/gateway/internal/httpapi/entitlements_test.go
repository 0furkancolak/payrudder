package httpapi

import (
	"context"
	"net/http"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	limitsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/limits/v1"
)

type fakeLimits struct {
	limitsv1.UnimplementedLimitsServiceServer
}

func (fakeLimits) CheckEntitlement(_ context.Context, req *limitsv1.CheckEntitlementRequest) (*limitsv1.CheckEntitlementResponse, error) {
	if req.GetCustomerId() != "cust-uuid" {
		return nil, status.Error(codes.Internal, "gateway must pass the internal customer id")
	}
	if req.GetMeterCode() == "nope" {
		return nil, status.Error(codes.NotFound, "meter not found")
	}
	st := &limitsv1.LimitStatus{CustomerId: "cust-uuid", MeterCode: req.GetMeterCode(), PeriodId: "2026-11", Used: "10.500",
		Limit: "10.500", UsedUnits: 10500, LimitUnits: 10500, QuantityScale: 3, AsOf: timestamppb.New(timestamppb.Now().AsTime())}
	if req.GetMeterCode() == "free" {
		st = &limitsv1.LimitStatus{CustomerId: "cust-uuid", MeterCode: "free", PeriodId: "2026-11", Used: "3", Limit: "0", Unlimited: true, Allowed: true}
	}
	return &limitsv1.CheckEntitlementResponse{Status: st}, nil
}

func (fakeLimits) GetLimitStatus(context.Context, *limitsv1.GetLimitStatusRequest) (*limitsv1.GetLimitStatusResponse, error) {
	return &limitsv1.GetLimitStatusResponse{Statuses: []*limitsv1.LimitStatus{
		{CustomerId: "cust-uuid", MeterCode: "gb", PeriodId: "2026-11", Used: "1.000", Limit: "5.000", Allowed: true}}}, nil
}

func TestGetEntitlement(t *testing.T) {
	ts, _ := newTestServer(t)
	resp, m := do(t, ts, "GET", "/v1/customers/cus_1/entitlements/gb?period=2026-11", "live-key", "")
	if resp.StatusCode != http.StatusOK || m["used"] != "10.500" || m["limit"] != "10.500" || m["allowed"] != false ||
		m["unlimited"] != false || m["customer_external_id"] != "cus_1" || m["as_of"] == nil {
		t.Fatalf("entitlement: %d %v", resp.StatusCode, m)
	}
	// Unlimited: limit is null.
	_, m = do(t, ts, "GET", "/v1/customers/cus_1/entitlements/free", "live-key", "")
	if m["unlimited"] != true || m["limit"] != nil || m["allowed"] != true || m["as_of"] != nil {
		t.Fatalf("unlimited entitlement: %v", m)
	}
	if resp, m := do(t, ts, "GET", "/v1/customers/cus_1/entitlements/nope", "live-key", ""); resp.StatusCode != 404 || errCode(m) != "NOT_FOUND" {
		t.Fatalf("unknown meter: %d %v", resp.StatusCode, m)
	}
	if resp, m := do(t, ts, "GET", "/v1/customers/cus_1/entitlements/gb?period=bad", "live-key", ""); resp.StatusCode != 400 || errCode(m) != "INVALID_ARGUMENT" {
		t.Fatalf("bad period: %d %v", resp.StatusCode, m)
	}
	if resp, _ := do(t, ts, "GET", "/v1/customers/nobody/entitlements/gb", "live-key", ""); resp.StatusCode != 404 {
		t.Fatalf("unknown customer: %d", resp.StatusCode)
	}
	if resp, _ := do(t, ts, "GET", "/v1/customers/cus_1/entitlements/gb", "", ""); resp.StatusCode != 401 {
		t.Fatalf("no key: %d", resp.StatusCode)
	}
}

func TestListEntitlements(t *testing.T) {
	ts, _ := newTestServer(t)
	resp, m := do(t, ts, "GET", "/v1/customers/cus_1/entitlements", "live-key", "")
	items, _ := m["items"].([]any)
	if resp.StatusCode != http.StatusOK || len(items) != 1 || items[0].(map[string]any)["meter_code"] != "gb" {
		t.Fatalf("list: %d %v", resp.StatusCode, m)
	}
}
