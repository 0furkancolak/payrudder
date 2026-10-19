package grpcapi

import (
	"context"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/idgen"
	"github.com/0furkancolak/quotify/pkg/money"
	"github.com/0furkancolak/quotify/pkg/subscription"
	"github.com/0furkancolak/quotify/pkg/tenant"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/convert"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/domain"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/store/sqlc"
)

func subscriptionMessage(r sqlc.GetSubscriptionRow, now time.Time) *plansv1.Subscription {
	return convert.Subscription(convert.SubscriptionFields{
		ID: r.ID.String(), CustomerID: r.CustomerID.String(), CustomerExternalID: r.CustomerExternalID,
		PlanVersionID: r.PlanVersionID.String(), Start: r.StartTime, End: r.EndTime, OpenEnded: r.OpenEnded, Created: r.CreatedAt,
		State: r.State, Interval: r.Interval, Currency: r.Currency, TrialEnds: r.TrialEndsAt, PausedAt: r.PausedAt,
		CancelAtPeriodEnd: r.CancelAtPeriodEnd, ChangedFromID: uuidString(r.ChangedFromID), Now: now,
	})
}

func uuidString(id *uuid.UUID) string {
	if id == nil {
		return ""
	}
	return id.String()
}

func parseSubscriptionID(id string) (uuid.UUID, error) {
	u, err := uuid.Parse(id)
	if err != nil {
		return uuid.Nil, domain.Invalid("subscription_id", "must be a UUID")
	}
	return u, nil
}

func location(name string) (*time.Location, error) {
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "tenant time zone %q: %v", name, err)
	}
	return loc, nil
}

func intervalFromProto(i plansv1.SubscriptionInterval) (subscription.Interval, error) {
	switch i {
	case plansv1.SubscriptionInterval_SUBSCRIPTION_INTERVAL_UNSPECIFIED, plansv1.SubscriptionInterval_SUBSCRIPTION_INTERVAL_MONTH:
		return subscription.Monthly, nil
	case plansv1.SubscriptionInterval_SUBSCRIPTION_INTERVAL_YEAR:
		return "", status.Error(codes.Unimplemented, "yearly subscriptions are not supported yet; billing runs on calendar months")
	}
	return "", domain.Invalid("interval", "unknown interval")
}

// feeIn returns the fixed fee of a plan version in currency: base_fee_minor in the tenant's currency, else the
// per-currency fee. A plan version without a fee in the currency cannot be sold in it.
func feeIn(ctx context.Context, q *sqlc.Queries, planVersionID uuid.UUID, baseFee int64, tenantCurrency, currency string) (int64, error) {
	if currency == tenantCurrency {
		return baseFee, nil
	}
	fees, err := q.ListPlanVersionFees(ctx, planVersionID)
	if err != nil {
		return 0, err
	}
	for _, f := range fees {
		if f.Currency == currency {
			return f.BaseFeeMinor, nil
		}
	}
	return 0, status.Errorf(codes.FailedPrecondition, "plan version has no fixed fee in %s", currency)
}

// monthStart is the first instant of the calendar month t falls in, in loc: the anchor of a subscription's billing periods,
// which therefore are the calendar months billing-svc closes.
func monthStart(t time.Time, loc *time.Location) time.Time {
	y, m, _ := t.In(loc).Date()
	return time.Date(y, m, 1, 0, 0, 0, 0, loc)
}

// requireUsagePrices refuses a currency in which the plan version has metered prices but no price set. Fixed-fee-only plan
// versions need nothing beyond their fee.
func requireUsagePrices(ctx context.Context, q *sqlc.Queries, planVersionID uuid.UUID, tenantCurrency, currency string) error {
	if currency == tenantCurrency {
		return nil
	}
	base, err := q.ListPlanPrices(ctx, planVersionID)
	if err != nil {
		return err
	}
	if len(base) == 0 {
		return nil
	}
	cps, err := q.ListPlanCurrencyPrices(ctx, planVersionID)
	if err != nil {
		return err
	}
	for _, p := range cps {
		if p.Currency == currency {
			return nil
		}
	}
	return status.Errorf(codes.FailedPrecondition, "plan version has metered prices but none in %s", currency)
}

