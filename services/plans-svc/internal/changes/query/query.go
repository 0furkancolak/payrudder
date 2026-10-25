// Package query prices subscription changes without making them, and lists the money moves they leave for billing.
package query

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/subscription"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/convert"
	couponsqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/coupons/query"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
	plansqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/plans/query"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
	subsqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/subscriptions/query"
	tenantsqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/tenants/query"
)

var (
	timingToProto = map[subscription.Timing]plansv1.ChangeTiming{
		subscription.Immediately: plansv1.ChangeTiming_CHANGE_TIMING_IMMEDIATELY,
		subscription.AtPeriodEnd: plansv1.ChangeTiming_CHANGE_TIMING_PERIOD_END,
	}
	prorationToProto = map[subscription.Proration]plansv1.ChangeProration{
		subscription.Prorate:     plansv1.ChangeProration_CHANGE_PRORATION_PRORATE,
		subscription.Full:        plansv1.ChangeProration_CHANGE_PRORATION_FULL,
		subscription.NoProration: plansv1.ChangeProration_CHANGE_PRORATION_NONE,
	}
)

// ErrCouponOnCredit: the unused part of a discounted fee is not worked out automatically.
var ErrCouponOnCredit = errkey.New(codes.FailedPrecondition, "plans.subscription.coupon_blocks_credit", "this subscription has a coupon; credit the unused part with a credit note on its fee invoice instead")

// Queries reads changes.
type Queries struct {
	db  *store.DB
	now func() time.Time
}

// New returns the change queries.
func New(db *store.DB, now func() time.Time) *Queries { return &Queries{db: db, now: now} }

// Priced is a change worked out but not yet made: what both the preview returns and the real change performs.
type Priced struct {
	Row         sqlc.GetSubscriptionRow
	NewPV       sqlc.GetPlanVersionByCodeRow
	NewInterval subscription.Interval
	Quote       subscription.Quote
	At          time.Time
	Trial       bool
}

// Message is the quote the way the API returns it.
func (c Priced) Message() *plansv1.ChangeQuote {
	q := c.Quote
	return &plansv1.ChangeQuote{
		Timing: timingToProto[q.Timing], Proration: prorationToProto[q.Proration], EffectiveTime: timestamppb.New(q.Effective),
		PeriodStart: timestamppb.New(q.PeriodStart), PeriodEnd: timestamppb.New(q.PeriodEnd),
		CreditMinor: q.CreditMinor, ChargeMinor: q.ChargeMinor, NetMinor: q.NetMinor, Currency: c.Row.Currency,
		NextRenewalTime: timestamppb.New(q.NextRenewal), NextRenewalMinor: q.NextRenewalMinor,
		PlanCode: c.NewPV.PlanCode, PlanVersion: c.NewPV.Version, Interval: convert.IntervalToProto(c.NewInterval),
	}
}

