package payments_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/0furkancolak/payrudder/pkg/payments"
)

// A 404 answers a status lookup ("no charge under this key"); for a charge or a refund it is a wrong URL, and must never read as a payment that went through.
func TestMockPSPNotFoundIsOnlyAnAnswerToAStatusLookup(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) }))
	defer srv.Close()
	p := payments.NewMockPSP(srv.URL, 0)
	ctx := context.Background()
	if got, err := p.Charge(ctx, payments.ChargeRequest{IdempotencyKey: "k", AmountMinor: 100, Currency: "USD"}); err == nil {
		t.Errorf("a 404 on a charge = %+v, nil; want an error", got)
	}
	if got, err := p.Refund(ctx, payments.RefundRequest{IdempotencyKey: "k", ChargeID: "c", AmountMinor: 100, Currency: "USD"}); err == nil {
		t.Errorf("a 404 on a refund = %+v, nil; want an error", got)
	}
	if got, err := p.Status(ctx, "k"); err != nil || got.State != payments.StateNotFound {
		t.Errorf("a 404 on a status lookup = %+v, %v; want not found", got, err)
	}
}
