// Package model is billing-svc's invoice saga as data: the pure transition table (table.go), the snapshot an invoice freezes for its commands and the
// vocabulary around them. The table is the single source of truth: tests check it exhaustively and docs/03-services/billing-svc.md embeds the diagram
// rendered from it. The engine that applies it is sagas/command.
package model

import (
	"fmt"
	"strings"
	"time"
)

// State of an invoice saga.
type State string

const (
	Draft            State = "DRAFT"
	Finalized        State = "FINALIZED"
	PdfRequested     State = "PDF_REQUESTED"
	PdfRendered      State = "PDF_RENDERED"
	PdfFailed        State = "PDF_FAILED"
	PaymentRequested State = "PAYMENT_REQUESTED"
	Paid             State = "PAID"
	PaymentDeclined  State = "PAYMENT_DECLINED"
	PaymentUnknown   State = "PAYMENT_UNKNOWN"
	PaymentPending   State = "PAYMENT_PENDING" // accepted, settles later (a bank transfer): no command is outstanding
	// PaymentActionRequired: the provider needs the customer to act (3-D Secure) before it charges; the customer has been mailed a link to the invoice page. No command is
	// outstanding. The invoice is payable on its page; the status is asked on a schedule, and past the window of the pending payments the charge counts as declined.
	PaymentActionRequired State = "PAYMENT_ACTION_REQUIRED"
	// PaymentHeld: the invoice is ready to be charged for the first time but the payment gate says no (the operator has taken the tenant out of service, or the seller has
	// blocked the customer for fraud or a chargeback). Nothing was sent to a provider and no command is outstanding; the invoice stays open for the seller to release
	// (Retry) or void, and the dunning sweep charges it, as the first charge, when the gate lets it through. It is not a decline and counts as no dunning attempt.
	PaymentHeld     State = "PAYMENT_HELD"
	Uncollectible   State = "UNCOLLECTIBLE"
	NotifyRequested State = "NOTIFY_REQUESTED"
	NotifyFailed    State = "NOTIFY_FAILED"
	Completed       State = "COMPLETED"
	RefundRequested State = "REFUND_REQUESTED"
	RefundFailed    State = "REFUND_FAILED"
	Refunded        State = "REFUNDED"
	Voided          State = "VOIDED"
	// AwaitingPayment: a link-collected invoice (phase 34) waits for its customer to pay; nothing is outstanding, so there is no deadline.
	AwaitingPayment State = "AWAITING_PAYMENT"
)

// States lists every state.
var States = []State{Draft, Finalized, PdfRequested, PdfRendered, PdfFailed, PaymentRequested, Paid, PaymentDeclined, PaymentUnknown, PaymentPending, PaymentActionRequired, PaymentHeld,
	Uncollectible, NotifyRequested, NotifyFailed, Completed, RefundRequested, RefundFailed, Refunded, Voided, AwaitingPayment}

// Terminal states end the saga (COMPLETED can still be voided, which refunds a paid invoice).
func (s State) Terminal() bool { return s == Completed || s == Voided }

// Awaiting reports whether a command is outstanding in the state, so a step deadline applies.
func (s State) Awaiting() bool {
	switch s {
	case PdfRequested, PaymentRequested, PaymentUnknown, NotifyRequested, RefundRequested:
		return true
	}
	return false
}

// Event drives a transition: a worker reply, a timer, an operator action or an automatic continuation.
type Event string

