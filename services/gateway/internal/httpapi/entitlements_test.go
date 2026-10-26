package httpapi

import (
	"context"
	"net/http"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	limitsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/limits/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
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
		Limit: "10.500", UsedUnits: 10500, LimitUnits: 10500, QuantityScale: 3, AsOf: timestamppb.New(timestamppb.Now().AsTime()),
		Window: "day", WindowId: "2026-11-05", ResetsAt: timestamppb.New(time.Date(2026, 11, 5, 21, 0, 0, 0, time.UTC)), Mode: "hard", OverLimit: true, Overridden: true}
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
	if m["window"] != "day" || m["window_id"] != "2026-11-05" || m["resets_at"] != "2026-11-05T21:00:00Z" || m["mode"] != "hard" || m["over_limit"] != true || m["overridden"] != true {
		t.Fatalf("quota fields: %v", m)
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

func (f *fakePlans) SetEntitlementOverride(_ context.Context, req *plansv1.SetEntitlementOverrideRequest) (*plansv1.SetEntitlementOverrideResponse, error) {
	if req.GetCustomerExternalId() != "cus_1" {
		return nil, status.Error(codes.NotFound, "customer not found")
	}
	return &plansv1.SetEntitlementOverrideResponse{Override: &plansv1.EntitlementOverride{CustomerExternalId: req.GetCustomerExternalId(), MeterCode: req.GetMeterCode(),
		Limit: req.GetLimit(), AlertThresholdsPct: req.GetAlertThresholdsPct(), Window: req.GetWindow(), Mode: req.GetMode()}}, nil
}

func (f *fakePlans) ListEntitlementOverrides(_ context.Context, req *plansv1.ListEntitlementOverridesRequest) (*plansv1.ListEntitlementOverridesResponse, error) {
	return &plansv1.ListEntitlementOverridesResponse{Overrides: []*plansv1.EntitlementOverride{{CustomerExternalId: req.GetCustomerExternalId(), MeterCode: "gb", Limit: "5"}}}, nil
}

func (f *fakePlans) DeleteEntitlementOverride(_ context.Context, req *plansv1.DeleteEntitlementOverrideRequest) (*plansv1.DeleteEntitlementOverrideResponse, error) {
	if req.GetMeterCode() != "gb" {
		return nil, status.Error(codes.NotFound, "no override")
	}
	return &plansv1.DeleteEntitlementOverrideResponse{}, nil
}

func TestEntitlementOverrides(t *testing.T) {
	ts, _ := newTestServer(t)
	resp, m := do(t, ts, "PUT", "/v1/customers/cus_1/entitlement-overrides/gb", "live-key",
		`{"limit":"20","alert_thresholds_pct":[50,100],"window":"QUOTA_WINDOW_WEEK","mode":"QUOTA_MODE_SOFT","customer_external_id":"someone_else"}`)
	if resp.StatusCode != http.StatusOK || m["customer_external_id"] != "cus_1" || m["meter_code"] != "gb" || m["window"] != "QUOTA_WINDOW_WEEK" || m["mode"] != "QUOTA_MODE_SOFT" || m["limit"] != "20" {
		t.Fatalf("set: %d %v", resp.StatusCode, m)
	}
	if resp, m := do(t, ts, "PUT", "/v1/customers/cus_1/entitlement-overrides/gb", "live-key", `{"window":"QUOTA_WINDOW_YEAR"}`); resp.StatusCode != 400 || errCode(m) != "INVALID_ARGUMENT" {
		t.Fatalf("unknown window: %d %v", resp.StatusCode, m)
	}
	resp, m = do(t, ts, "GET", "/v1/customers/cus_1/entitlement-overrides", "live-key", "")
	if items, _ := m["overrides"].([]any); resp.StatusCode != http.StatusOK || len(items) != 1 {
		t.Fatalf("list: %d %v", resp.StatusCode, m)
	}
	if resp, _ := do(t, ts, "DELETE", "/v1/customers/cus_1/entitlement-overrides/gb", "live-key", ""); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: %d", resp.StatusCode)
	}
	if resp, _ := do(t, ts, "DELETE", "/v1/customers/cus_1/entitlement-overrides/other", "live-key", ""); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("delete of none: %d", resp.StatusCode)
	}
}
