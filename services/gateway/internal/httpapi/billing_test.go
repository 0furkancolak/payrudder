package httpapi

import (
	"context"
	"net/http"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	billingv1 "github.com/0furkancolak/quotify/gen/go/quotify/billing/v1"
)

type fakeBilling struct {
	billingv1.UnimplementedBillingServiceServer
	billingv1.UnimplementedSandboxServiceServer
	billingv1.UnimplementedBillingAdminServiceServer
}

func (fakeBilling) ListInvoices(_ context.Context, req *billingv1.ListInvoicesRequest) (*billingv1.ListInvoicesResponse, error) {
	if req.GetCustomerId() != "" && req.GetCustomerId() != "cust-uuid" {
		return nil, status.Error(codes.Internal, "gateway must pass the internal customer id")
	}
	return &billingv1.ListInvoicesResponse{Invoices: []*billingv1.Invoice{{Id: "inv-1", Number: "INV-2026-000001", TotalMinor: 1116, Status: "paid", PeriodId: req.GetPeriodId()}}}, nil
}

func (fakeBilling) GetInvoice(_ context.Context, req *billingv1.GetInvoiceRequest) (*billingv1.GetInvoiceResponse, error) {
	if req.GetInvoiceId() == "missing" {
		return nil, status.Error(codes.NotFound, "invoice not found")
	}
	return &billingv1.GetInvoiceResponse{Invoice: &billingv1.Invoice{Id: req.GetInvoiceId(), Number: "INV-2026-000001",
		Lines: []*billingv1.InvoiceLine{{LineNo: 1, Kind: "BASE_FEE", AmountMinor: 1000, Description: "Base fee"}}}}, nil
}

func (fakeBilling) GetInvoicePdfUrl(_ context.Context, req *billingv1.GetInvoicePdfUrlRequest) (*billingv1.GetInvoicePdfUrlResponse, error) {
	if req.GetInvoiceId() == "norender" {
		return nil, status.Error(codes.FailedPrecondition, "the invoice PDF is not rendered yet")
	}
	return &billingv1.GetInvoicePdfUrlResponse{Url: "http://minio.local/invoices/x.pdf?sig=1", ExpireTime: timestamppb.New(time.Date(2026, 11, 1, 0, 5, 0, 0, time.UTC))}, nil
}

func (fakeBilling) ClosePeriodNow(_ context.Context, req *billingv1.ClosePeriodNowRequest) (*billingv1.ClosePeriodNowResponse, error) {
	return &billingv1.ClosePeriodNowResponse{Run: &billingv1.BillingRun{PeriodId: req.GetPeriodId(), Status: billingv1.RunStatus_RUN_STATUS_SCHEDULED}}, nil
}

func (fakeBilling) ClosePeriod(_ context.Context, req *billingv1.ClosePeriodRequest) (*billingv1.ClosePeriodResponse, error) {
	return &billingv1.ClosePeriodResponse{Run: &billingv1.BillingRun{TenantId: req.GetTenantId(), PeriodId: req.GetPeriodId()}}, nil
}

func (fakeBilling) GetBillingRun(_ context.Context, req *billingv1.GetBillingRunRequest) (*billingv1.GetBillingRunResponse, error) {
	return &billingv1.GetBillingRunResponse{Run: &billingv1.BillingRun{TenantId: req.GetTenantId(), PeriodId: req.GetPeriodId(), Status: billingv1.RunStatus_RUN_STATUS_COMPLETED}}, nil
}

func (fakeBilling) RetryInvoiceSaga(_ context.Context, req *billingv1.RetryInvoiceSagaRequest) (*billingv1.RetryInvoiceSagaResponse, error) {
	return &billingv1.RetryInvoiceSagaResponse{Saga: &billingv1.InvoiceSaga{InvoiceId: req.GetInvoiceId(), State: "PDF_REQUESTED"}}, nil
}

func (fakeBilling) VoidInvoice(_ context.Context, req *billingv1.VoidInvoiceRequest) (*billingv1.VoidInvoiceResponse, error) {
	return &billingv1.VoidInvoiceResponse{Saga: &billingv1.InvoiceSaga{InvoiceId: req.GetInvoiceId(), State: "VOIDED"}}, nil
}

