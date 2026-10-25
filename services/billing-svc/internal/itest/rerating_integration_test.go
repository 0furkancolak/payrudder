//go:build integration

package itest

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/testkit"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/runs"
)

// newVersion adds another version of the "pro" plan to the world: base fee, gb_hours per unit and the price per call
// above the free 100.
func (e *env) newVersion(base int64, gbPerUnit, callPrice string) uuid.UUID {
	e.t.Helper()
	pv := &plansv1.PlanVersion{Id: uuid.NewString(), PlanCode: "pro", Version: 2, BaseFeeMinor: base, Prices: []*plansv1.Price{
		{MeterCode: "gb_hours", Model: &plansv1.Price_PerUnit{PerUnit: &plansv1.PerUnitPrice{UnitPrice: gbPerUnit}}},
		{MeterCode: "calls", Model: &plansv1.Price_Tiered{Tiered: &plansv1.TieredGraduatedPrice{Tiers: []*plansv1.PriceTier{
			{UpTo: "100", UnitPrice: "0"}, {UnitPrice: callPrice}}}}},
	}}
	e.w.mu.Lock()
	e.w.versions[pv.Id] = pv
	e.w.mu.Unlock()
	return uuid.MustParse(pv.Id)
}

func (e *env) rerate(period string, pv uuid.UUID) (*runs.ReratingJob, bool) {
	e.t.Helper()
	job, created, err := e.mgr.StartRerating(context.Background(), e.tid, period, pv)
	if err != nil {
		e.t.Fatalf("StartRerating: %v", err)
	}
	return job, created
}

func (e *env) credits() map[string]int64 {
	e.t.Helper()
	rows, err := shared.system.Query(context.Background(), `SELECT cu.snapshot->>'customer_external_id', sum(c.amount_minor)::bigint FROM credit_balances c
		JOIN LATERAL (SELECT snapshot FROM invoices WHERE tenant_id = c.tenant_id AND customer_id = c.customer_id LIMIT 1) cu ON true
		WHERE c.tenant_id = $1 GROUP BY 1`, e.tid)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var c string
		var n int64
		if err := rows.Scan(&c, &n); err != nil {
			e.t.Fatal(err)
		}
		out[c] = n
	}
	return out
}

// invoicesOf returns kind -> number of invoices for the period.
func (e *env) invoiceKinds(period string) map[string]int {
	e.t.Helper()
	rows, err := shared.system.Query(context.Background(), `SELECT kind, count(*) FROM invoices WHERE tenant_id = $1 AND period_id = $2 GROUP BY 1`, e.tid, period)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var k string
		var n int
		_ = rows.Scan(&k, &n)
		out[k] = n
	}
	return out
}

func testRerateRequiresACompletedRun(t *testing.T) {
	e := newEnv(t, envOpts{})
	pv := e.newVersion(800, "10", "1.6")
	if _, _, err := e.mgr.StartRerating(context.Background(), e.tid, "2026-10", pv); err != runs.ErrNoRun {
		t.Fatalf("without a run: %v, want ErrNoRun", err)
	}
	e.addUsage("2026-10", "cus_a", "gb_hours", "1.250", 3)
	e.driveRun("2026-10") // INVOICING, no worker: not COMPLETED
	if _, _, err := e.mgr.StartRerating(context.Background(), e.tid, "2026-10", pv); err != runs.ErrRunNotCompleted {
		t.Fatalf("with an open run: %v, want ErrRunNotCompleted", err)
	}
	e.startWorkers(nil)
	completeOctober(e)
	if _, _, err := e.mgr.StartRerating(context.Background(), e.tid, "2026-10", uuid.New()); err == nil {
		t.Fatal("an unknown plan version was accepted")
	}
}

