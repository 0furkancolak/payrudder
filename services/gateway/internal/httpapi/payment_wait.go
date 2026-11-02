package httpapi

import (
	"context"
	"net/http"
	"net/url"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
	"github.com/0furkancolak/payrudder/pkg/tenant"
)

// What an invoice's payment waits for, and the two things the seller can do about a first charge that is held (plans 13 and 14, 08).

// getInvoicePaymentWait is GET /v1/invoices/{id}/payment-wait. Billing says what the saga waits for; for a payment that waits for the customer the gateway adds when the window
// ends, which payments-worker owns. Without that answer the field is left out: the rest is still true.
func (s *Server) getInvoicePaymentWait(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Billing.GetInvoicePaymentWait(r.Context(), &billingv1.GetInvoicePaymentWaitRequest{InvoiceId: r.PathValue("id")})
	wait := resp.GetWait()
	if err == nil && wait.GetState() == "PAYMENT_ACTION_REQUIRED" {
		if end, ok := s.chargeWindowEnd(r.Context(), r.PathValue("id")); ok {
			wait.WindowEndsTime = timestamppb.New(end)
		}
	}
	respond(w, r, http.StatusOK, wait, err)
}

// chargeWindowEnd asks payments-worker when the newest charge of the invoice counts as declined if the customer has not confirmed it.
func (s *Server) chargeWindowEnd(ctx context.Context, invoiceID string) (time.Time, bool) {
	ti, ok := tenant.FromContext(ctx)
	if !ok {
		return time.Time{}, false
	}
	var out struct {
		Found      bool       `json:"found"`
		WindowEnds *time.Time `json:"window_ends_time"`
	}
	code, err := s.paymentsDo(ctx, http.MethodGet, "/admin/tenants/"+url.PathEscape(ti.ID.String())+"/invoices/"+url.PathEscape(invoiceID)+"/charge-window", nil, nil, &out)
	if err != nil || code/100 != 2 || !out.Found || out.WindowEnds == nil {
		if err != nil || code/100 != 2 {
			s.d.Log.WarnContext(ctx, "the window of a payment that waits for the customer could not be read", "status", code, "error", err)
		}
		return time.Time{}, false
	}
	return out.WindowEnds.UTC(), true
}

// releaseHeldInvoice is POST /v1/invoices/{id}/release.
func (s *Server) releaseHeldInvoice(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Billing.ReleaseHeldInvoice(r.Context(), &billingv1.ReleaseHeldInvoiceRequest{InvoiceId: r.PathValue("id")})
	respond(w, r, http.StatusOK, resp.GetSaga(), err)
}

// voidHeldInvoice is POST /v1/invoices/{id}/void.
func (s *Server) voidHeldInvoice(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Billing.VoidHeldInvoice(r.Context(), &billingv1.VoidHeldInvoiceRequest{InvoiceId: r.PathValue("id")})
	respond(w, r, http.StatusOK, resp.GetSaga(), err)
}
