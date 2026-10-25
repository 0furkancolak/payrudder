package handler

import (
	"context"
	"testing"

	"github.com/google/uuid"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/payments"
)

func TestDemoModeNeverLeavesTheSandboxProvider(t *testing.T) {
	sandbox, live := payments.NewMockPSP("http://sandbox", 0), payments.NewMockPSP("http://live", 0)
	for _, demo := range []bool{false, true} {
		h := &Handler{Sandbox: sandbox, Live: live, Demo: demo}
		// A live tenant that names a provider of its own.
		got, err := h.providerFor(context.Background(), uuid.New(), plansv1.Environment_ENVIRONMENT_LIVE, uuid.New(), true, "iyzico")
		if err != nil {
			t.Fatal(err)
		}
		if want := map[bool]payments.PaymentProvider{false: live, true: sandbox}[demo]; got != want {
			t.Errorf("demo=%v: provider %p, want %p", demo, got, want)
		}
	}
}