const (
	EvStart           Event = "Start"            // finalize assigned the number
	EvContinue        Event = "Continue"         // automatic hop out of a transient state
	EvPdfRendered     Event = "PdfRendered"      // reply
	EvPdfFailed       Event = "PdfFailed"        // reply
	EvPaymentOK       Event = "PaymentSucceeded" // reply
	EvPaymentDeclined Event = "PaymentDeclined"  // reply
	EvPaymentErrored  Event = "PaymentErrored"   // reply
	EvStatusSucceeded Event = "StatusSucceeded"  // PaymentStatus reply: charged
	EvStatusNotFound  Event = "StatusNotFound"   // PaymentStatus reply: no charge
	EvStatusDeclined  Event = "StatusDeclined"   // PaymentStatus reply: declined
	// The provider said the payment must not be tried again with this method (PaymentDeclined.final): no dunning, whatever attempts are left.
	EvPaymentDeclinedFinal Event = "PaymentDeclinedFinal" // reply
	EvStatusDeclinedFinal  Event = "StatusDeclinedFinal"  // PaymentStatus reply
	EvStatusPending        Event = "StatusPending"        // PaymentStatus reply: accepted, not settled yet
	// The provider needs the customer to act before it charges (Stripe's 3-D Secure): the reply of a charge, and the answer of a status query that finds it still waiting.
	EvPaymentActionRequired Event = "PaymentActionRequired" // reply
	EvStatusActionRequired  Event = "StatusActionRequired"  // PaymentStatus reply
	EvPollDue               Event = "PollDue"               // pending-payment scheduler
	EvSettled               Event = "OfflineSettlement"     // operator: the money was received outside a provider
	EvRefunded              Event = "PaymentRefunded"       // reply
	EvNotified              Event = "NotificationSent"      // reply
	EvExhausted             Event = "StepExhausted"         // sweeper: retries of the step used up
	EvDunningDue            Event = "DunningDue"            // dunning scheduler
	EvRetry                 Event = "Retry"                 // operator
	EvVoid                  Event = "Void"                  // operator
	EvPayRequested          Event = "PayRequested"          // the customer asks to pay now on a hosted page (mock payment); counts as a retry
	EvProviderSettled       Event = "Settled"               // a provider confirmed a payment the customer made on its hosted page (payments-worker)
)

// Events lists every event.
var Events = []Event{EvStart, EvContinue, EvPdfRendered, EvPdfFailed, EvPaymentOK, EvPaymentDeclined, EvPaymentErrored,
	EvStatusSucceeded, EvStatusNotFound, EvStatusDeclined, EvPaymentDeclinedFinal, EvStatusDeclinedFinal, EvStatusPending, EvPaymentActionRequired, EvStatusActionRequired, EvPollDue, EvSettled, EvRefunded, EvNotified, EvExhausted, EvDunningDue, EvRetry, EvVoid, EvPayRequested, EvProviderSettled}

// Effect is what the engine does when a transition fires.
type Effect string

const (
	EffNone            Effect = ""
	EffRender          Effect = "render"           // RenderInvoicePdf
	EffCharge          Effect = "charge"           // ChargePayment, same dunning attempt (fresh command id)
	EffChargeDunning   Effect = "charge+dunning"   // ChargePayment for the next dunning attempt
	EffQueryStatus     Effect = "query-status"     // QueryPaymentStatus
	EffRefund          Effect = "refund"           // RefundPayment
	EffNotify          Effect = "notify"           // SendNotification (template follows the invoice outcome)
	EffScheduleDunning Effect = "schedule-dunning" // set next_attempt_at
	EffSchedulePoll    Effect = "schedule-poll"    // set next_attempt_at to the next status poll
	EffAwaitTransfer   Effect = "await-transfer"   // SendNotification with the payment instructions, then schedule-poll
	EffAwaitAction     Effect = "await-action"     // SendNotification with the link to the invoice page, then schedule-poll
	EffHold            Effect = "hold"             // no command: next_attempt_at is when the dunning sweep looks again at whether the payment gate lets the first charge through
	EffRecheck         Effect = "recheck"          // no command now: the sweeper asks the status again when the not-found grace period of the charge's key is over
)

// Guard picks between rows with the same (From, Event). Guards are mutually exclusive per pair.
type Guard string

const (
	Always        Guard = ""
	TotalZero     Guard = "total = 0"
	TotalPositive Guard = "total > 0"
	// ChargeHeld: the invoice is charged by the saga and the payment gate says not now (Ctx.Held).
	ChargeHeld Guard = "total > 0, we charge, held"
	// StillHeld and Released split a held first charge that is asked about again (Ctx.Held).
	StillHeld       Guard = "still held"
	Released        Guard = "gate lets it through"
	DunningLeft     Guard = "dunning attempts left"
	DunningSpent    Guard = "dunning exhausted"
	PaidWithMoney   Guard = "paid, total > 0"
	NothingToRefund Guard = "nothing to refund"
	ProviderMails   Guard = "the provider mails the receipt"
	// AwaitCustomer: a link-collected invoice is not charged by the saga; the customer pays.
	AwaitCustomer Guard = "total > 0, customer pays"
	// LinkDeclined: a declined payment on a link-collected invoice is the customer's to retry; there is no dunning.
	LinkDeclined Guard = "customer pays, no dunning"
	// WeCharge: the invoice is charged by the saga (not collected by a link); with a decline the provider said not to retry, dunning is skipped.
	WeCharge Guard = "we charge, provider says do not retry"
	// The three ages at which a "not found" answer is read (see Ctx.ChargeAge): too soon to trust, trustworthy, and past what the provider remembers of the key.
	NotFoundYoung   Guard = "not found, charge younger than the grace period"
	NotFoundSettled Guard = "not found, charge old enough to be sure"
	NotFoundStale   Guard = "not found, charge older than the key horizon"
)

