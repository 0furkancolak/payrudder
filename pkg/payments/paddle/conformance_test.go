package paddle

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/0furkancolak/payrudder/pkg/payments"
	"github.com/0furkancolak/payrudder/pkg/payments/conformance"
)

// paddleFake speaks the part of Paddle Billing's API the adapter uses, as developer.paddle.com documents it.
func paddleFake(t *testing.T, sc conformance.Scenario) (payments.Provider, *conformance.Probe) {
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
			w.Header().Set("Retry-After", "30")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"error":{"type":"api_error","code":"too_many_requests"}}`)
			return
		case conformance.NeverAnswers:
			select {
			case <-r.Context().Done():
			case <-time.After(5 * time.Second):
			}
			return
		}
		switch r.Method + " " + r.URL.Path {
		case "POST /subscriptions/sub_01/charge":
			price := body["items"].([]any)[0].(map[string]any)["price"].(map[string]any)
			key, _ := price["custom_data"].(map[string]any)["payrudder_key"].(string)
			probe.Charge(key)
			_, _ = io.WriteString(w, `{"data":{"id":"sub_01"}}`)
		case "GET /transactions/txn_01":
			_, _ = io.WriteString(w, `{"data":{"id":"txn_01","currency_code":"USD","details":{"totals":{"grand_total":"1116"},"line_items":[{"id":"txnitm_1"}]}}}`)
		case "POST /adjustments":
			amount := conformance.Total
			if body["type"] == "partial" {
				amount = 0
				for _, it := range body["items"].([]any) {
					n, _ := strconv.ParseInt(it.(map[string]any)["amount"].(string), 10, 64)
					amount += n
				}
			}
			probe.Refund(conformance.SeenRefund{AmountMinor: amount}) // Paddle takes no idempotency header
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"data":{"id":"adj_01"}}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	timeout := time.Second
	if sc == conformance.NeverAnswers {
		timeout = 100 * time.Millisecond
	}
	c, err := New(Settings{APIKey: "pdl_key", BaseURL: srv.URL, WebhookSecret: "whsec", UsageProductID: "pro_usage"}, timeout)
	if err != nil {
		t.Fatal(err)
	}
	return c, probe
}

func TestConformance(t *testing.T) {
	conformance.Run(t, conformance.Harness{
		Provider: paddleFake, Currency: "USD", PaymentMethod: "sub_01", ChargeRef: "txn_01", SettlesLater: true, PartialRefunds: true, Parse: ParseNotification,
		Skip: map[conformance.Scenario]string{
			conformance.ChargeDeclined: "Paddle accepts the charge and settles it later: a refused card arrives as transaction.payment_failed (the declined notification below)",
		},
		Webhook: func(t *testing.T, c conformance.WebhookCase) (payments.Provider, []byte, http.Header) {
			p, _ := paddleFake(t, conformance.ChargeSucceeds)
			body := `{"event_id":"evt_1","event_type":"transaction.completed","occurred_at":"2027-01-01T00:00:00Z","data":{"id":"txn_01","status":"completed","currency_code":"USD",
				"items":[{"price":{"custom_data":{"payrudder_key":"` + conformance.ChargeKey + `"}}}],"details":{"totals":{"grand_total":"1116"}}}}`
			secret := "whsec"
			switch c {
			case conformance.WebhookDeclined:
				body = `{"event_id":"evt_2","event_type":"transaction.payment_failed","occurred_at":"2027-01-01T00:00:00Z","data":{"id":"txn_01","currency_code":"USD",
					"items":[{"price":{"custom_data":{"payrudder_key":"` + conformance.ChargeKey + `"}}}],"details":{"totals":{"grand_total":"1116"}},
					"payments":[{"status":"error","error_code":"declined"}]}}`
			case conformance.WebhookForged:
				secret = "another-secret"
			}
			h := http.Header{}
			h.Set("Paddle-Signature", SignatureHeader(secret, time.Now(), []byte(body)))
			return p, []byte(body), h
		},
	})
}
