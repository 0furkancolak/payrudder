package model

import (
	"os"
	"strings"
	"testing"
	"time"
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
	// The payment gate says no (Ctx.Held), for every kind of invoice.
	{TotalMinor: 100, MaxDunning: 3, Held: true},
	{TotalMinor: 0, MaxDunning: 3, Held: true},
	{TotalMinor: 100, Link: true, MaxDunning: 3, Held: true},
	// A "not found" answer at each side of both limits, and with the horizon turned off.
	{TotalMinor: 100, MaxDunning: 3, NotFoundGrace: 10 * time.Minute, KeyHorizon: 24 * time.Hour},
	{TotalMinor: 100, MaxDunning: 3, NotFoundGrace: 10 * time.Minute, KeyHorizon: 24 * time.Hour, ChargeAge: 10 * time.Minute},
	{TotalMinor: 100, MaxDunning: 3, NotFoundGrace: 10 * time.Minute, KeyHorizon: 24 * time.Hour, ChargeAge: 24 * time.Hour},
	{TotalMinor: 100, MaxDunning: 3, NotFoundGrace: 10 * time.Minute, KeyHorizon: 24 * time.Hour, ChargeAge: 25 * time.Hour},
	{TotalMinor: 100, MaxDunning: 3, NotFoundGrace: 10 * time.Minute, ChargeAge: 25 * time.Hour},
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
		{Finalized, EvPaymentOK},
		{Draft, EvStart},  // drafts are finalized by the run, not by the saga
		{Paid, EvSettled}, // a second payment of a paid invoice is a discrepancy for the operator, not a transition
		{Completed, EvPayRequested},
		{Voided, EvSettled},
		{Paid, EvProviderSettled}, // a second payment of a paid invoice is a discrepancy for the operator, not a transition
		{Completed, EvProviderSettled},
		{Voided, EvProviderSettled},
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

// A payment that settles later (a bank transfer) is waited for, polled, settled by a receipt or by hand, and given up on through the ordinary dunning path.
func TestPendingPaymentTransitions(t *testing.T) {
	c := Ctx{TotalMinor: 500, MaxDunning: 3}
	check := func(s State, ev Event, ctx Ctx, to State, eff Effect) {
		t.Helper()
		r, ok := Next(s, ev, ctx)
		if !ok || r.To != to || r.Effect != eff {
			t.Errorf("Next(%s, %s) = %+v %v, want %s/%q", s, ev, r, ok, to, eff)
		}
	}
	check(PaymentRequested, EvPaymentErrored, c, PaymentUnknown, EffQueryStatus) // the charge answers "accepted, not settled": ask the status
	check(PaymentUnknown, EvStatusPending, c, PaymentPending, EffAwaitTransfer)  // first time: tell the customer how to pay, schedule a poll
	check(PaymentPending, EvStatusPending, c, PaymentPending, EffSchedulePoll)   // later polls: no second mail
	check(PaymentPending, EvPollDue, c, PaymentPending, EffQueryStatus)
	check(PaymentPending, EvRetry, c, PaymentPending, EffQueryStatus) // the nudge that follows an operator's receipt
	check(PaymentPending, EvStatusSucceeded, c, Paid, EffNone)
	check(PaymentPending, EvExhausted, c, PaymentPending, EffSchedulePoll) // a poll nobody answered waits for the next one, it does not park
	check(PaymentPending, EvStatusDeclined, Ctx{TotalMinor: 500, DunningAttempt: 1, MaxDunning: 3}, PaymentDeclined, EffScheduleDunning)
	check(PaymentPending, EvStatusDeclined, Ctx{TotalMinor: 500, DunningAttempt: 3, MaxDunning: 3}, Uncollectible, EffNone)
	check(PaymentPending, EvVoid, c, Voided, EffNone) // an invoice nobody will pay can be voided
	// A parked invoice whose payment is unknown can be voided too (plan 22 G2 says it can). It is not refunded: nothing is known to have been taken, and the operator confirms in
	// the call that no charge was made (sagas/command Void), which is why this is the one void that needs it.
	check(PaymentUnknown, EvVoid, c, Voided, EffNone)
	check(PaymentUnknown, EvVoid, Ctx{TotalMinor: 500, Paid: true, MaxDunning: 3}, Voided, EffNone)
	check(PaymentDeclined, EvRetry, c, PaymentUnknown, EffQueryStatus)
	for _, s := range []State{PaymentUnknown, PaymentPending, PaymentDeclined, NotifyRequested, NotifyFailed, Completed} {
		check(s, EvSettled, c, Paid, EffNone)
	}
	for _, s := range []State{PaymentRequested, PdfRequested, Finalized, Voided, Paid} {
		if _, ok := Next(s, EvSettled, c); ok {
			t.Errorf("(%s, OfflineSettlement) must be invalid: a charge is in flight, or the invoice is not payable", s)
		}
	}
	if PaymentPending.Awaiting() {
		t.Error("no command is outstanding while a transfer is awaited: there is no step deadline")
	}
}

// A charge the provider will not take without the customer (3-D Secure) is not a decline: the customer is mailed once, the status is polled, the customer's own payment on
// the invoice page settles it, and what the status says afterwards decides (a decline only after the window of the pending payments, then dunning as usual).
func TestPaymentThatNeedsTheCustomerTransitions(t *testing.T) {
	c := Ctx{TotalMinor: 500, MaxDunning: 3}
	check := func(s State, ev Event, ctx Ctx, to State, eff Effect) {
		t.Helper()
		r, ok := Next(s, ev, ctx)
		if !ok || r.To != to || r.Effect != eff {
			t.Errorf("Next(%s, %s) = %+v %v, want %s/%q", s, ev, r, ok, to, eff)
		}
	}
	check(PaymentRequested, EvPaymentActionRequired, c, PaymentActionRequired, EffAwaitAction)      // the mail, once
	check(PaymentUnknown, EvPaymentActionRequired, c, PaymentActionRequired, EffAwaitAction)        // the original reply arrives late
	check(PaymentUnknown, EvStatusActionRequired, c, PaymentActionRequired, EffAwaitAction)         // the status of a charge whose answer was lost
	check(PaymentActionRequired, EvStatusActionRequired, c, PaymentActionRequired, EffSchedulePoll) // later polls: no second mail
	check(PaymentActionRequired, EvPollDue, c, PaymentActionRequired, EffQueryStatus)
	check(PaymentActionRequired, EvRetry, c, PaymentActionRequired, EffQueryStatus)
	check(PaymentActionRequired, EvExhausted, c, PaymentActionRequired, EffSchedulePoll)
	check(PaymentActionRequired, EvStatusSucceeded, c, Paid, EffNone)
	check(PaymentActionRequired, EvProviderSettled, c, Paid, EffNone) // the customer paid on the invoice page
	check(PaymentActionRequired, EvSettled, c, Paid, EffNone)         // or the seller recorded a transfer
	check(PaymentActionRequired, EvVoid, c, Voided, EffNone)
	check(PaymentActionRequired, EvStatusDeclined, Ctx{TotalMinor: 500, DunningAttempt: 1, MaxDunning: 3}, PaymentDeclined, EffScheduleDunning)
	check(PaymentActionRequired, EvStatusDeclined, Ctx{TotalMinor: 500, DunningAttempt: 3, MaxDunning: 3}, Uncollectible, EffNone)
	check(PaymentActionRequired, EvStatusDeclinedFinal, c, Uncollectible, EffNone)
	if PaymentActionRequired.Awaiting() || PaymentActionRequired.Terminal() {
		t.Error("no command is outstanding while the customer is asked to act: there is no step deadline, and the invoice is not over")
	}
	// A reply of the charge cannot be a decline or a success again once the saga waits for the customer: those would be a second charge's.
	for _, ev := range []Event{EvPaymentOK, EvPaymentDeclined, EvPaymentErrored} {
		if _, ok := Next(PaymentActionRequired, ev, c); ok {
			t.Errorf("(%s, %s) must be invalid", PaymentActionRequired, ev)
		}
	}
}

// The first charge of an invoice finalized while the payment gate says no is held, not sent and not declined: the invoice stays open, nothing counts as a dunning attempt, the
// sweep or the seller's Retry asks the gate again, and what it lets through is charged as the first charge (EffCharge, not EffChargeDunning).
func TestFirstChargeHeldByThePaymentGate(t *testing.T) {
	open := Ctx{TotalMinor: 500, MaxDunning: 3}
	held := Ctx{TotalMinor: 500, MaxDunning: 3, Held: true}
	check := func(s State, ev Event, ctx Ctx, to State, eff Effect) {
		t.Helper()
		r, ok := Next(s, ev, ctx)
		if !ok || r.To != to || r.Effect != eff {
			t.Errorf("Next(%s, %s) with %+v = %+v %v, want %s/%q", s, ev, ctx, r, ok, to, eff)
		}
	}
	check(PdfRendered, EvContinue, open, PaymentRequested, EffCharge)
	check(PdfRendered, EvContinue, held, PaymentHeld, EffHold)
	// Nothing to charge, or the customer pays: the gate is not the saga's to apply.
	check(PdfRendered, EvContinue, Ctx{Held: true}, NotifyRequested, EffNotify)
	check(PdfRendered, EvContinue, Ctx{TotalMinor: 500, Link: true, Held: true, MaxDunning: 3}, AwaitingPayment, EffNone)
	for _, ev := range []Event{EvDunningDue, EvRetry} {
		check(PaymentHeld, ev, open, PaymentRequested, EffCharge)
		check(PaymentHeld, ev, held, PaymentHeld, EffHold)
	}
	check(PaymentHeld, EvSettled, held, Paid, EffNone) // a transfer the seller recorded
	check(PaymentHeld, EvVoid, held, Voided, EffNone)  // or the seller gives up on the invoice
	if PaymentHeld.Awaiting() || PaymentHeld.Terminal() {
		t.Error("no command is outstanding while the first charge is held, and the invoice is not over")
	}
	for _, ev := range []Event{EvPaymentOK, EvPaymentDeclined, EvPaymentErrored, EvStatusSucceeded, EvProviderSettled, EvPayRequested} {
		if _, ok := Next(PaymentHeld, ev, open); ok {
			t.Errorf("(%s, %s) must be invalid: nothing was sent to a provider", PaymentHeld, ev)
		}
	}
}

// The diagram in docs/03-services/billing-svc.md between the markers is generated from the table.
func TestDocsDiagramIsInSync(t *testing.T) {
	raw, err := os.ReadFile("../../../../../docs/03-services/billing-svc.md")
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
			if err := os.WriteFile("../../../../../docs/03-services/billing-svc.md", []byte(out), 0o644); err != nil {
				t.Fatal(err)
			}
			return
		}
		t.Fatalf("the saga diagram in the docs is stale; run UPDATE_DOCS=1 go test ./internal/sagas/model")
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
	check(AwaitingPayment, EvProviderSettled, link, Paid, EffNone)
	check(AwaitingPayment, EvVoid, link, Voided, EffNone)
	check(PaymentRequested, EvPaymentDeclined, link, AwaitingPayment, EffNone)
	check(PaymentUnknown, EvStatusDeclined, link, AwaitingPayment, EffNone)
	check(PaymentRequested, EvProviderSettled, link, Paid, EffNone)
	// An ordinary invoice in dunning can be paid by its customer too.
	check(PaymentDeclined, EvPayRequested, Ctx{TotalMinor: 500, MaxDunning: 3}, PaymentRequested, EffChargeDunning)
	check(PaymentDeclined, EvProviderSettled, Ctx{TotalMinor: 500, MaxDunning: 3}, Paid, EffNone)
	// And an ordinary invoice is still charged by the saga.
	check(PdfRendered, EvContinue, Ctx{TotalMinor: 500, MaxDunning: 3}, PaymentRequested, EffCharge)
}

