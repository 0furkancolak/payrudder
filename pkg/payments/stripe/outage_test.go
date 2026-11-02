package stripe

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/0furkancolak/payrudder/pkg/payments"
)

// What the failover rule (docs/provider-health.md) sees of a real adapter: a connection that never opens is proof the charge was not processed; a timeout, a 5xx and
// a 429 are not (a 502 can come from a gateway after the provider charged), and neither is any other answer. iyzico, Paddle and mock-psp classify transport errors with
// the same code, so this stands for them.
func TestWhatTheFailoverRuleSeesOfAnAdapter(t *testing.T) {
	charge := func(c *Client) error {
		_, err := c.Charge(context.Background(), payments.ChargeRequest{IdempotencyKey: "k", InvoiceID: "inv", AmountMinor: 1116, Currency: "USD", PaymentMethod: "pm_1"})
		return err
	}
	closed := httptest.NewServer(http.NotFoundHandler())
	closed.Close() // nothing listens there now: the connection is refused
	c, err := New(Settings{APIKey: key, WebhookSecret: secret, BaseURL: closed.URL}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := charge(c); !payments.NotProcessed(err) || !payments.IsOutage(err) {
		t.Errorf("connection refused: %v; want proof that nothing was processed, and an outage", err)
	}

	hang := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		<-r.Context().Done()
	}))
	t.Cleanup(hang.Close)
	if c, err = New(Settings{APIKey: key, WebhookSecret: secret, BaseURL: hang.URL}, 100*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if err := charge(c); !errors.Is(err, payments.ErrTimeout) || payments.NotProcessed(err) || !payments.IsOutage(err) {
		t.Errorf("no answer: %v; want ErrTimeout, which proves nothing", err)
	}

	for _, status := range []int{http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusTooManyRequests} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) }))
		if c, err = New(Settings{APIKey: key, WebhookSecret: secret, BaseURL: srv.URL}, time.Second); err != nil {
			t.Fatal(err)
		}
		if err := charge(c); payments.NotProcessed(err) || !payments.IsOutage(err) {
			t.Errorf("%d: %v; want an outage that proves nothing", status, err)
		}
		srv.Close()
	}
}
