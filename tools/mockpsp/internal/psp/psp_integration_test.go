//go:build integration

package psp_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/0furkancolak/quotify/pkg/payments"
	"github.com/0furkancolak/quotify/pkg/pgxx"
	"github.com/0furkancolak/quotify/pkg/testkit"
	"github.com/0furkancolak/quotify/tools/mockpsp/internal/psp"
	"github.com/0furkancolak/quotify/tools/mockpsp/migrations"
)

func start(t *testing.T, cfg psp.Config) (*payments.MockPSP, func()) {
	t.Helper()
	pg := testkit.StartPostgres(t)
	db := testkit.CreateServiceDB(t, pg, "mockpsp")
	if err := pgxx.Migrate(context.Background(), db.Migrator, migrations.FS, nil); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxx.NewPool(context.Background(), db.App)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	srv := httptest.NewServer(psp.New(pool, cfg))
	t.Cleanup(srv.Close)
	return payments.NewMockPSP(srv.URL, 1500*time.Millisecond), srv.Close
}

// charge sends a charge whose invoice is derived from the key's family ("k-if-0" and "k-if-1" are one invoice's attempts).
func charge(c *payments.MockPSP, key, method string) (payments.ChargeResult, error) {
	inv := key
	if i := strings.LastIndex(key, "-"); i > 0 && len(key)-i == 2 {
		inv = key[:i]
	}
	return c.Charge(context.Background(), payments.ChargeRequest{IdempotencyKey: key, InvoiceID: "inv-" + inv, AmountMinor: 500, Currency: "USD", PaymentMethod: method})
}

func TestScenarios(t *testing.T) {
	c, _ := start(t, psp.Config{HangFor: 5 * time.Second})

	t.Run("SameKeyReturnsTheOriginalCharge", func(t *testing.T) {
		a, err := charge(c, "k-same", "pm_sandbox_success")
		if err != nil || a.ChargeID == "" {
			t.Fatalf("charge = %+v, %v", a, err)
		}
		b, err := charge(c, "k-same", "pm_sandbox_success")
		if err != nil || b.ChargeID != a.ChargeID {
			t.Fatalf("replay = %+v, %v; want the original %s", b, err, a.ChargeID)
		}
	})

	t.Run("DeclinedIsSticky", func(t *testing.T) {
		for range 2 {
			_, err := charge(c, "k-decl", "pm_sandbox_declined")
			var d *payments.DeclinedError
			if !errors.As(err, &d) || d.Code != "card_declined" {
				t.Fatalf("err = %v, want a card_declined decline", err)
			}
		}
		st, err := c.Status(context.Background(), "k-decl")
		if err != nil || st.State != payments.StateDeclined {
			t.Fatalf("status = %+v, %v", st, err)
		}
	})

	t.Run("InsufficientFundsOnceThenSucceeds", func(t *testing.T) {
		_, err := charge(c, "k-if-0", "pm_sandbox_insufficient_funds_once")
		var d *payments.DeclinedError
		if !errors.As(err, &d) || d.Code != "insufficient_funds" {
			t.Fatalf("first attempt: %v", err)
		}
		if _, err := charge(c, "k-if-1", "pm_sandbox_insufficient_funds_once"); err != nil { // the dunning retry: a new key
			t.Fatalf("second attempt: %v", err)
		}
	})

	t.Run("ErrorTwiceThenSuccess", func(t *testing.T) {
		for i := 1; i <= 2; i++ {
			_, err := charge(c, "k-err", "pm_sandbox_error_then_success")
			var tr *payments.TransientError
			if !errors.As(err, &tr) {
				t.Fatalf("attempt %d: %v, want a transient error", i, err)
			}
		}
		if _, err := charge(c, "k-err", "pm_sandbox_error_then_success"); err != nil {
			t.Fatalf("third attempt: %v", err)
		}
	})

	t.Run("TimeoutCommittedIsVisibleToStatus", func(t *testing.T) {
		_, err := charge(c, "k-tc", "pm_sandbox_timeout_committed")
		if !errors.Is(err, payments.ErrTimeout) {
			t.Fatalf("err = %v, want a timeout", err)
		}
		st, err := c.Status(context.Background(), "k-tc")
		if err != nil || st.State != payments.StateSucceeded || st.ChargeID == "" {
			t.Fatalf("status = %+v, %v; want the committed charge", st, err)
		}
	})

	t.Run("TimeoutNotCommittedThenRechargeWithTheSameKey", func(t *testing.T) {
		if _, err := charge(c, "k-tn", "pm_sandbox_timeout_not_committed"); !errors.Is(err, payments.ErrTimeout) {
			t.Fatalf("err = %v, want a timeout", err)
		}
		st, err := c.Status(context.Background(), "k-tn")
		if err != nil || st.State != payments.StateNotFound {
			t.Fatalf("status = %+v, %v; want not found", st, err)
		}
		if _, err := charge(c, "k-tn", "pm_sandbox_timeout_not_committed"); err != nil {
			t.Fatalf("recharge: %v", err)
		}
	})

	t.Run("RefundIsIdempotent", func(t *testing.T) {
		a, err := c.Refund(context.Background(), payments.RefundRequest{IdempotencyKey: "r1", InvoiceID: "inv-1", AmountMinor: 500, Currency: "USD"})
		b, err2 := c.Refund(context.Background(), payments.RefundRequest{IdempotencyKey: "r1", InvoiceID: "inv-1", AmountMinor: 500, Currency: "USD"})
		if err != nil || err2 != nil || a.RefundID != b.RefundID {
			t.Fatalf("refunds = %+v %+v, %v %v", a, b, err, err2)
		}
	})
}

func TestEnvironmentKnobs(t *testing.T) {
	c, _ := start(t, psp.Config{TimeoutRate: 1, HangFor: 5 * time.Second})
	// MOCKPSP_TIMEOUT_RATE commits the charge and then hangs: exactly the PAYMENT_UNKNOWN case.
	if _, err := charge(c, "k-knob", "pm_card_visa"); !errors.Is(err, payments.ErrTimeout) {
		t.Fatalf("err = %v, want a timeout", err)
	}
	if st, _ := c.Status(context.Background(), "k-knob"); st.State != payments.StateSucceeded {
		t.Fatalf("status = %+v, want the charge committed before the hang", st)
	}
}
