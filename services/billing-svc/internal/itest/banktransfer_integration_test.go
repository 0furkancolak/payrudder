//go:build integration

package itest

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	billingv1 "github.com/0furkancolak/quotify/gen/go/quotify/billing/v1"
	commandsv1 "github.com/0furkancolak/quotify/gen/go/quotify/commands/v1"
	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/errkey"
	"github.com/0furkancolak/quotify/pkg/stubworker"
	"github.com/0furkancolak/quotify/pkg/tenant"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/api"
)

// mine are the commands of this test's tenant that a stub worker received: the queues are shared with the other tests of the package, whose leftovers a stub may take.
func (e *env) mine(stub *stubworker.Worker) []*commandsv1.CommandEnvelope {
	var out []*commandsv1.CommandEnvelope
	for _, c := range stub.Commands() {
		if c.GetTenantId() == e.tid.String() {
			out = append(out, c)
		}
	}
	return out
}

func (e *env) count(stub *stubworker.Worker, kind string) int {
	n := 0
	for _, c := range e.mine(stub) {
		if stubworker.Kind(c) == kind {
			n++
		}
	}
	return n
}

// A bank transfer is waited for: the charge is accepted, the saga rests in PAYMENT_PENDING (no step deadline, so no retry storm and no dead letters), tells the
// customer how to pay once, polls the status on a schedule, is nudged to settle when a receipt arrives, and a pending invoice can be voided.
func testBankTransferSagaWaitsForTheMoney(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.w.tenant.BankTransfer = &plansv1.BankTransferDetails{BankName: "Example Bank", AccountHolder: "Acme Ltd", Iban: "TR330006100519786457841326", Bic: "TCZBTR2A",
		ReferenceTemplate: "{invoice_number}", IsDefault: true}
	var received atomic.Bool
	stub := e.startWorkers(func(c *commandsv1.CommandEnvelope, _ int) stubworker.Action {
		switch stubworker.Kind(c) {
		case "charge":
			return stubworker.Action{Reply: stubworker.Pending}
		case "status":
			if received.Load() {
				return stubworker.Action{Reply: stubworker.Succeed}
			}
			return stubworker.Action{Reply: stubworker.StatusPending}
		}
		return stubworker.Action{Reply: stubworker.Succeed}
	})
	e.addUsage("2026-10", "cus_a", "gb_hours", "1.250", 3)
	if run := e.driveRun("2026-10"); run.Status != "INVOICING" {
		t.Fatalf("run is %s (%s)", run.Status, run.LastError)
	}
	eventually(t, 30*time.Second, "both invoices to wait for their transfer", func() bool {
		s := e.sagaStates("2026-10")
		return s["cus_a"] == "PAYMENT_PENDING" && s["cus_b"] == "PAYMENT_PENDING"
	})

	// The charge carried the invoice number, and the customer was mailed where to pay, once per invoice.
	for _, c := range e.mine(stub) {
		if ch := c.GetChargePayment(); ch != nil && ch.GetInvoiceNumber() == "" {
			t.Errorf("the charge lacks the invoice number: %v", ch)
		}
	}
	mails := 0
	for _, c := range e.mine(stub) {
		if n := c.GetSendNotification(); n != nil {
			mails++
			d := n.GetData()
			if n.GetTemplate() != "invoice_transfer.v1" || d["iban"] != "TR330006100519786457841326" || d["reference"] == "" || d["reference"] != d["invoice_number"] || d["bank_name"] != "Example Bank" {
				t.Errorf("the transfer mail = %v", n)
			}
		}
	}
	if mails != 2 || e.count(stub, "charge") != 2 || e.count(stub, "status") != 2 {
		t.Fatalf("mails %d, charges %d, status queries %d; want 2, 2, 2", mails, e.count(stub, "charge"), e.count(stub, "status"))
	}

	// Nothing is outstanding while the money is awaited: no step deadline, so the sweeper leaves the sagas alone however long it takes.
	var deadlines int
	if err := shared.system.QueryRow(context.Background(), `SELECT count(*) FROM invoice_sagas WHERE tenant_id = $1 AND state = 'PAYMENT_PENDING' AND step_deadline IS NOT NULL`, e.tid).Scan(&deadlines); err != nil || deadlines != 0 {
		t.Fatalf("pending sagas with a step deadline = %d, %v; want none", deadlines, err)
	}
	e.clk.Advance(2 * time.Hour)
	if _, err := e.eng.Sweep(context.Background(), shared.system, 100); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	if n := e.count(stub, "status"); n != 2 {
		t.Fatalf("status queries before the poll is due = %d, want still 2", n)
	}

	// The poll asks again, and the answer is still pending: no second mail, the saga stays.
	e.clk.Advance(5 * time.Hour)
	if res, err := e.eng.Sweep(context.Background(), shared.system, 100); err != nil || res.Polled < 2 {
		t.Fatalf("poll pass: %+v, %v; want at least our 2 polls", res, err)
	}
	eventually(t, 20*time.Second, "the poll to be answered", func() bool { return e.count(stub, "status") == 4 })
	eventually(t, 20*time.Second, "both sagas to wait again", func() bool {
		s := e.sagaStates("2026-10")
		return s["cus_a"] == "PAYMENT_PENDING" && s["cus_b"] == "PAYMENT_PENDING"
	})
	mails = 0
	for _, c := range e.mine(stub) {
		if c.GetSendNotification() != nil {
			mails++
		}
	}
	if mails != 2 {
		t.Fatalf("mails after a poll = %d, want still 2", mails)
	}

	admin := api.New(shared.app, shared.system, e.mgr, e.eng)
	inv := func(customer string) string {
		var id string
		if err := shared.system.QueryRow(context.Background(), `SELECT id::text FROM invoices WHERE tenant_id = $1 AND period_id = '2026-10' AND snapshot->>'customer_external_id' = $2`, e.tid, customer).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}

	// The hosted page of the open invoice says where to pay, with the reference; once paid it does not.
	hosted := api.New(shared.app, shared.system, e.mgr, e.eng)
	mine := tenant.NewContext(context.Background(), tenant.Info{ID: e.tid})
	link, err := hosted.CreateInvoiceLink(mine, &billingv1.CreateInvoiceLinkRequest{InvoiceId: inv("cus_a")})
	if err != nil {
		t.Fatal(err)
	}
	page, err := hosted.GetHostedInvoice(context.Background(), &billingv1.GetHostedInvoiceRequest{Token: link.GetToken()})
	in := page.GetPaymentInstructions()
	if err != nil || in.GetIban() != "TR330006100519786457841326" || in.GetBankName() != "Example Bank" || in.GetReference() == "" || in.GetReference() != page.GetInvoice().GetNumber() {
		t.Fatalf("hosted page instructions = %v, %v; want the bank details and the invoice number as the reference", in, err)
	}

	// A receipt arrives for cus_a: payments-worker nudges the saga (Retry), which asks the status at once instead of waiting for the next poll.
	received.Store(true)
	if _, err := admin.RetryInvoiceSaga(opCtx(), &billingv1.RetryInvoiceSagaRequest{TenantId: e.tid.String(), InvoiceId: inv("cus_a")}); err != nil {
		t.Fatal(err)
	}
	eventually(t, 30*time.Second, "cus_a to be paid and completed", func() bool { return e.sagaStates("2026-10")["cus_a"] == "COMPLETED" })

	// cus_b will never pay: the invoice is voided straight from PAYMENT_PENDING.
	if _, err := admin.VoidInvoice(opCtx(), &billingv1.VoidInvoiceRequest{TenantId: e.tid.String(), InvoiceId: inv("cus_b")}); err != nil {
		t.Fatal(err)
	}
	eventually(t, 20*time.Second, "cus_b to be voided", func() bool { return e.sagaStates("2026-10")["cus_b"] == "VOIDED" })
	if page, err := hosted.GetHostedInvoice(context.Background(), &billingv1.GetHostedInvoiceRequest{Token: link.GetToken()}); err != nil || page.GetPaymentInstructions() != nil {
		t.Errorf("a paid invoice still shows where to pay: %v, %v", page.GetPaymentInstructions(), err)
	}
	var nstatus string
	if err := shared.system.QueryRow(context.Background(), `SELECT status FROM invoices WHERE tenant_id = $1 AND id = $2::uuid`, e.tid, inv("cus_a")).Scan(&nstatus); err != nil || nstatus != "paid" {
		t.Fatalf("cus_a invoice status = %q, %v", nstatus, err)
	}
	// A paid invoice is not nudged back to life.
	if _, err := admin.RetryInvoiceSaga(opCtx(), &billingv1.RetryInvoiceSagaRequest{TenantId: e.tid.String(), InvoiceId: inv("cus_a")}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("nudging a completed saga: %v, want FailedPrecondition", err)
	}
}