// Ctx is what guards look at.
type Ctx struct {
	TotalMinor int64
	// CreditedMinor is what the credit notes of the invoice already gave back; ByHandMinor is what was received outside a provider (invoice_payments). A void gives
	// the rest back: the card part through the provider, the hand part as a credit balance (see VoidSplit).
	CreditedMinor  int64
	ByHandMinor    int64
	Paid           bool // the invoice was paid (status = paid)
	DunningAttempt int
	MaxDunning     int
	// ProviderMails: the provider that took the payment sends the customer its own receipt, so Payrudder sends none.
	ProviderMails bool
	// Link: the invoice is collected through a payment link (the customer pays), not charged by the saga.
	Link bool
	// Held: the payment gate (pkg/paymentgate, op pay an invoice) refuses the charge now. The engine asks it only where a charge would go out for the first time (PDF_RENDERED
	// and PAYMENT_HELD), so it is false everywhere else.
	Held bool
	// ChargeAge is how long ago the charge under the current idempotency key was first sent. A "not found" answer is read by it: a provider may not show a charge
	// it has only just taken (eventual consistency, a response that was lost and committed late), and one that no longer remembers the key (Stripe keeps it for 24
	// hours) would charge again. NotFoundGrace and KeyHorizon are the two limits; a zero KeyHorizon turns the second check off.
	ChargeAge, NotFoundGrace, KeyHorizon time.Duration
}

// notFoundWindow says how far to trust a "not found" answer for a charge of this age.
func (c Ctx) notFoundWindow() Guard {
	switch {
	case c.ChargeAge < c.NotFoundGrace:
		return NotFoundYoung
	case c.KeyHorizon > 0 && c.ChargeAge > c.KeyHorizon:
		return NotFoundStale
	}
	return NotFoundSettled
}

func (c Ctx) cardRefund() int64 {
	card, _ := VoidSplit(c.TotalMinor, c.CreditedMinor, c.ByHandMinor)
	return card
}

// VoidSplit is what a voided, paid invoice gives back: card goes back through the provider that took it, hand becomes a credit balance of the customer, because money
// recorded by hand (a bank transfer, cash) never passed a provider and has no card to return to. What credit notes already gave back is taken off the card part first,
// so the two never add up to more than the invoice total less the notes. Money still owed on an invoice that was not paid in full is all hand: it has no card part.
func VoidSplit(total, credited, byHand int64) (card, hand int64) {
	left := max(total-credited, 0)
	hand = min(max(byHand, 0), left)
	return left - hand, hand
}

func (g Guard) holds(c Ctx) bool {
	switch g {
	case Always:
		return true
	case TotalZero:
		return c.TotalMinor == 0
	case TotalPositive:
		return c.TotalMinor > 0 && !c.Link && !c.Held
	case ChargeHeld:
		return c.TotalMinor > 0 && !c.Link && c.Held
	case StillHeld:
		return c.Held
	case Released:
		return !c.Held
	case AwaitCustomer:
		return c.TotalMinor > 0 && c.Link
	case LinkDeclined:
		return c.Link
	case WeCharge:
		return !c.Link
	case DunningLeft:
		return !c.Link && c.DunningAttempt < c.MaxDunning
	case DunningSpent:
		return !c.Link && c.DunningAttempt >= c.MaxDunning
	case PaidWithMoney:
		return c.Paid && c.cardRefund() > 0
	case ProviderMails:
		return c.ProviderMails
	case NothingToRefund:
		return !c.Paid || c.cardRefund() <= 0
	case NotFoundYoung, NotFoundSettled, NotFoundStale:
		return c.notFoundWindow() == g
	}
	return false
}

