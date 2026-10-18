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
		{Draft, EvStart}, // drafts are finalized by the run, not by the saga
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
