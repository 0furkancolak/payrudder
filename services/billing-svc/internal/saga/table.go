// Package saga is billing-svc's invoice saga: the pure transition table (this file) and the database engine that applies
// it (engine.go). The table is the single source of truth: tests check it exhaustively and docs/03-services/billing-svc.md
// embeds the diagram rendered from it.
package saga

import (
	"fmt"
	"sort"
	"strings"
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
	Uncollectible    State = "UNCOLLECTIBLE"
	NotifyRequested  State = "NOTIFY_REQUESTED"
	NotifyFailed     State = "NOTIFY_FAILED"
	Completed        State = "COMPLETED"
	RefundRequested  State = "REFUND_REQUESTED"
	RefundFailed     State = "REFUND_FAILED"
	Refunded         State = "REFUNDED"
	Voided           State = "VOIDED"
)

// States lists every state.
var States = []State{Draft, Finalized, PdfRequested, PdfRendered, PdfFailed, PaymentRequested, Paid, PaymentDeclined, PaymentUnknown,
	Uncollectible, NotifyRequested, NotifyFailed, Completed, RefundRequested, RefundFailed, Refunded, Voided}

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
	EvRefunded        Event = "PaymentRefunded"  // reply
	EvNotified        Event = "NotificationSent" // reply
	EvExhausted       Event = "StepExhausted"    // sweeper: retries of the step used up
	EvDunningDue      Event = "DunningDue"       // dunning scheduler
	EvRetry           Event = "Retry"            // operator
	EvVoid            Event = "Void"             // operator
)

// Events lists every event.
var Events = []Event{EvStart, EvContinue, EvPdfRendered, EvPdfFailed, EvPaymentOK, EvPaymentDeclined, EvPaymentErrored,
	EvStatusSucceeded, EvStatusNotFound, EvStatusDeclined, EvRefunded, EvNotified, EvExhausted, EvDunningDue, EvRetry, EvVoid}

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
)

// Guard picks between rows with the same (From, Event). Guards are mutually exclusive per pair.
type Guard string

const (
	Always          Guard = ""
	TotalZero       Guard = "total = 0"
	TotalPositive   Guard = "total > 0"
	DunningLeft     Guard = "dunning attempts left"
	DunningSpent    Guard = "dunning exhausted"
	PaidWithMoney   Guard = "paid, total > 0"
	NothingToRefund Guard = "nothing to refund"
)

// Ctx is what guards look at.
type Ctx struct {
	TotalMinor     int64
	Paid           bool // the invoice was paid (status = paid)
	DunningAttempt int
	MaxDunning     int
}

func (g Guard) holds(c Ctx) bool {
	switch g {
	case Always:
		return true
	case TotalZero:
		return c.TotalMinor == 0
	case TotalPositive:
		return c.TotalMinor > 0
	case DunningLeft:
		return c.DunningAttempt < c.MaxDunning
	case DunningSpent:
		return c.DunningAttempt >= c.MaxDunning
	case PaidWithMoney:
		return c.Paid && c.TotalMinor > 0
	case NothingToRefund:
		return !c.Paid || c.TotalMinor <= 0
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
	{PdfRendered, EvContinue, TotalZero, NotifyRequested, EffNotify},

	{PaymentRequested, EvPaymentOK, Always, Paid, EffNone},
	{PaymentRequested, EvPaymentDeclined, DunningLeft, PaymentDeclined, EffScheduleDunning},
	{PaymentRequested, EvPaymentDeclined, DunningSpent, Uncollectible, EffNone},
	{PaymentRequested, EvPaymentErrored, Always, PaymentUnknown, EffQueryStatus},
	{PaymentRequested, EvExhausted, Always, PaymentUnknown, EffQueryStatus},

	// The original charge's own reply may still arrive while the status query is in flight.
	{PaymentUnknown, EvPaymentOK, Always, Paid, EffNone},
	{PaymentUnknown, EvPaymentDeclined, DunningLeft, PaymentDeclined, EffScheduleDunning},
	{PaymentUnknown, EvPaymentDeclined, DunningSpent, Uncollectible, EffNone},
	{PaymentUnknown, EvStatusSucceeded, Always, Paid, EffNone},
	{PaymentUnknown, EvStatusNotFound, Always, PaymentRequested, EffCharge},
	{PaymentUnknown, EvStatusDeclined, DunningLeft, PaymentDeclined, EffScheduleDunning},
	{PaymentUnknown, EvStatusDeclined, DunningSpent, Uncollectible, EffNone},
	{PaymentUnknown, EvRetry, Always, PaymentUnknown, EffQueryStatus},
	{PaymentUnknown, EvExhausted, Always, PaymentUnknown, EffNone}, // parks for the operator

	{PaymentDeclined, EvDunningDue, Always, PaymentRequested, EffChargeDunning},

	{Paid, EvContinue, Always, NotifyRequested, EffNotify},
	{Uncollectible, EvContinue, Always, NotifyRequested, EffNotify},

	{NotifyRequested, EvNotified, Always, Completed, EffNone},
	{NotifyRequested, EvExhausted, Always, NotifyFailed, EffNone},
	{NotifyFailed, EvRetry, Always, NotifyRequested, EffNotify},

	{Finalized, EvVoid, Always, Voided, EffNone},
	{PdfRequested, EvVoid, Always, Voided, EffNone},
	{PdfFailed, EvVoid, Always, Voided, EffNone},
	{PaymentDeclined, EvVoid, Always, Voided, EffNone},
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
	sort.SliceStable(order, func(i, j int) bool { return false })
	for _, e := range order {
		fmt.Fprintf(&b, "    %s --> %s: %s\n", e.from, e.to, strings.Join(labels[e], " / "))
	}
	b.WriteString("    COMPLETED --> [*]\n    VOIDED --> [*]\n")
	return b.String()
}
