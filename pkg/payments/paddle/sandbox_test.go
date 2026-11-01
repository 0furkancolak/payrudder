package paddle

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestSandbox talks to Paddle's sandbox. It settles what the tests cannot (that the key and Paddle-Version are accepted, that a list pages by cursor), so it is
// skipped, with the reason, without credentials. It has never been run: no Paddle sandbox account exists yet (docs/paddle-compatibility-matrix.md).
//
//	PADDLE_SANDBOX_API_KEY: a sandbox API key (pdl_sdbx_apikey_...)
func TestSandbox(t *testing.T) {
	apiKey := os.Getenv("PADDLE_SANDBOX_API_KEY")
	if apiKey == "" {
		t.Skip("PADDLE_SANDBOX_API_KEY is not set: the behaviour marked UNVERIFIED is not checked")
	}
	c, err := New(Settings{APIKey: apiKey, BaseURL: SandboxURL, WebhookSecret: "unused", UsageProductID: "pro_unused"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := c.CheckHealth(ctx); err != nil {
		t.Fatalf("CheckHealth: %v", err)
	}
	// One entity a page: with more than one customer in the account this walks several pages by cursor.
	customers, err := listAll[struct {
		ID string `json:"id"`
	}](ctx, c, "/customers?per_page=1")
	if err != nil {
		t.Fatalf("listing customers page by page: %v", err)
	}
	t.Logf("%d customers", len(customers))
}
