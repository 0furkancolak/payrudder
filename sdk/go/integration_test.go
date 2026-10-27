package payrudder

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestIntegrationSmoke talks to a real gateway. It runs only when PAYRUDDER_API_KEY is set (use a qk_test_ key; the
// address comes from PAYRUDDER_BASE_URL, default http://localhost:42800) and changes nothing.
func TestIntegrationSmoke(t *testing.T) {
	if os.Getenv(EnvAPIKey) == "" {
		t.Skip("PAYRUDDER_API_KEY is not set")
	}
	c, err := New()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := c.Meters.ListMeters(ctx); err != nil {
		t.Fatalf("ListMeters: %v", err)
	}
	n := 0
	for _, err := range c.Customers.ListCustomersAll(ctx, &ListCustomersParams{PageSize: Ptr(50)}) {
		if err != nil {
			t.Fatalf("ListCustomersAll: %v", err)
		}
		n++
		if n >= 100 {
			break
		}
	}
}
