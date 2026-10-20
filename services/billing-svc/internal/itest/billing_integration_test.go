//go:build integration

package itest

import (
	"context"
	"fmt"
	"math/rand/v2"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	billingv1 "github.com/0furkancolak/quotify/gen/go/quotify/billing/v1"
	commandsv1 "github.com/0furkancolak/quotify/gen/go/quotify/commands/v1"
	"github.com/0furkancolak/quotify/pkg/rabbitx"
	"github.com/0furkancolak/quotify/pkg/stubworker"
	"github.com/0furkancolak/quotify/pkg/tenant"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/api"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/saga"
)

func TestBilling(t *testing.T) {
	top = t
	for _, tc := range []struct {
		name string
		fn   func(*testing.T)
	}{
		{"HappyPathCompletesWithStubWorker", testHappyPath},
		{"WatermarkBlocksUntilOutboxDrainsAndAggregatorCatchesUp", testWatermark},
		{"LateUsageIsBilledIncrementallyOnTheNextInvoice", testLateUsage},
		{"CommandsSurviveAKillAfterCommit", testKillAfterCommit},
		{"DuplicateRepliesTransitionOnce", testDuplicateReplies},
		{"SilentWorkerIsRetriedThenFailsAndOperatorRetryResumes", testSilentWorker},
		{"PaymentErroredQueriesStatusWithoutASecondCharge", testPaymentUnknown},
		{"StatusNotFoundRechargesWithTheSameIdempotencyKey", testStatusNotFound},
		{"DunningEndsUncollectible", testDunning},
		{"TenantDunningSettingsReplaceTheDefaults", testTenantDunningSettings},
		{"ClosePeriodTwiceMakesOneRunAndOneInvoicePerCustomer", testClosePeriodTwice},
		{"InvoiceNumbersAreGapFreeUnderConcurrency", testGapFree},
		{"SandboxClosePeriodNowAndLiveIsRefused", testSandbox},
		{"VoidingAPaidInvoiceRefundsIt", testVoidRefund},
		{"RandomRepliesNeverChargeTwicePerAttempt", testRandomRepliesNeverDoubleCharge},
		{"RerateNeedsACompletedRunAndAKnownVersion", testRerateRequiresACompletedRun},
		{"RerateCheaperMakesCreditsAppliedToTheNextInvoice", testRerateCheaperMakesCreditsAppliedToTheNextInvoice},
		{"CreditLargerThanTheInvoiceIsAppliedInParts", testCreditLargerThanTheInvoiceIsAppliedInParts},
		{"RerateHigherMakesCorrectionInvoicesAndRepeatsAreNoOps", testRerateHigherMakesCorrectionInvoices},
		{"RerateRunTwiceIsIdempotent", testRerateRunTwiceIsIdempotent},
		{"OperatorSeesTheSagaTimeline", testInvoiceSagaTimeline},
		{"PlanChangeSplitsTheFixedFeeBetweenThePlans", testPlanChangeSplitsTheFixedFee},
		{"TrialAndPauseWaiveTheFixedFee", testTrialAndPauseWaiveTheFixedFee},
		{"SubscriptionInAnotherCurrencyIsInvoicedInIt", testSubscriptionInAnotherCurrencyIsInvoicedInIt},
		{"SubscriptionWithoutAFeeInItsCurrencyFailsPermanently", testSubscriptionWithoutAFeeInItsCurrencyFailsPermanently},
		{"ProviderOwnedSubscriptionBillsOnlyUsageThroughItsProvider", testProviderOwnedSubscriptionBillsOnlyUsageThroughItsProvider},
		{"ReportsSumInvoicesByPeriodProviderCountryAndCurrency", testReports},
		{"AdvanceFeesAreInvoicedOncePerPeriod", testAdvanceFeesAreInvoicedOncePerPeriod},
		{"AdvanceBilledSubscriptionPaysOnlyUsageAtMonthClose", testAdvanceBilledSubscriptionPaysOnlyUsageAtMonthClose},
		{"PlanChangesAreInvoicedOrCredited", testPlanChangesAreInvoicedOrCredited},
		{"CreditNotesCorrectAPaidInvoiceWithoutEditingIt", testCreditNotes},
		{"HostedInvoiceLinksShowAnInvoiceToAVisitor", testHostedInvoiceLinks},
		{"InvoiceAndCreditNoteEventsAreHandedToPlansOnce", testTenantEvents},
		{"CreditNoteRefundsToTheCardAndIsRetriedUntilAnswered", testCreditNoteRefundToCard},
		{"FeeInvoiceCarriesTheCouponAndServicePeriod", testFeeInvoiceCarriesTheCouponAndServicePeriod},
		{"TaxIsAddedOnTopOfTheInvoicesQuotifyRaises", testTaxIsAddedOnTopOfTheInvoicesQuotifyRaises},
		{"FeeInvoiceTaxIsOnTheDiscountedFeeNotOnCredit", testFeeInvoiceTaxIsOnTheDiscountedFeeNotOnCredit},
	} {
		t.Run(tc.name, tc.fn)
	}
}

