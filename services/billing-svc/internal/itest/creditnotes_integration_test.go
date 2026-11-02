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
	"github.com/0furkancolak/payrudder/pkg/audit"
	"github.com/0furkancolak/payrudder/pkg/stubworker"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/pkg/testkit"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/auditlog"
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

	// Each note left an entry in the audit log, written in its transaction, with the actor of the request and no more than the note's own facts.
	logged, err := auditlog.Source{DB: shared.system}.Tenant(context.Background(), audit.Filter{TenantID: e.tid, Actions: []string{"credit_note.*"}})
	if err != nil || len(logged) != 2 {
		t.Fatalf("audit entries of the two notes = %d, %v", len(logged), err)
	}
	if ev := logged[1]; ev.Action != "credit_note.issued" || ev.TargetID != first.GetId() || ev.TenantID == nil || *ev.TenantID != e.tid || ev.ActorType != audit.ActorSystem ||
		!strings.Contains(ev.Summary, first.GetNumber()) || !strings.Contains(string(ev.Detail), `"amount_minor": 300`) || !strings.Contains(string(ev.Detail), `"refund": false`) {
		t.Errorf("the first note's entry = %+v (detail %s)", ev, ev.Detail)
	}
	if _, err := shared.sql.Exec(context.Background(), `DELETE FROM audit_events`); err == nil {
		t.Error("audit entries can be deleted")
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
	// The refund of "failing" is rejected the first time; the one of "silent" is never answered the first two times; the one of "unsure" times out the first time.
	var failing, silent, unsure uuid.UUID
	errored := func(code string) func(*commandsv1.CommandEnvelope) *commandsv1.ReplyEnvelope {
		return stubworker.Fixed(&commandsv1.ReplyEnvelope{Reply: &commandsv1.ReplyEnvelope_PaymentErrored{PaymentErrored: &commandsv1.PaymentErrored{Code: code, Message: "stub"}}})
	}
	stub := e.startWorkers(func(c *commandsv1.CommandEnvelope, seen int) stubworker.Action {
		if stubworker.Kind(c) != "refund" {
			return stubworker.Action{Reply: stubworker.Succeed}
		}
		rf := c.GetRefundPayment()
		switch {
		case rf.GetInvoiceId() == failing.String() && !strings.HasSuffix(rf.GetIdempotencyKey(), ":1"):
			return stubworker.Action{Reply: errored("REJECTED")}
		case rf.GetInvoiceId() == unsure.String() && seen == 0:
			return stubworker.Action{Reply: errored("TIMEOUT")}
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

	// A refund whose outcome is unknown (the provider timed out, or payments-worker will not repeat a refund it cannot vouch for) fails the note, and the operator's
	// retry asks under the same key: the provider may have made it, and a new key would refund twice.
	maybe := seedOne(t, e.tid, customer, "cus_rf4", "paid", "USD", "INV-2026-000104", 200, "live")
	unsure = maybe
	seedSaga(t, e.tid, maybe)
	ucn, err := refund(maybe, 0)
	if err != nil {
		t.Fatal(err)
	}
	testkit.Eventually(t, 30*time.Second, "the timed-out refund to fail", func() bool { return state(ucn.GetId()) == billingv1.RefundState_REFUND_STATE_FAILED })
	if _, err := svc.RetryCreditNoteRefund(op, &billingv1.RetryCreditNoteRefundRequest{TenantId: e.tid.String(), CreditNoteId: ucn.GetId()}); err != nil {
		t.Fatal(err)
	}
	testkit.Eventually(t, 30*time.Second, "the retried refund to go through", func() bool { return state(ucn.GetId()) == billingv1.RefundState_REFUND_STATE_REFUNDED })
	keys = map[string]int{}
	for _, c := range stub.Commands() {
		if rf := c.GetRefundPayment(); rf != nil && rf.GetInvoiceId() == maybe.String() {
			keys[rf.GetIdempotencyKey()]++
		}
	}
	if len(keys) != 1 {
		t.Errorf("a refund of unknown outcome was asked for under %d keys: %v, want one", len(keys), keys)
	}
}

// A refund whose outcome is unknown is parked until an operator says what they found at the provider (plan 22 G1): that it was made (the worker is told to record the
// reference under the key the refund was asked under, and the note becomes refunded with it) or that it was not (the refund is asked again under a new key).
func testResolveCreditNoteRefund(t *testing.T) {
	e := newEnv(t, envOpts{})
	var parked, quiet uuid.UUID // the first refund of these invoices comes back REFUND_OUTCOME_UNKNOWN; the confirmation of quiet's is not answered until it is sent again
	stub := e.startWorkers(func(c *commandsv1.CommandEnvelope, seen int) stubworker.Action {
		rf := c.GetRefundPayment()
		switch {
		case rf == nil:
			return stubworker.Action{Reply: stubworker.Succeed}
		case rf.GetConfirmedRefundId() == "" && (rf.GetInvoiceId() == parked.String() || rf.GetInvoiceId() == quiet.String()) && seen == 0:
			return stubworker.Action{Reply: stubworker.Fixed(&commandsv1.ReplyEnvelope{Reply: &commandsv1.ReplyEnvelope_PaymentErrored{PaymentErrored: &commandsv1.PaymentErrored{
				Code: "REFUND_OUTCOME_UNKNOWN", Message: "stub"}}})}
		case rf.GetConfirmedRefundId() != "" && rf.GetInvoiceId() == quiet.String() && c.GetAttempt() < 3:
			return stubworker.Action{}
		}
		return stubworker.Action{Reply: stubworker.Succeed}
	})
	svc := e.api()
	op := tenant.WithOperator(context.Background())
	tctx := tenant.NewContext(context.Background(), tenant.Info{ID: e.tid})
	customer := uuid.New()
	get := func(id string) *billingv1.CreditNote {
		r, err := svc.GetCreditNote(tctx, &billingv1.GetCreditNoteRequest{CreditNoteId: id})
		if err != nil {
			t.Fatal(err)
		}
		return r.GetCreditNote()
	}
	failedNote := func(inv uuid.UUID, number string) string {
		seedSaga(t, e.tid, inv)
		r, err := svc.CreateCreditNote(op, &billingv1.CreateCreditNoteRequest{TenantId: e.tid.String(), InvoiceId: inv.String(), RefundToCard: true})
		if err != nil {
			t.Fatal(err)
		}
		id := r.GetCreditNote().GetId()
		testkit.Eventually(t, 30*time.Second, "the refund of unknown outcome to park", func() bool { return get(id).GetRefundState() == billingv1.RefundState_REFUND_STATE_FAILED })
		if got := get(id).GetRefundError(); !strings.HasPrefix(got, "REFUND_OUTCOME_UNKNOWN:") {
			t.Fatalf("%s: refund_error = %q", number, got)
		}
		return id
	}
	resolve := func(id string, r billingv1.RefundResolution, ref, note string) (*billingv1.CreditNote, error) {
		out, err := svc.ResolveCreditNoteRefund(op, &billingv1.ResolveCreditNoteRefundRequest{TenantId: e.tid.String(), CreditNoteId: id, Resolution: r, ProviderRefundId: ref, Note: note})
		return out.GetCreditNote(), err
	}
	keysOf := func(inv uuid.UUID) (keys []string, confirmed []string) {
		for _, c := range stub.Commands() {
			if rf := c.GetRefundPayment(); rf != nil && rf.GetInvoiceId() == inv.String() {
				keys = append(keys, rf.GetIdempotencyKey())
				confirmed = append(confirmed, rf.GetConfirmedRefundId())
			}
		}
		return keys, confirmed
	}
	audited := func(id string) (resolution, ref, note string, same bool, n int) {
		rows, err := shared.sql.Query(context.Background(), `SELECT detail->>'resolution', COALESCE(detail->>'provider_refund_id', ''), COALESCE(detail->>'note', ''), (detail->>'same_key')::bool
			FROM audit_events WHERE tenant_id = $1 AND action = 'credit_note.refund_resolved' AND target_id = $2`, e.tid, id)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			n++
			if err := rows.Scan(&resolution, &ref, &note, &same); err != nil {
				t.Fatal(err)
			}
		}
		return
	}

	parked = seedOne(t, e.tid, customer, "cus_rs1", "paid", "USD", "INV-2026-000201", 900, "live")
	id := failedNote(parked, "parked")

	// What is refused: nothing says which, a reference for a refund that was not made, none for one that was, a long note, a stranger's note, a note that did not fail.
	for name, c := range map[string]struct {
		r         billingv1.RefundResolution
		ref, note string
		key       string
	}{
		"no resolution":           {billingv1.RefundResolution_REFUND_RESOLUTION_UNSPECIFIED, "", "", "billing.credit_note.bad_resolution"},
		"refunded without a ref":  {billingv1.RefundResolution_REFUND_RESOLUTION_REFUNDED, "  ", "", "billing.credit_note.bad_refund_id"},
		"a ref with two lines":    {billingv1.RefundResolution_REFUND_RESOLUTION_REFUNDED, "re_1\nre_2", "", "billing.credit_note.bad_refund_id"},
		"not refunded with a ref": {billingv1.RefundResolution_REFUND_RESOLUTION_NOT_REFUNDED, "re_1", "", "billing.credit_note.bad_refund_id"},
		"a long note":             {billingv1.RefundResolution_REFUND_RESOLUTION_REFUNDED, "re_1", strings.Repeat("x", 501), "billing.credit_note.note_too_long"},
	} {
		_, err := resolve(id, c.r, c.ref, c.note)
		t.Run(name, func(t *testing.T) { keyOf(t, err, codes.InvalidArgument, c.key) })
	}
	if _, err := resolve(uuid.NewString(), billingv1.RefundResolution_REFUND_RESOLUTION_REFUNDED, "re_1", ""); status.Code(err) != codes.NotFound {
		t.Errorf("an unknown note: %v", err)
	}
	if _, err := svc.ResolveCreditNoteRefund(tctx, &billingv1.ResolveCreditNoteRefundRequest{TenantId: e.tid.String(), CreditNoteId: id,
		Resolution: billingv1.RefundResolution_REFUND_RESOLUTION_REFUNDED, ProviderRefundId: "re_1"}); status.Code(err) == codes.OK {
		t.Errorf("a tenant (not the operator) resolved a refund")
	}
	if get(id).GetRefundState() != billingv1.RefundState_REFUND_STATE_FAILED {
		t.Fatalf("a refused resolution changed the note: %v", get(id))
	}

	// It was made: the note is refunded with the operator's reference, the worker was told under the same key, and the audit log says who checked what.
	keys0, _ := keysOf(parked)
	out, err := resolve(id, billingv1.RefundResolution_REFUND_RESOLUTION_REFUNDED, " re_8841 ", "seen in the provider's dashboard")
	if err != nil || out.GetRefundState() != billingv1.RefundState_REFUND_STATE_REQUESTED {
		t.Fatalf("resolve as refunded = %v, %v", out, err)
	}
	testkit.Eventually(t, 30*time.Second, "the confirmed refund to be recorded", func() bool { return get(id).GetRefundState() == billingv1.RefundState_REFUND_STATE_REFUNDED })
	var state, ref string
	if err := shared.sql.QueryRow(context.Background(), `SELECT refund_state, COALESCE(refund_ref, '') FROM credit_notes WHERE tenant_id = $1 AND id = $2`, e.tid, id).Scan(&state, &ref); err != nil || ref != "re_8841" {
		t.Fatalf("refund_ref = %q (%s), %v", ref, state, err)
	}
	keys, confirmed := keysOf(parked)
	if len(keys) != 2 || keys[1] != keys0[0] || confirmed[0] != "" || confirmed[1] != "re_8841" {
		t.Errorf("commands: keys %v confirmed %v; the confirmation must carry the key of the refund that was parked and the reference", keys, confirmed)
	}
	if r, rf, note, same, n := audited(id); n != 1 || r != "refunded" || rf != "re_8841" || note != "seen in the provider's dashboard" || !same {
		t.Errorf("audit = %q %q %q same_key=%v x%d", r, rf, note, same, n)
	}
	if _, err := resolve(id, billingv1.RefundResolution_REFUND_RESOLUTION_NOT_REFUNDED, "", ""); status.Code(err) != codes.FailedPrecondition {
		t.Errorf("resolving a refunded note again: %v", err)
	}

	// It was not made: the refund is asked again under a new key, only because the operator said so.
	parked2 := seedOne(t, e.tid, customer, "cus_rs2", "paid", "USD", "INV-2026-000202", 400, "live")
	parked = parked2
	id2 := failedNote(parked2, "parked2")
	out, err = resolve(id2, billingv1.RefundResolution_REFUND_RESOLUTION_NOT_REFUNDED, "", "not in the provider's dashboard")
	if err != nil || out.GetRefundState() != billingv1.RefundState_REFUND_STATE_REQUESTED {
		t.Fatalf("resolve as not refunded = %v, %v", out, err)
	}
	testkit.Eventually(t, 30*time.Second, "the new refund to go through", func() bool { return get(id2).GetRefundState() == billingv1.RefundState_REFUND_STATE_REFUNDED })
	keys, confirmed = keysOf(parked2)
	if len(keys) != 2 || keys[0] == keys[1] || confirmed[1] != "" {
		t.Errorf("commands: keys %v confirmed %v; the second refund must carry a new key and no confirmation", keys, confirmed)
	}
	if r, _, _, same, n := audited(id2); n != 1 || r != "not_refunded" || same {
		t.Errorf("audit = %q same_key=%v x%d", r, same, n)
	}

	// The confirmation is resent with its reference when nobody answers it, so a resend never goes to a provider as a plain refund.
	quiet = seedOne(t, e.tid, customer, "cus_rs3", "paid", "USD", "INV-2026-000203", 300, "live")
	id3 := failedNote(quiet, "quiet")
	if _, err := resolve(id3, billingv1.RefundResolution_REFUND_RESOLUTION_REFUNDED, "re_77", ""); err != nil {
		t.Fatal(err)
	}
	testkit.Eventually(t, 20*time.Second, "the unanswered confirmation to arrive", func() bool { _, c := keysOf(quiet); return len(c) >= 2 })
	for i := 0; i < 2; i++ {
		e.clk.Advance(6 * time.Minute)
		if err := e.notes.SweepRefunds(context.Background()); err != nil {
			t.Fatal(err)
		}
		testkit.Eventually(t, 20*time.Second, "the resent command", func() bool {
			_, c := keysOf(quiet)
			return len(c) >= 3+i || get(id3).GetRefundState() == billingv1.RefundState_REFUND_STATE_REFUNDED
		})
	}
	testkit.Eventually(t, 30*time.Second, "the resent confirmation to be recorded", func() bool { return get(id3).GetRefundState() == billingv1.RefundState_REFUND_STATE_REFUNDED })
	_, confirmed = keysOf(quiet)
	for i, c := range confirmed[1:] {
		if c != "re_77" {
			t.Errorf("command %d of the confirmation carries %q, want re_77", i+2, c)
		}
	}
}

// A credit note that refunds to the card writes the note, the next number of the series, the refund request, the command for payments-worker and the audit entry in
// one transaction (plan 22 G15). The audit entry is the last write: an address the log cannot store fails it after all the others, and nothing of the others stays:
// no note, no number used up (the next note is the first of the series, no gap), no command in the outbox for the provider, no event for the tenant's endpoints.
func testCreditNoteRollsBackWhole(t *testing.T) {
	e := newEnv(t, envOpts{})
	stub := e.startWorkers(nil)
	svc := e.api()
	op := tenant.WithOperator(context.Background())
	inv := seedOne(t, e.tid, uuid.New(), "cus_rb", "paid", "USD", "INV-2026-000301", 1000, "live")
	seedSaga(t, e.tid, inv)
	count := func(table string) (n int) {
		if err := shared.sql.QueryRow(context.Background(), "SELECT count(*) FROM "+table+" WHERE tenant_id = $1", e.tid).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	issue := func(ctx context.Context) (*billingv1.CreateCreditNoteResponse, error) {
		return svc.CreateCreditNote(ctx, &billingv1.CreateCreditNoteRequest{TenantId: e.tid.String(), InvoiceId: inv.String(), AmountMinor: 400, RefundToCard: true})
	}
	tables := []string{"credit_notes", "credit_note_seq", "outbox", "tenant_event_outbox", "audit_events"}
	before := map[string]int{}
	for _, tb := range tables {
		before[tb] = count(tb)
	}

	if _, err := issue(audit.NewContext(op, audit.Actor{Type: audit.ActorOperator, Email: "ops\x00@example.com"})); status.Code(err) != codes.Internal {
		t.Fatalf("a note whose audit entry cannot be written: %v, want Internal", err)
	}
	for _, tb := range tables {
		if got := count(tb); got != before[tb] {
			t.Errorf("%s has %d rows after the failed change, %d before: the transaction left a half state", tb, got, before[tb])
		}
	}
	var state string
	if err := shared.sql.QueryRow(context.Background(), `SELECT state FROM invoice_sagas WHERE tenant_id = $1 AND invoice_id = $2`, e.tid, inv).Scan(&state); err != nil || state != "COMPLETED" {
		t.Errorf("saga = %q, %v; a failed credit note must not touch it", state, err)
	}

	// The same call without the fault is the first note of the series, and it is the only refund that ever reaches the provider.
	ok, err := issue(op)
	if err != nil || ok.GetCreditNote().GetNumber() != "CN-2026-000001" {
		t.Fatalf("the note after the failure = %v, %v; want CN-2026-000001 (no number was used up)", ok.GetCreditNote(), err)
	}
	testkit.Eventually(t, 30*time.Second, "the refund to reach the worker", func() bool { return stub.Count("refund") >= 1 })
	if n := stub.Count("refund"); n != 1 {
		t.Errorf("refund commands = %d, want 1: the failed change must not have sent one", n)
	}
	if got := count("credit_notes"); got != before["credit_notes"]+1 {
		t.Errorf("credit notes = %d, want %d", got, before["credit_notes"]+1)
	}
}