// A 20 % cheaper version: the exact difference becomes a credit balance per customer, and the next regular invoice
// applies it as a CREDIT line.
func testRerateCheaperMakesCreditsAppliedToTheNextInvoice(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.startWorkers(nil)
	completeOctober(e) // cus_a 1116 (1000 + 16 + 100), cus_b 1000
	// 800 base, gb_hours 10/unit (1.250 -> 12.5 -> 12 half-even), calls 1.6 above 100 (50 -> 80).
	pv := e.newVersion(800, "10", "1.6")

	job, created := e.rerate("2026-10", pv)
	if !created || job.Status != runs.ReratingCompleted || job.CustomersRated != 2 || job.Corrections != 0 || job.Credits != 2 {
		t.Fatalf("job = %+v created=%v, want COMPLETED, 2 customers rated, 0 corrections, 2 credits", job, created)
	}
	if got := e.credits(); got["cus_a"] != 1116-(800+12+80) || got["cus_b"] != 1000-800 {
		t.Fatalf("credits = %v, want cus_a %d and cus_b %d", got, 1116-(800+12+80), 200)
	}
	if job.CreditMinor != 224+200 {
		t.Fatalf("job credit total = %d, want 424", job.CreditMinor)
	}
	if k := e.invoiceKinds("2026-10"); k["CORRECTION"] != 0 || k["REGULAR"] != 2 {
		t.Fatalf("invoices = %v: a price drop must not create a correction", k)
	}

	// November: the next regular invoices carry the credits.
	e.clk.Advance(30 * 24 * time.Hour)
	e.addUsage("2026-11", "cus_a", "gb_hours", "2.000", 3) // 2 x 12.5 = 25
	e.driveRun("2026-11")
	testkit.Eventually(t, 30*time.Second, "the November sagas", func() bool {
		s := e.sagaStates("2026-11")
		return len(s) == 2 && s["cus_a"] == "COMPLETED" && s["cus_b"] == "COMPLETED"
	})
	if got := e.invoiceTotal("2026-11", "cus_a"); got != 1025-224 {
		t.Fatalf("cus_a November total = %d, want 1025 - 224 = 801", got)
	}
	if got := e.invoiceTotal("2026-11", "cus_b"); got != 1000-200 {
		t.Fatalf("cus_b November total = %d, want 800", got)
	}
	var creditLines int
	var sum int64
	if err := shared.system.QueryRow(context.Background(), `SELECT count(*), COALESCE(sum(amount_minor), 0)::bigint FROM invoice_lines l JOIN invoices i ON i.tenant_id = l.tenant_id AND i.id = l.invoice_id
		WHERE l.tenant_id = $1 AND l.kind = 'CREDIT' AND i.period_id = '2026-11'`, e.tid).Scan(&creditLines, &sum); err != nil || creditLines != 2 || sum != -424 {
		t.Fatalf("CREDIT lines = %d summing to %d (%v), want 2 summing to -424", creditLines, sum, err)
	}
	var unapplied int
	if err := shared.system.QueryRow(context.Background(), `SELECT count(*) FROM credit_balances WHERE tenant_id = $1 AND applied_invoice_id IS NULL`, e.tid).Scan(&unapplied); err != nil || unapplied != 0 {
		t.Fatalf("%d credit balances are still unapplied (%v)", unapplied, err)
	}
}

// A credit bigger than the invoice is applied up to what the invoice costs; the rest waits for the next one.
func testCreditLargerThanTheInvoiceIsAppliedInParts(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.startWorkers(nil)
	completeOctober(e)
	cust := e.customer("cus_b").Id
	if _, err := shared.system.Exec(context.Background(), `INSERT INTO credit_balances (tenant_id, customer_id, id, amount_minor, source_rerating_job_id)
		VALUES ($1,$2,$3,2500,$4)`, e.tid, cust, uuid.New(), uuid.New()); err != nil {
		t.Fatal(err)
	}
	for i, period := range []string{"2026-11", "2026-12", "2027-01"} {
		e.clk.Advance(32 * 24 * time.Hour)
		e.driveRun(period)
		testkit.Eventually(t, 30*time.Second, "the sagas of "+period, func() bool {
			s := e.sagaStates(period)
			return len(s) == 2 && s["cus_b"] == "COMPLETED"
		})
		want := int64(0)
		switch i {
		case 0, 1:
			want = 0 // 1000 base fee, credit 1000 -> 0, then 1000 -> 0
		case 2:
			want = 500 // 2500 - 1000 - 1000 = 500 left: 1000 - 500
		}
		if got := e.invoiceTotal(period, "cus_b"); got != want {
			t.Fatalf("%s total = %d, want %d", period, got, want)
		}
	}
}

