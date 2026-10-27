package payrudder

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// recorder collects the delays the client would have slept.
type recorder struct {
	mu     sync.Mutex
	sleeps []time.Duration
}

func (r *recorder) sleep(ctx context.Context, d time.Duration) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sleeps = append(r.sleeps, d)
	return ctx.Err()
}

func (r *recorder) got() []time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]time.Duration(nil), r.sleeps...)
}

// newTestClient returns a client against a fake gateway. Sleeping is recorded, not done, and jitter is off, so retry
// tests are instant and exact.
func newTestClient(t *testing.T, h http.Handler, opts ...Option) (*Client, *recorder) {
	t.Helper()
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	all := append([]Option{WithAPIKey("qk_test_abcdefgh_secret"), WithBaseURL(ts.URL)}, opts...)
	c, err := New(all...)
	if err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	c.sleep = rec.sleep
	c.jitter = func() float64 { return 0 }
	return c, rec
}

// roundTripFunc adapts a function to http.RoundTripper.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