// Price works out a change of the (locked) subscription row. It writes nothing.
func Price(ctx context.Context, q *sqlc.Queries, now time.Time, row sqlc.GetSubscriptionRow, req *plansv1.ChangeSubscriptionRequest) (*Priced, error) {
	eff, err := requireChangeable(row, now)
	if err != nil {
		return nil, err
	}
	at, err := changeInstant(req, now, row.StartTime)
	if err != nil {
		return nil, err
	}
	t, err := tenantsqry.Current(ctx, q)
	if err != nil {
		return nil, err
	}
	loc, err := tenantsqry.Location(t.Timezone)
	if err != nil {
		return nil, err
	}
	oldInterval := subscription.Interval(row.Interval)
	newInterval, err := changeInterval(req.GetInterval(), oldInterval)
	if err != nil {
		return nil, err
	}
	oldPV, err := q.GetPlanVersionByID(ctx, row.PlanVersionID)
	if err != nil {
		return nil, err
	}
	newPV := sqlc.GetPlanVersionByCodeRow(oldPV)
	if code := req.GetPlanCode(); code != "" {
		if newPV, err = q.GetPlanVersionByCode(ctx, sqlc.GetPlanVersionByCodeParams{PlanCode: code, Version: req.GetPlanVersion()}); err != nil {
			return nil, convert.Err(err, "plan version")
		}
	}
	if newPV.ID == row.PlanVersionID && newInterval == oldInterval {
		return nil, domain.Invalid("plan_code", "the subscription is already on this plan version and interval")
	}
	oldFee, err := plansqry.FeeIn(ctx, q, oldPV.ID, oldPV.BaseFeeMinor, t.Currency, row.Currency, oldInterval)
	if err != nil {
		return nil, err
	}
	newFee, err := plansqry.FeeIn(ctx, q, newPV.ID, newPV.BaseFeeMinor, t.Currency, row.Currency, newInterval)
	if err != nil {
		return nil, err
	}
	if err := plansqry.RequireUsagePrices(ctx, q, newPV.ID, t.Currency, row.Currency); err != nil {
		return nil, err
	}

	pc := &Priced{Row: row, NewPV: newPV, NewInterval: newInterval, At: at}
	if eff == subscription.Trialing {
		if err := pc.quoteTrial(req.GetTiming(), newFee); err != nil {
			return nil, err
		}
		return pc, nil
	}
	// The credit is for a fee that was invoiced. If billing has not yet invoiced the period the change falls in, wait for it.
	due, err := q.GetSubscriptionFeeDue(ctx, row.ID)
	if err != nil {
		return nil, err
	}
	if due != nil && !due.After(at) {
		return nil, errkey.New(codes.Aborted, "plans.change.fee_not_invoiced", "the fee of the current period has not been invoiced yet; try again in a moment")
	}
	cr := subscription.ChangeRequest{
		Old: subscription.Plan{FeeMinor: oldFee, Interval: oldInterval}, New: subscription.Plan{FeeMinor: newFee, Interval: newInterval},
		Anchor: row.BillingAnchor, At: at, Loc: loc, Timing: timingFromProto(req.GetTiming()), Proration: prorationFromProto(req.GetProration()),
	}
	pc.Quote, err = subscription.QuoteChange(cr)
	if errors.Is(err, subscription.ErrBadChange) {
		return nil, errkey.New(codes.InvalidArgument, "plans.change.invalid", err.Error())
	}
	if err != nil {
		return pc, err
	}
	if pc.Quote.Timing == subscription.AtPeriodEnd || (pc.Quote.CreditMinor == 0 && pc.Quote.ChargeMinor == 0) {
		return pc, nil
	}
	// The credit is a share of what was paid and the charge a share of what the new plan costs, both after the coupon that carries over. The change itself
	// (its timing and proration) was decided on the list fees.
	discount, known, err := couponsqry.InForce(ctx, q, row.ID, row.Currency)
	if err != nil {
		return nil, err
	}
	if !known {
		return nil, ErrCouponOnCredit
	}
	if d := discount(oldFee) + discount(newFee); d == 0 {
		return pc, nil
	}
	cr.Old.FeeMinor, cr.New.FeeMinor = oldFee-discount(oldFee), newFee-discount(newFee)
	cr.Timing, cr.Proration = pc.Quote.Timing, pc.Quote.Proration
	renewal := pc.Quote.NextRenewalMinor
	if pc.Quote, err = subscription.QuoteChange(cr); err != nil {
		return nil, err
	}
	pc.Quote.NextRenewalMinor = renewal
	return pc, nil
}

// requireChangeable refuses a subscription that cannot change and returns its effective state.
func requireChangeable(row sqlc.GetSubscriptionRow, now time.Time) (subscription.State, error) {
	if err := domain.RequirePayrudderOwned(row.Owner, row.Provider); err != nil {
		return "", err
	}
	if row.FeeTiming != "advance" {
		return "", errkey.New(codes.FailedPrecondition, "plans.change.billed_in_arrears", "this subscription is billed in arrears; ChangePlan splits the month by time")
	}
	if !row.OpenEnded || row.CancelAtPeriodEnd {
		return "", errkey.New(codes.FailedPrecondition, "plans.change.subscription_ended", "an ended or ending subscription cannot change")
	}
	eff := convert.SubscriptionState(row.State, row.TrialEndsAt, row.OpenEnded, row.EndTime, now)
	if eff != subscription.Active && eff != subscription.Trialing {
		return "", errkey.Newf(codes.FailedPrecondition, "plans.change.state_not_changeable", "a %s subscription cannot change", eff)
	}
	return eff, nil
}

