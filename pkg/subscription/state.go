// Package subscription holds the pure rules of a subscription: its lifecycle states, the events that move it, the
// length of its billing periods, and proration of a mid-period plan change. Nothing here does I/O.
package subscription

import "fmt"

// State of a subscription.
type State string

const (
	Trialing  State = "trialing"
	Active    State = "active"
	PastDue   State = "past_due"
	Paused    State = "paused"
	Cancelled State = "cancelled"
)

// Event that moves a subscription.
type Event string

const (
	// TrialEnded: the trial is over and the first charge is due.
	TrialEnded Event = "trial_ended"
	// PaymentSucceeded: a renewal or retried charge went through.
	PaymentSucceeded Event = "payment_succeeded"
	// PaymentFailed: a charge was declined and dunning has started or continues.
	PaymentFailed Event = "payment_failed"
	// DunningExhausted: every retry failed.
	DunningExhausted Event = "dunning_exhausted"
	Pause            Event = "pause"
	Resume           Event = "resume"
	// Cancel: cancel now.
	Cancel Event = "cancel"
	// PeriodEnded with cancel_at_period_end set ends the subscription at the period boundary.
	PeriodEndedCancelling Event = "period_ended_cancelling"
)

// ErrInvalidTransition is returned when an event is not allowed in a state.
type ErrInvalidTransition struct {
	From  State
	Event Event
}

func (e *ErrInvalidTransition) Error() string {
	return fmt.Sprintf("subscription: event %q is not allowed in state %q", e.Event, e.From)
}

var transitions = map[State]map[Event]State{
	Trialing: {
		TrialEnded:            Active,
		PaymentFailed:         PastDue,
		Cancel:                Cancelled,
		PeriodEndedCancelling: Cancelled,
	},
	Active: {
		PaymentSucceeded:      Active,
		PaymentFailed:         PastDue,
		Pause:                 Paused,
		Cancel:                Cancelled,
		PeriodEndedCancelling: Cancelled,
	},
	PastDue: {
		PaymentSucceeded: Active,
		PaymentFailed:    PastDue,
		DunningExhausted: Cancelled,
		Cancel:           Cancelled,
	},
	Paused: {
		Resume: Active,
		Cancel: Cancelled,
	},
	Cancelled: {},
}

// Next returns the state after event, or *ErrInvalidTransition.
func Next(from State, ev Event) (State, error) {
	if to, ok := transitions[from][ev]; ok {
		return to, nil
	}
	return from, &ErrInvalidTransition{From: from, Event: ev}
}

// Valid reports whether s is a known state.
func Valid(s State) bool {
	_, ok := transitions[s]
	return ok
}

// Billable reports whether a subscription in s is charged at renewal. Trialing is not charged until the trial ends,
// paused and cancelled never.
func Billable(s State) bool { return s == Active || s == PastDue }

// Terminal reports whether no event can leave s.
func Terminal(s State) bool { return len(transitions[s]) == 0 }
