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
	"google.golang.org/protobuf/proto"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
	commandsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/commands/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/stubworker"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/pkg/testkit"
	einvoicecmd "github.com/0furkancolak/payrudder/services/billing-svc/internal/einvoices/command"
)

// einvoiceEnv turns e-invoicing on for the test tenant and gives seller and buyers their tax data.
func einvoiceEnv(t *testing.T, on string) *env {
	e := newEnv(t, envOpts{})
	e.w.tenant.EInvoiceOn = on
	e.w.tenant.Legal = &plansv1.LegalInfo{LegalName: "Acme Yazilim A.S.", TaxId: "1234567890", TaxOffice: "Kadikoy"}
	e.w.tenant.Address = &plansv1.Address{Line1: "Bagdat Cad. 1", City: "Istanbul", Country: "TR"}
	for _, c := range e.w.customers {
		c.TaxId, c.TaxOffice = "11111111111", "Besiktas"
		c.Address.Country = "TR"
	}
	return e
}

type docRow struct {
	ID, Kind, Status, Legal, Number, ErrKey string
	Attempts                                int
}

func docs(t *testing.T, tid uuid.UUID) []docRow {
	t.Helper()
	rows, err := shared.system.Query(context.Background(), `SELECT id::text, kind, status, COALESCE(legal_number,''), number, last_error_key, attempts
		FROM einvoice_documents WHERE tenant_id = $1 ORDER BY created_at, number`, tid)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []docRow
	for rows.Next() {
		var d docRow
		if err := rows.Scan(&d.ID, &d.Kind, &d.Status, &d.Legal, &d.Number, &d.ErrKey, &d.Attempts); err != nil {
			t.Fatal(err)
		}
		out = append(out, d)
	}
	return out
}

func allIssued(t *testing.T, tid uuid.UUID, n int) func() bool {
	return func() bool {
		d := docs(t, tid)
		if len(d) != n {
			return false
		}
		for _, x := range d {
			if x.Status != "issued" {
				return false
			}
		}
		return true
	}
}