// changeInstant is the moment the change is priced at: now, or a past instant the request names.
func changeInstant(req *plansv1.ChangeSubscriptionRequest, now, start time.Time) (time.Time, error) {
	// Postgres keeps microseconds: a change at a finer instant would price a period start the stored subscription does not have.
	at := now.Truncate(time.Microsecond)
	if req.GetChangeTime() != nil {
		if err := req.GetChangeTime().CheckValid(); err != nil {
			return at, domain.Invalid("change_time", "invalid timestamp")
		}
		at = req.GetChangeTime().AsTime().Truncate(time.Microsecond)
		if at.After(now) {
			return at, domain.Invalid("change_time", "cannot be in the future; ask for a change at the end of the period instead")
		}
	}
	if !at.After(start) {
		return at, domain.Invalid("change_time", "must be after the subscription start")
	}
	return at, nil
}

// changeInterval is the interval after the change: the request's, or the old one when it names none.
func changeInterval(asked plansv1.SubscriptionInterval, old subscription.Interval) (subscription.Interval, error) {
	switch asked {
	case plansv1.SubscriptionInterval_SUBSCRIPTION_INTERVAL_UNSPECIFIED:
		return old, nil
	case plansv1.SubscriptionInterval_SUBSCRIPTION_INTERVAL_MONTH:
		return subscription.Monthly, nil
	case plansv1.SubscriptionInterval_SUBSCRIPTION_INTERVAL_YEAR:
		return subscription.Yearly, nil
	}
	return old, domain.Invalid("interval", "unknown interval")
}

// quoteTrial prices a change during a trial: nothing has been paid yet, so the plan changes now and the first fee, at the
// end of the trial, is the new plan's.
func (c *Priced) quoteTrial(timing plansv1.ChangeTiming, newFee int64) error {
	if timing == plansv1.ChangeTiming_CHANGE_TIMING_PERIOD_END {
		return domain.Invalid("timing", "a trial changes immediately")
	}
	row := c.Row
	c.Trial = true
	c.Quote = subscription.Quote{
		Timing: subscription.Immediately, Proration: subscription.NoProration, Effective: c.At, PeriodStart: c.At, PeriodEnd: *row.TrialEndsAt,
		NewAnchor: row.BillingAnchor, NewFeeDue: row.BillingAnchor, NextRenewal: row.BillingAnchor, NextRenewalMinor: newFee,
	}
	return nil
}

func timingFromProto(t plansv1.ChangeTiming) subscription.Timing {
	switch t {
	case plansv1.ChangeTiming_CHANGE_TIMING_IMMEDIATELY:
		return subscription.Immediately
	case plansv1.ChangeTiming_CHANGE_TIMING_PERIOD_END:
		return subscription.AtPeriodEnd
	}
	return ""
}

func prorationFromProto(p plansv1.ChangeProration) subscription.Proration {
	switch p {
	case plansv1.ChangeProration_CHANGE_PRORATION_PRORATE:
		return subscription.Prorate
	case plansv1.ChangeProration_CHANGE_PRORATION_FULL:
		return subscription.Full
	case plansv1.ChangeProration_CHANGE_PRORATION_NONE:
		return subscription.NoProration
	}
	return ""
}

// Preview prices a change without making it.
func (r *Queries) Preview(ctx context.Context, preview *plansv1.PreviewSubscriptionChangeRequest) (*plansv1.ChangeQuote, error) {
	req := preview.GetChange()
	id, err := subsqry.ParseID(req.GetSubscriptionId())
	if err != nil {
		return nil, err
	}
	var out *plansv1.ChangeQuote
	err = r.db.Tx(ctx, func(q *sqlc.Queries) error {
		row, err := q.GetSubscription(ctx, id)
		if err != nil {
			return err
		}
		pc, err := Price(ctx, q, r.now(), row, req)
		if err != nil {
			return err
		}
		out = pc.Message()
		return nil
	})
	if err != nil {
		return nil, convert.Err(err, "subscription")
	}
	return out, nil
}

