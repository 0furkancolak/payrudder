package iyzico

import (
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/0furkancolak/payrudder/pkg/payments"
	"github.com/0furkancolak/payrudder/pkg/payments/conformance"
)

// iyzicoFake speaks the part of iyzico's API the adapter uses, as docs.iyzico.com documents it. detail is what /payment/detail answers.
func iyzicoFake(t *testing.T, sc conformance.Scenario, detail string) (payments.Provider, *conformance.Probe) {
	t.Helper()
	probe := &conformance.Probe{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body) // the server notices a client that went away only once the body has been read
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		switch sc {
		case conformance.ServerError:
			w.WriteHeader(http.StatusInternalServerError)
			return
		case conformance.RateLimited:
			w.WriteHeader(http.StatusTooManyRequests)
			return
		case conformance.NeverAnswers:
			select {
			case <-r.Context().Done():
			case <-time.After(5 * time.Second):
			}
			return
		}
		switch r.URL.Path {
		case "/payment/auth":
			key, _ := body["conversationId"].(string)
			probe.Charge(key)
			if sc == conformance.ChargeDeclined {
				_, _ = io.WriteString(w, `{"status":"failure","errorCode":"10051","errorMessage":"Not sufficient funds","errorGroup":"NOT_SUFFICIENT_FUNDS"}`)
				return
			}
			_, _ = io.WriteString(w, `{"status":"success","paymentId":"23456","fraudStatus":1}`)
		case "/v2/payment/refund":
			price, _ := strconv.ParseFloat(body["price"].(string), 64)
			probe.Refund(conformance.SeenRefund{AmountMinor: int64(math.Round(price * 100))}) // iyzico takes no idempotency key on a refund
			_, _ = io.WriteString(w, `{"status":"success","paymentId":"23456","refundHostReference":"ref-9"}`)
		case "/payment/detail":
			_, _ = io.WriteString(w, detail)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	timeout := time.Second
	if sc == conformance.NeverAnswers {
		timeout = 100 * time.Millisecond
	}
	c, err := New(Settings{APIKey: "api", SecretKey: "sec", BaseURL: srv.URL}, timeout)
	if err != nil {
		t.Fatal(err)
	}
	return c, probe
}

func TestConformance(t *testing.T) {
	conformance.Run(t, conformance.Harness{
		Provider: func(t *testing.T, sc conformance.Scenario) (payments.Provider, *conformance.Probe) {
			return iyzicoFake(t, sc, `{}`)
		},
		Currency: "TRY", PaymentMethod: "ck:ct", ChargeRef: "23456", PartialRefunds: true, Parse: ParseNotification,
		Webhook: func(t *testing.T, c conformance.WebhookCase) (payments.Provider, []byte, http.Header) {
			// iyzico is asked what the notified payment is: the answer decides, the body and the signature only name it.
			detail := `{"status":"success","paymentId":"23456","paymentStatus":"SUCCESS","basketId":"` + conformance.InvoiceID + `","fraudStatus":1}`
			status := "SUCCESS"
			if c == conformance.WebhookDeclined {
				detail = `{"status":"success","paymentId":"23456","paymentStatus":"FAILURE","basketId":"` + conformance.InvoiceID + `"}`
				status = "FAILURE"
			}
			p, _ := iyzicoFake(t, conformance.ChargeSucceeds, detail)
			e := WebhookEvent{Type: "API_AUTH", Reference: "ref-1", PaymentID: "23456", ConversationID: conformance.ChargeKey, Status: status}
			body, _ := json.Marshal(map[string]any{"iyziEventType": e.Type, "iyziEventTime": 1790000000, "iyziReferenceCode": e.Reference, "paymentId": 23456,
				"paymentConversationId": e.ConversationID, "status": e.Status})
			h := http.Header{}
			h.Set(SignatureHeader, NotificationSignature("sec", e))
			if c == conformance.WebhookForged {
				h.Set(SignatureHeader, NotificationSignature("another-secret", e))
			}
			return p, body, h
		},
	})
}