func opCtx() context.Context { return tenant.WithOperator(context.Background()) }

// completeOctober drives the 2026-10 run and waits for every saga to COMPLETE.
func completeOctober(e *env) {
	e.t.Helper()
	e.addUsage("2026-10", "cus_a", "gb_hours", "1.250", 3)
	e.addUsage("2026-10", "cus_a", "calls", "150", 0)
	run := e.driveRun("2026-10")
	if run.Status != "INVOICING" {
		e.t.Fatalf("after Drive the run is %s (%s), want INVOICING", run.Status, run.LastError)
	}
	eventually(e.t, 30*time.Second, "every saga to COMPLETE", func() bool {
		s := e.sagaStates("2026-10")
		return len(s) == 2 && s["cus_a"] == "COMPLETED" && s["cus_b"] == "COMPLETED"
	})
	run, err := e.mgr.Drive(context.Background(), e.tid, "2026-10")
	if err != nil || run.Status != "COMPLETED" {
		e.t.Fatalf("run = %+v, %v; want COMPLETED", run, err)
	}
}

func testHappyPath(t *testing.T) {
	e := newEnv(t, envOpts{})
	stub := e.startWorkers(nil)
	completeOctober(e)

	// 1000 base + gb_hours 1.250 × 12.5 = 15.625 → 16 + calls (150-100) × 2 = 100.
	if got := e.invoiceTotal("2026-10", "cus_a"); got != 1116 {
		t.Fatalf("cus_a total = %d, want 1116", got)
	}
	if got := e.invoiceTotal("2026-10", "cus_b"); got != 1000 {
		t.Fatalf("cus_b (base fee only) total = %d, want 1000", got)
	}
	rows, err := shared.system.Query(context.Background(), `SELECT number FROM invoices WHERE tenant_id = $1 ORDER BY number`, e.tid)
	if err != nil {
		t.Fatal(err)
	}
	var numbers []string
	for rows.Next() {
		var n string
		_ = rows.Scan(&n)
		numbers = append(numbers, n)
	}
	rows.Close()
	if len(numbers) != 2 || numbers[0] != "INV-2026-000001" || numbers[1] != "INV-2026-000002" {
		t.Fatalf("numbers = %v", numbers)
	}
	for kind, want := range map[string]int{"render": 2, "charge": 2, "notify": 2, "refund": 0} {
		if got := stub.Count(kind); got != want {
			t.Errorf("%s commands = %d, want %d", kind, got, want)
		}
	}
	e.w.mu.Lock()
	frozen := append([]string(nil), e.w.frozen...)
	e.w.mu.Unlock()
	if len(frozen) != 1 || frozen[0] != "2026-10" {
		t.Errorf("frozen periods = %v, want [2026-10]", frozen)
	}
	var lines int
	if err := shared.system.QueryRow(context.Background(), `SELECT count(*) FROM invoice_lines WHERE tenant_id = $1`, e.tid).Scan(&lines); err != nil || lines != 4 {
		t.Errorf("lines = %d, %v; want 4 (cus_a: base, calls, gb_hours; cus_b: base)", lines, err)
	}
	// The render command carries the whole snapshot.
	for _, c := range stub.Commands() {
		if r := c.GetRenderInvoicePdf(); r != nil && r.GetInvoice().GetTotalMinor() == 1116 {
			inv := r.GetInvoice()
			if inv.GetNumber() == "" || inv.GetCurrency() != "USD" || inv.GetCurrencyExponent() != 2 || len(inv.GetLines()) != 3 ||
				inv.GetCustomerEmail() != "cus_a@example.com" || inv.GetTenantName() != "Acme" || c.GetEnvironment().String() != "ENVIRONMENT_LIVE" {
				t.Errorf("render snapshot = %v (env %v)", inv, c.GetEnvironment())
			}
			if got := inv.GetCustomerAddressLines(); len(got) != 3 || got[0] != "cus_a street 1" || got[1] != "10115 Berlin" || got[2] != "DE" {
				t.Errorf("customer address lines = %q", got)
			}
			if got := inv.GetSellerAddressLines(); len(got) != 3 || got[0] != "1 Market St" || got[1] != "London" || got[2] != "GB" {
				t.Errorf("seller address lines = %q", got)
			}
			if inv.GetLines()[2].GetQuantity() != "1.250" || inv.GetLines()[2].GetUnitLabel() != "GB-h" {
				t.Errorf("gb_hours line = %v", inv.GetLines()[2])
			}
		}
	}
	eventually(t, 10*time.Second, "facts to reach the Kafka sink", func() bool { return e.kafka.count() >= 4 })
}