// Row is one allowed transition.
type Row struct {
	From   State
	Event  Event
	Guard  Guard
	To     State
	Effect Effect
}

// Table is every allowed transition. A (state, event) pair without a row is invalid and its event is ignored.
var Table = []Row{
	{Finalized, EvStart, Always, PdfRequested, EffRender},

	{PdfRequested, EvPdfRendered, Always, PdfRendered, EffNone},
	{PdfRequested, EvPdfFailed, Always, PdfFailed, EffNone},
	{PdfRequested, EvExhausted, Always, PdfFailed, EffNone},
	{PdfFailed, EvRetry, Always, PdfRequested, EffRender},
	{PdfRendered, EvContinue, TotalPositive, PaymentRequested, EffCharge},
	{PdfRendered, EvContinue, ChargeHeld, PaymentHeld, EffHold},
	{PdfRendered, EvContinue, AwaitCustomer, AwaitingPayment, EffNone},
	{PdfRendered, EvContinue, TotalZero, NotifyRequested, EffNotify},

	{PaymentRequested, EvPaymentOK, Always, Paid, EffNone},
	{PaymentRequested, EvPaymentDeclined, DunningLeft, PaymentDeclined, EffScheduleDunning},
	{PaymentRequested, EvPaymentDeclined, DunningSpent, Uncollectible, EffNone},
	{PaymentRequested, EvPaymentDeclined, LinkDeclined, AwaitingPayment, EffNone},
	{PaymentRequested, EvPaymentDeclinedFinal, WeCharge, Uncollectible, EffNone},
	{PaymentRequested, EvPaymentDeclinedFinal, LinkDeclined, AwaitingPayment, EffNone},
	{PaymentRequested, EvPaymentErrored, Always, PaymentUnknown, EffQueryStatus},
	{PaymentRequested, EvPaymentActionRequired, Always, PaymentActionRequired, EffAwaitAction},
	{PaymentRequested, EvExhausted, Always, PaymentUnknown, EffQueryStatus},

	// The original charge's own reply may still arrive while the status query is in flight.
	{PaymentUnknown, EvPaymentOK, Always, Paid, EffNone},
	{PaymentUnknown, EvPaymentDeclined, DunningLeft, PaymentDeclined, EffScheduleDunning},
	{PaymentUnknown, EvPaymentDeclined, DunningSpent, Uncollectible, EffNone},
	{PaymentUnknown, EvPaymentDeclined, LinkDeclined, AwaitingPayment, EffNone},
	{PaymentUnknown, EvPaymentDeclinedFinal, WeCharge, Uncollectible, EffNone},
	{PaymentUnknown, EvPaymentDeclinedFinal, LinkDeclined, AwaitingPayment, EffNone},
	{PaymentUnknown, EvStatusDeclinedFinal, WeCharge, Uncollectible, EffNone},
	{PaymentUnknown, EvStatusDeclinedFinal, LinkDeclined, AwaitingPayment, EffNone},
	{PaymentUnknown, EvStatusSucceeded, Always, Paid, EffNone},
	// "Not found" is acted on only inside a window (Ctx.ChargeAge): before it the status is asked again later, after it nobody charges again with a key the provider
	// may have forgotten, and the saga parks for the operator.
	{PaymentUnknown, EvStatusNotFound, NotFoundYoung, PaymentUnknown, EffRecheck},
	{PaymentUnknown, EvStatusNotFound, NotFoundSettled, PaymentRequested, EffCharge},
	{PaymentUnknown, EvStatusNotFound, NotFoundStale, PaymentUnknown, EffNone},
	{PaymentUnknown, EvStatusDeclined, DunningLeft, PaymentDeclined, EffScheduleDunning},
	{PaymentUnknown, EvStatusDeclined, DunningSpent, Uncollectible, EffNone},
	{PaymentUnknown, EvStatusDeclined, LinkDeclined, AwaitingPayment, EffNone},
	{PaymentUnknown, EvStatusPending, Always, PaymentPending, EffAwaitTransfer},
	{PaymentUnknown, EvPaymentActionRequired, Always, PaymentActionRequired, EffAwaitAction},
	{PaymentUnknown, EvStatusActionRequired, Always, PaymentActionRequired, EffAwaitAction},
	{PaymentUnknown, EvRetry, Always, PaymentUnknown, EffQueryStatus},
	{PaymentUnknown, EvExhausted, Always, PaymentUnknown, EffNone}, // parks for the operator

	// The provider accepted the charge and the money comes later (a bank transfer). The saga polls the status on a schedule; an operator's receipt nudges it
	// (Retry) so a payment does not wait for the poll. The status answer itself carries the timeout: past the payment window it is a decline, and dunning applies.
	{PaymentPending, EvStatusPending, Always, PaymentPending, EffSchedulePoll},
	{PaymentPending, EvPollDue, Always, PaymentPending, EffQueryStatus},
	{PaymentPending, EvRetry, Always, PaymentPending, EffQueryStatus},
	{PaymentPending, EvStatusSucceeded, Always, Paid, EffNone},
	{PaymentPending, EvStatusNotFound, Always, PaymentRequested, EffCharge},
	{PaymentPending, EvStatusDeclined, DunningLeft, PaymentDeclined, EffScheduleDunning},
	{PaymentPending, EvStatusDeclined, DunningSpent, Uncollectible, EffNone},
	{PaymentPending, EvStatusDeclined, LinkDeclined, AwaitingPayment, EffNone},
	{PaymentPending, EvStatusDeclinedFinal, WeCharge, Uncollectible, EffNone},
	{PaymentPending, EvStatusDeclinedFinal, LinkDeclined, AwaitingPayment, EffNone},
	{PaymentPending, EvExhausted, Always, PaymentPending, EffSchedulePoll}, // a poll nobody answered: wait for the next one, do not park

	// The provider wants the customer to authenticate (3-D Secure). The customer is mailed a link to the invoice page once, on entering; the status is asked on a schedule
	// (a customer who pays on the page settles it at once, through Settled). Like a pending payment, but payable on its page, and what the status says decides.
	{PaymentActionRequired, EvStatusActionRequired, Always, PaymentActionRequired, EffSchedulePoll},
	{PaymentActionRequired, EvPollDue, Always, PaymentActionRequired, EffQueryStatus},
	{PaymentActionRequired, EvRetry, Always, PaymentActionRequired, EffQueryStatus},
	{PaymentActionRequired, EvStatusSucceeded, Always, Paid, EffNone},
	{PaymentActionRequired, EvStatusNotFound, Always, PaymentRequested, EffCharge},
	{PaymentActionRequired, EvStatusDeclined, DunningLeft, PaymentDeclined, EffScheduleDunning},
	{PaymentActionRequired, EvStatusDeclined, DunningSpent, Uncollectible, EffNone},
	{PaymentActionRequired, EvStatusDeclined, LinkDeclined, AwaitingPayment, EffNone},
	{PaymentActionRequired, EvStatusDeclinedFinal, WeCharge, Uncollectible, EffNone},
	{PaymentActionRequired, EvStatusDeclinedFinal, LinkDeclined, AwaitingPayment, EffNone},
	{PaymentActionRequired, EvExhausted, Always, PaymentActionRequired, EffSchedulePoll}, // a poll nobody answered: wait for the next one, do not park

	// The first charge, held by the payment gate: asked again by the dunning sweep and by the seller's Retry (the release); both ask the gate again, and what is still held
	// stays held. The charge that follows is the first one (same key as it would have had), not a dunning attempt.
	{PaymentHeld, EvDunningDue, Released, PaymentRequested, EffCharge},
	{PaymentHeld, EvDunningDue, StillHeld, PaymentHeld, EffHold},
	{PaymentHeld, EvRetry, Released, PaymentRequested, EffCharge},
	{PaymentHeld, EvRetry, StillHeld, PaymentHeld, EffHold},

	{PaymentDeclined, EvDunningDue, Always, PaymentRequested, EffChargeDunning},
	{PaymentDeclined, EvRetry, Always, PaymentUnknown, EffQueryStatus}, // a transfer that arrived after the window: ask the ledger again

	// Money received outside a provider (RecordInvoicePayment) settles an invoice whose payment is waited for, failed, or given up on.
	{PaymentUnknown, EvSettled, Always, Paid, EffNone},
	{PaymentPending, EvSettled, Always, Paid, EffNone},
	{PaymentActionRequired, EvSettled, Always, Paid, EffNone},
	{PaymentHeld, EvSettled, Always, Paid, EffNone},
	{PaymentDeclined, EvSettled, Always, Paid, EffNone},
	{NotifyRequested, EvSettled, Always, Paid, EffNone},
	{NotifyFailed, EvSettled, Always, Paid, EffNone},
	{Completed, EvSettled, Always, Paid, EffNone},

	// A customer pays on a hosted page (phase 34): a charge now, or a provider's confirmed payment. A payment racing a dunning charge is settled once:
	// after PAID the late reply has no row.
	{AwaitingPayment, EvPayRequested, Always, PaymentRequested, EffChargeDunning},
	{AwaitingPayment, EvProviderSettled, Always, Paid, EffNone},
	{PaymentDeclined, EvPayRequested, Always, PaymentRequested, EffChargeDunning},
	{PaymentDeclined, EvProviderSettled, Always, Paid, EffNone},
	{PaymentRequested, EvProviderSettled, Always, Paid, EffNone},
	{PaymentUnknown, EvProviderSettled, Always, Paid, EffNone},
	{PaymentActionRequired, EvProviderSettled, Always, Paid, EffNone},

	{Paid, EvContinue, ProviderMails, Completed, EffNone},
	{Paid, EvContinue, Always, NotifyRequested, EffNotify},
	{Uncollectible, EvContinue, Always, NotifyRequested, EffNotify},

	{NotifyRequested, EvNotified, Always, Completed, EffNone},
	{NotifyRequested, EvExhausted, Always, NotifyFailed, EffNone},
	{NotifyFailed, EvRetry, Always, NotifyRequested, EffNotify},

	{Finalized, EvVoid, Always, Voided, EffNone},
	{PdfRequested, EvVoid, Always, Voided, EffNone},
	{PdfFailed, EvVoid, Always, Voided, EffNone},
	{PaymentDeclined, EvVoid, Always, Voided, EffNone},
	{AwaitingPayment, EvVoid, Always, Voided, EffNone},
	{PaymentPending, EvVoid, Always, Voided, EffNone},
	{PaymentActionRequired, EvVoid, Always, Voided, EffNone},
	{PaymentHeld, EvVoid, Always, Voided, EffNone},
	{NotifyRequested, EvVoid, PaidWithMoney, RefundRequested, EffRefund},
	{NotifyRequested, EvVoid, NothingToRefund, Voided, EffNone},
	{NotifyFailed, EvVoid, PaidWithMoney, RefundRequested, EffRefund},
	{NotifyFailed, EvVoid, NothingToRefund, Voided, EffNone},
	{Completed, EvVoid, PaidWithMoney, RefundRequested, EffRefund},
	{Completed, EvVoid, NothingToRefund, Voided, EffNone},

	{RefundRequested, EvRefunded, Always, Refunded, EffNone},
	{RefundRequested, EvExhausted, Always, RefundFailed, EffNone},
	{RefundFailed, EvRetry, Always, RefundRequested, EffRefund},
	{Refunded, EvContinue, Always, Voided, EffNone},
}

