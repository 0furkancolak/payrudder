package payments_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/0furkancolak/payrudder/pkg/payments"
	"github.com/0furkancolak/payrudder/pkg/payments/conformance"
)

// mockPSPFake speaks mock-psp's HTTP API (tools/mockpsp): POST /v1/charges and /v1/refunds with an Idempotency-Key, GET /v1/charges?idempotency_key=.
func mockPSPFake(t *testing.T, sc conformance.Scenario) (payments.Provider, *conformance.Probe) {
	t.Helper()
	probe := &conformance.Probe{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch sc {
		case conformance.ServerError:
			w.WriteHeader(http.StatusInternalServerError)
			return
		case conformance.RateLimited:
			w.WriteHeader(http.StatusTooManyRequests)
			return
		case conformance.NeverAnswers:
			_, _ = io.Copy(io.Discard, r.Body) // the server notices a client that went away only once the body has been read
			select {
			case <-r.Context().Done():
			case <-time.After(5 * time.Second):
			}
			return
		}
		switch r.Method + " " + r.URL.Path {
		case "POST /v1/charges":
			probe.Charge(r.Header.Get("Idempotency-Key"))
			if sc == conformance.ChargeDeclined {
				w.WriteHeader(http.StatusPaymentRequired)
				_, _ = io.WriteString(w, `{"error":{"code":"card_declined"}}`)
				return
			}
			_, _ = io.WriteString(w, `{"id":"ch_1"}`)
		case "POST /v1/refunds":
			var in payments.RefundRequest
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &in)
			amount := in.AmountMinor
			if amount == 0 {
				amount = conformance.Total
			}
			probe.Refund(conformance.SeenRefund{Key: r.Header.Get("Idempotency-Key"), AmountMinor: amount})
			_, _ = io.WriteString(w, `{"id":"re_1"}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	timeout := time.Second
	if sc == conformance.NeverAnswers {
		timeout = 100 * time.Millisecond
	}
	return payments.NewMockPSP(srv.URL, timeout), probe
}

func TestMockPSPConformance(t *testing.T) {
	conformance.Run(t, conformance.Harness{
		Provider: mockPSPFake, Currency: "USD", PaymentMethod: "pm_sandbox_success", ChargeRef: "ch_1", PartialRefunds: true, RefundsByInvoice: true,
	})
}