// testEInvoiceFinalizedIssuesInvoicesAndCreditNotes: finalized invoices get one document each, issued with the stub's legal number; a credit note gets one that
// refers to the invoice and adds up to the credited amount; the tenant sees them (and only its own); replays create nothing.
func testEInvoiceFinalizedIssuesInvoicesAndCreditNotes(t *testing.T) {
	e := einvoiceEnv(t, "finalized")
	stub := e.startWorkers(nil)
	completeOctober(e)
	testkit.Eventually(t, 20*time.Second, "both invoices e-invoiced", allIssued(t, e.tid, 2))
	if n := stub.Count("einvoice"); n != 2 {
		t.Fatalf("%d e-invoice commands, want 2", n)
	}
	for _, d := range docs(t, e.tid) {
		if d.Kind != "invoice" || d.Legal == "" {
			t.Fatalf("document %+v", d)
		}
	}
	// A command carries the frozen snapshot with the parties.
	var issue *commandsv1.IssueEInvoice
	for _, c := range stub.Commands() {
		if stubworker.Kind(c) == "einvoice" {
			issue = c.GetIssueEInvoice()
		}
	}
	if issue.GetDocument().GetSeller().GetTaxId() != "1234567890" || issue.GetDocument().GetBuyer().GetTaxOffice() != "Besiktas" || issue.GetAction() != "issue" {
		t.Fatalf("snapshot = %v", issue)
	}

	// Finalizing again (a replayed transition) creates no second document.
	if err := e.mgr.Finalize(context.Background(), e.tid, uuid.MustParse(invoiceID(t, e, "cus_a"))); err != nil {
		t.Fatal(err)
	}
	if got := len(docs(t, e.tid)); got != 2 {
		t.Fatalf("%d documents after a replay", got)
	}

	// A credit note for the first invoice.
	svc := e.api()
	cn, err := svc.CreateCreditNote(tenant.WithOperator(context.Background()), &billingv1.CreateCreditNoteRequest{TenantId: e.tid.String(), InvoiceId: invoiceID(t, e, "cus_a"), AmountMinor: 300, Reason: "goodwill"})
	if err != nil {
		t.Fatal(err)
	}
	testkit.Eventually(t, 20*time.Second, "credit note e-invoiced", allIssued(t, e.tid, 3))
	var cnCmd *commandsv1.IssueEInvoice
	for _, c := range stub.Commands() {
		if x := c.GetIssueEInvoice(); x != nil && x.GetKind() == "credit_note" {
			cnCmd = x
		}
	}
	if cnCmd == nil || cnCmd.GetDocument().GetNumber() != cn.GetCreditNote().GetNumber() || cnCmd.GetOriginalNumber() == "" || cnCmd.GetOriginalDocumentId() == "" {
		t.Fatalf("credit note command = %v", cnCmd)
	}
	var sum int64
	for _, l := range cnCmd.GetDocument().GetLines() {
		sum += l.GetAmountMinor()
	}
	if sum != 300 || cnCmd.GetDocument().GetTotalMinor() != 300 {
		t.Fatalf("credit note lines add up to %d, want 300", sum)
	}

	// The tenant lists its documents; another tenant sees none (RLS).
	tctx := tenant.NewContext(context.Background(), tenant.Info{ID: e.tid})
	list, err := svc.ListEInvoices(tctx, &billingv1.ListEInvoicesRequest{PageSize: 2})
	if err != nil || len(list.GetDocuments()) != 2 || list.GetTotal() != 3 || list.GetNextPageToken() == "" {
		t.Fatalf("list = %v, %v", list, err)
	}
	only, err := svc.ListEInvoices(tctx, &billingv1.ListEInvoicesRequest{Status: "issued", CreditNoteId: cn.GetCreditNote().GetId()})
	if err != nil || len(only.GetDocuments()) != 1 || only.GetDocuments()[0].GetKind() != "credit_note" {
		t.Fatalf("filtered = %v, %v", only, err)
	}
	other := tenant.NewContext(context.Background(), tenant.Info{ID: uuid.New()})
	if l, err := svc.ListEInvoices(other, &billingv1.ListEInvoicesRequest{}); err != nil || len(l.GetDocuments()) != 0 {
		t.Fatalf("another tenant sees %v, %v", l, err)
	}
	if _, err := svc.GetEInvoice(other, &billingv1.GetEInvoiceRequest{Id: only.GetDocuments()[0].GetId()}); status.Code(err) != codes.NotFound || errkey.Of(err) != "einvoice.document.not_found" {
		t.Fatalf("another tenant's get: %v", err)
	}
	// No object storage is configured in the test: the file link says so rather than lying.
	if _, err := svc.GetEInvoiceFileUrl(tctx, &billingv1.GetEInvoiceFileUrlRequest{Id: only.GetDocuments()[0].GetId(), Kind: "xml"}); status.Code(err) != codes.Unimplemented {
		t.Fatalf("file url: %v", err)
	}
	// Cancel where the stub says yes: issued -> cancelled.
	if _, err := svc.CancelEInvoice(tenant.WithOperator(context.Background()), &billingv1.CancelEInvoiceRequest{TenantId: e.tid.String(), Id: only.GetDocuments()[0].GetId(), Reason: "mistake"}); err != nil {
		t.Fatal(err)
	}
	testkit.Eventually(t, 20*time.Second, "document cancelled", func() bool {
		d, err := svc.GetEInvoice(tctx, &billingv1.GetEInvoiceRequest{Id: only.GetDocuments()[0].GetId()})
		return err == nil && d.GetDocument().GetStatus() == "cancelled"
	})
}