func testWatermark(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.startWorkers(nil)
	e.addUsage("2026-10", "cus_a", "gb_hours", "1.000", 3)
	// An on-time event is still in ingest's outbox.
	e.w.oldest = tsAt(2026, 10, 31, 23, 59)
	run := e.driveRun("2026-10")
	if run.Status != "WAITING_WATERMARK" {
		t.Fatalf("status = %s, want WAITING_WATERMARK while the ingest outbox holds an on-time event", run.Status)
	}
	var n int
	_ = shared.system.QueryRow(context.Background(), `SELECT count(*) FROM invoices WHERE tenant_id = $1`, e.tid).Scan(&n)
	if n != 0 {
		t.Fatalf("%d invoices exist while waiting", n)
	}
	// The outbox drains; the aggregator is still behind the snapshotted offsets.
	e.w.mu.Lock()
	e.w.oldest, e.w.caughtUp = nil, false
	e.w.mu.Unlock()
	run, err := e.mgr.Drive(context.Background(), e.tid, "2026-10")
	if err != nil || run.Status != "WAITING_WATERMARK" || len(run.Offsets) != 2 {
		t.Fatalf("run = %+v, %v; want still waiting with the offsets snapshot stored", run, err)
	}
	e.w.mu.Lock()
	e.w.caughtUp = true
	e.w.mu.Unlock()
	run, err = e.mgr.Drive(context.Background(), e.tid, "2026-10")
	if err != nil || run.Status != "INVOICING" {
		t.Fatalf("run = %+v, %v; want INVOICING once the watermark passed", run, err)
	}
	// Before cutoff + 30 s nothing is checked at all.
	e2 := newEnv(t, envOpts{})
	e2.clk.t = time.Date(2026, 11, 1, 0, 0, 10, 0, time.UTC)
	if run := e2.driveRun("2026-10"); run.Status != "SCHEDULED" {
		t.Fatalf("10 s after the cutoff the run is %s, want SCHEDULED", run.Status)
	}
}

func tsAt(y, mo, d, h, mi int) *timestamppbT {
	return newTS(time.Date(y, time.Month(mo), d, h, mi, 0, 0, time.UTC))
}

func testLateUsage(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.startWorkers(nil)
	completeOctober(e) // October: calls = 150, so 50 above the free tier.
	octoberLines := countLines(t, e, "2026-10", "cus_a")

	// 30 more October calls arrive late and are billed in November: (180−100)×2 − (150−100)×2 = 60.
	e.addLate("2026-11", "2026-10", "cus_a", "calls", "30", 0)
	e.clk.t = time.Date(2026, 12, 5, 12, 0, 0, 0, time.UTC)
	run := e.driveRun("2026-11")
	if run.Status != "INVOICING" {
		t.Fatalf("november run = %s (%s)", run.Status, run.LastError)
	}
	eventually(t, 30*time.Second, "november sagas", func() bool {
		s := e.sagaStates("2026-11")
		return s["cus_a"] == "COMPLETED" && s["cus_b"] == "COMPLETED"
	})
	if got := e.invoiceTotal("2026-11", "cus_a"); got != 1060 {
		t.Fatalf("november cus_a total = %d, want 1060 (base 1000 + 60 late)", got)
	}
	var amount int64
	var qty, origin string
	err := shared.system.QueryRow(context.Background(), `SELECT l.amount_minor, l.quantity::text, l.origin_period_id FROM invoice_lines l JOIN invoices i USING (tenant_id) 
		WHERE l.invoice_id = i.id AND i.tenant_id = $1 AND i.period_id = '2026-11' AND l.kind = 'LATE_USAGE'`, e.tid).Scan(&amount, &qty, &origin)
	if err != nil || amount != 60 || qty != "30" || origin != "2026-10" {
		t.Fatalf("late line = %d %s %s, %v", amount, qty, origin, err)
	}
	if e.invoiceTotal("2026-10", "cus_a") != 1116 || countLines(t, e, "2026-10", "cus_a") != octoberLines {
		t.Fatal("the original invoice changed")
	}

	// A second late batch for the same origin prices on top of the first: (190−100)×2 − (180−100)×2 = 20.
	e.addLate("2026-12", "2026-10", "cus_a", "calls", "10", 0)
	e.clk.t = time.Date(2027, 1, 5, 12, 0, 0, 0, time.UTC)
	e.driveRun("2026-12")
	eventually(t, 30*time.Second, "december sagas", func() bool { return e.sagaStates("2026-12")["cus_a"] == "COMPLETED" })
	if got := e.invoiceTotal("2026-12", "cus_a"); got != 1020 {
		t.Fatalf("december cus_a total = %d, want 1020 (base 1000 + 20 late)", got)
	}
}

