//go:build integration

package itest

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
	commandsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/commands/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/paymentgate"
	"github.com/0furkancolak/payrudder/pkg/stubworker"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/pkg/testkit"
	sagacommand "github.com/0furkancolak/payrudder/services/billing-svc/internal/sagas/command"
)

// The seller reads why the first charge of an invoice is held and can release it or void it (plans 13 and 14, the console's invoice sheet). Reading asks the payment gate now;
// Release asks it again and never overrides it; Void ends the invoice without a refund (nothing was charged); both are refused for an invoice that is not held, belong to the
// caller's tenant only, and are recorded in the audit log.
func testTheSellerReadsAndEndsAHeldCharge(t *testing.T) {
	var verdict atomic.Int32 // a paymentgate.Verdict
	var tid atomic.Value
	e := newEnv(t, envOpts{gate: func(_ context.Context, id uuid.UUID, customer string) (paymentgate.Verdict, error) {
		if want, _ := tid.Load().(uuid.UUID); id != want || customer != "cus_a" {
			return paymentgate.Allowed, nil
		}
		return paymentgate.Verdict(verdict.Load()), nil
	}})
	tid.Store(e.tid)
	verdict.Store(int32(paymentgate.CustomerBlocked))
	stub := e.startWorkers(func(c *commandsv1.CommandEnvelope, _ int) stubworker.Action {
		return stubworker.Action{Reply: stubworker.Succeed}
	})
	e.addUsage("2026-10", "cus_a", "gb_hours", "1.000", 3)
	e.driveRun("2026-10")
	testkit.Eventually(t, 90*time.Second, "cus_b charged and cus_a held", func() bool {
		s := e.sagaStates("2026-10")
		return s["cus_a"] == "PAYMENT_HELD" && s["cus_b"] == "COMPLETED"
	})
	invoice := func(period, customer string) string {
		var id string
		if err := shared.sql.QueryRow(context.Background(), `SELECT id::text FROM invoices WHERE tenant_id = $1 AND period_id = $2 AND snapshot->>'customer_external_id' = $3`, e.tid, period, customer).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	charges := func(inv string) (n int) {
		for _, c := range e.mine(stub) {
			if ch := c.GetChargePayment(); ch != nil && ch.GetInvoiceId() == inv {
				n++
			}
		}
		return n
	}
	svc := e.api()
	mine := tenant.NewContext(context.Background(), tenant.Info{ID: e.tid})
	stranger := tenant.NewContext(context.Background(), tenant.Info{ID: uuid.New()})
	held, done := invoice("2026-10", "cus_a"), invoice("2026-10", "cus_b")
	wait := func(inv string) *billingv1.PaymentWait {
		t.Helper()
		r, err := svc.GetInvoicePaymentWait(mine, &billingv1.GetInvoicePaymentWaitRequest{InvoiceId: inv})
		if err != nil {
			t.Fatal(err)
		}
		return r.GetWait()
	}
	refused := func(what string, err error, code codes.Code, key string) {
		t.Helper()
		if status.Code(err) != code || errkey.Of(err) != key {
			t.Errorf("%s: %v (key %q), want %v %s", what, err, errkey.Of(err), code, key)
		}
	}

	t.Run("the read says why, since when and when it is looked at again", func(t *testing.T) {
		w := wait(held)
		if w.GetState() != "PAYMENT_HELD" || w.GetHoldReason() != "customer_blocked" || w.GetSinceTime() == nil || w.GetNextCheckTime() == nil {
			t.Fatalf("wait = %v; want PAYMENT_HELD, customer_blocked, a start and a next look", w)
		}
		if d := w.GetNextCheckTime().AsTime().Sub(w.GetSinceTime().AsTime()); d < sagacommand.DunningHold-time.Minute || d > sagacommand.DunningHold+time.Minute {
			t.Errorf("the next look is %v after the hold began, want about %v", d, sagacommand.DunningHold)
		}
		for v, reason := range map[paymentgate.Verdict]string{paymentgate.TenantOnHold: "tenant_on_hold", paymentgate.Allowed: "none", paymentgate.CustomerBlocked: "customer_blocked"} {
			verdict.Store(int32(v))
			if got := wait(held).GetHoldReason(); got != reason {
				t.Errorf("the gate says %v: reason %q, want %q", v, got, reason)
			}
		}
		if w := wait(done); w.GetState() != "COMPLETED" || w.GetHoldReason() != "" || w.GetSinceTime() != nil || w.GetNextCheckTime() != nil {
			t.Errorf("an invoice that is not waiting answers its state alone: %v", w)
		}
		_, err := svc.GetInvoicePaymentWait(stranger, &billingv1.GetInvoicePaymentWaitRequest{InvoiceId: held})
		refused("another tenant reads the wait", err, codes.NotFound, "billing.saga.not_found")
		_, err = svc.GetInvoicePaymentWait(mine, &billingv1.GetInvoicePaymentWaitRequest{InvoiceId: "nope"})
		refused("a bad id", err, codes.InvalidArgument, "billing.invoice.bad_id")
	})

	t.Run("only a held first charge can be released or voided, and only by its own tenant", func(t *testing.T) {
		_, err := svc.ReleaseHeldInvoice(mine, &billingv1.ReleaseHeldInvoiceRequest{InvoiceId: done})
		refused("release a completed invoice", err, codes.FailedPrecondition, "billing.invoice.not_held")
		_, err = svc.VoidHeldInvoice(mine, &billingv1.VoidHeldInvoiceRequest{InvoiceId: done})
		refused("void a completed (paid) invoice", err, codes.FailedPrecondition, "billing.invoice.not_held")
		_, err = svc.ReleaseHeldInvoice(stranger, &billingv1.ReleaseHeldInvoiceRequest{InvoiceId: held})
		refused("release another tenant's invoice", err, codes.NotFound, "billing.saga.not_found")
		_, err = svc.VoidHeldInvoice(stranger, &billingv1.VoidHeldInvoiceRequest{InvoiceId: held})
		refused("void another tenant's invoice", err, codes.NotFound, "billing.saga.not_found")
		if s := e.sagaStates("2026-10"); s["cus_a"] != "PAYMENT_HELD" || s["cus_b"] != "COMPLETED" {
			t.Fatalf("a refused call changed a saga: %v", s)
		}
		if targets, _ := auditEntries(t, e.tid, "invoice.void"); len(targets) != 0 {
			t.Errorf("a refused void left an audit entry: %v", targets)
		}
	})

	t.Run("release asks the gate again and never overrides it", func(t *testing.T) {
		verdict.Store(int32(paymentgate.CustomerBlocked))
		r, err := svc.ReleaseHeldInvoice(mine, &billingv1.ReleaseHeldInvoiceRequest{InvoiceId: held})
		if err != nil || r.GetSaga().GetState() != "PAYMENT_HELD" {
			t.Fatalf("release while the customer is blocked: %v, %v; want it still PAYMENT_HELD", r.GetSaga(), err)
		}
		time.Sleep(500 * time.Millisecond) // a charge the call should not have made would be on its way
		if n := charges(held); n != 0 {
			t.Fatalf("%d charge commands while the gate said no", n)
		}
		verdict.Store(int32(paymentgate.Allowed))
		r, err = svc.ReleaseHeldInvoice(mine, &billingv1.ReleaseHeldInvoiceRequest{InvoiceId: held})
		if err != nil || r.GetSaga().GetState() != "PAYMENT_REQUESTED" {
			t.Fatalf("release once nothing holds it: %v, %v; want PAYMENT_REQUESTED", r.GetSaga(), err)
		}
		testkit.Eventually(t, 30*time.Second, "the released invoice to be charged and completed", func() bool { return e.sagaStates("2026-10")["cus_a"] == "COMPLETED" })
		if n := charges(held); n != 1 {
			t.Errorf("%d charge commands for the released invoice, want 1", n)
		}
		for _, c := range e.mine(stub) {
			if ch := c.GetChargePayment(); ch != nil && ch.GetInvoiceId() == held && (ch.GetDunningAttempt() != 0 || ch.GetIdempotencyKey() != held+":charge:0") {
				t.Errorf("the released charge = attempt %d, key %q; want the first charge (attempt 0, %s:charge:0)", ch.GetDunningAttempt(), ch.GetIdempotencyKey(), held)
			}
		}
		targets, details := auditEntries(t, e.tid, "invoice.release")
		if len(targets) != 2 || targets[0] != held || details[0]["from"] != "PAYMENT_HELD" || details[0]["to"] != "PAYMENT_HELD" || details[1]["to"] != "PAYMENT_REQUESTED" || details[1]["cause"] != "seller:release" {
			t.Errorf("invoice.release entries = %v %v; want one per press, the first leaving it held and the second releasing it", targets, details)
		}
	})

	t.Run("void ends a held invoice without a refund, and records it", func(t *testing.T) {
		verdict.Store(int32(paymentgate.TenantOnHold))
		e.addUsage("2026-11", "cus_a", "gb_hours", "1.000", 3)
		e.clk.Advance(30 * 24 * time.Hour)
		e.driveRun("2026-11")
		testkit.Eventually(t, 30*time.Second, "the november invoice of cus_a to be held", func() bool { return e.sagaStates("2026-11")["cus_a"] == "PAYMENT_HELD" })
		nov := invoice("2026-11", "cus_a")
		r, err := svc.VoidHeldInvoice(mine, &billingv1.VoidHeldInvoiceRequest{InvoiceId: nov})
		if err != nil || r.GetSaga().GetState() != "VOIDED" {
			t.Fatalf("void a held invoice: %v, %v; want VOIDED", r.GetSaga(), err)
		}
		var status string
		if err := shared.sql.QueryRow(context.Background(), `SELECT status FROM invoices WHERE tenant_id = $1 AND id = $2::uuid`, e.tid, nov).Scan(&status); err != nil || status != "void" {
			t.Errorf("invoice status = %q (%v), want void", status, err)
		}
		for _, c := range e.mine(stub) {
			if c.GetRefundPayment() != nil && c.GetRefundPayment().GetInvoiceId() == nov {
				t.Error("a refund was asked for an invoice that was never charged")
			}
		}
		targets, details := auditEntries(t, e.tid, "invoice.void")
		if len(targets) != 1 || targets[0] != nov || details[0]["from"] != "PAYMENT_HELD" || details[0]["to"] != "VOIDED" || details[0]["cause"] != "seller:void" {
			t.Errorf("invoice.void entries = %v %v; want one, for the november invoice", targets, details)
		}
		_, err = svc.VoidHeldInvoice(mine, &billingv1.VoidHeldInvoiceRequest{InvoiceId: nov})
		refused("void it twice", err, codes.FailedPrecondition, "billing.invoice.not_held")
	})
}
