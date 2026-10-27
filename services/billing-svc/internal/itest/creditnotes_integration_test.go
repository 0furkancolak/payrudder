//go:build integration

package itest

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
	commandsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/commands/v1"
	"github.com/0furkancolak/payrudder/pkg/stubworker"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/pkg/testkit"
)

var seededInvoices int

func seedOne(t *testing.T, tid, customer uuid.UUID, ext, status, currency, number string, total int64, env string) uuid.UUID {
	t.Helper()
	seededInvoices++
	period := fmt.Sprintf("2025-%02d", seededInvoices%12+1) // one regular invoice per customer and period
	id := uuid.New()
	if _, err := shared.sql.Exec(context.Background(), `INSERT INTO invoices (tenant_id, id, customer_id, period_id, kind, number, plan_version_id, currency, subtotal_minor, total_minor, status, snapshot, provider)
		VALUES ($1,$2,$3,$10,'REGULAR',$4,$5,$6,$7,$7,$8,$9::jsonb,'')`,
		tid, id, customer, number, uuid.New(), currency, total, status, `{"customer_external_id":"`+ext+`","environment":"`+env+`"}`, period); err != nil {
		t.Fatal(err)
	}
	return id
}

func testCreditNotes(t *testing.T) {
	e := newEnv(t, envOpts{})
	svc := e.api()
	op := tenant.WithOperator(context.Background())
	tctx := tenant.NewContext(context.Background(), tenant.Info{ID: e.tid})
	customer := uuid.New()
	paid := seedOne(t, e.tid, customer, "cus_cn", "paid", "USD", "INV-2026-000001", 1000, "live")
	open := seedOne(t, e.tid, customer, "cus_cn", "open", "USD", "INV-2026-000002", 500, "live")
	code := func(err error) codes.Code { return status.Code(err) }
	create := func(inv uuid.UUID, amount int64, reason string) (*billingv1.CreditNote, error) {
		r, err := svc.CreateCreditNote(op, &billingv1.CreateCreditNoteRequest{TenantId: e.tid.String(), InvoiceId: inv.String(), AmountMinor: amount, Reason: reason})
		return r.GetCreditNote(), err
	}

	first, err := create(paid, 300, "goodwill")
	if err != nil {
		t.Fatal(err)
	}
	if first.GetNumber() != "CN-2026-000001" || first.GetTotalMinor() != 300 || first.GetInvoiceNumber() != "INV-2026-000001" || first.GetCustomerExternalId() != "cus_cn" || first.GetReason() != "goodwill" {
		t.Fatalf("first = %v", first)
	}
	e.clk.Advance(time.Second) // the list is newest first, by time
	second, err := create(paid, 0, "")
	if err != nil || second.GetTotalMinor() != 700 || second.GetNumber() != "CN-2026-000002" {
		t.Fatalf("the rest of the invoice = %v, %v", second, err)
	}
	if _, err := create(paid, 1, ""); code(err) != codes.FailedPrecondition {
		t.Errorf("a fully credited invoice: %v", err)
	}
	if _, err := create(open, 100, ""); code(err) != codes.FailedPrecondition {
		t.Errorf("an open invoice: %v", err)
	}
	other := seedOne(t, e.tid, customer, "cus_cn", "paid", "USD", "INV-2026-000003", 400, "live")
	if _, err := create(other, 401, ""); code(err) != codes.InvalidArgument {
		t.Errorf("more than the invoice: %v", err)
	}
	if _, err := create(other, -5, ""); code(err) != codes.InvalidArgument {
		t.Errorf("a negative amount: %v", err)
	}
	if _, err := create(uuid.New(), 1, ""); code(err) != codes.NotFound {
		t.Errorf("an unknown invoice: %v", err)
	}
	if _, err := svc.CreateCreditNote(tctx, &billingv1.CreateCreditNoteRequest{TenantId: e.tid.String(), InvoiceId: paid.String()}); err == nil {
		t.Error("a tenant, not an operator, issued a credit note")
	}

	// The amounts are in the customer's credit balance, in the invoice's currency.
	var n int
	var sum int64
	var cur string
	if err := shared.sql.QueryRow(context.Background(), `SELECT count(*), sum(amount_minor), max(currency) FROM credit_balances WHERE tenant_id = $1 AND customer_id = $2`, e.tid, customer).Scan(&n, &sum, &cur); err != nil {
		t.Fatal(err)
	}
	if n != 2 || sum != 1000 || cur != "USD" {
		t.Errorf("credit balances = %d × %d %s; want two summing to 1000 USD", n, sum, cur)
	}

	// Reads: one note, the list (newest first, paged), by invoice.
	got, err := svc.GetCreditNote(tctx, &billingv1.GetCreditNoteRequest{CreditNoteId: first.GetId()})
	if err != nil || got.GetCreditNote().GetNumber() != first.GetNumber() {
		t.Fatalf("get = %v, %v", got, err)
	}
	page, err := svc.ListCreditNotes(tctx, &billingv1.ListCreditNotesRequest{PageSize: 1})
	if err != nil || len(page.GetCreditNotes()) != 1 || page.GetCreditNotes()[0].GetNumber() != "CN-2026-000002" || page.GetNextPageToken() == "" {
		t.Fatalf("first page = %v, %v", page, err)
	}
	next, err := svc.ListCreditNotes(tctx, &billingv1.ListCreditNotesRequest{PageSize: 1, PageToken: page.GetNextPageToken()})
	if err != nil || len(next.GetCreditNotes()) != 1 || next.GetCreditNotes()[0].GetNumber() != "CN-2026-000001" || next.GetNextPageToken() != "" {
		t.Fatalf("second page = %v, %v", next, err)
	}
	if byInvoice, err := svc.ListCreditNotes(tctx, &billingv1.ListCreditNotesRequest{InvoiceId: other.String()}); err != nil || len(byInvoice.GetCreditNotes()) != 0 {
		t.Errorf("notes of an invoice with none = %v, %v", byInvoice, err)
	}
	if byCustomer, err := svc.ListCreditNotes(tctx, &billingv1.ListCreditNotesRequest{CustomerExternalId: "cus_cn"}); err != nil || len(byCustomer.GetCreditNotes()) != 2 {
		t.Errorf("notes of a customer = %v, %v", byCustomer, err)
	}
	if _, err := svc.GetCreditNote(context.Background(), &billingv1.GetCreditNoteRequest{CreditNoteId: first.GetId()}); err == nil {
		t.Error("a read without a tenant")
	}
	// Another tenant does not see them.
	stranger := tenant.NewContext(context.Background(), tenant.Info{ID: uuid.New()})
	if _, err := svc.GetCreditNote(stranger, &billingv1.GetCreditNoteRequest{CreditNoteId: first.GetId()}); code(err) != codes.NotFound {
		t.Errorf("another tenant reads the note: %v", err)
	}

	// A sandbox tenant's notes cannot be mistaken for real ones.
	sb := seedOne(t, e.tid, customer, "cus_cn", "paid", "USD", "SBX-2026-000001", 100, "sandbox")
	if sn, err := create(sb, 0, ""); err != nil || sn.GetNumber() != "SBX-CN-2026-000003" {
		t.Errorf("sandbox note = %v, %v", sn, err)
	}

	// The revenue report shows what was credited.
	rep, err := svc.GetRevenueReport(tctx, &billingv1.GetRevenueReportRequest{})
	if err != nil {
		t.Fatal(err)
	}
	var credited int64
	for _, r := range rep.GetRows() {
		credited += r.GetCreditedMinor()
	}
	if credited != 1100 {
		t.Errorf("credited in the report = %d; want 1100", credited)
	}
}