func countLines(t *testing.T, e *env, period, customer string) int {
	t.Helper()
	var n int
	err := shared.system.QueryRow(context.Background(), `SELECT count(*) FROM invoice_lines l JOIN invoices i ON i.tenant_id = l.tenant_id AND i.id = l.invoice_id
		WHERE i.tenant_id = $1 AND i.period_id = $2 AND i.snapshot->>'customer_external_id' = $3`, e.tid, period, customer).Scan(&n)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// The commands live in the outbox until the relay publishes them: a billing-svc killed right after a transition commit
// loses nothing, and a relay that crashes after publishing (before deleting) publishes again — workers dedup.
func testKillAfterCommit(t *testing.T) {
	e := newEnv(t, envOpts{})
	// Stub worker and reply consumer run, the relay does not: the process was killed before it published anything.
	stub := stubworker.New(shared.amqp, nil)
	stub.Log = logger()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = stub.Run(ctx) }()
	e.addUsage("2026-10", "cus_a", "gb_hours", "1.250", 3)
	run := e.driveRun("2026-10")
	if run.Status != "INVOICING" {
		t.Fatalf("run = %s", run.Status)
	}
	var pending int
	_ = shared.system.QueryRow(context.Background(), `SELECT count(*) FROM outbox WHERE tenant_id = $1 AND headers->>'x-destination' = 'rabbitmq'`, e.tid).Scan(&pending)
	if pending != 2 {
		t.Fatalf("%d render commands wait in the outbox, want 2", pending)
	}
	time.Sleep(500 * time.Millisecond)
	if stub.Count("render") != 0 {
		t.Fatal("a command reached the worker without the relay")
	}

	// A restarted pod: the relay publishes, but crashes once after the broker acked (rows stay → published again).
	failed := atomic.Bool{}
	pub := rabbitx.NewPublisher(shared.amqp)
	t.Cleanup(pub.Close)
	rctx, rcancel := context.WithCancel(context.Background())
	t.Cleanup(rcancel)
	e.startConsumersWith(rctx, pub, func() error {
		if failed.CompareAndSwap(false, true) {
			return fmt.Errorf("simulated crash after publish")
		}
		return nil
	})
	eventually(t, 30*time.Second, "sagas to complete after the restart", func() bool {
		s := e.sagaStates("2026-10")
		return len(s) == 2 && s["cus_a"] == "COMPLETED" && s["cus_b"] == "COMPLETED"
	})
	if !failed.Load() {
		t.Fatal("the crash hook never fired")
	}
	if got := stub.Count("render"); got != 2 {
		t.Fatalf("worker saw %d distinct render commands, want 2 (redelivered ones are deduplicated)", got)
	}
}

// The same reply twice, and the same outcome with a fresh reply id, move the saga once.
func testDuplicateReplies(t *testing.T) {
	e := newEnv(t, envOpts{})
	stub := e.startWorkers(func(c *commandsv1.CommandEnvelope, _ int) stubworker.Action {
		if stubworker.Kind(c) == "charge" {
			return stubworker.Action{Reply: stubworker.Succeed, Copies: 2}
		}
		return stubworker.Action{Reply: stubworker.Succeed}
	})
	completeOctober(e)
	var transitions int
	err := shared.system.QueryRow(context.Background(), `SELECT count(*) FROM saga_events WHERE tenant_id = $1 AND to_state = 'PAID'`, e.tid).Scan(&transitions)
	if err != nil || transitions != 2 {
		t.Fatalf("PAID transitions = %d, %v; want one per invoice", transitions, err)
	}
	// A late PaymentSucceeded under a new reply id (as a second charge attempt would produce): the state guard ignores it.
	var sagaID uuid.UUID
	var cmd string
	for _, c := range stub.Commands() {
		if stubworker.Kind(c) == "charge" {
			sagaID, _ = uuid.Parse(c.GetSagaId())
			cmd = c.GetCommandId()
			break
		}
	}
	var before int
	_ = shared.system.QueryRow(context.Background(), `SELECT count(*) FROM saga_events WHERE tenant_id = $1 AND saga_id = $2`, e.tid, sagaID).Scan(&before)
	body, _ := proto.Marshal(&commandsv1.ReplyEnvelope{CommandId: cmd, SagaId: sagaID.String(), TenantId: e.tid.String(), ReplyId: uuid.NewString(),
		Reply: &commandsv1.ReplyEnvelope_PaymentSucceeded{PaymentSucceeded: &commandsv1.PaymentSucceeded{PspChargeId: "dup"}}})
	pub := rabbitx.NewPublisher(shared.amqp)
	defer pub.Close()
	if err := pub.Publish(context.Background(), rabbitx.Message{Exchange: rabbitx.ExchangeReplies, RoutingKey: rabbitx.ReplyRoutingKey,
		Publishing: amqp.Publishing{Body: body}}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Second)
	var after int
	_ = shared.system.QueryRow(context.Background(), `SELECT count(*) FROM saga_events WHERE tenant_id = $1 AND saga_id = $2`, e.tid, sagaID).Scan(&after)
	if after != before || before < 6 {
		t.Fatalf("saga events before = %d, after the stale duplicate = %d; want unchanged", before, after)
	}
}

