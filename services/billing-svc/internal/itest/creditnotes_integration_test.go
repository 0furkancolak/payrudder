//go:build integration

package itest

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	billingv1 "github.com/0furkancolak/quotify/gen/go/quotify/billing/v1"
	"github.com/0furkancolak/quotify/pkg/tenant"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/api"
)

var seededInvoices int

func seedOne(t *testing.T, tid, customer uuid.UUID, ext, status, currency, number string, total int64, env string) uuid.UUID {
	t.Helper()
	seededInvoices++
	period := fmt.Sprintf("2025-%02d", seededInvoices%12+1) // one regular invoice per customer and period
	id := uuid.New()
	if _, err := shared.system.Exec(context.Background(), `INSERT INTO invoices (tenant_id, id, customer_id, period_id, kind, number, plan_version_id, currency, subtotal_minor, total_minor, status, snapshot, provider)
		VALUES ($1,$2,$3,$10,'REGULAR',$4,$5,$6,$7,$7,$8,$9::jsonb,'')`,
		tid, id, customer, number, uuid.New(), currency, total, status, `{"customer_external_id":"`+ext+`","environment":"`+env+`"}`, period); err != nil {
		t.Fatal(err)
	}
	return id
}

func testCreditNotes(t *testing.T) {
	e := newEnv(t, envOpts{})
	svc := api.New(shared.app, shared.system, e.mgr, e.eng)
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
	if err := shared.system.QueryRow(context.Background(), `SELECT count(*), sum(amount_minor), max(currency) FROM credit_balances WHERE tenant_id = $1 AND customer_id = $2`, e.tid, customer).Scan(&n, &sum, &cur); err != nil {
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