func TestInvoices(t *testing.T) {
	ts, _ := newTestServer(t)
	resp, m := do(t, ts, "GET", "/v1/invoices?customer=cus_1&period=2026-10", "live-key", "")
	items, _ := m["invoices"].([]any)
	if resp.StatusCode != http.StatusOK || len(items) != 1 || items[0].(map[string]any)["number"] != "INV-2026-000001" {
		t.Fatalf("list: %d %v", resp.StatusCode, m)
	}
	if resp, m := do(t, ts, "GET", "/v1/invoices?customer=nobody", "live-key", ""); resp.StatusCode != 404 {
		t.Fatalf("unknown customer: %d %v", resp.StatusCode, m)
	}
	if resp, m := do(t, ts, "GET", "/v1/invoices?page_size=x", "live-key", ""); resp.StatusCode != 400 {
		t.Fatalf("bad page size: %d %v", resp.StatusCode, m)
	}
	resp, m = do(t, ts, "GET", "/v1/invoices/inv-9", "live-key", "")
	if resp.StatusCode != 200 || m["id"] != "inv-9" || len(m["lines"].([]any)) != 1 {
		t.Fatalf("get: %d %v", resp.StatusCode, m)
	}
	if resp, _ := do(t, ts, "GET", "/v1/invoices/missing", "live-key", ""); resp.StatusCode != 404 {
		t.Fatalf("missing: %d", resp.StatusCode)
	}
}

func TestInvoicePDF(t *testing.T) {
	ts, _ := newTestServer(t)
	resp, m := do(t, ts, "GET", "/v1/invoices/inv-1/pdf", "live-key", "")
	if resp.StatusCode != 200 || m["url"] == "" || m["expires_at"] != "2026-11-01T00:05:00Z" {
		t.Fatalf("pdf: %d %v", resp.StatusCode, m)
	}
	if resp, m := do(t, ts, "GET", "/v1/invoices/norender/pdf", "live-key", ""); resp.StatusCode != 409 || errCode(m) != "FAILED_PRECONDITION" {
		t.Fatalf("not rendered: %d %v", resp.StatusCode, m)
	}
}

func TestSandboxClosePeriod(t *testing.T) {
	ts, _ := newTestServer(t)
	if resp, _ := do(t, ts, "POST", "/v1/sandbox/close-period", "live-key", ""); resp.StatusCode != 403 {
		t.Fatalf("live key: %d, want 403", resp.StatusCode)
	}
	resp, m := do(t, ts, "POST", "/v1/sandbox/close-period", "sandbox-key", `{"period":"2026-11"}`)
	if resp.StatusCode != http.StatusAccepted || m["period_id"] != "2026-11" {
		t.Fatalf("sandbox close: %d %v", resp.StatusCode, m)
	}
	if resp, m := do(t, ts, "POST", "/v1/sandbox/close-period", "sandbox-key", `{"period":"nope"}`); resp.StatusCode != 400 {
		t.Fatalf("bad period: %d %v", resp.StatusCode, m)
	}
}

func TestAdminBilling(t *testing.T) {
	ts, _ := newTestServer(t)
	if resp, _ := do(t, ts, "POST", "/admin/v1/billing/close-period", "live-key", `{}`); resp.StatusCode != 401 {
		t.Fatalf("tenant key on an admin route: %d, want 401", resp.StatusCode)
	}
	resp, m := do(t, ts, "POST", "/admin/v1/billing/close-period", operatorToken, `{"tenant_id":"t1","period_id":"2026-10"}`)
	if resp.StatusCode != http.StatusAccepted || m["period_id"] != "2026-10" {
		t.Fatalf("close: %d %v", resp.StatusCode, m)
	}
	if resp, m := do(t, ts, "GET", "/admin/v1/tenants/t1/billing-runs/2026-10", operatorToken, ""); resp.StatusCode != 200 || m["status"] != "RUN_STATUS_COMPLETED" {
		t.Fatalf("run: %d %v", resp.StatusCode, m)
	}
	if resp, m := do(t, ts, "POST", "/admin/v1/invoices/i1/retry", operatorToken, `{"tenant_id":"t1"}`); resp.StatusCode != 200 || m["state"] != "PDF_REQUESTED" {
		t.Fatalf("retry: %d %v", resp.StatusCode, m)
	}
	if resp, m := do(t, ts, "POST", "/admin/v1/invoices/i1/void", operatorToken, `{"tenant_id":"t1","reason":"x"}`); resp.StatusCode != 200 || m["state"] != "VOIDED" {
		t.Fatalf("void: %d %v", resp.StatusCode, m)
	}
}