// scriptSwitch lets a test change the stub's behaviour while it runs.
type scriptSwitch struct {
	mu sync.Mutex
	fn stubworker.Script
}

func (s *scriptSwitch) set(fn stubworker.Script) {
	s.mu.Lock()
	s.fn = fn
	s.mu.Unlock()
}

func (s *scriptSwitch) script(c *commandsv1.CommandEnvelope, seen int) stubworker.Action {
	s.mu.Lock()
	fn := s.fn
	s.mu.Unlock()
	return fn(c, seen)
}

func testSilentWorker(t *testing.T) {
	e := newEnv(t, envOpts{})
	sw := &scriptSwitch{}
	sw.set(func(c *commandsv1.CommandEnvelope, _ int) stubworker.Action {
		if stubworker.Kind(c) == "render" {
			return stubworker.Action{} // never replies
		}
		return stubworker.Action{Reply: stubworker.Succeed}
	})
	stub := e.startWorkers(sw.script)
	e.addUsage("2026-10", "cus_a", "gb_hours", "1.000", 3)
	e.driveRun("2026-10")
	eventually(t, 20*time.Second, "first render commands", func() bool { return stub.Count("render") == 2 })

	// Each missed deadline re-issues the command (fresh command id), three times in all, then the step fails.
	for round := 2; round <= 3; round++ {
		e.clk.Advance(2 * time.Hour)
		res, err := e.eng.Sweep(context.Background(), shared.system, 100)
		if err != nil || res.Reissued != 2 {
			t.Fatalf("round %d: %+v, %v; want 2 re-issued", round, res, err)
		}
		want := 2 * round
		eventually(t, 20*time.Second, fmt.Sprintf("render commands = %d", want), func() bool { return stub.Count("render") == want })
	}
	e.clk.Advance(2 * time.Hour)
	res, err := e.eng.Sweep(context.Background(), shared.system, 100)
	if err != nil || res.Exhausted != 2 {
		t.Fatalf("final sweep: %+v, %v; want 2 exhausted", res, err)
	}
	if s := e.sagaStates("2026-10"); s["cus_a"] != "PDF_FAILED" || s["cus_b"] != "PDF_FAILED" {
		t.Fatalf("states = %v, want PDF_FAILED", s)
	}

	// The worker is back; the operator resumes.
	sw.set(func(*commandsv1.CommandEnvelope, int) stubworker.Action {
		return stubworker.Action{Reply: stubworker.Succeed}
	})
	svc := api.New(shared.app, shared.system, e.mgr, e.eng)
	for _, name := range []string{"cus_a", "cus_b"} {
		var invID uuid.UUID
		if err := shared.system.QueryRow(context.Background(), `SELECT id FROM invoices WHERE tenant_id = $1 AND snapshot->>'customer_external_id' = $2`, e.tid, name).Scan(&invID); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.RetryInvoiceSaga(opCtx(), &billingv1.RetryInvoiceSagaRequest{TenantId: e.tid.String(), InvoiceId: invID.String()}); err != nil {
			t.Fatalf("RetryInvoiceSaga: %v", err)
		}
	}
	eventually(t, 30*time.Second, "sagas to complete after the retry", func() bool {
		s := e.sagaStates("2026-10")
		return s["cus_a"] == "COMPLETED" && s["cus_b"] == "COMPLETED"
	})
	// Retrying something that is not failed is refused.
	var invID uuid.UUID
	_ = shared.system.QueryRow(context.Background(), `SELECT id FROM invoices WHERE tenant_id = $1 LIMIT 1`, e.tid).Scan(&invID)
	_, err = svc.RetryInvoiceSaga(opCtx(), &billingv1.RetryInvoiceSagaRequest{TenantId: e.tid.String(), InvoiceId: invID.String()})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("retry of a completed saga: %v, want FAILED_PRECONDITION", err)
	}
}

func testPaymentUnknown(t *testing.T) {
	e := newEnv(t, envOpts{})
	stub := e.startWorkers(func(c *commandsv1.CommandEnvelope, _ int) stubworker.Action {
		if stubworker.Kind(c) == "charge" {
			return stubworker.Action{Reply: stubworker.Errored} // the charge may or may not have gone through
		}
		return stubworker.Action{Reply: stubworker.Succeed} // the status query says: it did
	})
	completeOctober(e)
	if got := stub.Count("charge"); got != 2 {
		t.Fatalf("charge commands = %d, want 2 (one per invoice, no re-charge)", got)
	}
	if got := stub.Count("status"); got != 2 {
		t.Fatalf("status queries = %d, want 2", got)
	}
}