// Result is the outcome of one transition.
type Result struct {
	To     State
	Effect Effect
}

// Next returns the transition for (state, event) under the guard context, or false when the pair is invalid.
func Next(s State, ev Event, c Ctx) (Result, bool) {
	for _, r := range Table {
		if r.From == s && r.Event == ev && r.Guard.holds(c) {
			return Result{To: r.To, Effect: r.Effect}, true
		}
	}
	return Result{}, false
}

// Mermaid renders the table as a state diagram (docs/03-services/billing-svc.md embeds it; a test keeps them in sync).
func Mermaid() string {
	var b strings.Builder
	b.WriteString("stateDiagram-v2\n    [*] --> FINALIZED\n")
	type edge struct{ from, to State }
	labels := map[edge][]string{}
	var order []edge
	for _, r := range Table {
		e := edge{r.From, r.To}
		l := string(r.Event)
		if r.Guard != Always {
			l += " [" + string(r.Guard) + "]"
		}
		if _, ok := labels[e]; !ok {
			order = append(order, e)
		}
		labels[e] = append(labels[e], l)
	}
	for _, e := range order {
		fmt.Fprintf(&b, "    %s --> %s: %s\n", e.from, e.to, strings.Join(labels[e], " / "))
	}
	b.WriteString("    COMPLETED --> [*]\n    VOIDED --> [*]\n")
	return b.String()
}
