package stripe

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/0furkancolak/payrudder/pkg/payments"
	"github.com/0furkancolak/payrudder/pkg/payments/conformance"
)

// stripeFake speaks the part of Stripe's API the adapter uses, as docs.stripe.com documents it.
func stripeFake(t *testing.T, sc conformance.Scenario) (payments.Provider, *conformance.Probe) {
	t.Helper()
	probe := &conformance.Probe{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body) // the server notices a client that went away only once the body has been read
		form, _ := url.ParseQuery(string(raw))
		switch sc {
		case conformance.ServerError:
			w.WriteHeader(http.StatusInternalServerError)
			return
		case conformance.RateLimited:
			w.Header().Set("Stripe-Rate-Limited-Reason", "global-rate")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		case conformance.NeverAnswers:
			select {
			case <-r.Context().Done():
			case <-time.After(5 * time.Second):
			}
			return
		}
		switch r.Method + " " + r.URL.Path {
		case "POST /v1/payment_intents":
			probe.Charge(r.Header.Get("Idempotency-Key"))
			if sc == conformance.ChargeDeclined {
				w.WriteHeader(http.StatusPaymentRequired)
				_, _ = io.WriteString(w, `{"error":{"type":"card_error","code":"card_declined","decline_code":"insufficient_funds"}}`)
				return
			}
			_, _ = io.WriteString(w, `{"id":"pi_1","status":"succeeded","amount":1116,"currency":"usd"}`)
		case "POST /v1/refunds":
			amount := conformance.Total
			if a := form.Get("amount"); a != "" {
				amount, _ = strconv.ParseInt(a, 10, 64)
			}
			probe.Refund(conformance.SeenRefund{Key: r.Header.Get("Idempotency-Key"), AmountMinor: amount})
			_, _ = io.WriteString(w, `{"id":"re_1","status":"succeeded"}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	timeout := time.Second
	if sc == conformance.NeverAnswers {
		timeout = 100 * time.Millisecond
	}
	c, err := New(Settings{APIKey: key, WebhookSecret: secret, BaseURL: srv.URL}, timeout)
	if err != nil {
		t.Fatal(err)
	}
	return c, probe
}

func TestConformance(t *testing.T) {
	conformance.Run(t, conformance.Harness{
		Provider: stripeFake, Currency: "USD", PaymentMethod: "cus_1:pm_1", ChargeRef: "pi_1", PartialRefunds: true, Parse: ParseNotification,
		Webhook: func(t *testing.T, c conformance.WebhookCase) (payments.Provider, []byte, http.Header) {
			p, _ := stripeFake(t, conformance.ChargeSucceeds)
			body := `{"id":"evt_1","type":"payment_intent.succeeded","created":1790000000,"data":{"object":{"id":"pi_1","amount":1116,"amount_received":1116,"currency":"usd","metadata":{"payrudder_key":"` + conformance.ChargeKey + `"}}}}`
			signedWith := secret
			switch c {
			case conformance.WebhookDeclined:
				body = `{"id":"evt_2","type":"payment_intent.payment_failed","data":{"object":{"id":"pi_1","amount":1116,"currency":"usd","metadata":{"payrudder_key":"` + conformance.ChargeKey + `"},"last_payment_error":{"decline_code":"expired_card"}}}}`
			case conformance.WebhookForged:
				signedWith = "whsec_not_ours"
			}
			h := http.Header{}
			h.Set("Stripe-Signature", SignatureHeader(signedWith, time.Now(), []byte(body)))
			return p, []byte(body), h
		},
	})
}