func testStatusNotFound(t *testing.T) {
	e := newEnv(t, envOpts{})
	stub := e.startWorkers(func(c *commandsv1.CommandEnvelope, seen int) stubworker.Action {
		switch stubworker.Kind(c) {
		case "charge":
			if seen == 0 {
				return stubworker.Action{Reply: stubworker.Errored}
			}
		case "status":
			return stubworker.Action{Reply: stubworker.StatusNotFound}
		}
		return stubworker.Action{Reply: stubworker.Succeed}
	})
	completeOctober(e)
	keys := map[string]int{}
	for _, c := range stub.Commands() {
		if ch := c.GetChargePayment(); ch != nil {
			keys[ch.GetIdempotencyKey()]++
		}
	}
	if len(keys) != 2 {
		t.Fatalf("idempotency keys = %v, want 2 distinct (one per invoice)", keys)
	}
	for k, n := range keys {
		if n != 2 {
			t.Errorf("key %s used by %d charge commands, want 2 (the charge and its re-charge share the key)", k, n)
		}
	}
}

func testDunning(t *testing.T) {
	e := newEnv(t, envOpts{})
	stub := e.startWorkers(func(c *commandsv1.CommandEnvelope, _ int) stubworker.Action {
		if stubworker.Kind(c) == "charge" {
			return stubworker.Action{Reply: stubworker.Declined}
		}
		return stubworker.Action{Reply: stubworker.Succeed}
	})
	e.addUsage("2026-10", "cus_a", "gb_hours", "1.000", 3)
	e.driveRun("2026-10")
	declined := func() bool {
		s := e.sagaStates("2026-10")
		return s["cus_a"] == "PAYMENT_DECLINED" && s["cus_b"] == "PAYMENT_DECLINED"
	}
	eventually(t, 20*time.Second, "first decline", declined)
	// +1 d, +3 d, +7 d (the test clock; live tenants), then the invoice is uncollectible.
	for attempt, delay := range []time.Duration{24 * time.Hour, 72 * time.Hour, 168 * time.Hour} {
		e.clk.Advance(delay + time.Minute)
		res, err := e.eng.Sweep(context.Background(), shared.system, 100)
		if err != nil || res.Dunned != 2 {
			t.Fatalf("dunning pass %d: %+v, %v; want 2 retries", attempt+1, res, err)
		}
		want := 2 * (attempt + 2)
		eventually(t, 20*time.Second, fmt.Sprintf("charge commands = %d", want), func() bool { return stub.Count("charge") == want })
		if attempt < 2 {
			eventually(t, 20*time.Second, "declined again", declined)
		}
	}
	eventually(t, 30*time.Second, "uncollectible invoices to complete their notification", func() bool {
		s := e.sagaStates("2026-10")
		return s["cus_a"] == "COMPLETED" && s["cus_b"] == "COMPLETED"
	})
	var status string
	if err := shared.system.QueryRow(context.Background(), `SELECT status FROM invoices WHERE tenant_id = $1 AND snapshot->>'customer_external_id' = 'cus_a'`, e.tid).Scan(&status); err != nil || status != "uncollectible" {
		t.Fatalf("invoice status = %q, %v", status, err)
	}
	perKey := map[string]int{}
	templates := map[string]int{}
	for _, c := range stub.Commands() {
		if ch := c.GetChargePayment(); ch != nil {
			perKey[ch.GetIdempotencyKey()]++
		}
		if n := c.GetSendNotification(); n != nil {
			templates[n.GetTemplate()]++
		}
	}
	if len(perKey) != 8 { // 2 invoices × dunning attempts 0..3
		t.Fatalf("charge keys = %d, want 8: %v", len(perKey), perKey)
	}
	for k, n := range perKey {
		if n != 1 {
			t.Errorf("key %s charged %d times", k, n)
		}
	}
	if templates["payment_failed.v1"] != 2 {
		t.Fatalf("notification templates = %v, want payment_failed.v1 ×2", templates)
	}
}

