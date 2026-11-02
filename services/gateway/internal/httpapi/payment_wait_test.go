package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
)

var since = time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)

func (fakeBilling) GetInvoicePaymentWait(_ context.Context, req *billingv1.GetInvoicePaymentWaitRequest) (*billingv1.GetInvoicePaymentWaitResponse, error) {
	switch req.GetInvoiceId() {
	case "held":
		return &billingv1.GetInvoicePaymentWaitResponse{Wait: &billingv1.PaymentWait{State: "PAYMENT_HELD", HoldReason: "customer_blocked", SinceTime: timestamppb.New(since), NextCheckTime: timestamppb.New(since.Add(time.Hour))}}, nil
	case "action":
		return &billingv1.GetInvoicePaymentWaitResponse{Wait: &billingv1.PaymentWait{State: "PAYMENT_ACTION_REQUIRED", SinceTime: timestamppb.New(since), NextCheckTime: timestamppb.New(since.Add(6 * time.Hour))}}, nil
	case "missing":
		return nil, status.Error(codes.NotFound, "invoice has no saga")
	}
	return &billingv1.GetInvoicePaymentWaitResponse{Wait: &billingv1.PaymentWait{State: "COMPLETED"}}, nil
}

func (fakeBilling) ReleaseHeldInvoice(_ context.Context, req *billingv1.ReleaseHeldInvoiceRequest) (*billingv1.ReleaseHeldInvoiceResponse, error) {
	if req.GetInvoiceId() != "held" {
		return nil, status.Error(codes.FailedPrecondition, "not held")
	}
	return &billingv1.ReleaseHeldInvoiceResponse{Saga: &billingv1.InvoiceSaga{InvoiceId: req.GetInvoiceId(), State: "PAYMENT_REQUESTED"}}, nil
}

func (fakeBilling) VoidHeldInvoice(_ context.Context, req *billingv1.VoidHeldInvoiceRequest) (*billingv1.VoidHeldInvoiceResponse, error) {
	if req.GetInvoiceId() != "held" {
		return nil, status.Error(codes.FailedPrecondition, "not held")
	}
	return &billingv1.VoidHeldInvoiceResponse{Saga: &billingv1.InvoiceSaga{InvoiceId: req.GetInvoiceId(), State: "VOIDED"}}, nil
}