// A transfer that never arrives is declined by the status answer; the ordinary dunning path then decides: a new payment window with a new mail, then uncollectible.
func testExpiredTransferFollowsDunning(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.w.tenant.BankTransfer = &plansv1.BankTransferDetails{BankName: "B", AccountHolder: "H", Iban: "TR330006100519786457841326", IsDefault: true}
	stub := e.startWorkers(func(c *commandsv1.CommandEnvelope, _ int) stubworker.Action {
		switch stubworker.Kind(c) {
		case "charge":
			return stubworker.Action{Reply: stubworker.Pending}
		case "status":
			return stubworker.Action{Reply: func(*commandsv1.CommandEnvelope) *commandsv1.ReplyEnvelope {
				return &commandsv1.ReplyEnvelope{Reply: &commandsv1.ReplyEnvelope_PaymentStatus{PaymentStatus: &commandsv1.PaymentStatus{
					State: commandsv1.PaymentStatus_STATE_DECLINED, DeclineCode: "transfer_expired"}}}
			}}
		}
		return stubworker.Action{Reply: stubworker.Succeed}
	})
	svc := api.New(shared.app, shared.system, e.mgr, e.eng)
	tctx := tenant.NewContext(context.Background(), tenant.Info{ID: e.tid})
	if _, err := svc.SetDunningSettings(tctx, &billingv1.SetDunningSettingsRequest{RetryHours: []int32{2}}); err != nil {
		t.Fatal(err)
	}
	e.addUsage("2026-10", "cus_a", "gb_hours", "1.000", 3)
	e.driveRun("2026-10")
	// The window ran out as soon as the first status was asked (the stub says so): the invoice goes to dunning, not to a parked state.
	eventually(t, 30*time.Second, "both invoices to be declined", func() bool {
		s := e.sagaStates("2026-10")
		return s["cus_a"] == "PAYMENT_DECLINED" && s["cus_b"] == "PAYMENT_DECLINED"
	})
	e.clk.Advance(2*time.Hour + time.Minute)
	if res, err := e.eng.Sweep(context.Background(), shared.system, 100); err != nil || res.Dunned < 2 {
		t.Fatalf("dunning pass: %+v, %v; want at least our 2 retries (each opens a new payment window)", res, err)
	}
	eventually(t, 30*time.Second, "uncollectible after the only retry", func() bool {
		var n int
		err := shared.system.QueryRow(context.Background(), `SELECT count(*) FROM invoices WHERE tenant_id = $1 AND status = 'uncollectible'`, e.tid).Scan(&n)
		return err == nil && n == 2
	})
	if n := e.count(stub, "charge"); n != 4 { // 2 invoices × (the first charge and the retry)
		t.Fatalf("charge commands = %d, want 4", n)
	}
	// Each charge told the customer how to pay again; the give-up notice repeats the bank details.
	var withBank int
	for _, c := range e.mine(stub) {
		if n := c.GetSendNotification(); n != nil && n.GetTemplate() == "payment_failed.v1" && n.GetData()["iban"] != "" {
			withBank++
		}
	}
	if withBank != 2 {
		t.Fatalf("give-up notices with the bank details = %d, want 2", withBank)
	}
}

