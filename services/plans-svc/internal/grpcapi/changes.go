package grpcapi

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/errkey"
	"github.com/0furkancolak/quotify/pkg/idgen"
	"github.com/0furkancolak/quotify/pkg/subscription"
	"github.com/0furkancolak/quotify/pkg/tenant"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/convert"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/domain"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/store/sqlc"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/webhooks"
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

func intervalToProto(i subscription.Interval) plansv1.SubscriptionInterval {
	if i == subscription.Yearly {
		return plansv1.SubscriptionInterval_SUBSCRIPTION_INTERVAL_YEAR
	}
	return plansv1.SubscriptionInterval_SUBSCRIPTION_INTERVAL_MONTH
}

// pricedChange is a change worked out but not yet made: what both the preview returns and the real change performs.
type pricedChange struct {
	row         sqlc.GetSubscriptionRow
	newPV       sqlc.GetPlanVersionByCodeRow
	newInterval subscription.Interval
	quote       subscription.Quote
	at          time.Time
	trial       bool
}

func (c pricedChange) message() *plansv1.ChangeQuote {
	q := c.quote
	return &plansv1.ChangeQuote{
		Timing: timingToProto[q.Timing], Proration: prorationToProto[q.Proration], EffectiveTime: timestamppb.New(q.Effective),
		PeriodStart: timestamppb.New(q.PeriodStart), PeriodEnd: timestamppb.New(q.PeriodEnd),
		CreditMinor: q.CreditMinor, ChargeMinor: q.ChargeMinor, NetMinor: q.NetMinor, Currency: c.row.Currency,
		NextRenewalTime: timestamppb.New(q.NextRenewal), NextRenewalMinor: q.NextRenewalMinor,
		PlanCode: c.newPV.PlanCode, PlanVersion: c.newPV.Version, Interval: intervalToProto(c.newInterval),
	}
}

// priceChange works out a change of the (locked) subscription row. It writes nothing.
func (s *Server) priceChange(ctx context.Context, q *sqlc.Queries, row sqlc.GetSubscriptionRow, req *plansv1.ChangeSubscriptionRequest) (*pricedChange, error) {
	now := s.now()
	eff, err := requireChangeable(row, now)
	if err != nil {
		return nil, err
	}
	at, err := changeInstant(req, now, row.StartTime)
	if err != nil {
		return nil, err
	}
	t, err := current(ctx, q)
	if err != nil {
		return nil, err
	}
	loc, err := location(t.Timezone)
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
	oldFee, err := feeIn(ctx, q, oldPV.ID, oldPV.BaseFeeMinor, t.Currency, row.Currency, oldInterval)
	if err != nil {
		return nil, err
	}
	newFee, err := feeIn(ctx, q, newPV.ID, newPV.BaseFeeMinor, t.Currency, row.Currency, newInterval)
	if err != nil {
		return nil, err
	}
	if err := requireUsagePrices(ctx, q, newPV.ID, t.Currency, row.Currency); err != nil {
		return nil, err
	}

	pc := &pricedChange{row: row, newPV: newPV, newInterval: newInterval, at: at}
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
		return nil, status.Error(codes.Aborted, "the fee of the current period has not been invoiced yet; try again in a moment")
	}
	pc.quote, err = subscription.QuoteChange(subscription.ChangeRequest{
		Old: subscription.Plan{FeeMinor: oldFee, Interval: oldInterval}, New: subscription.Plan{FeeMinor: newFee, Interval: newInterval},
		Anchor: row.BillingAnchor, At: at, Loc: loc, Timing: timingFromProto(req.GetTiming()), Proration: prorationFromProto(req.GetProration()),
	})
	if errors.Is(err, subscription.ErrBadChange) {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	return pc, err
}

