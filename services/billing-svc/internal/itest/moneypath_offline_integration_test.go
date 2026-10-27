//go:build integration

package itest

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
	commandsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/commands/v1"
	"github.com/0furkancolak/payrudder/pkg/stubworker"
	"github.com/0furkancolak/payrudder/pkg/testkit"
	paymentscmd "github.com/0furkancolak/payrudder/services/billing-svc/internal/payments/command"
)

// seedSagaIn seeds the saga of an invoice in state.
func seedSagaIn(t *testing.T, tid, invoice uuid.UUID, state string) {
	t.Helper()
	if _, err := shared.system.Exec(context.Background(), `INSERT INTO invoice_sagas (tenant_id, id, invoice_id, state, version) VALUES ($1,$2,$3,$4,1)`, tid, uuid.New(), invoice, state); err != nil {
		t.Fatal(err)
	}
}

// seedHandPayment records money received outside a provider against an invoice without going through the saga.
func seedHandPayment(t *testing.T, tid, invoice uuid.UUID, amount int64) {
	t.Helper()
	if _, err := shared.system.Exec(context.Background(), `INSERT INTO invoice_payments (tenant_id, id, invoice_id, method, amount_minor, currency, received_at, reference, note, recorded_by, recorded_at)
		SELECT tenant_id, $3, id, 'bank_transfer', $4, currency, now(), '', '', 'op', now() FROM invoices WHERE tenant_id = $1 AND id = $2`, tid, invoice, uuid.New(), amount); err != nil {
		t.Fatal(err)
	}
}

// A part payment recorded by hand does not vanish when the invoice is voided: it is the customer's money, and becomes a credit balance in the invoice's currency.
func testVoidingAPartPaidInvoiceCreditsWhatWasReceived(t *testing.T) {
	e := newEnv(t, envOpts{})
	stub := e.startWorkers(func(c *commandsv1.CommandEnvelope, _ int) stubworker.Action {
		return stubworker.Action{Reply: stubworker.Succeed}
	})
	customer := uuid.New()
	inv := seedOne(t, e.tid, customer, "cus_part", "open", "USD", "INV-2026-000801", 1000, "live")
	seedSagaIn(t, e.tid, inv, "PAYMENT_DECLINED")
	res, err := paymentscmd.Record(context.Background(), shared.system, e.eng, paymentscmd.Payment{Tenant: e.tid, Invoice: inv, Method: "bank_transfer", AmountMinor: 600, Reference: "BANK-9", Actor: "op"})
	if err != nil || res.Settled {
		t.Fatalf("recording the part payment: %+v, %v", res, err)
	}
	if _, err := e.api().VoidInvoice(opCtx(), &billingv1.VoidInvoiceRequest{TenantId: e.tid.String(), InvoiceId: inv.String(), Reason: "wrong customer"}); err != nil {
		t.Fatal(err)
	}
	testkit.Eventually(t, 30*time.Second, "the invoice to be voided", func() bool {
		var state string
		_ = shared.system.QueryRow(context.Background(), `SELECT state FROM invoice_sagas WHERE tenant_id = $1 AND invoice_id = $2`, e.tid, inv).Scan(&state)
		return state == "VOIDED"
	})
	var n int
	var amount int64
	var currency string
	if err := shared.system.QueryRow(context.Background(), `SELECT count(*), COALESCE(sum(amount_minor), 0)::bigint, COALESCE(max(currency), '') FROM credit_balances WHERE tenant_id = $1 AND customer_id = $2`,
		e.tid, customer).Scan(&n, &amount, &currency); err != nil || n != 1 || amount != 600 || currency != "USD" {
		t.Fatalf("credit balances of the customer: %d summing to %d %s (%v); want one of 600 USD", n, amount, currency, err)
	}
	if got := e.count(stub, "refund"); got != 0 {
		t.Fatalf("%d refund commands for money no provider took", got)
	}
}

// An invoice nothing was received for gives no credit, and one that is voided after being paid in full does not get one on top of its refund or credit notes.
func testVoidingAnUnpaidInvoiceCreditsNothing(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.startWorkers(nil)
	customer := uuid.New()
	inv := seedOne(t, e.tid, customer, "cus_none", "open", "USD", "INV-2026-000802", 1000, "live")
	seedSagaIn(t, e.tid, inv, "PAYMENT_DECLINED")
	if _, err := e.api().VoidInvoice(opCtx(), &billingv1.VoidInvoiceRequest{TenantId: e.tid.String(), InvoiceId: inv.String(), Reason: "wrong"}); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := shared.system.QueryRow(context.Background(), `SELECT count(*) FROM credit_balances WHERE tenant_id = $1 AND customer_id = $2`, e.tid, customer).Scan(&n); err != nil || n != 0 {
		t.Fatalf("%d credit balances for a void with nothing received (%v)", n, err)
	}
}

// A refund credit note asks the invoice's payment provider to return a card payment. An invoice paid by bank transfer or cash has none: the note is refused at the
// door instead of failing at the provider after it has a number, and a credit to the balance still works.
func testRefundCreditNoteNeedsACardPayment(t *testing.T) {
	e := newEnv(t, envOpts{})
	stub := e.startWorkers(nil)
	customer := uuid.New()
	inv := seedOne(t, e.tid, customer, "cus_bank", "paid", "USD", "INV-2026-000803", 1000, "live")
	seedSaga(t, e.tid, inv)
	seedHandPayment(t, e.tid, inv, 1000)
	svc := e.api()
	_, err := svc.CreateCreditNote(opCtx(), &billingv1.CreateCreditNoteRequest{TenantId: e.tid.String(), InvoiceId: inv.String(), AmountMinor: 300, RefundToCard: true})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("refund note on a bank-paid invoice: %v; want FAILED_PRECONDITION", err)
	}
	var notes int
	if err := shared.system.QueryRow(context.Background(), `SELECT count(*) FROM credit_notes WHERE tenant_id = $1 AND invoice_id = $2`, e.tid, inv).Scan(&notes); err != nil || notes != 0 {
		t.Fatalf("%d credit notes were numbered for the refused refund (%v)", notes, err)
	}
	if got := e.count(stub, "refund"); got != 0 {
		t.Fatalf("%d refund commands went to the worker", got)
	}
	if _, err := svc.CreateCreditNote(opCtx(), &billingv1.CreateCreditNoteRequest{TenantId: e.tid.String(), InvoiceId: inv.String(), AmountMinor: 300}); err != nil {
		t.Fatalf("a credit to the balance on the same invoice: %v", err)
	}
}