// currentPeriod returns the billing period of sub that contains at.
func currentPeriod(sub sqlc.GetSubscriptionRow, loc *time.Location, at time.Time) (start, end time.Time, err error) {
	n, err := subscription.PeriodIndex(sub.BillingAnchor, subscription.Interval(sub.Interval), at, loc)
	if err != nil {
		return start, end, status.Error(codes.FailedPrecondition, err.Error())
	}
	if start, err = subscription.PeriodStart(sub.BillingAnchor, subscription.Interval(sub.Interval), n, loc); err != nil {
		return start, end, err
	}
	end, err = subscription.PeriodEnd(sub.BillingAnchor, subscription.Interval(sub.Interval), n, loc)
	return start, end, err
}

func (s *Server) CreateSubscription(ctx context.Context, req *plansv1.CreateSubscriptionRequest) (*plansv1.CreateSubscriptionResponse, error) {
	start := s.now()
	if req.GetStartTime() != nil {
		if err := req.GetStartTime().CheckValid(); err != nil {
			return nil, domain.Invalid("start_time", "invalid timestamp")
		}
		start = req.GetStartTime().AsTime()
	}
	if d := req.GetTrialDays(); d < 0 || d > 365 {
		return nil, domain.Invalid("trial_days", "must be between 0 and 365")
	}
	interval, err := intervalFromProto(req.GetInterval())
	if err != nil {
		return nil, err
	}
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	var out *plansv1.Subscription
	err = s.tx(ctx, func(q *sqlc.Queries) error {
		t, err := current(ctx, q)
		if err != nil {
			return err
		}
		loc, err := location(t.Timezone)
		if err != nil {
			return err
		}
		c, err := q.GetCustomerByExternalID(ctx, req.GetCustomerExternalId())
		if err != nil {
			return convert.Err(err, "customer")
		}
		pv, err := q.GetPlanVersionByCode(ctx, sqlc.GetPlanVersionByCodeParams{PlanCode: req.GetPlanCode(), Version: req.GetPlanVersion()})
		if err != nil {
			return convert.Err(err, "plan version")
		}
		currency := req.GetCurrency()
		if currency == "" && c.BillingCurrency != nil {
			currency = *c.BillingCurrency
		}
		if currency == "" {
			currency = t.Currency
		}
		if _, ok := money.LookupCurrency(currency); !ok {
			return domain.Invalid("currency", "unsupported currency")
		}
		if _, err := feeIn(ctx, q, pv.ID, pv.BaseFeeMinor, t.Currency, currency); err != nil {
			return err
		}
		if err := requireUsagePrices(ctx, q, pv.ID, t.Currency, currency); err != nil {
			return err
		}
		state := subscription.Active
		var trialEnds *time.Time
		if d := int(req.GetTrialDays()); d > 0 {
			state = subscription.Trialing
			e := start.In(loc).AddDate(0, 0, d)
			trialEnds = &e
		}
		r, err := q.InsertSubscription(ctx, sqlc.InsertSubscriptionParams{
			ID: idgen.NewUUIDv7(), CustomerID: c.ID, PlanVersionID: pv.ID, StartTime: start,
			State: string(state), Interval: string(interval), Currency: currency, TrialEndsAt: trialEnds,
			BillingAnchor: monthStart(start, loc),
		})
		if err != nil {
			return err
		}
		out = convert.Subscription(convert.SubscriptionFields{
			ID: r.ID.String(), CustomerID: r.CustomerID.String(), CustomerExternalID: c.ExternalID, PlanVersionID: r.PlanVersionID.String(),
			Start: r.StartTime, End: r.EndTime, OpenEnded: r.OpenEnded, Created: r.CreatedAt, State: r.State, Interval: r.Interval,
			Currency: r.Currency, TrialEnds: r.TrialEndsAt, PausedAt: r.PausedAt, CancelAtPeriodEnd: r.CancelAtPeriodEnd, ChangedFromID: uuidString(r.ChangedFromID), Now: s.now(),
		})
		return publishEntitlements(ctx, q, ti.ID, r.CustomerID, r.PlanVersionID, r.StartTime, nil, s.now())
	})
	if err != nil {
		return nil, convert.Err(err, "subscription")
	}
	return &plansv1.CreateSubscriptionResponse{Subscription: out}, nil
}