// A provider that says a payment must not be tried again (PaymentDeclined.final) ends dunning at once, however many attempts are left: the invoice is uncollectible
// (still payable by the customer on the hosted page), or for a link-collected invoice back with its customer. An ordinary decline with attempts left is dunned.
func TestADeclineThatMustNotBeRetriedSkipsDunning(t *testing.T) {
	left := Ctx{TotalMinor: 500, DunningAttempt: 0, MaxDunning: 3}
	for _, c := range []struct {
		from State
		ev   Event
	}{
		{PaymentRequested, EvPaymentDeclinedFinal}, {PaymentUnknown, EvPaymentDeclinedFinal}, {PaymentUnknown, EvStatusDeclinedFinal}, {PaymentPending, EvStatusDeclinedFinal},
	} {
		if r, ok := Next(c.from, c.ev, left); !ok || r.To != Uncollectible || r.Effect != EffNone {
			t.Errorf("Next(%s, %s) with attempts left = %+v %v, want UNCOLLECTIBLE", c.from, c.ev, r, ok)
		}
		if r, ok := Next(c.from, c.ev, Ctx{TotalMinor: 500, Link: true, MaxDunning: 3}); !ok || r.To != AwaitingPayment {
			t.Errorf("Next(%s, %s) on a link = %+v %v, want AWAITING_PAYMENT", c.from, c.ev, r, ok)
		}
	}
	if r, _ := Next(PaymentRequested, EvPaymentDeclined, left); r.To != PaymentDeclined || r.Effect != EffScheduleDunning {
		t.Errorf("an ordinary decline with attempts left = %+v, want dunning", r)
	}
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

// A "not found" status answer is trusted only inside a window of the charge's age: before the grace period the status is asked again later (a provider may not show a
// charge it has just taken, or committed late after a lost answer), after the key horizon the saga parks instead of charging again with a key the provider may have
// forgotten (G2 of plan 22). The limits themselves belong to the trusted window.
func TestNotFoundIsReadByTheAgeOfTheCharge(t *testing.T) {
	const grace, horizon = 10 * time.Minute, 24 * time.Hour
	for name, tc := range map[string]struct {
		age, horizon time.Duration
		to           State
		eff          Effect
	}{
		"just charged":              {0, horizon, PaymentUnknown, EffRecheck},
		"a nanosecond before grace": {grace - 1, horizon, PaymentUnknown, EffRecheck},
		"exactly at the grace":      {grace, horizon, PaymentRequested, EffCharge},
		"in the middle":             {6 * time.Hour, horizon, PaymentRequested, EffCharge},
		"exactly at the horizon":    {horizon, horizon, PaymentRequested, EffCharge},
		"past the horizon":          {horizon + 1, horizon, PaymentUnknown, EffNone},
		"a week old":                {7 * 24 * time.Hour, horizon, PaymentUnknown, EffNone},
		"no horizon set":            {7 * 24 * time.Hour, 0, PaymentRequested, EffCharge},
	} {
		r, ok := Next(PaymentUnknown, EvStatusNotFound, Ctx{TotalMinor: 500, MaxDunning: 3, NotFoundGrace: grace, KeyHorizon: tc.horizon, ChargeAge: tc.age})
		if !ok || r.To != tc.to || r.Effect != tc.eff {
			t.Errorf("%s: Next = %+v %v, want %s/%q", name, r, ok, tc.to, tc.eff)
		}
	}
	// Any other answer is not read by age: a charge that was found is never charged again, however old.
	old := Ctx{TotalMinor: 500, MaxDunning: 3, NotFoundGrace: grace, KeyHorizon: horizon, ChargeAge: 30 * 24 * time.Hour}
	if r, ok := Next(PaymentUnknown, EvStatusSucceeded, old); !ok || r.To != Paid || r.Effect != EffNone {
		t.Errorf("a found charge, old: %+v %v", r, ok)
	}
}
