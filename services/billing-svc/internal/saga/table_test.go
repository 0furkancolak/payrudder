package saga

import (
	"os"
	"strings"
	"testing"
)

var guardContexts = []Ctx{
	{TotalMinor: 0, Paid: false, DunningAttempt: 0, MaxDunning: 3},
	{TotalMinor: 100, Paid: false, DunningAttempt: 0, MaxDunning: 3},
	{TotalMinor: 100, Paid: true, DunningAttempt: 1, MaxDunning: 3},
	{TotalMinor: 100, Paid: false, DunningAttempt: 3, MaxDunning: 3},
	{TotalMinor: 0, Paid: true, DunningAttempt: 3, MaxDunning: 3},
	{TotalMinor: 100, Link: true, DunningAttempt: 0, MaxDunning: 3},
	{TotalMinor: 100, Link: true, DunningAttempt: 3, MaxDunning: 3},
	{TotalMinor: 0, Link: true, Paid: true, DunningAttempt: 0, MaxDunning: 0},
}

// Every (state, event) pair either has exactly one row for each guard context or none: guards never overlap and never
// leave a gap, so the engine can't pick between transitions or fall through.
func TestTableIsDeterministic(t *testing.T) {
	for _, s := range States {
		for _, ev := range Events {
			var rows []Row
			for _, r := range Table {
				if r.From == s && r.Event == ev {
					rows = append(rows, r)
				}
			}
			if len(rows) == 0 {
				for _, c := range guardContexts {
					if _, ok := Next(s, ev, c); ok {
						t.Errorf("(%s, %s) has no row but Next accepted it", s, ev)
					}
				}
				continue
			}
			for _, c := range guardContexts {
				matches := 0
				for _, r := range rows {
					if r.Guard.holds(c) {
						matches++
					}
				}
				if matches != 1 {
					t.Errorf("(%s, %s) with %+v matches %d rows, want exactly 1", s, ev, c, matches)
				}
			}
		}
	}
}

func TestInvalidPairsAreRejected(t *testing.T) {
	invalid := []struct {
		s  State
		ev Event
	}{
		{Completed, EvPaymentOK}, // a late reply after the end
		{Voided, EvPdfRendered},  // a render reply for a voided invoice
		{PaymentDeclined, EvPaymentOK},
		{PaymentRequested, EvVoid}, // never void while a charge is in flight
		{PaymentUnknown, EvVoid},
		{Finalized, EvPaymentOK},
		{Draft, EvStart},  // drafts are finalized by the run, not by the saga
		{Paid, EvSettled}, // a second payment of a paid invoice is a discrepancy for the operator, not a transition
		{Completed, EvSettled},
		{Completed, EvPayRequested},
		{Voided, EvSettled},
		{PdfRequested, EvPayRequested}, // the invoice is not ready to be paid yet
	}
	for _, tc := range invalid {
		if _, ok := Next(tc.s, tc.ev, Ctx{TotalMinor: 100, MaxDunning: 3}); ok {
			t.Errorf("(%s, %s) must be invalid", tc.s, tc.ev)
		}
	}
}

func TestTransitionsOfInterest(t *testing.T) {
	c := Ctx{TotalMinor: 500, MaxDunning: 3}
	check := func(s State, ev Event, ctx Ctx, to State, eff Effect) {
		t.Helper()
		r, ok := Next(s, ev, ctx)
		if !ok || r.To != to || r.Effect != eff {
			t.Errorf("Next(%s, %s) = %+v %v, want %s/%q", s, ev, r, ok, to, eff)
		}
	}
	check(PdfRendered, EvContinue, c, PaymentRequested, EffCharge)
	check(PdfRendered, EvContinue, Ctx{}, NotifyRequested, EffNotify) // total 0 skips the payment
	check(PaymentRequested, EvPaymentErrored, c, PaymentUnknown, EffQueryStatus)
	check(PaymentUnknown, EvStatusSucceeded, c, Paid, EffNone) // no second charge
	check(PaymentUnknown, EvStatusNotFound, c, PaymentRequested, EffCharge)
	check(PaymentRequested, EvPaymentDeclined, Ctx{TotalMinor: 1, DunningAttempt: 2, MaxDunning: 3}, PaymentDeclined, EffScheduleDunning)
	check(PaymentRequested, EvPaymentDeclined, Ctx{TotalMinor: 1, DunningAttempt: 3, MaxDunning: 3}, Uncollectible, EffNone)
	check(Completed, EvVoid, Ctx{TotalMinor: 5, Paid: true}, RefundRequested, EffRefund)
	check(Completed, EvVoid, Ctx{TotalMinor: 5, Paid: false}, Voided, EffNone)
}

