package command

import (
	"context"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/subscription"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/convert"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
	plansqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/plans/query"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
	subsqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/subscriptions/query"
	tenantsqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/tenants/query"
)

// PlanChanged is the outcome of ChangePlan: the new subscription and the time-split fees of the month.
type PlanChanged struct {
	Subscription           *plansv1.Subscription
	OldPortion, NewPortion int64
}

// ChangePlan moves a subscription whose fee is invoiced in arrears to another plan version, splitting the month's fee by time.
func (c *Commands) ChangePlan(ctx context.Context, req *plansv1.ChangePlanRequest) (PlanChanged, error) {
	id, err := domain.ParseID("subscription_id", req.GetSubscriptionId())
	if err != nil {
		return PlanChanged{}, err
	}
	at := c.now()
	if req.GetChangeTime() != nil {
		if err := req.GetChangeTime().CheckValid(); err != nil {
			return PlanChanged{}, domain.Invalid("change_time", "invalid timestamp")
		}
		at = req.GetChangeTime().AsTime()
	}
	ti, err := tenant.Require(ctx)
	if err != nil {
		return PlanChanged{}, err
	}
	var res PlanChanged
	err = c.db.Tx(ctx, func(q *sqlc.Queries) error {
		var err error
		res, err = c.changePlan(ctx, q, ti.ID, id, at, req)
		return err
	})
	if err != nil {
		return PlanChanged{}, convert.Err(err, "subscription")
	}
	return res, nil
}

func (c *Commands) changePlan(ctx context.Context, q *sqlc.Queries, tenantID, id uuid.UUID, at time.Time, req *plansv1.ChangePlanRequest) (PlanChanged, error) {
	var res PlanChanged
	t, loc, err := tenantsqry.CurrentIn(ctx, q)
	if err != nil {
		return res, err
	}
	row, err := subsqry.Lock(ctx, q, id)
	if err != nil {
		return res, err
	}
	cust, err := q.GetCustomerByID(ctx, row.CustomerID)
	if err != nil {
		return res, convert.Err(err, "customer")
	}
	if cust.BlockedAt != nil {
		return res, errkey.New(codes.FailedPrecondition, "plans.customer.blocked", "this customer is blocked: no plan change")
	}
	eff, err := c.requirePlanChangeable(row, at)
	if err != nil {
		return res, err
	}
	newPV, err := q.GetPlanVersionByCode(ctx, sqlc.GetPlanVersionByCodeParams{PlanCode: req.GetPlanCode(), Version: req.GetPlanVersion()})
	if err != nil {
		return res, convert.Err(err, "plan version")
	}
	if newPV.ID == row.PlanVersionID {
		return res, domain.Invalid("plan_code", "the subscription is already on this plan version")
	}
	oldPV, err := q.GetPlanVersionByID(ctx, row.PlanVersionID)
	if err != nil {
		return res, err
	}
	interval := subscription.Interval(row.Interval)
	newFee, err := plansqry.FeeIn(ctx, q, newPV.ID, newPV.BaseFeeMinor, t.Currency, row.Currency, interval)
	if err != nil {
		return res, err
	}
	if err := plansqry.RequireUsagePrices(ctx, q, newPV.ID, t.Currency, row.Currency); err != nil {
		return res, err
	}
	if eff == subscription.Active {
		oldFee, err := plansqry.FeeIn(ctx, q, oldPV.ID, oldPV.BaseFeeMinor, t.Currency, row.Currency, interval)
		if err != nil {
			return res, err
		}
		periodStart, periodEnd, err := currentPeriod(row, loc, at)
		if err != nil {
			return res, err
		}
		if res.OldPortion, res.NewPortion, err = splitFees(oldFee, newFee, at, periodStart, periodEnd); err != nil {
			return res, err
		}
	}
	res.Subscription, err = c.replacePlan(ctx, q, tenantID, row, newPV.ID, eff, at)
	return res, err
}

// requirePlanChangeable refuses a subscription ChangePlan cannot move and returns its effective state.
func (c *Commands) requirePlanChangeable(row sqlc.GetSubscriptionRow, at time.Time) (subscription.State, error) {
	if err := domain.RequirePayrudderOwned(row.Owner, row.Provider); err != nil {
		return "", err
	}
	if !row.OpenEnded || row.CancelAtPeriodEnd {
		return "", errkey.New(codes.FailedPrecondition, "plans.change.subscription_ended", "an ended or ending subscription cannot change plan")
	}
	if row.FeeTiming == subscription.FeeInAdvance {
		return "", errkey.New(codes.FailedPrecondition, "plans.change.use_change_subscription", "a subscription whose fee is invoiced in advance is changed with ChangeSubscription (POST /v1/subscriptions/{id}/change), which prices the change and credits the unused time")
	}
	eff := convert.SubscriptionState(row.State, row.TrialEndsAt, row.OpenEnded, row.EndTime, c.now())
	if eff != subscription.Active && eff != subscription.Trialing {
		return "", errkey.Newf(codes.FailedPrecondition, "plans.change.state_not_changeable", "a %s subscription cannot change plan", eff)
	}
	if !at.After(row.StartTime) {
		return "", domain.Invalid("change_time", "must be after the subscription start")
	}
	return eff, nil
}

// splitFees is the part of each plan's fee the period is charged for: the old plan up to at, the new one from at.
func splitFees(oldFee, newFee int64, at, periodStart, periodEnd time.Time) (oldPortion, newPortion int64, err error) {
	if oldPortion, err = subscription.Portion(oldFee, periodStart, at, periodStart, periodEnd); err != nil {
		return 0, 0, errkey.New(codes.FailedPrecondition, "plans.change.bad_proration", err.Error())
	}
	if newPortion, err = subscription.Portion(newFee, at, periodEnd, periodStart, periodEnd); err != nil {
		return oldPortion, 0, errkey.New(codes.FailedPrecondition, "plans.change.bad_proration", err.Error())
	}
	return oldPortion, newPortion, nil
}

// replacePlan ends the subscription at `at` and starts its successor on another plan version.
func (c *Commands) replacePlan(ctx context.Context, q *sqlc.Queries, tenantID uuid.UUID, row sqlc.GetSubscriptionRow, planVersionID uuid.UUID, state subscription.State, at time.Time) (*plansv1.Subscription, error) {
	nr, err := SwitchTo(ctx, q, c.now(), tenantID, row, Successor{
		PlanVersionID: planVersionID, Interval: subscription.Interval(row.Interval), State: state, TrialEnds: row.TrialEndsAt, At: at,
		Anchor: row.BillingAnchor, FeeTiming: row.FeeTiming,
	})
	if err != nil {
		return nil, err
	}
	full, err := q.GetSubscription(ctx, nr.ID)
	if err != nil {
		return nil, err
	}
	return convert.SubscriptionOf(full, c.now()), nil
}