// A more expensive version: one CORRECTION invoice per customer for the exact difference, through the normal saga.
func testRerateHigherMakesCorrectionInvoices(t *testing.T) {
	e := newEnv(t, envOpts{})
	stub := e.startWorkers(nil)
	completeOctober(e)
	renders := stub.Count("render")
	// base 1200, gb_hours 15/unit (1.250 -> 18.75 -> 19), calls 2 above 100 (unchanged: 100).
	pv := e.newVersion(1200, "15", "2")

	job, created := e.rerate("2026-10", pv)
	if !created || job.CustomersRated != 2 || job.Corrections != 2 || job.Credits != 0 || job.CorrectionMinor != 203+200 {
		t.Fatalf("job = %+v, want 2 corrections summing to 403", job)
	}
	if k := e.invoiceKinds("2026-10"); k["CORRECTION"] != 2 || k["REGULAR"] != 2 {
		t.Fatalf("invoices = %v, want 2 regular and 2 correction", k)
	}
	rows, err := shared.system.Query(context.Background(), `SELECT i.snapshot->>'customer_external_id', i.total_minor, i.number, l.kind, COALESCE(l.meter_code, ''), l.amount_minor
		FROM invoices i JOIN invoice_lines l ON l.tenant_id = i.tenant_id AND l.invoice_id = i.id WHERE i.tenant_id = $1 AND i.kind = 'CORRECTION' ORDER BY 1, l.line_no`, e.tid)
	if err != nil {
		t.Fatal(err)
	}
	type row struct {
		cust, number, kind, meter string
		total, amount             int64
	}
	var got []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.cust, &r.total, &r.number, &r.kind, &r.meter, &r.amount); err != nil {
			t.Fatal(err)
		}
		got = append(got, r)
	}
	rows.Close()
	// cus_a: base +200, gb_hours +3 (19 - 16), calls unchanged -> no line. cus_b: base +200.
	if len(got) != 3 || got[0].cust != "cus_a" || got[0].total != 203 || got[0].meter != "" || got[0].amount != 200 ||
		got[1].meter != "gb_hours" || got[1].amount != 3 || got[2].cust != "cus_b" || got[2].total != 200 || got[2].amount != 200 {
		t.Fatalf("correction lines = %+v", got)
	}
	for _, r := range got {
		if r.number == "" || r.kind != "CORRECTION" {
			t.Fatalf("correction %+v is not a finalized CORRECTION line", r)
		}
	}
	testkit.Eventually(t, 30*time.Second, "both correction sagas to COMPLETE", func() bool {
		var n int
		_ = shared.system.QueryRow(context.Background(), `SELECT count(*) FROM invoices i JOIN invoice_sagas s ON s.tenant_id = i.tenant_id AND s.invoice_id = i.id
			WHERE i.tenant_id = $1 AND i.kind = 'CORRECTION' AND s.state = 'COMPLETED'`, e.tid).Scan(&n)
		return n == 2
	})
	if got := stub.Count("render") - renders; got != 2 {
		t.Fatalf("%d render commands for the corrections, want 2", got)
	}
	// The same request again changes nothing.
	again, createdAgain := e.rerate("2026-10", pv)
	if createdAgain || again.ID != job.ID {
		t.Fatalf("repeat = %+v created=%v, want the same job and created=false", again, createdAgain)
	}
	if k := e.invoiceKinds("2026-10"); k["CORRECTION"] != 2 {
		t.Fatalf("after the repeat there are %d corrections, want 2", k["CORRECTION"])
	}
}

// Running a job twice (a crash after some customers were handled) neither duplicates a correction nor a credit.
func testRerateRunTwiceIsIdempotent(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.startWorkers(nil)
	completeOctober(e)
	pv := e.newVersion(1200, "15", "2")
	job, _ := e.rerate("2026-10", pv)
	// Put the job back to RUNNING, as after a crash before the final update, and let the scheduler finish it.
	if _, err := shared.system.Exec(context.Background(), `UPDATE rerating_jobs SET status = 'RUNNING', created_at = created_at - interval '5 minutes' WHERE tenant_id = $1 AND id = $2`, e.tid, job.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.mgr.ResumeRerating(context.Background()); err != nil {
		t.Fatal(err)
	}
	done, err := e.mgr.GetReratingJob(context.Background(), e.tid, job.ID)
	if err != nil || done.Status != runs.ReratingCompleted || done.Corrections != 2 {
		t.Fatalf("job after the resume = %+v, %v; want COMPLETED with 2 corrections", done, err)
	}
	if k := e.invoiceKinds("2026-10"); k["CORRECTION"] != 2 {
		t.Fatalf("%d corrections after the resume, want 2", k["CORRECTION"])
	}
}

// The operator's view of an invoice saga: every transition of a paid invoice, in order, ending in COMPLETED.
func testInvoiceSagaTimeline(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.startWorkers(nil)
	completeOctober(e)
	var invoiceID uuid.UUID
	if err := shared.system.QueryRow(context.Background(), `SELECT id FROM invoices WHERE tenant_id = $1 AND snapshot->>'customer_external_id' = 'cus_a'`, e.tid).Scan(&invoiceID); err != nil {
		t.Fatal(err)
	}
	srv := e.api()
	resp, err := srv.GetInvoiceSaga(opCtx(), &billingv1.GetInvoiceSagaRequest{TenantId: e.tid.String(), InvoiceId: invoiceID.String()})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetSaga().GetState() != "COMPLETED" || len(resp.GetEvents()) < 5 {
		t.Fatalf("saga = %v with %d events, want COMPLETED with at least 5", resp.GetSaga().GetState(), len(resp.GetEvents()))
	}
	for i, ev := range resp.GetEvents() {
		if int(ev.GetSeq()) != i+1 || ev.GetAt() == nil || ev.GetToState() == "" {
			t.Fatalf("event %d = %v, want consecutive seq with a time and a state", i, ev)
		}
		if i > 0 && ev.GetFromState() != resp.GetEvents()[i-1].GetToState() {
			t.Fatalf("event %d starts in %s but the previous ended in %s", i, ev.GetFromState(), resp.GetEvents()[i-1].GetToState())
		}
	}
	if _, err := srv.GetInvoiceSaga(context.Background(), &billingv1.GetInvoiceSagaRequest{TenantId: e.tid.String(), InvoiceId: invoiceID.String()}); err == nil {
		t.Fatal("the saga log was readable without the operator flag")
	}
}