func (s *Server) GetSubscription(ctx context.Context, req *plansv1.GetSubscriptionRequest) (*plansv1.GetSubscriptionResponse, error) {
	id, err := parseSubscriptionID(req.GetSubscriptionId())
	if err != nil {
		return nil, err
	}
	var out *plansv1.Subscription
	err = s.tx(ctx, func(q *sqlc.Queries) error {
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
	return &plansv1.GetSubscriptionResponse{Subscription: out}, nil
}

func (s *Server) CancelSubscription(ctx context.Context, req *plansv1.CancelSubscriptionRequest) (*plansv1.CancelSubscriptionResponse, error) {
	id, err := parseSubscriptionID(req.GetSubscriptionId())
	if err != nil {
		return nil, err
	}
	end := s.now()
	if req.GetEndTime() != nil {
		end = req.GetEndTime().AsTime()
	}
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	var out *plansv1.Subscription
	err = s.tx(ctx, func(q *sqlc.Queries) error {
		cur, err := q.GetSubscriptionForUpdate(ctx, id)
		if err != nil {
			return err
		}
		if !cur.OpenEnded {
			return status.Error(codes.FailedPrecondition, "subscription already ended")
		}
		if !end.After(cur.StartTime) {
			return domain.Invalid("end_time", "must be after the subscription start")
		}
		if _, err := subscription.Next(subscription.State(cur.State), subscription.Cancel); err != nil {
			return status.Error(codes.FailedPrecondition, err.Error())
		}
		n, err := q.EndSubscription(ctx, sqlc.EndSubscriptionParams{ID: id, EndTime: end})
		if err != nil {
			return err
		}
		if n != 1 {
			return status.Error(codes.Aborted, "subscription changed concurrently; retry")
		}
		if !end.After(s.now()) {
			if _, err := q.SetSubscriptionState(ctx, sqlc.SetSubscriptionStateParams{ID: id, State: string(subscription.Cancelled), PausedAt: cur.PausedAt}); err != nil {
				return err
			}
		}
		r, err := q.GetSubscription(ctx, id)
		if err != nil {
			return err
		}
		out = subscriptionMessage(r, s.now())
		return publishEntitlements(ctx, q, ti.ID, r.CustomerID, r.PlanVersionID, r.StartTime, &end, s.now())
	})
	if err != nil {
		return nil, convert.Err(err, "subscription")
	}
	return &plansv1.CancelSubscriptionResponse{Subscription: out}, nil
}

// moveSubscription applies event to the subscription's effective state and stores the result.
func (s *Server) moveSubscription(ctx context.Context, id uuid.UUID, ev subscription.Event, mutate func(sqlc.GetSubscriptionRow, subscription.State) (paused *time.Time, cancelAtEnd bool)) (*plansv1.Subscription, error) {
	var out *plansv1.Subscription
	err := s.tx(ctx, func(q *sqlc.Queries) error {
		cur, err := q.GetSubscriptionForUpdate(ctx, id)
		if err != nil {
			return err
		}
		row := sqlc.GetSubscriptionRow(cur)
		if !row.OpenEnded {
			return status.Error(codes.FailedPrecondition, "subscription already ended")
		}
		eff := convert.SubscriptionState(row.State, row.TrialEndsAt, row.OpenEnded, row.EndTime, s.now())
		next, err := subscription.Next(eff, ev)
		if err != nil {
			return status.Error(codes.FailedPrecondition, err.Error())
		}
		paused, cancelAtEnd := mutate(row, next)
		if _, err := q.SetSubscriptionState(ctx, sqlc.SetSubscriptionStateParams{
			ID: id, State: string(next), PausedAt: paused, CancelAtPeriodEnd: cancelAtEnd,
		}); err != nil {
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
	return out, nil
}

func (s *Server) PauseSubscription(ctx context.Context, req *plansv1.PauseSubscriptionRequest) (*plansv1.PauseSubscriptionResponse, error) {
	id, err := parseSubscriptionID(req.GetSubscriptionId())
	if err != nil {
		return nil, err
	}
	now := s.now()
	sub, err := s.moveSubscription(ctx, id, subscription.Pause, func(r sqlc.GetSubscriptionRow, _ subscription.State) (*time.Time, bool) {
		return &now, r.CancelAtPeriodEnd
	})
	if err != nil {
		return nil, err
	}
	return &plansv1.PauseSubscriptionResponse{Subscription: sub}, nil
}

func (s *Server) ResumeSubscription(ctx context.Context, req *plansv1.ResumeSubscriptionRequest) (*plansv1.ResumeSubscriptionResponse, error) {
	id, err := parseSubscriptionID(req.GetSubscriptionId())
	if err != nil {
		return nil, err
	}
	sub, err := s.moveSubscription(ctx, id, subscription.Resume, func(r sqlc.GetSubscriptionRow, _ subscription.State) (*time.Time, bool) {
		return nil, r.CancelAtPeriodEnd
	})
	if err != nil {
		return nil, err
	}
	return &plansv1.ResumeSubscriptionResponse{Subscription: sub}, nil
}

func (s *Server) ScheduleCancellation(ctx context.Context, req *plansv1.ScheduleCancellationRequest) (*plansv1.ScheduleCancellationResponse, error) {
	id, err := parseSubscriptionID(req.GetSubscriptionId())
	if err != nil {
		return nil, err
	}
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	var out *plansv1.Subscription
	err = s.tx(ctx, func(q *sqlc.Queries) error {
		t, err := current(ctx, q)
		if err != nil {
			return err
		}
		loc, err := location(t.Timezone)
		if err != nil {
			return err
		}
		cur, err := q.GetSubscriptionForUpdate(ctx, id)
		if err != nil {
			return err
		}
		row := sqlc.GetSubscriptionRow(cur)
		now := s.now()
		if req.GetCancel() {
			if !row.OpenEnded {
				return status.Error(codes.FailedPrecondition, "subscription already ended or already scheduled to end")
			}
			eff := convert.SubscriptionState(row.State, row.TrialEndsAt, row.OpenEnded, row.EndTime, now)
			if _, err := subscription.Next(eff, subscription.PeriodEndedCancelling); err != nil {
				return status.Error(codes.FailedPrecondition, err.Error())
			}
			// A subscription that has not started yet ends with its first period.
			ref := now
			if ref.Before(row.StartTime) {
				ref = row.StartTime
			}
			_, end, err := currentPeriod(row, loc, ref)
			if err != nil {
				return err
			}
			if n, err := q.EndSubscription(ctx, sqlc.EndSubscriptionParams{ID: id, EndTime: end}); err != nil {
				return err
			} else if n != 1 {
				return status.Error(codes.Aborted, "subscription changed concurrently; retry")
			}
			if _, err := q.SetSubscriptionState(ctx, sqlc.SetSubscriptionStateParams{ID: id, State: row.State, PausedAt: row.PausedAt, CancelAtPeriodEnd: true}); err != nil {
				return err
			}
			if err := publishEntitlements(ctx, q, ti.ID, row.CustomerID, row.PlanVersionID, row.StartTime, &end, now); err != nil {
				return err
			}
		} else {
			if !row.CancelAtPeriodEnd || row.OpenEnded || !row.EndTime.After(now) {
				return status.Error(codes.FailedPrecondition, "no scheduled cancellation to withdraw")
			}
			if n, err := q.ReopenSubscription(ctx, sqlc.ReopenSubscriptionParams{ID: id, Now: now}); err != nil {
				return err
			} else if n != 1 {
				return status.Error(codes.Aborted, "subscription changed concurrently; retry")
			}
			if _, err := q.SetSubscriptionState(ctx, sqlc.SetSubscriptionStateParams{ID: id, State: row.State, PausedAt: row.PausedAt, CancelAtPeriodEnd: false}); err != nil {
				return err
			}
			if err := publishEntitlements(ctx, q, ti.ID, row.CustomerID, row.PlanVersionID, row.StartTime, nil, now); err != nil {
				return err
			}
		}
		r, err := q.GetSubscription(ctx, id)
		if err != nil {
			return err
		}
		out = subscriptionMessage(r, now)
		return nil
	})
	if err != nil {
		return nil, convert.Err(err, "subscription")
	}
	return &plansv1.ScheduleCancellationResponse{Subscription: out}, nil
}

func (s *Server) ChangePlan(ctx context.Context, req *plansv1.ChangePlanRequest) (*plansv1.ChangePlanResponse, error) {
	id, err := parseSubscriptionID(req.GetSubscriptionId())
	if err != nil {
		return nil, err
	}
	at := s.now()
	if req.GetChangeTime() != nil {
		if err := req.GetChangeTime().CheckValid(); err != nil {
			return nil, domain.Invalid("change_time", "invalid timestamp")
		}
		at = req.GetChangeTime().AsTime()
	}
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	var (
		out                    *plansv1.Subscription
		oldPortion, newPortion int64
	)
	err = s.tx(ctx, func(q *sqlc.Queries) error {
		t, err := current(ctx, q)
		if err != nil {
			return err
		}
		loc, err := location(t.Timezone)
		if err != nil {
			return err
		}
		cur, err := q.GetSubscriptionForUpdate(ctx, id)
		if err != nil {
			return err
		}
		row := sqlc.GetSubscriptionRow(cur)
		if !row.OpenEnded || row.CancelAtPeriodEnd {
			return status.Error(codes.FailedPrecondition, "an ended or ending subscription cannot change plan")
		}
		eff := convert.SubscriptionState(row.State, row.TrialEndsAt, row.OpenEnded, row.EndTime, s.now())
		if eff != subscription.Active && eff != subscription.Trialing {
			return status.Errorf(codes.FailedPrecondition, "a %s subscription cannot change plan", eff)
		}
		if !at.After(row.StartTime) {
			return domain.Invalid("change_time", "must be after the subscription start")
		}
		newPV, err := q.GetPlanVersionByCode(ctx, sqlc.GetPlanVersionByCodeParams{PlanCode: req.GetPlanCode(), Version: req.GetPlanVersion()})
		if err != nil {
			return convert.Err(err, "plan version")
		}
		if newPV.ID == row.PlanVersionID {
			return domain.Invalid("plan_code", "the subscription is already on this plan version")
		}
		oldPV, err := q.GetPlanVersionByID(ctx, row.PlanVersionID)
		if err != nil {
			return err
		}
		newFee, err := feeIn(ctx, q, newPV.ID, newPV.BaseFeeMinor, t.Currency, row.Currency)
		if err != nil {
			return err
		}
		if err := requireUsagePrices(ctx, q, newPV.ID, t.Currency, row.Currency); err != nil {
			return err
		}
		if eff == subscription.Active {
			oldFee, err := feeIn(ctx, q, oldPV.ID, oldPV.BaseFeeMinor, t.Currency, row.Currency)
			if err != nil {
				return err
			}
			periodStart, periodEnd, err := currentPeriod(row, loc, at)
			if err != nil {
				return err
			}
			if oldPortion, err = subscription.Portion(oldFee, periodStart, at, periodStart, periodEnd); err != nil {
				return status.Error(codes.FailedPrecondition, err.Error())
			}
			if newPortion, err = subscription.Portion(newFee, at, periodEnd, periodStart, periodEnd); err != nil {
				return status.Error(codes.FailedPrecondition, err.Error())
			}
		}
		if n, err := q.EndSubscription(ctx, sqlc.EndSubscriptionParams{ID: id, EndTime: at}); err != nil {
			return err
		} else if n != 1 {
			return status.Error(codes.Aborted, "subscription changed concurrently; retry")
		}
		if _, err := q.SetSubscriptionState(ctx, sqlc.SetSubscriptionStateParams{ID: id, State: string(subscription.Cancelled), PausedAt: row.PausedAt}); err != nil {
			return err
		}
		anchor := row.BillingAnchor
		nr, err := q.InsertSubscription(ctx, sqlc.InsertSubscriptionParams{
			ID: idgen.NewUUIDv7(), CustomerID: row.CustomerID, PlanVersionID: newPV.ID, StartTime: at,
			State: string(eff), Interval: row.Interval, Currency: row.Currency, TrialEndsAt: row.TrialEndsAt,
			BillingAnchor: anchor, ChangedFromID: &id,
		})
		if err != nil {
			return err
		}
		out = convert.Subscription(convert.SubscriptionFields{
			ID: nr.ID.String(), CustomerID: nr.CustomerID.String(), CustomerExternalID: row.CustomerExternalID, PlanVersionID: nr.PlanVersionID.String(),
			Start: nr.StartTime, End: nr.EndTime, OpenEnded: nr.OpenEnded, Created: nr.CreatedAt, State: nr.State, Interval: nr.Interval,
			Currency: nr.Currency, TrialEnds: nr.TrialEndsAt, PausedAt: nr.PausedAt, CancelAtPeriodEnd: nr.CancelAtPeriodEnd, ChangedFromID: uuidString(nr.ChangedFromID), Now: s.now(),
		})
		// Old entitlements first, new ones last: the topic is compacted per customer and meter, so the last write wins.
		if err := publishEntitlements(ctx, q, ti.ID, row.CustomerID, row.PlanVersionID, row.StartTime, &at, s.now()); err != nil {
			return err
		}
		return publishEntitlements(ctx, q, ti.ID, nr.CustomerID, nr.PlanVersionID, at, nil, s.now())
	})
	if err != nil {
		return nil, convert.Err(err, "subscription")
	}
	return &plansv1.ChangePlanResponse{Subscription: out, OldPlanFeeMinor: oldPortion, NewPlanFeeMinor: newPortion}, nil
}
