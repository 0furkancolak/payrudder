package subscription

import (
	"errors"
	"fmt"
	"time"
)

// Timing says when a plan change takes effect.
type Timing string

const (
	// Immediately: now.
	Immediately Timing = "immediately"
	// AtPeriodEnd: when the period the customer has paid for ends.
	AtPeriodEnd Timing = "period_end"
)

// Proration says how an immediate change settles the fee that was paid in advance.
type Proration string

const (
	// Prorate credits the unused part of the old fee and, within the same interval, charges the new fee only for the rest of the period
	// (the billing cycle is kept). Across intervals the cycle cannot be kept, so a new one starts now and its first fee is charged in full.
	Prorate Proration = "prorate"
	// Full credits the unused part of the old fee and starts a new billing cycle now, charging the new fee in full.
	Full Proration = "full"
	// NoProration moves no money now: the new plan is billed from the next period on.
	NoProration Proration = "none"
)

// Plan is what a change needs to know about a plan version: its fixed fee for the interval the subscription is billed at.
type Plan struct {
	FeeMinor int64
	Interval Interval
}

// ChangeRequest asks what moving a subscription from Old to New at At would cost.
type ChangeRequest struct {
	Old, New Plan
	// Anchor is the billing anchor of the subscription being changed.
	Anchor time.Time
	At     time.Time
	Loc    *time.Location
	// Timing and Proration are the caller's choice; empty means the default for the direction of the change.
	Timing    Timing
	Proration Proration
}

// Quote is the outcome of a change: what is settled now, when the new plan starts, and what the next renewal costs. The same Quote
// prices a preview and performs the change, so the two cannot disagree.
type Quote struct {
	Timing    Timing
	Proration Proration
	// Effective is when the new plan starts.
	Effective time.Time
	// PeriodStart and PeriodEnd bound the paid period the change happens in.
	PeriodStart, PeriodEnd time.Time
	// CreditMinor is the unused, prepaid part of the old fee; ChargeMinor is what the new plan costs from Effective until the period ends
	// (or in full, when a new cycle starts). NetMinor = Charge - Credit: positive is invoiced now, negative goes to the customer's credit balance.
	CreditMinor, ChargeMinor, NetMinor int64
	// NewAnchor is the billing anchor of the new subscription. NewFeeDue is the start of the next period whose fee the fee run invoices.
	NewAnchor, NewFeeDue time.Time
	// NextRenewal is when the next fee is invoiced and NextRenewalMinor how much.
	NextRenewal      time.Time
	NextRenewalMinor int64
}

// ErrBadChange is wrapped by every refusal of QuoteChange.
var ErrBadChange = errors.New("subscription: bad plan change")

func badChange(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrBadChange, fmt.Sprintf(format, args...))
}

// monthly returns the plan's fee as a ratio num/den of a month, so plans billed at different intervals can be compared without rounding.
func (p Plan) perMonth() (num, den int64) {
	if p.Interval == Yearly {
		return p.FeeMinor, 12
	}
	return p.FeeMinor, 1
}

// IsDowngrade reports whether the new plan costs less per month than the old one.
func IsDowngrade(oldPlan, newPlan Plan) bool {
	on, od := oldPlan.perMonth()
	nn, nd := newPlan.perMonth()
	return nn*od < on*nd
}

// QuoteChange prices a change of plan or interval for a subscription billed in advance.
//
// Defaults: within an interval, a change to a plan that costs more per month (or the same) takes effect immediately and prorates, and a
// change to a cheaper one takes effect at the end of the paid period and moves no money. A switch from yearly to monthly also waits for the end of
// the paid year; a switch from monthly to yearly starts the year at once. Whatever the caller sets overrides that, except that an immediate change of interval always starts a new cycle.
func QuoteChange(r ChangeRequest) (Quote, error) {
	if !r.Old.Interval.Valid() || !r.New.Interval.Valid() {
		return Quote{}, badChange("unknown interval")
	}
	if r.Old.FeeMinor < 0 || r.New.FeeMinor < 0 {
		return Quote{}, badChange("negative fee")
	}
	if r.Loc == nil {
		r.Loc = time.UTC
	}
	n, err := PeriodIndex(r.Anchor, r.Old.Interval, r.At, r.Loc)
	if err != nil {
		return Quote{}, badChange("%v", err)
	}
	ps, err := PeriodStart(r.Anchor, r.Old.Interval, n, r.Loc)
	if err != nil {
		return Quote{}, err
	}
	pe, err := PeriodEnd(r.Anchor, r.Old.Interval, n, r.Loc)
	if err != nil {
		return Quote{}, err
	}

	timing := r.Timing
	if timing == "" {
		switch {
		case r.Old.Interval == Yearly && r.New.Interval == Monthly:
			timing = AtPeriodEnd
		case r.Old.Interval == r.New.Interval && IsDowngrade(r.Old, r.New):
			timing = AtPeriodEnd
		default: // an upgrade, or monthly to yearly (a yearly price is usually cheaper per month, and it is a commitment the customer makes now)
			timing = Immediately
		}
	}
	if timing != Immediately && timing != AtPeriodEnd {
		return Quote{}, badChange("unknown timing %q", timing)
	}
	q := Quote{Timing: timing, PeriodStart: ps, PeriodEnd: pe}

	if timing == AtPeriodEnd {
		if r.Proration != "" && r.Proration != NoProration {
			return Quote{}, badChange("a change at the end of the period moves no money, proration %q does not apply", r.Proration)
		}
		q.Proration = NoProration
		q.Effective, q.NewAnchor, q.NewFeeDue, q.NextRenewal = pe, pe, pe, pe
		q.NextRenewalMinor = r.New.FeeMinor
		return q, nil
	}

	proration := r.Proration
	if proration == "" {
		proration = Prorate
	}
	if proration != Prorate && proration != Full && proration != NoProration {
		return Quote{}, badChange("unknown proration %q", proration)
	}
	changesInterval := r.Old.Interval != r.New.Interval
	if changesInterval && proration == NoProration {
		return Quote{}, badChange("an immediate change of interval needs proration prorate or full")
	}
	q.Proration, q.Effective = proration, r.At

	switch {
	case proration == NoProration:
		q.NewAnchor, q.NewFeeDue, q.NextRenewal = r.Anchor, pe, pe
		q.NextRenewalMinor = r.New.FeeMinor
	case proration == Prorate && !changesInterval:
		credit, err := Portion(r.Old.FeeMinor, r.At, pe, ps, pe)
		if err != nil {
			return Quote{}, badChange("%v", err)
		}
		charge, err := Portion(r.New.FeeMinor, r.At, pe, ps, pe)
		if err != nil {
			return Quote{}, badChange("%v", err)
		}
		q.CreditMinor, q.ChargeMinor = credit, charge
		q.NewAnchor, q.NewFeeDue, q.NextRenewal = r.Anchor, pe, pe
		q.NextRenewalMinor = r.New.FeeMinor
	default: // a new cycle starts now: Full, or an immediate change of interval
		credit, err := Portion(r.Old.FeeMinor, r.At, pe, ps, pe)
		if err != nil {
			return Quote{}, badChange("%v", err)
		}
		end, err := PeriodEnd(r.At, r.New.Interval, 0, r.Loc)
		if err != nil {
			return Quote{}, err
		}
		q.CreditMinor, q.ChargeMinor = credit, r.New.FeeMinor
		q.NewAnchor, q.NewFeeDue, q.NextRenewal = r.At, end, end
		q.NextRenewalMinor = r.New.FeeMinor
	}
	q.NetMinor = q.ChargeMinor - q.CreditMinor
	return q, nil
}