// seedSaga gives a seeded paid invoice the (completed) saga a refund command is sent on behalf of.
func seedSaga(t *testing.T, tid, invoice uuid.UUID) {
	t.Helper()
	if _, err := shared.sql.Exec(context.Background(), `INSERT INTO invoice_sagas (tenant_id, id, invoice_id, state, version) VALUES ($1,$2,$3,'COMPLETED',1)`, tid, uuid.New(), invoice); err != nil {
		t.Fatal(err)
	}
}

func testCreditNoteRefundToCard(t *testing.T) {
	e := newEnv(t, envOpts{})
	// The refund of "failing" is rejected the first time; the one of "silent" is never answered the first two times.
	var failing, silent uuid.UUID
	stub := e.startWorkers(func(c *commandsv1.CommandEnvelope, _ int) stubworker.Action {
		if stubworker.Kind(c) != "refund" {
			return stubworker.Action{Reply: stubworker.Succeed}
		}
		rf := c.GetRefundPayment()
		switch {
		case rf.GetInvoiceId() == failing.String() && !strings.HasSuffix(rf.GetIdempotencyKey(), ":1"):
			return stubworker.Action{Reply: stubworker.Errored}
		case rf.GetInvoiceId() == silent.String() && c.GetAttempt() < 3:
			return stubworker.Action{}
		}
		return stubworker.Action{Reply: stubworker.Succeed}
	})
	svc := e.api()
	op := tenant.WithOperator(context.Background())
	tctx := tenant.NewContext(context.Background(), tenant.Info{ID: e.tid})
	customer := uuid.New()
	code := func(err error) codes.Code { return status.Code(err) }
	refund := func(inv uuid.UUID, amount int64) (*billingv1.CreditNote, error) {
		r, err := svc.CreateCreditNote(op, &billingv1.CreateCreditNoteRequest{TenantId: e.tid.String(), InvoiceId: inv.String(), AmountMinor: amount, RefundToCard: true})
		return r.GetCreditNote(), err
	}
	state := func(id string) billingv1.RefundState {
		r, err := svc.GetCreditNote(tctx, &billingv1.GetCreditNoteRequest{CreditNoteId: id})
		if err != nil {
			t.Fatal(err)
		}
		return r.GetCreditNote().GetRefundState()
	}

	// A refund asks the provider for the money and puts nothing on the credit balance.
	ok := seedOne(t, e.tid, customer, "cus_rf", "paid", "USD", "INV-2026-000101", 1000, "live")
	seedSaga(t, e.tid, ok)
	cn, err := refund(ok, 400)
	if err != nil || cn.GetDisposition() != billingv1.CreditNoteDisposition_CREDIT_NOTE_DISPOSITION_REFUND || cn.GetRefundState() != billingv1.RefundState_REFUND_STATE_REQUESTED {
		t.Fatalf("refund = %v, %v", cn, err)
	}
	testkit.Eventually(t, 30*time.Second, "the refund to be confirmed", func() bool { return state(cn.GetId()) == billingv1.RefundState_REFUND_STATE_REFUNDED })
	var balances int
	if err := shared.sql.QueryRow(context.Background(), `SELECT count(*) FROM credit_balances WHERE tenant_id = $1 AND source_credit_note_id = $2`, e.tid, cn.GetId()).Scan(&balances); err != nil || balances != 0 {
		t.Fatalf("a refunded note left %d credit balances, %v", balances, err)
	}
	if n := stub.Count("refund"); n != 1 {
		t.Fatalf("refund commands = %d, want 1", n)
	}
	// The refunded part counts against the invoice like any credit note.
	if _, err := refund(ok, 700); code(err) != codes.InvalidArgument {
		t.Fatalf("refund above what is left: %v", err)
	}

	// A refund the provider rejects fails the note; a retry asks with a new key and goes through.
	bad := seedOne(t, e.tid, customer, "cus_rf2", "paid", "USD", "INV-2026-000102", 500, "live")
	failing = bad
	seedSaga(t, e.tid, bad)
	fcn, err := refund(bad, 0)
	if err != nil {
		t.Fatal(err)
	}
	testkit.Eventually(t, 30*time.Second, "the rejected refund to fail", func() bool { return state(fcn.GetId()) == billingv1.RefundState_REFUND_STATE_FAILED })
	if _, err := svc.RetryCreditNoteRefund(op, &billingv1.RetryCreditNoteRefundRequest{TenantId: e.tid.String(), CreditNoteId: ok.String()}); code(err) != codes.NotFound {
		t.Errorf("retry of an invoice id: %v", err)
	}
	if _, err := svc.RetryCreditNoteRefund(op, &billingv1.RetryCreditNoteRefundRequest{TenantId: e.tid.String(), CreditNoteId: cn.GetId()}); code(err) != codes.FailedPrecondition {
		t.Errorf("retry of a refund that went through: %v", err)
	}
	if _, err := svc.RetryCreditNoteRefund(op, &billingv1.RetryCreditNoteRefundRequest{TenantId: e.tid.String(), CreditNoteId: fcn.GetId()}); err != nil {
		t.Fatal(err)
	}
	testkit.Eventually(t, 30*time.Second, "the retried refund to go through", func() bool { return state(fcn.GetId()) == billingv1.RefundState_REFUND_STATE_REFUNDED })

	// A refund nobody answers is sent again, with the same key, until it is answered.
	quiet := seedOne(t, e.tid, customer, "cus_rf3", "paid", "USD", "INV-2026-000103", 300, "live")
	silent = quiet
	seedSaga(t, e.tid, quiet)
	scn, err := refund(quiet, 0)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		testkit.Eventually(t, 20*time.Second, "the unanswered command to arrive", func() bool { return stub.Count("refund") >= 4+i })
		e.clk.Advance(6 * time.Minute)
		if err := e.notes.SweepRefunds(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	testkit.Eventually(t, 30*time.Second, "the resent refund to be confirmed", func() bool { return state(scn.GetId()) == billingv1.RefundState_REFUND_STATE_REFUNDED })
	keys := map[string]int{}
	for _, c := range stub.Commands() {
		if rf := c.GetRefundPayment(); rf != nil && rf.GetInvoiceId() == quiet.String() {
			keys[rf.GetIdempotencyKey()]++
		}
	}
	if len(keys) != 1 {
		t.Errorf("a resent refund used %d idempotency keys: %v, want one", len(keys), keys)
	}
}
