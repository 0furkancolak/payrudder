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
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/stubworker"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/api"
	paymentscmd "github.com/0furkancolak/payrudder/services/billing-svc/internal/payments/command"
)

func (e *env) invoiceIDOf(customer string) uuid.UUID {
	e.t.Helper()
	var id uuid.UUID
	if err := shared.system.QueryRow(context.Background(), `SELECT id FROM invoices WHERE tenant_id = $1 AND snapshot->>'customer_external_id' = $2 ORDER BY created_at LIMIT 1`,
		e.tid, customer).Scan(&id); err != nil {
		e.t.Fatal(err)
	}
	return id
}

// chargeAmounts are the amounts of the ChargePayment commands of an invoice by dunning attempt.
func (e *env) chargeAmounts(stub *stubworker.Worker, invoice uuid.UUID) map[int32]int64 {
	out := map[int32]int64{}
	for _, c := range e.mine(stub) {
		if ch := c.GetChargePayment(); ch != nil && ch.GetInvoiceId() == invoice.String() {
			out[ch.GetDunningAttempt()] = ch.GetAmountMinor()
		}
	}
	return out
}

// A retry charge asks for what is still outstanding: a part payment recorded by hand is not charged a second time.
func testDunningChargesWhatIsOutstandingAfterAPartPayment(t *testing.T) {
	e := newEnv(t, envOpts{})
	stub := e.startWorkers(func(c *commandsv1.CommandEnvelope, _ int) stubworker.Action {
		if stubworker.Kind(c) == "charge" {
			return stubworker.Action{Reply: stubworker.Declined}
		}
		return stubworker.Action{Reply: stubworker.Succeed}
	})
	e.addUsage("2026-10", "cus_a", "gb_hours", "1.000", 3)
	e.driveRun("2026-10")
	eventually(t, 30*time.Second, "both invoices to be declined", func() bool {
		s := e.sagaStates("2026-10")
		return s["cus_a"] == "PAYMENT_DECLINED" && s["cus_b"] == "PAYMENT_DECLINED"
	})
	id, total := e.invoiceIDOf("cus_a"), e.invoiceTotal("2026-10", "cus_a")
	if _, err := paymentscmd.Record(context.Background(), shared.system, e.eng, paymentscmd.Payment{
		Tenant: e.tid, Invoice: id, Method: "bank_transfer", AmountMinor: 600, Reference: "BANK-1", Actor: "op"}); err != nil {
		t.Fatal(err)
	}
	e.clk.Advance(24*time.Hour + time.Minute)
	if res, err := e.eng.Sweep(context.Background(), shared.system, 100); err != nil || res.Dunned < 1 {
		t.Fatalf("dunning pass: %+v, %v", res, err)
	}
	eventually(t, 30*time.Second, "the retry charge", func() bool { return len(e.chargeAmounts(stub, id)) == 2 })
	got := e.chargeAmounts(stub, id)
	if got[0] != total || got[1] != total-600 {
		t.Fatalf("charges by dunning attempt = %v; want the full %d, then the outstanding %d", got, total, total-600)
	}
}

// A void refunds what the credit notes left: a note already gave part of the invoice back, and no new refund note can start while the void is under way.
func testVoidRefundsWhatCreditNotesLeft(t *testing.T) {
	e := newEnv(t, envOpts{})
	var silent uuid.UUID
	stub := e.startWorkers(func(c *commandsv1.CommandEnvelope, _ int) stubworker.Action {
		if stubworker.Kind(c) == "refund" && c.GetRefundPayment().GetInvoiceId() == silent.String() {
			return stubworker.Action{} // never answered: the saga stays in REFUND_REQUESTED
		}
		return stubworker.Action{Reply: stubworker.Succeed}
	})
	completeOctober(e)
	svc := api.New(shared.app, shared.system, e.mgr, e.eng)
	note := func(inv uuid.UUID, amount int64) (*billingv1.CreateCreditNoteResponse, error) {
		return svc.CreateCreditNote(opCtx(), &billingv1.CreateCreditNoteRequest{TenantId: e.tid.String(), InvoiceId: inv.String(), AmountMinor: amount, RefundToCard: true})
	}
	void := func(inv uuid.UUID) {
		t.Helper()
		if _, err := svc.VoidInvoice(opCtx(), &billingv1.VoidInvoiceRequest{TenantId: e.tid.String(), InvoiceId: inv.String(), Reason: "test"}); err != nil {
			t.Fatalf("VoidInvoice: %v", err)
		}
	}
	refunds := func(inv uuid.UUID) (amounts []int64) {
		for _, c := range e.mine(stub) {
			if r := c.GetRefundPayment(); r != nil && r.GetInvoiceId() == inv.String() && r.GetIdempotencyKey() == inv.String()+":refund" {
				amounts = append(amounts, r.GetAmountMinor())
			}
		}
		return amounts
	}

	// 400 went back to the card as a credit note; the void refunds the rest, not the whole invoice again.
	a, aTotal := e.invoiceIDOf("cus_a"), e.invoiceTotal("2026-10", "cus_a")
	if _, err := note(a, 400); err != nil {
		t.Fatal(err)
	}
	void(a)
	eventually(t, 30*time.Second, "cus_a to be voided", func() bool { return e.sagaStates("2026-10")["cus_a"] == "VOIDED" })
	if got := refunds(a); len(got) != 1 || got[0] != aTotal-400 {
		t.Fatalf("void refund commands = %v; want one of %d", got, aTotal-400)
	}

	// While the void waits for its refund, a refund credit note would send a second refund.
	b := e.invoiceIDOf("cus_b")
	silent = b
	void(b)
	eventually(t, 30*time.Second, "cus_b to wait for its refund", func() bool { return e.sagaStates("2026-10")["cus_b"] == "REFUND_REQUESTED" })
	if _, err := note(b, 100); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("credit note while voiding: %v, want FAILED_PRECONDITION", err)
	}

	// An invoice credited in full has nothing left to refund: it is voided at once, no refund is asked for.
	full := seedOne(t, e.tid, uuid.New(), "cus_full", "paid", "USD", "INV-2026-000777", 500, "live")
	seedSaga(t, e.tid, full)
	if _, err := svc.CreateCreditNote(opCtx(), &billingv1.CreateCreditNoteRequest{TenantId: e.tid.String(), InvoiceId: full.String()}); err != nil {
		t.Fatal(err)
	}
	void(full)
	var state string
	if err := shared.system.QueryRow(context.Background(), `SELECT state FROM invoice_sagas WHERE tenant_id = $1 AND invoice_id = $2`, e.tid, full).Scan(&state); err != nil || state != "VOIDED" {
		t.Fatalf("fully credited invoice saga = %q, %v; want VOIDED", state, err)
	}
	if got := refunds(full); len(got) != 0 {
		t.Fatalf("a fully credited invoice asked for refunds %v", got)
	}
}