// Adjustments lists the money changes moved that billing-svc has not yet turned into an invoice or a credit.
func (r *Queries) Adjustments(ctx context.Context, pageSize int32) ([]*plansv1.Adjustment, error) {
	size := pageSize
	if size <= 0 {
		size = 100
	}
	size = min(size, 500)
	var out []*plansv1.Adjustment
	err := r.db.Tx(ctx, func(q *sqlc.Queries) error {
		rows, err := q.ListUnbilledAdjustments(ctx, size)
		if err != nil {
			return err
		}
		for _, a := range rows {
			sub, err := q.GetSubscription(ctx, a.SubscriptionID)
			if err != nil {
				return err
			}
			out = append(out, &plansv1.Adjustment{
				Id: a.ID.String(), Subscription: subsqry.Message(sub, r.now()), CreditMinor: a.CreditMinor, ChargeMinor: a.ChargeMinor,
				FromTime: timestamppb.New(a.FromAt), ToTime: timestamppb.New(a.ToAt), OldPlanCode: a.OldPlanCode,
			})
		}
		return nil
	})
	if err != nil {
		return nil, convert.Err(err, "subscription")
	}
	return out, nil
}

// UnusedFee is the prepaid, unused part of a subscription's fixed fee from at to the end of the paid period, and the end of that period.
// It is zero (ok=false) when nothing was prepaid for the period at is in: a trial, a pause, or a fee billing has not invoiced yet. The caller
// holds the row locked. A coupon makes the fee actually paid differ from the list fee, so a subscription with one is refused (ErrCouponOnCredit).
func UnusedFee(ctx context.Context, q *sqlc.Queries, now time.Time, row sqlc.GetSubscriptionRow, at time.Time) (credit int64, periodEnd time.Time, ok bool, err error) {
	if row.FeeTiming != "advance" {
		return 0, time.Time{}, false, nil
	}
	eff := convert.SubscriptionState(row.State, row.TrialEndsAt, row.OpenEnded, row.EndTime, now)
	if eff != subscription.Active {
		return 0, time.Time{}, false, nil
	}
	due, err := q.GetSubscriptionFeeDue(ctx, row.ID)
	if err != nil {
		return 0, time.Time{}, false, err
	}
	if due == nil || !due.After(at) {
		return 0, time.Time{}, false, nil
	}
	if _, err := q.GetSubscriptionCoupon(ctx, row.ID); err == nil {
		return 0, time.Time{}, false, ErrCouponOnCredit
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return 0, time.Time{}, false, err
	}
	t, err := tenantsqry.Current(ctx, q)
	if err != nil {
		return 0, time.Time{}, false, err
	}
	loc, err := tenantsqry.Location(t.Timezone)
	if err != nil {
		return 0, time.Time{}, false, err
	}
	pv, err := q.GetPlanVersionByID(ctx, row.PlanVersionID)
	if err != nil {
		return 0, time.Time{}, false, err
	}
	interval := subscription.Interval(row.Interval)
	fee, err := plansqry.FeeIn(ctx, q, pv.ID, pv.BaseFeeMinor, t.Currency, row.Currency, interval)
	if err != nil {
		return 0, time.Time{}, false, err
	}
	n, err := subscription.PeriodIndex(row.BillingAnchor, interval, at, loc)
	if err != nil {
		return 0, time.Time{}, false, err
	}
	ps, err := subscription.PeriodStart(row.BillingAnchor, interval, n, loc)
	if err != nil {
		return 0, time.Time{}, false, err
	}
	pe, err := subscription.PeriodEnd(row.BillingAnchor, interval, n, loc)
	if err != nil {
		return 0, time.Time{}, false, err
	}
	credit, err = subscription.Portion(fee, at, pe, ps, pe)
	if err != nil {
		return 0, time.Time{}, false, err
	}
	return credit, pe, credit > 0, nil
}