func invoiceID(t *testing.T, e *env, customer string) string {
	t.Helper()
	var id string
	if err := shared.system.QueryRow(context.Background(), `SELECT id::text FROM invoices WHERE tenant_id = $1 AND snapshot->>'customer_external_id' = $2 AND status <> 'void' ORDER BY created_at LIMIT 1`,
		e.tid, customer).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// testEInvoicePaidModeWaitsForPayment: with e_invoice_on = paid there is no document until the saga reaches Paid, and a tenant with the setting off gets none.
func testEInvoicePaidModeWaitsForPayment(t *testing.T) {
	t.Run("declined", paidModeDeclined)
	t.Run("off", settingOff)
	t.Run("paid", paidModePaid)
}

func paidModeDeclined(t *testing.T) {
	e := einvoiceEnv(t, "paid")
	// The charge is declined, so the invoices are finalized but never paid.
	e.startWorkers(func(cmd *commandsv1.CommandEnvelope, seen int) stubworker.Action {
		if stubworker.Kind(cmd) == "charge" {
			return stubworker.Action{Reply: stubworker.Declined}
		}
		return stubworker.Action{Reply: stubworker.Succeed}
	})
	e.addUsage("2026-10", "cus_a", "calls", "150", 0)
	e.driveRun("2026-10")
	testkit.Eventually(t, 20*time.Second, "declined sagas", func() bool {
		s := e.sagaStates("2026-10")
		return len(s) == 2 && s["cus_a"] != "" && s["cus_a"] != "PDF_REQUESTED" && s["cus_a"] != "CHARGE_REQUESTED"
	})
	if n := len(docs(t, e.tid)); n != 0 {
		t.Fatalf("%d documents before any payment", n)
	}

}

func settingOff(t *testing.T) {
	off := einvoiceEnv(t, "off")
	off.startWorkers(nil)
	completeOctober(off)
	if n := len(docs(t, off.tid)); n != 0 {
		t.Fatalf("%d documents with the setting off", n)
	}
}

func paidModePaid(t *testing.T) {
	on := einvoiceEnv(t, "paid")
	on.startWorkers(nil)
	completeOctober(on)
	testkit.Eventually(t, 20*time.Second, "paid invoices e-invoiced", allIssued(t, on.tid, 2))
}

// testEInvoiceRetriesWithBackoffAndTheOperatorRetriesFailures: a timeout is retried after the backoff; a refusal waits for the operator, whose retry reads the
// parties afresh and sends the same document under the same id; a failed document is not retried automatically.
func testEInvoiceRetriesWithBackoffAndTheOperatorRetriesFailures(t *testing.T) {
	e := einvoiceEnv(t, "finalized")
	var refuse = map[string]bool{}
	stub := e.startWorkers(func(cmd *commandsv1.CommandEnvelope, seen int) stubworker.Action {
		if stubworker.Kind(cmd) != "einvoice" {
			return stubworker.Action{Reply: stubworker.Succeed}
		}
		tax := cmd.GetIssueEInvoice().GetDocument().GetBuyer().GetTaxId()
		switch {
		case seen == 0 && cmd.GetIssueEInvoice().GetDocument().GetBuyer().GetName() == "Customer cus_a":
			return stubworker.Action{Reply: stubworker.EInvoiceRetryable} // a timeout on the first attempt
		case refuse[cmd.GetIssueEInvoice().GetDocumentId()] || (tax == "00000000000" && cmd.GetIssueEInvoice().GetDocument().GetBuyer().GetName() == "Customer cus_b"):
			return stubworker.Action{Reply: stubworker.EInvoiceRejected}
		}
		return stubworker.Action{Reply: stubworker.Succeed}
	})
	e.customer("cus_b").TaxId = "00000000000" // the integrator refuses this buyer
	completeOctober(e)

	testkit.Eventually(t, 20*time.Second, "a is pending with its next attempt, b is failed", func() bool {
		var a, b string
		for _, d := range docs(t, e.tid) {
			if d.ErrKey == "einvoice.provider.unavailable" && d.Status == "pending" {
				a = d.ID
			}
			if d.Status == "failed" && d.ErrKey == "einvoice.provider.rejected" {
				b = d.ID
			}
		}
		return a != "" && b != ""
	})
	sweeper := &einvoicecmd.Commands{Pool: shared.system, Now: e.clk.Now}
	if n, err := sweeper.Sweep(context.Background(), 10); err != nil || n != 0 {
		// The retry is due 1 minute after the reply (real time); the test clock is days ahead of it, so it is due: the sweep must find exactly cus_a's document.
		if err != nil || n != 1 {
			t.Fatalf("sweep = %d, %v", n, err)
		}
	}
	testkit.Eventually(t, 20*time.Second, "a retried and issued", func() bool {
		issued := 0
		for _, d := range docs(t, e.tid) {
			if d.Status == "issued" {
				issued++
				if d.Attempts < 2 {
					t.Errorf("issued after %d attempts, want a retry", d.Attempts)
				}
			}
		}
		return issued == 1
	})
	if n, err := sweeper.Sweep(context.Background(), 10); err != nil || n != 0 {
		t.Fatalf("a failed document must not be retried automatically: sweep = %d, %v", n, err)
	}

	// The operator fixes the buyer's tax id and retries: the parties are read afresh, the document id is unchanged.
	e.customer("cus_b").TaxId = "22222222222"
	var failedID string
	for _, d := range docs(t, e.tid) {
		if d.Status == "failed" {
			failedID = d.ID
		}
	}
	svc := e.api()
	op := tenant.WithOperator(context.Background())
	if _, err := svc.RetryEInvoice(op, &billingv1.RetryEInvoiceRequest{TenantId: e.tid.String(), Id: uuid.NewString()}); errkey.Of(err) != "einvoice.document.not_found" {
		t.Fatalf("retry of an unknown document: %v", err)
	}
	if _, err := svc.RetryEInvoice(op, &billingv1.RetryEInvoiceRequest{TenantId: e.tid.String(), Id: failedID}); err != nil {
		t.Fatal(err)
	}
	testkit.Eventually(t, 20*time.Second, "both issued", allIssued(t, e.tid, 2))
	var last *commandsv1.IssueEInvoice
	for _, c := range stub.Commands() {
		if x := c.GetIssueEInvoice(); x != nil && x.GetDocumentId() == failedID {
			last = x
		}
	}
	if last.GetDocument().GetBuyer().GetTaxId() != "22222222222" {
		t.Fatalf("the retry carried buyer tax id %q, want the corrected one", last.GetDocument().GetBuyer().GetTaxId())
	}
	// An issued document cannot be retried.
	if _, err := svc.RetryEInvoice(op, &billingv1.RetryEInvoiceRequest{TenantId: e.tid.String(), Id: failedID}); errkey.Of(err) != "einvoice.document.not_failed" {
		t.Fatalf("retry of an issued document: %v", err)
	}
}

func testEInvoiceSilentWorkerIsAskedAgain(t *testing.T) {
	e := einvoiceEnv(t, "finalized")
	var answers atomic.Int32
	e.startWorkers(func(cmd *commandsv1.CommandEnvelope, seen int) stubworker.Action {
		if stubworker.Kind(cmd) == "einvoice" {
			if answers.Add(1) == 1 {
				return stubworker.Action{} // no reply
			}
		}
		return stubworker.Action{Reply: stubworker.Succeed}
	})
	completeOctober(e)
	testkit.Eventually(t, 20*time.Second, "one document waiting", func() bool {
		issued := 0
		for _, d := range docs(t, e.tid) {
			if d.Status == "issued" {
				issued++
			}
		}
		return issued == 1
	})
	sweeper := &einvoicecmd.Commands{Pool: shared.system, Now: func() time.Time { return e.clk.Now().Add(einvoicecmd.ReplyWait + time.Minute) }}
	if n, err := sweeper.Sweep(context.Background(), 10); err != nil || n != 1 {
		t.Fatalf("sweep = %d, %v", n, err)
	}
	testkit.Eventually(t, 20*time.Second, "both issued", allIssued(t, e.tid, 2))
}

var _ = proto.Equal
