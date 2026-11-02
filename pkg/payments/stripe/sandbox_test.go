package stripe

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// TestSandbox talks to Stripe with a test-mode key. It settles what the tests cannot (that the key is accepted and the balance says the account is in test mode, which
// CheckHealth compares with the key's prefix), so it is skipped, with the reason, without a key. It has never been run: no Stripe account exists yet
// (docs/stripe-compatibility-matrix.md). It makes one read-only request, GET /v1/balance.
//
//	STRIPE_SANDBOX_API_KEY: a test-mode secret or restricted key (sk_test_..., rk_test_...). A live key is refused: nothing here may touch real money.
func TestSandbox(t *testing.T) {
	key := os.Getenv("STRIPE_SANDBOX_API_KEY")
	if key == "" {
		t.Skip("STRIPE_SANDBOX_API_KEY is not set: the behaviour marked UNVERIFIED is not checked")
	}
	if strings.Contains(key, "_live_") {
		t.Fatal("STRIPE_SANDBOX_API_KEY is a live key; use a test-mode key")
	}
	c, err := New(Settings{APIKey: key}, 0)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := c.CheckHealth(ctx); err != nil {
		t.Fatalf("CheckHealth: %v", err)
	}
}