// The diagram in docs/03-services/billing-svc.md between the markers is generated from the table.
func TestDocsDiagramIsInSync(t *testing.T) {
	raw, err := os.ReadFile("../../../../docs/03-services/billing-svc.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(raw)
	start := strings.Index(doc, "<!-- saga-diagram:start -->")
	end := strings.Index(doc, "<!-- saga-diagram:end -->")
	if start < 0 || end < start {
		t.Fatal("docs/03-services/billing-svc.md lacks the saga-diagram markers")
	}
	want := "<!-- saga-diagram:start -->\n```mermaid\n" + Mermaid() + "```\n"
	if got := doc[start:end]; got != want {
		if os.Getenv("UPDATE_DOCS") != "" {
			out := doc[:start] + want + doc[end:]
			if err := os.WriteFile("../../../../docs/03-services/billing-svc.md", []byte(out), 0o644); err != nil {
				t.Fatal(err)
			}
			return
		}
		t.Fatalf("the saga diagram in the docs is stale; run UPDATE_DOCS=1 go test ./internal/saga")
	}
}

// A link-collected invoice (phase 34) waits for its customer; a decline is the customer's to retry, never dunning.
func TestLinkCollectedInvoiceWaitsForTheCustomer(t *testing.T) {
	link := Ctx{TotalMinor: 500, Link: true, MaxDunning: 3}
	check := func(s State, ev Event, ctx Ctx, to State, eff Effect) {
		t.Helper()
		r, ok := Next(s, ev, ctx)
		if !ok || r.To != to || r.Effect != eff {
			t.Errorf("Next(%s, %s) = %+v %v, want %s/%q", s, ev, r, ok, to, eff)
		}
	}
	check(PdfRendered, EvContinue, link, AwaitingPayment, EffNone) // no charge of a stored method
	check(AwaitingPayment, EvPayRequested, link, PaymentRequested, EffChargeDunning)
	check(AwaitingPayment, EvSettled, link, Paid, EffNone)
	check(AwaitingPayment, EvVoid, link, Voided, EffNone)
	check(PaymentRequested, EvPaymentDeclined, link, AwaitingPayment, EffNone)
	check(PaymentUnknown, EvStatusDeclined, link, AwaitingPayment, EffNone)
	check(PaymentRequested, EvSettled, link, Paid, EffNone)
	// An ordinary invoice in dunning can be paid by its customer too.
	check(PaymentDeclined, EvPayRequested, Ctx{TotalMinor: 500, MaxDunning: 3}, PaymentRequested, EffChargeDunning)
	check(PaymentDeclined, EvSettled, Ctx{TotalMinor: 500, MaxDunning: 3}, Paid, EffNone)
	// And an ordinary invoice is still charged by the saga.
	check(PdfRendered, EvContinue, Ctx{TotalMinor: 500, MaxDunning: 3}, PaymentRequested, EffCharge)
}

func TestProviderThatMailsItsOwnReceiptSkipsOurs(t *testing.T) {
	got, ok := Next(Paid, EvContinue, Ctx{TotalMinor: 100, Paid: true, ProviderMails: true})
	if !ok || got.To != Completed || got.Effect != EffNone {
		t.Fatalf("provider mails: %+v %v", got, ok)
	}
	got, ok = Next(Paid, EvContinue, Ctx{TotalMinor: 100, Paid: true})
	if !ok || got.To != NotifyRequested || got.Effect != EffNotify {
		t.Fatalf("we mail: %+v %v", got, ok)
	}
	// A give-up notice is still ours: the provider's receipts are for payments.
	got, _ = Next(Uncollectible, EvContinue, Ctx{ProviderMails: true})
	if got.To != NotifyRequested {
		t.Fatalf("uncollectible with a mailing provider: %+v", got)
	}
}
