//go:build integration

package itest

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
	"github.com/0furkancolak/payrudder/pkg/testkit"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/credits/command"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/credits/query"
)

// A credit pack that was paid for and then voided or credited keeps its credits unless they are taken back. They are, as a "revoke" row of the append-only ledger: the whole
// lot for a void, a proportional part for a credit note (the notes of an invoice add up to the lot exactly), and no more than the customer still has (the balance is never
// negative); what could not be taken is on record in the row's reason. A replay takes nothing more.
func testAVoidedOrCreditedPaidPackTakesItsCreditsBack(t *testing.T) {
	l := newLinkEnv(t, envOpts{sandbox: true}, false)
	l.startWorkers(nil)
	pack := l.pack("Starter", 1000, 500, "USD", 0)

	// The scenarios share one customer and run in turn; each ends with its lot used up, so the balance is 0 again for the next.
	const external = "cus_a"
	cid := uuid.MustParse(l.customer(external).Id)
	// buy pays a pack of 1000 credits for 500 and returns the invoice.
	buy := func() uuid.UUID {
		l.t.Helper()
		c := l.create(&billingv1.CreatePaymentLinkRequest{PackId: pack.GetId(), CustomerExternalId: external})
		child := l.start(c.GetToken())
		l.waitState(child, "open")
		if _, err := l.svc.RequestHostedPayment(l.visitor, &billingv1.RequestHostedPaymentRequest{Token: child, Method: "mock"}); err != nil {
			l.t.Fatal(err)
		}
		page := l.waitState(child, "paid")
		if got := balanceOf(t, l.tid, cid); got != 1000 {
			t.Fatalf("credits after the purchase = %d, want 1000", got)
		}
		return uuid.MustParse(page.GetInvoice().GetId())
	}
	note := func(invoice uuid.UUID, amount int64) {
		t.Helper()
		if _, err := l.svc.CreateCreditNote(opCtx(), &billingv1.CreateCreditNoteRequest{TenantId: l.tid.String(), InvoiceId: invoice.String(), AmountMinor: amount, Reason: "returned"}); err != nil {
			t.Fatalf("credit note of %d: %v", amount, err)
		}
	}
	void := func(invoice uuid.UUID) {
		t.Helper()
		if _, err := l.svc.VoidInvoice(opCtx(), &billingv1.VoidInvoiceRequest{TenantId: l.tid.String(), InvoiceId: invoice.String(), Reason: "returned"}); err != nil {
			t.Fatalf("void: %v", err)
		}
		testkit.Eventually(t, 30*time.Second, "the invoice to be void", func() bool {
			var s string
			_ = shared.sql.QueryRow(context.Background(), `SELECT status FROM invoices WHERE tenant_id = $1 AND id = $2`, l.tid, invoice).Scan(&s)
			return s == "void"
		})
	}
	// revokes are the customer's revoke rows, oldest first: the amount and the reason.
	type row struct {
		amount int64
		reason string
	}
	revokes := func() []row {
		t.Helper()
		entries, _, err := query.Ledger(context.Background(), shared.app, l.tid, cid, 50, "")
		if err != nil {
			t.Fatal(err)
		}
		var out []row
		for i := len(entries) - 1; i >= 0; i-- {
			if entries[i].Kind == "revoke" {
				out = append(out, row{entries[i].Amount, entries[i].Reason})
			}
		}
		return out
	}
	spend := func(n int64, key string) {
		t.Helper()
		if _, err := command.Consume(context.Background(), shared.app, l.tid, cid, n, "used", key, ""); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("a void takes back what is left and records what the customer had spent", func(t *testing.T) {
		invoice := buy()
		spend(300, "use-1")
		void(invoice)
		if got := balanceOf(t, l.tid, cid); got != 0 {
			t.Fatalf("credits after the void = %d, want 0", got)
		}
		r := revokes()
		if len(r) != 1 || r[0].amount != -700 || !strings.Contains(r[0].reason, "unrecoverable: 300 of 1000") {
			t.Fatalf("revoke rows = %+v; want one of -700 that records the 300 spent", r)
		}
		// The same void again (a replayed saga step) takes nothing more.
		err := shared.system.InTx(context.Background(), l.tid, pgx.TxOptions{}, func(tx pgx.Tx) error {
			return command.RevokePurchase(context.Background(), tx, l.tid, invoice, 0, 500, 500, "void", "again")
		})
		if err != nil || len(revokes()) != 1 {
			t.Fatalf("a replayed revoke: %v, %d rows; want it to change nothing", err, len(revokes()))
		}
	})

	t.Run("credit notes take a proportional part, the last one the rest", func(t *testing.T) {
		before := len(revokes())
		invoice := buy()
		note(invoice, 100) // a fifth of the invoice
		if got := balanceOf(t, l.tid, cid); got != 800 {
			t.Fatalf("credits after a note of a fifth = %d, want 800", got)
		}
		note(invoice, 0) // what is left
		if got := balanceOf(t, l.tid, cid); got != 0 {
			t.Fatalf("credits after the invoice was credited in full = %d, want 0", got)
		}
		r := revokes()[before:]
		if len(r) != 2 || r[0].amount != -200 || r[1].amount != -800 || strings.Contains(r[0].reason+r[1].reason, "unrecoverable") {
			t.Fatalf("revoke rows = %+v; want -200 then -800", r)
		}
	})

	t.Run("a void after a credit note takes only the rest", func(t *testing.T) {
		before := len(revokes())
		invoice := buy()
		note(invoice, 100)
		void(invoice)
		r := revokes()[before:]
		if got := balanceOf(t, l.tid, cid); got != 0 || len(r) != 2 || r[0].amount != -200 || r[1].amount != -800 {
			t.Fatalf("credits %d, revoke rows %+v; want 0 and -200, -800", got, r)
		}
	})

	t.Run("credits that were all spent leave a row of zero that says so", func(t *testing.T) {
		before := len(revokes())
		invoice := buy()
		spend(1000, "use-all")
		void(invoice)
		r := revokes()[before:]
		if got := balanceOf(t, l.tid, cid); got != 0 || len(r) != 1 || r[0].amount != 0 || !strings.Contains(r[0].reason, "unrecoverable: 1000 of 1000") {
			t.Fatalf("credits %d, revoke rows %+v; want 0 and one row of 0 recording the 1000", got, r)
		}
	})
}