func testClosePeriodTwice(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.startWorkers(nil)
	e.addUsage("2026-10", "cus_a", "gb_hours", "1.000", 3)
	svc := api.New(shared.app, shared.system, e.mgr, e.eng)
	for i := 0; i < 2; i++ {
		if _, err := svc.ClosePeriod(opCtx(), &billingv1.ClosePeriodRequest{TenantId: e.tid.String(), PeriodId: "2026-10"}); err != nil {
			t.Fatalf("ClosePeriod #%d: %v", i+1, err)
		}
	}
	var runsN, invoices int
	_ = shared.system.QueryRow(context.Background(), `SELECT count(*) FROM billing_runs WHERE tenant_id = $1`, e.tid).Scan(&runsN)
	_ = shared.system.QueryRow(context.Background(), `SELECT count(*) FROM invoices WHERE tenant_id = $1`, e.tid).Scan(&invoices)
	if runsN != 1 || invoices != 2 {
		t.Fatalf("runs = %d, invoices = %d; want 1 and 2", runsN, invoices)
	}
	// A period that is still open cannot be closed.
	if _, err := svc.ClosePeriod(opCtx(), &billingv1.ClosePeriodRequest{TenantId: e.tid.String(), PeriodId: "2026-11"}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("closing the open period: %v, want FAILED_PRECONDITION", err)
	}
	if _, err := svc.ClosePeriod(context.Background(), &billingv1.ClosePeriodRequest{TenantId: e.tid.String(), PeriodId: "2026-10"}); err == nil {
		t.Fatal("ClosePeriod without the operator flag must be refused")
	}
}

func testGapFree(t *testing.T) {
	e := newEnv(t, envOpts{})
	const n = 20
	ids := make([]uuid.UUID, n)
	for i := range ids {
		ids[i] = uuid.New()
		snap := `{"environment":"live","currency_exponent":2,"customer_external_id":"c","customer_email":"c@example.com"}`
		_, err := shared.system.Exec(context.Background(), `INSERT INTO invoices (tenant_id, id, customer_id, period_id, kind, plan_version_id, currency, subtotal_minor, total_minor, status, snapshot)
			VALUES ($1,$2,$3,'2026-10','REGULAR',$4,'USD',0,0,'draft',$5)`, e.tid, ids[i], uuid.New(), uuid.New(), snap)
		if err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for _, id := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- e.mgr.Finalize(context.Background(), e.tid, id)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("finalize: %v", err)
		}
	}
	rows, err := shared.system.Query(context.Background(), `SELECT number FROM invoices WHERE tenant_id = $1 ORDER BY number`, e.tid)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var s string
		_ = rows.Scan(&s)
		got = append(got, s)
	}
	sort.Strings(got)
	for i, s := range got {
		if want := fmt.Sprintf("INV-2026-%06d", i+1); s != want {
			t.Fatalf("number #%d = %s, want %s (numbers must be gap-free)", i, s, want)
		}
	}
	if len(got) != n {
		t.Fatalf("%d numbers assigned, want %d", len(got), n)
	}
}

func testSandbox(t *testing.T) {
	e := newEnv(t, envOpts{sandbox: true})
	e.startWorkers(nil)
	// The sandbox tenant closes the current month early: cutoff = now + 5 s, then the normal flow.
	e.w.earlyCut = e.clk.Now().Add(5 * time.Second)
	e.addUsage("2026-11", "cus_a", "gb_hours", "2.000", 3)
	svc := api.New(shared.app, shared.system, e.mgr, e.eng)
	ctx := tenant.NewContext(context.Background(), tenant.Info{ID: e.tid, Environment: tenant.EnvSandbox, Timezone: "UTC"})
	resp, err := svc.ClosePeriodNow(ctx, &billingv1.ClosePeriodNowRequest{})
	if err != nil || resp.GetRun().GetPeriodId() != "2026-11" {
		t.Fatalf("ClosePeriodNow = %v, %v", resp, err)
	}
	// The watermark check starts 30 s after the new cutoff.
	e.clk.Advance(40 * time.Second)
	run, err := e.mgr.Drive(context.Background(), e.tid, "2026-11")
	if err != nil || run.Status != "INVOICING" {
		t.Fatalf("run = %+v, %v", run, err)
	}
	eventually(t, 30*time.Second, "sandbox sagas", func() bool {
		s := e.sagaStates("2026-11")
		return s["cus_a"] == "COMPLETED" && s["cus_b"] == "COMPLETED"
	})
	var number string
	_ = shared.system.QueryRow(context.Background(), `SELECT number FROM invoices WHERE tenant_id = $1 ORDER BY number LIMIT 1`, e.tid).Scan(&number)
	if number != "SBX-2026-000001" {
		t.Fatalf("sandbox invoice number = %q, want SBX-2026-000001", number)
	}
	for _, c := range e.stubs[0].Commands() {
		if c.GetTenantId() != e.tid.String() {
			continue // RabbitMQ is shared by the subtests: commands of other tenants may reach this stub
		}
		if c.GetEnvironment().String() != "ENVIRONMENT_SANDBOX" {
			t.Fatalf("command environment = %v, want SANDBOX on every command", c.GetEnvironment())
		}
	}
	// Live tenants are refused.
	live := tenant.NewContext(context.Background(), tenant.Info{ID: e.tid, Environment: tenant.EnvLive, Timezone: "UTC"})
	if _, err := svc.ClosePeriodNow(live, &billingv1.ClosePeriodNowRequest{}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("live ClosePeriodNow: %v, want FAILED_PRECONDITION", err)
	}
}