// requireChangeable refuses a subscription that cannot change and returns its effective state.
func requireChangeable(row sqlc.GetSubscriptionRow, now time.Time) (subscription.State, error) {
	if err := requireQuotifyOwned(row.Owner, row.Provider); err != nil {
		return "", err
	}
	if row.FeeTiming != "advance" {
		return "", status.Error(codes.FailedPrecondition, "this subscription is billed in arrears; ChangePlan splits the month by time")
	}
	if !row.OpenEnded || row.CancelAtPeriodEnd {
		return "", status.Error(codes.FailedPrecondition, "an ended or ending subscription cannot change")
	}
	eff := convert.SubscriptionState(row.State, row.TrialEndsAt, row.OpenEnded, row.EndTime, now)
	if eff != subscription.Active && eff != subscription.Trialing {
		return "", status.Errorf(codes.FailedPrecondition, "a %s subscription cannot change", eff)
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
func (pc *pricedChange) quoteTrial(timing plansv1.ChangeTiming, newFee int64) error {
	if timing == plansv1.ChangeTiming_CHANGE_TIMING_PERIOD_END {
		return domain.Invalid("timing", "a trial changes immediately")
	}
	row := pc.row
	pc.trial = true
	pc.quote = subscription.Quote{
		Timing: subscription.Immediately, Proration: subscription.NoProration, Effective: pc.at, PeriodStart: pc.at, PeriodEnd: *row.TrialEndsAt,
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

// PreviewSubscriptionChange prices a change without making it.
func (s *Server) PreviewSubscriptionChange(ctx context.Context, preview *plansv1.PreviewSubscriptionChangeRequest) (*plansv1.PreviewSubscriptionChangeResponse, error) {
	req := preview.GetChange()
	id, err := parseSubscriptionID(req.GetSubscriptionId())
	if err != nil {
		return nil, err
	}
	var out *plansv1.ChangeQuote
	err = s.tx(ctx, func(q *sqlc.Queries) error {
		row, err := q.GetSubscription(ctx, id)
		if err != nil {
			return err
		}
		pc, err := s.priceChange(ctx, q, row, req)
		if err != nil {
			return err
		}
		out = pc.message()
		return nil
	})
	if err != nil {
		return nil, convert.Err(err, "subscription")
	}
	return &plansv1.PreviewSubscriptionChangeResponse{Quote: out}, nil
}

// successor is the subscription that takes over from another at a plan or interval change.
type successor struct {
	PlanVersion sqlc.GetPlanVersionByCodeRow
	Interval    subscription.Interval
	State       subscription.State
	TrialEnds   *time.Time
	At, Anchor  time.Time
	// FeeTiming and FeeDue: "advance" with the start of the next period to invoice, or "arrears" with none.
	FeeTiming string
	FeeDue    *time.Time
	// Owner and ProviderRef carry a provider-run subscription's link over.
	Owner       string
	ProviderRef *string
}

// switchTo ends old at next.At and starts its successor in the same transaction, with the entitlements of both.
func (s *Server) switchTo(ctx context.Context, q *sqlc.Queries, tenantID uuid.UUID, old sqlc.GetSubscriptionRow, next successor) (sqlc.InsertSubscriptionRow, error) {
	at := next.At
	if n, err := q.EndSubscription(ctx, sqlc.EndSubscriptionParams{ID: old.ID, EndTime: at}); err != nil {
		return sqlc.InsertSubscriptionRow{}, err
	} else if n != 1 {
		return sqlc.InsertSubscriptionRow{}, status.Error(codes.Aborted, "subscription changed concurrently; retry")
	}
	if _, err := q.SetSubscriptionState(ctx, sqlc.SetSubscriptionStateParams{ID: old.ID, State: string(subscription.Cancelled), PausedAt: old.PausedAt}); err != nil {
		return sqlc.InsertSubscriptionRow{}, err
	}
	if next.ProviderRef != nil && old.ProviderRef != nil && *next.ProviderRef == *old.ProviderRef {
		if _, err := q.ReleaseProviderLink(ctx, old.ID); err != nil {
			return sqlc.InsertSubscriptionRow{}, err
		}
	}
	oldID := old.ID
	owner := next.Owner
	if owner == "" {
		owner = "quotify"
	}
	providerRef := next.ProviderRef
	nr, err := q.InsertSubscription(ctx, sqlc.InsertSubscriptionParams{
		ID: idgen.NewUUIDv7(), CustomerID: old.CustomerID, PlanVersionID: next.PlanVersion.ID, StartTime: at,
		State: string(next.State), Interval: string(next.Interval), Currency: old.Currency, TrialEndsAt: next.TrialEnds,
		BillingAnchor: next.Anchor, ChangedFromID: &oldID, Provider: old.Provider, ProviderRef: providerRef, Owner: owner, FeeTiming: next.FeeTiming, FeeDueAt: next.FeeDue,
	})
	if err != nil {
		return nr, err
	}
	if _, err := q.MoveSubscriptionCoupon(ctx, sqlc.MoveSubscriptionCouponParams{OldID: old.ID, NewID: nr.ID}); err != nil {
		return nr, err
	}
	// Old entitlements first, new ones last: the topic is compacted per customer and meter, so the last write wins.
	if err := publishEntitlements(ctx, q, tenantID, old.CustomerID, old.PlanVersionID, old.StartTime, &at, s.now()); err != nil {
		return nr, err
	}
	return nr, publishEntitlements(ctx, q, tenantID, nr.CustomerID, nr.PlanVersionID, at, nil, s.now())
}

// ChangeSubscription moves a subscription billed in advance to another plan version and/or interval, now or at the end of the paid period.
func (s *Server) ChangeSubscription(ctx context.Context, req *plansv1.ChangeSubscriptionRequest) (*plansv1.ChangeSubscriptionResponse, error) {
	id, err := parseSubscriptionID(req.GetSubscriptionId())
	if err != nil {
		return nil, err
	}
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	resp := &plansv1.ChangeSubscriptionResponse{}
	err = s.tx(ctx, func(q *sqlc.Queries) error {
		cur, err := q.GetSubscriptionForUpdate(ctx, id)
		if err != nil {
			return err
		}
		row := sqlc.GetSubscriptionRow(cur)
		pc, err := s.priceChange(ctx, q, row, req)
		if err != nil {
			return err
		}
		// A change replaces the one that was waiting.
		if pending, err := q.GetPendingChange(ctx, id); err == nil {
			if _, err := q.SettleChange(ctx, sqlc.SettleChangeParams{ID: pending.ID, State: "cancelled"}); err != nil {
				return err
			}
		}
		resp.Quote = pc.message()
		qt := pc.quote
		if qt.Timing == subscription.AtPeriodEnd {
			if _, err := q.InsertSubscriptionChange(ctx, sqlc.InsertSubscriptionChangeParams{
				ID: idgen.NewUUIDv7(), SubscriptionID: id, PlanVersionID: pc.newPV.ID, Interval: string(pc.newInterval), EffectiveAt: qt.Effective}); err != nil {
				return err
			}
			resp.Scheduled = true
			resp.Subscription = subscriptionMessage(row, s.now())
			if err := attachExtras(ctx, q, resp.Subscription); err != nil {
				return err
			}
			return s.emit(ctx, q, webhooks.SubscriptionChangeScheduled, resp.Subscription, map[string]any{"quote": json.RawMessage(quoteJSON(resp.Quote))})
		}
		state := subscription.Active
		if pc.trial {
			state = subscription.Trialing
		}
		feeDue := qt.NewFeeDue
		nr, err := s.switchTo(ctx, q, ti.ID, row, successor{PlanVersion: pc.newPV, Interval: pc.newInterval, State: state, TrialEnds: row.TrialEndsAt, At: pc.at,
			Anchor: qt.NewAnchor, FeeTiming: "advance", FeeDue: &feeDue})
		if err != nil {
			return err
		}
		if qt.CreditMinor > 0 || qt.ChargeMinor > 0 {
			if err := q.InsertSubscriptionAdjustment(ctx, sqlc.InsertSubscriptionAdjustmentParams{
				ID: idgen.NewUUIDv7(), SubscriptionID: nr.ID, OldPlanCode: row.PlanCode, FromAt: pc.at, ToAt: qt.PeriodEnd, CreditMinor: qt.CreditMinor, ChargeMinor: qt.ChargeMinor}); err != nil {
				return err
			}
		}
		full, err := q.GetSubscription(ctx, nr.ID)
		if err != nil {
			return err
		}
		resp.Subscription = subscriptionMessage(full, s.now())
		if err := attachExtras(ctx, q, resp.Subscription); err != nil {
			return err
		}
		return s.emit(ctx, q, webhooks.SubscriptionChanged, resp.Subscription, map[string]any{"previous_subscription_id": row.ID.String(), "quote": json.RawMessage(quoteJSON(resp.Quote))})
	})
	if err != nil {
		return nil, convert.Err(err, "subscription")
	}
	return resp, nil
}

// CancelScheduledChange withdraws the change waiting for the end of the period.
func (s *Server) CancelScheduledChange(ctx context.Context, req *plansv1.CancelScheduledChangeRequest) (*plansv1.CancelScheduledChangeResponse, error) {
	id, err := parseSubscriptionID(req.GetSubscriptionId())
	if err != nil {
		return nil, err
	}
	var out *plansv1.Subscription
	err = s.tx(ctx, func(q *sqlc.Queries) error {
		if _, err := q.GetSubscriptionForUpdate(ctx, id); err != nil {
			return err
		}
		pending, err := q.GetPendingChange(ctx, id)
		if err != nil {
			return status.Error(codes.FailedPrecondition, "no change is scheduled")
		}
		if _, err := q.SettleChange(ctx, sqlc.SettleChangeParams{ID: pending.ID, State: "cancelled"}); err != nil {
			return err
		}
		r, err := q.GetSubscription(ctx, id)
		if err != nil {
			return err
		}
		out = subscriptionMessage(r, s.now())
		return nil
	})
	if err != nil {
		return nil, convert.Err(err, "subscription")
	}
	return &plansv1.CancelScheduledChangeResponse{Subscription: out}, nil
}

// ApplyScheduledChanges applies the changes whose time has come. A change whose subscription is no longer able to change (ended, paused,
// past due, ending) is cancelled instead: the customer's situation moved on. Applying twice does nothing.
func (s *Server) ApplyScheduledChanges(ctx context.Context, req *plansv1.ApplyScheduledChangesRequest) (*plansv1.ApplyScheduledChangesResponse, error) {
	if req.GetNow() == nil || req.GetNow().CheckValid() != nil {
		return nil, domain.Invalid("now", "required")
	}
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	applied := int32(0)
	err = s.tx(ctx, func(q *sqlc.Queries) error {
		applied = 0
		due, err := q.ListDueChanges(ctx, req.GetNow().AsTime())
		if err != nil {
			return err
		}
		for _, c := range due {
			cur, err := q.GetSubscriptionForUpdate(ctx, c.SubscriptionID)
			if err != nil {
				return err
			}
			row := sqlc.GetSubscriptionRow(cur)
			eff := convert.SubscriptionState(row.State, row.TrialEndsAt, row.OpenEnded, row.EndTime, req.GetNow().AsTime())
			if !row.OpenEnded || row.CancelAtPeriodEnd || eff != subscription.Active {
				if _, err := q.SettleChange(ctx, sqlc.SettleChangeParams{ID: c.ID, State: "cancelled"}); err != nil {
					return err
				}
				continue
			}
			pv, err := q.GetPlanVersionByID(ctx, c.PlanVersionID)
			if err != nil {
				return err
			}
			effective := c.EffectiveAt
			nr, err := s.switchTo(ctx, q, ti.ID, row, successor{PlanVersion: sqlc.GetPlanVersionByCodeRow(pv), Interval: subscription.Interval(c.Interval), State: subscription.Active,
				At: effective, Anchor: effective, FeeTiming: "advance", FeeDue: &effective})
			if err != nil {
				return err
			}
			if _, err := q.SettleChange(ctx, sqlc.SettleChangeParams{ID: c.ID, State: "applied", NewSubscriptionID: &nr.ID}); err != nil {
				return err
			}
			applied++
			full, err := q.GetSubscription(ctx, nr.ID)
			if err != nil {
				return err
			}
			msg := subscriptionMessage(full, s.now())
			if err := attachExtras(ctx, q, msg); err != nil {
				return err
			}
			if err := s.emit(ctx, q, webhooks.SubscriptionChanged, msg, map[string]any{"previous_subscription_id": row.ID.String(), "scheduled": true}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, convert.Err(err, "subscription")
	}
	return &plansv1.ApplyScheduledChangesResponse{Applied: applied}, nil
}

// ListSubscriptionAdjustments lists the money changes moved that billing-svc has not yet turned into an invoice or a credit.
func (s *Server) ListSubscriptionAdjustments(ctx context.Context, req *plansv1.ListSubscriptionAdjustmentsRequest) (*plansv1.ListSubscriptionAdjustmentsResponse, error) {
	size := req.GetPageSize()
	if size <= 0 {
		size = 100
	}
	size = min(size, 500)
	resp := &plansv1.ListSubscriptionAdjustmentsResponse{}
	err := s.tx(ctx, func(q *sqlc.Queries) error {
		rows, err := q.ListUnbilledAdjustments(ctx, size)
		if err != nil {
			return err
		}
		for _, a := range rows {
			sub, err := q.GetSubscription(ctx, a.SubscriptionID)
			if err != nil {
				return err
			}
			resp.Adjustments = append(resp.Adjustments, &plansv1.Adjustment{
				Id: a.ID.String(), Subscription: subscriptionMessage(sub, s.now()), CreditMinor: a.CreditMinor, ChargeMinor: a.ChargeMinor,
				FromTime: timestamppb.New(a.FromAt), ToTime: timestamppb.New(a.ToAt), OldPlanCode: a.OldPlanCode,
			})
		}
		return nil
	})
	if err != nil {
		return nil, convert.Err(err, "subscription")
	}
	return resp, nil
}

// MarkSubscriptionAdjustmentBilled records that billing-svc has dealt with an adjustment. Marking twice is fine.
func (s *Server) MarkSubscriptionAdjustmentBilled(ctx context.Context, req *plansv1.MarkSubscriptionAdjustmentBilledRequest) (*plansv1.MarkSubscriptionAdjustmentBilledResponse, error) {
	id, err := uuid.Parse(req.GetAdjustmentId())
	if err != nil {
		return nil, domain.Invalid("adjustment_id", "must be a UUID")
	}
	err = s.tx(ctx, func(q *sqlc.Queries) error {
		_, err := q.MarkAdjustmentBilled(ctx, id)
		return err
	})
	if err != nil {
		return nil, convert.Err(err, "adjustment")
	}
	return &plansv1.MarkSubscriptionAdjustmentBilledResponse{}, nil
}

// attachExtras sets scheduled_change and coupon_code on the subscriptions that have a change waiting or a coupon on.
func attachExtras(ctx context.Context, q *sqlc.Queries, subs ...*plansv1.Subscription) error {
	var ids []uuid.UUID
	byID := map[string]*plansv1.Subscription{}
	for _, sb := range subs {
		if id, err := uuid.Parse(sb.GetId()); err == nil {
			ids = append(ids, id)
			byID[sb.GetId()] = sb
		}
	}
	if len(ids) == 0 {
		return nil
	}
	rows, err := q.ListPendingChangesFor(ctx, ids)
	if err != nil {
		return err
	}
	for _, c := range rows {
		if sb := byID[c.SubscriptionID.String()]; sb != nil {
			sb.ScheduledChange = &plansv1.ScheduledChange{PlanCode: c.PlanCode, PlanVersion: c.PlanVersion, Interval: intervalToProto(subscription.Interval(c.Interval)), EffectiveTime: timestamppb.New(c.EffectiveAt)}
		}
	}
	coupons, err := q.ListCouponCodesFor(ctx, ids)
	if err != nil {
		return err
	}
	for _, c := range coupons {
		if sb := byID[c.SubscriptionID.String()]; sb != nil {
			sb.CouponCode = c.CouponCode
		}
	}
	return nil
}

// unusedFee is the prepaid, unused part of a subscription's fixed fee from at to the end of the paid period, and the end of that period.
// It is zero (ok=false) when nothing was prepaid for the period at is in: a trial, a pause, or a fee billing has not invoiced yet. The caller
// holds the row locked. A coupon makes the fee actually paid differ from the list fee, so a subscription with one is refused (errCouponOnCredit).
func (s *Server) unusedFee(ctx context.Context, q *sqlc.Queries, row sqlc.GetSubscriptionRow, at time.Time) (credit int64, periodEnd time.Time, ok bool, err error) {
	if row.FeeTiming != "advance" {
		return 0, time.Time{}, false, nil
	}
	eff := convert.SubscriptionState(row.State, row.TrialEndsAt, row.OpenEnded, row.EndTime, s.now())
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
		return 0, time.Time{}, false, errCouponOnCredit
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return 0, time.Time{}, false, err
	}
	t, err := current(ctx, q)
	if err != nil {
		return 0, time.Time{}, false, err
	}
	loc, err := location(t.Timezone)
	if err != nil {
		return 0, time.Time{}, false, err
	}
	pv, err := q.GetPlanVersionByID(ctx, row.PlanVersionID)
	if err != nil {
		return 0, time.Time{}, false, err
	}
	interval := subscription.Interval(row.Interval)
	fee, err := feeIn(ctx, q, pv.ID, pv.BaseFeeMinor, t.Currency, row.Currency, interval)
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

// errCouponOnCredit: the unused part of a discounted fee is not worked out automatically.
var errCouponOnCredit = errkey.New(codes.FailedPrecondition, "plans.subscription.coupon_blocks_credit", "this subscription has a coupon; credit the unused part with a credit note on its fee invoice instead")

// creditUnusedFee records the unused prepaid fee as an adjustment on the subscription, which billing turns into credit balance.
func (s *Server) creditUnusedFee(ctx context.Context, q *sqlc.Queries, row sqlc.GetSubscriptionRow, at time.Time) (int64, error) {
	credit, pe, ok, err := s.unusedFee(ctx, q, row, at)
	if err != nil || !ok {
		return 0, err
	}
	return credit, q.InsertSubscriptionAdjustment(ctx, sqlc.InsertSubscriptionAdjustmentParams{
		ID: idgen.NewUUIDv7(), SubscriptionID: row.ID, OldPlanCode: row.PlanCode, FromAt: at, ToAt: pe, CreditMinor: credit, ChargeMinor: 0})
}

// quoteJSON is a change quote the way the API returns it, for the event that announces the change.
func quoteJSON(q *plansv1.ChangeQuote) []byte {
	b, err := eventJSON.Marshal(q)
	if err != nil {
		return []byte("null")
	}
	return b
}