// The credit a draft consumed comes back when the invoice is voided: the same invoice, made again, can use it.
func testVoidingAnInvoiceReopensTheCreditsItConsumed(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.startWorkers(nil)
	lot := uuid.New()
	if _, err := shared.system.Exec(context.Background(), `INSERT INTO credit_balances (tenant_id, customer_id, id, amount_minor, source_adjustment_id, currency) VALUES ($1,$2,$3,300,$4,'USD')`,
		e.tid, e.customer("cus_a").Id, lot, uuid.New()); err != nil {
		t.Fatal(err)
	}
	completeOctober(e)
	remaining := func() (applied bool, apps int, left int64) {
		t.Helper()
		var inv *uuid.UUID
		if err := shared.system.QueryRow(context.Background(), `SELECT applied_invoice_id FROM credit_balances WHERE tenant_id = $1 AND id = $2`, e.tid, lot).Scan(&inv); err != nil {
			t.Fatal(err)
		}
		if err := shared.system.QueryRow(context.Background(), `SELECT count(*), 300 - COALESCE(sum(amount_minor), 0) FROM credit_applications WHERE tenant_id = $1 AND credit_id = $2`, e.tid, lot).Scan(&apps, &left); err != nil {
			t.Fatal(err)
		}
		return inv != nil, apps, left
	}
	if applied, apps, left := remaining(); !applied || apps != 1 || left != 0 {
		t.Fatalf("before the void: applied %v, %d applications, %d left; want the lot consumed by the invoice", applied, apps, left)
	}
	svc := api.New(shared.app, shared.system, e.mgr, e.eng)
	if _, err := svc.VoidInvoice(opCtx(), &billingv1.VoidInvoiceRequest{TenantId: e.tid.String(), InvoiceId: e.invoiceIDOf("cus_a").String(), Reason: "wrong"}); err != nil {
		t.Fatal(err)
	}
	eventually(t, 30*time.Second, "cus_a to be voided", func() bool { return e.sagaStates("2026-10")["cus_a"] == "VOIDED" })
	if applied, apps, left := remaining(); applied || apps != 0 || left != 300 {
		t.Fatalf("after the void: applied %v, %d applications, %d left; want the 300 open again", applied, apps, left)
	}
}

// The unused time of the old plan lowers what is taxed, like a discount: 20 % of 1000 - 800, not of 1000.
func testPlanChangeTaxIsOnWhatIsLeftAfterTheUnusedTime(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.startWorkers(nil)
	e.customer("cus_a").Country = "DE"
	e.setTax(&plansv1.TaxRate{Country: "DE", Name: "VAT", RateBps: 2000})
	e.adjustment("cus_a", e.pv(), 800, 1000)
	if err := e.mgr.BillFees(context.Background()); err != nil {
		t.Fatal(err)
	}
	l := e.feeInvoiceLines("cus_a")
	if len(l) != 3 || l[0].Kind != "BASE_FEE" || l[0].Amount != 1000 || l[1].Amount != -800 || l[2].Kind != "TAX" || l[2].Amount != 40 {
		t.Fatalf("lines = %+v; want fee 1000, unused time -800, tax 40", l)
	}
	if n, total, _ := e.feeInvoices("cus_a"); n != 1 || total != 240 {
		t.Fatalf("%d invoice(s) totalling %d; want one of 240", n, total)
	}
}