// Money received outside a provider settles an invoice that was never charged through Quotify (here: its card payment was declined), with who and when
// recorded; partial payments add up, an overpayment is refused with an error key, and a repeat is a no-op.
func testOfflinePaymentsSettleAnInvoice(t *testing.T) {
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
	svc := api.New(shared.app, shared.system, e.mgr, e.eng)
	var id string
	if err := shared.system.QueryRow(context.Background(), `SELECT id::text FROM invoices WHERE tenant_id = $1 AND snapshot->>'customer_external_id' = 'cus_b'`, e.tid).Scan(&id); err != nil {
		t.Fatal(err)
	}
	total := e.invoiceTotal("2026-10", "cus_b")
	record := func(method string, amount int64, ref string) (*billingv1.RecordInvoicePaymentResponse, error) {
		return svc.RecordInvoicePayment(opCtx(), &billingv1.RecordInvoicePaymentRequest{TenantId: e.tid.String(), InvoiceId: id, Method: method, AmountMinor: amount, Reference: ref,
			ReceivedAt: timestamppb.New(e.clk.Now().Add(-time.Hour)), Note: "paid at the desk", Actor: "alice@example.com"})
	}
	wantKey := func(err error, code codes.Code, key string) {
		t.Helper()
		if status.Code(err) != code || errkey.Of(err) != key {
			t.Fatalf("error = %v (key %q), want %s %s", err, errkey.Of(err), code, key)
		}
	}

	_, err := record("barter", 100, "x")
	wantKey(err, codes.InvalidArgument, "billing.payment.bad_method")
	_, err = record("cash", 0, "x")
	wantKey(err, codes.InvalidArgument, "billing.payment.bad_amount")
	_, err = svc.RecordInvoicePayment(opCtx(), &billingv1.RecordInvoicePaymentRequest{TenantId: e.tid.String(), InvoiceId: id, Method: "cash", AmountMinor: 1,
		ReceivedAt: timestamppb.New(e.clk.Now().Add(time.Hour))})
	wantKey(err, codes.InvalidArgument, "billing.payment.bad_date")

	// A part payment is recorded; the invoice stays unpaid and the dunning schedule keeps running.
	part, err := record("bank_transfer", 400, "BANK-1")
	if err != nil || part.GetSettled() || part.GetPaidMinor() != 400 || part.GetOutstandingMinor() != total-400 || part.GetDuplicate() {
		t.Fatalf("part payment = %+v, %v", part, err)
	}
	if again, err := record("bank_transfer", 400, "BANK-1"); err != nil || !again.GetDuplicate() || again.GetPayment().GetId() != part.GetPayment().GetId() || again.GetPaidMinor() != 400 {
		t.Fatalf("the same payment again = %+v, %v; want the first one back", again, err)
	}
	_, err = record("bank_transfer", 401, "BANK-1")
	wantKey(err, codes.FailedPrecondition, "billing.payment.reference_reused")
	_, err = record("cash", total-400+1, "")
	wantKey(err, codes.FailedPrecondition, "billing.payment.overpayment")
	if s := e.sagaStates("2026-10")["cus_b"]; s != "PAYMENT_DECLINED" {
		t.Fatalf("after a part payment the saga is %s, want PAYMENT_DECLINED still", s)
	}

	// The rest settles it: the saga is told in the same transaction, the invoice is paid and the customer gets the receipt.
	done, err := record("cash", total-400, "")
	if err != nil || !done.GetSettled() || done.GetOutstandingMinor() != 0 || done.GetPaidMinor() != total {
		t.Fatalf("settling payment = %+v, %v", done, err)
	}
	eventually(t, 30*time.Second, "cus_b to complete", func() bool { return e.sagaStates("2026-10")["cus_b"] == "COMPLETED" })
	var st string
	if err := shared.system.QueryRow(context.Background(), `SELECT status FROM invoices WHERE id = $1::uuid`, id).Scan(&st); err != nil || st != "paid" {
		t.Fatalf("invoice status = %q, %v", st, err)
	}
	receipts := 0
	for _, c := range e.mine(stub) {
		if n := c.GetSendNotification(); n != nil && n.GetTemplate() == "invoice_paid.v1" && n.GetData()["customer_name"] == "Customer cus_b" {
			receipts++
		}
	}
	if receipts != 1 {
		t.Fatalf("receipts mailed to cus_b = %d, want 1", receipts)
	}
	_, err = record("cash", 1, "late")
	wantKey(err, codes.FailedPrecondition, "billing.payment.already_paid")

	// Who and when are on the invoice.
	tctx := tenant.NewContext(context.Background(), tenant.Info{ID: e.tid})
	got, err := svc.GetInvoice(tctx, &billingv1.GetInvoiceRequest{InvoiceId: id})
	if err != nil || len(got.GetInvoice().GetPayments()) != 2 {
		t.Fatalf("invoice payments = %+v, %v; want 2", got.GetInvoice().GetPayments(), err)
	}
	p := got.GetInvoice().GetPayments()[0]
	if p.GetReference() != "BANK-1" {
		p = got.GetInvoice().GetPayments()[1] // both were received at the same minute of the test clock
	}
	if p.GetRecordedBy() != "alice@example.com" || p.GetMethod() != "bank_transfer" || p.GetAmountMinor() != 400 || p.GetReference() != "BANK-1" || p.GetNote() != "paid at the desk" ||
		p.GetReceivedAt().AsTime().After(e.clk.Now()) || p.GetRecordedAt() == nil {
		t.Errorf("payment = %+v", p)
	}
	// A card-declined sibling invoice that nobody paid is untouched, and a tenant cannot record a payment on another tenant's invoice.
	if s := e.sagaStates("2026-10")["cus_a"]; s != "PAYMENT_DECLINED" {
		t.Errorf("cus_a saga = %s, want PAYMENT_DECLINED", s)
	}
	other := newEnv(t, envOpts{})
	_, err = svc.RecordInvoicePayment(opCtx(), &billingv1.RecordInvoicePaymentRequest{TenantId: other.tid.String(), InvoiceId: id, Method: "cash", AmountMinor: 1})
	wantKey(err, codes.NotFound, "billing.invoice.not_found")
	if _, err := svc.RecordInvoicePayment(context.Background(), &billingv1.RecordInvoicePaymentRequest{TenantId: e.tid.String(), InvoiceId: id, Method: "cash", AmountMinor: 1}); status.Code(err) != codes.PermissionDenied {
		t.Errorf("without the operator flag: %v, want PermissionDenied", err)
	}
}