// What a payment waits for is read with invoices:read; for a payment that waits for the customer the gateway adds the end of the window from payments-worker (asked as the
// caller's own tenant and for the invoice in the path), and leaves it out when payments-worker cannot be asked or has no charge.
func TestPaymentWaitIsReadWithTheWindowFromPaymentsWorker(t *testing.T) {
	var path string
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_, _ = io.WriteString(w, `{"found":true,"started_time":"2026-11-01T08:59:00Z","window_ends_time":"2026-11-15T08:59:00Z"}`)
	}))
	t.Cleanup(worker.Close)
	u, _ := url.Parse(worker.URL)
	ts, _, _, _ := newTestServerWith(t, func(d *Deps) { d.PaymentsURL, d.Log = u, slog.New(slog.NewTextHandler(io.Discard, nil)) })

	resp, m := do(t, ts, "GET", "/v1/invoices/held/payment-wait", "read-key", "")
	if resp.StatusCode != 200 || m["state"] != "PAYMENT_HELD" || m["hold_reason"] != "customer_blocked" || m["since_time"] != "2026-11-01T09:00:00Z" || m["next_check_time"] != "2026-11-01T10:00:00Z" || m["window_ends_time"] != nil {
		t.Fatalf("held: %d %v; want the reason and the two times, and no window", resp.StatusCode, m)
	}
	if path != "" {
		t.Errorf("payments-worker was asked (%s) about a held charge", path)
	}
	resp, m = do(t, ts, "GET", "/v1/invoices/action/payment-wait", "live-key", "")
	if resp.StatusCode != 200 || m["state"] != "PAYMENT_ACTION_REQUIRED" || m["window_ends_time"] != "2026-11-15T08:59:00Z" {
		t.Fatalf("action required: %d %v; want the end of the window", resp.StatusCode, m)
	}
	if want := "/admin/tenants/" + liveTenant.String() + "/invoices/action/charge-window"; path != want {
		t.Errorf("payments-worker was asked %q, want %q: the caller's tenant and the invoice of the path", path, want)
	}
	if resp, m := do(t, ts, "GET", "/v1/invoices/done/payment-wait", "live-key", ""); resp.StatusCode != 200 || m["state"] != "COMPLETED" || m["since_time"] != nil {
		t.Errorf("another state: %d %v; want the state alone", resp.StatusCode, m)
	}
	if resp, _ := do(t, ts, "GET", "/v1/invoices/missing/payment-wait", "live-key", ""); resp.StatusCode != 404 {
		t.Errorf("an invoice with no saga: %d, want 404", resp.StatusCode)
	}
	if resp, _ := do(t, ts, "GET", "/v1/invoices/held/payment-wait", "audit-key", ""); resp.StatusCode != 403 {
		t.Errorf("a key without invoices:read: %d, want 403", resp.StatusCode)
	}

	// payments-worker down: the rest is still true.
	worker.Close()
	resp, m = do(t, ts, "GET", "/v1/invoices/action/payment-wait", "live-key", "")
	if resp.StatusCode != 200 || m["state"] != "PAYMENT_ACTION_REQUIRED" || m["since_time"] == nil || m["window_ends_time"] != nil {
		t.Errorf("payments-worker down: %d %v; want the state and the times without a window", resp.StatusCode, m)
	}
}

// Releasing and voiding a held charge are writes of invoices:write; a viewer's session, a read-only key, and a tenant the operator froze are refused before billing is asked, and
// billing's refusal of an invoice that is not held is a 409.
func TestReleaseAndVoidAreWritesOfTheInvoicesPermission(t *testing.T) {
	ts, _, _, _ := newFullTestServer(t)
	for _, tc := range []struct{ path, state string }{{"/v1/invoices/held/release", "PAYMENT_REQUESTED"}, {"/v1/invoices/held/void", "VOIDED"}} {
		if resp, m := do(t, ts, "POST", tc.path, "live-key", ""); resp.StatusCode != 200 || m["state"] != tc.state || m["invoice_id"] != "held" {
			t.Errorf("%s: %d %v; want 200 %s", tc.path, resp.StatusCode, m, tc.state)
		}
		if resp, m := do(t, ts, "POST", tc.path, "billing-key", ""); resp.StatusCode != 200 {
			t.Errorf("%s with the billing role: %d %v", tc.path, resp.StatusCode, m)
		}
		for _, key := range []string{"read-key", "custom-key", "checkout-key"} {
			if resp, m := do(t, ts, "POST", tc.path, key, ""); resp.StatusCode != 403 || errKey(m) != "gateway.auth.permission_denied" {
				t.Errorf("%s with %s: %d %v; want 403 permission_denied", tc.path, key, resp.StatusCode, m)
			}
		}
		if resp, m := do(t, ts, "POST", tc.path, "frozen-key", ""); resp.StatusCode != 403 || errKey(m) != "gateway.auth.tenant_read_only" {
			t.Errorf("%s on a frozen tenant: %d %v; want 403 tenant_read_only", tc.path, resp.StatusCode, m)
		}
	}
	for _, path := range []string{"/v1/invoices/paid/release", "/v1/invoices/paid/void"} {
		if resp, m := do(t, ts, "POST", path, "live-key", ""); resp.StatusCode != 409 || errCode(m) != "FAILED_PRECONDITION" {
			t.Errorf("%s: %d %v; want 409 FAILED_PRECONDITION", path, resp.StatusCode, m)
		}
	}
}