func testVoidRefund(t *testing.T) {
	e := newEnv(t, envOpts{})
	stub := e.startWorkers(nil)
	completeOctober(e)
	svc := api.New(shared.app, shared.system, e.mgr, e.eng)
	var invID uuid.UUID
	if err := shared.system.QueryRow(context.Background(), `SELECT id FROM invoices WHERE tenant_id = $1 AND snapshot->>'customer_external_id' = 'cus_a'`, e.tid).Scan(&invID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.VoidInvoice(opCtx(), &billingv1.VoidInvoiceRequest{TenantId: e.tid.String(), InvoiceId: invID.String(), Reason: "test"}); err != nil {
		t.Fatalf("VoidInvoice: %v", err)
	}
	eventually(t, 20*time.Second, "refund and void", func() bool { return e.sagaStates("2026-10")["cus_a"] == "VOIDED" })
	if stub.Count("refund") != 1 {
		t.Fatalf("refund commands = %d, want 1", stub.Count("refund"))
	}
	var st string
	_ = shared.system.QueryRow(context.Background(), `SELECT status FROM invoices WHERE id = $1 AND tenant_id = $2`, invID, e.tid).Scan(&st)
	if st != "void" {
		t.Fatalf("invoice status = %s", st)
	}
	if _, err := svc.VoidInvoice(opCtx(), &billingv1.VoidInvoiceRequest{TenantId: e.tid.String(), InvoiceId: invID.String()}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("voiding twice: %v, want FAILED_PRECONDITION", err)
	}
}

// Random, duplicated and reordered replies never produce a second ChargePayment for the same dunning attempt.
func testRandomRepliesNeverDoubleCharge(t *testing.T) {
	e := newEnv(t, envOpts{})
	seed := uint64(time.Now().UnixNano())
	t.Logf("seed %d", seed)
	rng := rand.New(rand.NewPCG(seed, 7))
	events := []saga.Event{saga.EvPdfRendered, saga.EvPdfFailed, saga.EvPaymentOK, saga.EvPaymentDeclined, saga.EvPaymentErrored,
		saga.EvStatusSucceeded, saga.EvStatusDeclined, saga.EvNotified, saga.EvRefunded, saga.EvDunningDue}
	for round := 0; round < 60; round++ {
		invID := uuid.New()
		snap := `{"environment":"live","currency_exponent":2,"customer_external_id":"c","customer_email":"c@example.com","payment_method":"pm"}`
		if _, err := shared.system.Exec(context.Background(), `INSERT INTO invoices (tenant_id, id, customer_id, period_id, kind, plan_version_id, currency, subtotal_minor, total_minor, status, snapshot)
			VALUES ($1,$2,$3,'2026-10','REGULAR',$4,'USD',500,500,'draft',$5)`, e.tid, invID, uuid.New(), uuid.New(), snap); err != nil {
			t.Fatal(err)
		}
		if err := e.mgr.Finalize(context.Background(), e.tid, invID); err != nil {
			t.Fatal(err)
		}
		sagaID := uuid.NewSHA1(uuid.MustParse("c0a1d7f2-3e57-5b6e-8d13-9a4f0b7c2e41"), []byte("saga|"+invID.String()))
		for i := 0; i < 14; i++ {
			ev := events[rng.IntN(len(events))]
			for rep := 0; rep < 1+rng.IntN(2); rep++ { // sometimes twice in a row
				err := pgxxTx(func(tx pgxTx) error {
					_, err := e.eng.Handle(context.Background(), tx, e.tid, sagaID, ev, "random", nil)
					return err
				})
				if err != nil {
					t.Fatalf("seed %d: handle %s: %v", seed, ev, err)
				}
			}
		}
		rows, err := shared.system.Query(context.Background(), `SELECT payload FROM outbox WHERE tenant_id = $1 AND headers->>'x-destination' = 'rabbitmq' AND topic = 'billing.commands' AND partition_key = 'payment.charge'
			AND payload IS NOT NULL`, e.tid)
		if err != nil {
			t.Fatal(err)
		}
		perKey := map[string]int{}
		for rows.Next() {
			var b []byte
			_ = rows.Scan(&b)
			var env commandsv1.CommandEnvelope
			if err := proto.Unmarshal(b, &env); err == nil && env.GetChargePayment().GetInvoiceId() == invID.String() {
				perKey[env.GetChargePayment().GetIdempotencyKey()]++
			}
		}
		rows.Close()
		for k, n := range perKey {
			if n > 1 {
				t.Fatalf("seed %d: key %s charged %d times", seed, k, n)
			}
		}
	}
}
