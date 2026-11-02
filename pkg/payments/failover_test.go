package payments

import (
	"context"
	"testing"
	"time"
)

// mock-psp, which the sandbox and the demo use, classifies a connection that never opened the same way: proof that nothing was processed.
func TestMockPSPConnectionRefusedIsProof(t *testing.T) {
	_, err := NewMockPSP("http://127.0.0.1:1", time.Second).Charge(context.Background(), ChargeRequest{IdempotencyKey: "k", InvoiceID: "i", AmountMinor: 1, Currency: "USD"})
	if !NotProcessed(err) || !IsOutage(err) {
		t.Fatalf("%v; want proof that nothing was processed", err)
	}
}
