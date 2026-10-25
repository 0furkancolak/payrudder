package iyzico

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestSandbox talks to iyzico's sandbox. It settles the points marked UNVERIFIED in the package comment, so it is not
// reported as passing without credentials: it is skipped, with the reason.
//
//	IYZICO_SANDBOX_API_KEY, IYZICO_SANDBOX_SECRET_KEY: sandbox merchant keys
//	IYZICO_SANDBOX_CARD: "cardUserKey:cardToken" of a stored sandbox card
func TestSandbox(t *testing.T) {
	apiKey, secret, card := os.Getenv("IYZICO_SANDBOX_API_KEY"), os.Getenv("IYZICO_SANDBOX_SECRET_KEY"), os.Getenv("IYZICO_SANDBOX_CARD")
	if apiKey == "" || secret == "" || card == "" {
		t.Skip("iyzico sandbox credentials are not set (IYZICO_SANDBOX_API_KEY, IYZICO_SANDBOX_SECRET_KEY, IYZICO_SANDBOX_CARD): the UNVERIFIED behaviours are not checked")
	}
	c, err := New(Settings{APIKey: apiKey, SecretKey: secret, BaseURL: SandboxURL, Buyer: Buyer{Name: "Test", Surname: "User", IdentityNumber: "11111111111",
		Address: "Test address 1", City: "Istanbul", Country: "Turkey", ZipCode: "34000", IP: "85.34.78.112", Email: "test@example.com"}}, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	key := "payrudder-sandbox-" + randomKey()
	if _, err := c.Status(ctx, key); err == nil {
		t.Log("Status of an unknown key answered without an error; set NotFoundCodes accordingly")
	} else {
		t.Logf("Status of an unknown key: %v (put its errorCode in Settings.NotFoundCodes)", err)
	}
}
