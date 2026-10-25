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
	changescmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/changes/command"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/convert"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
	entitlementscmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/entitlements/command"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
	subsqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/subscriptions/query"
)

// providerStates maps a provider's status word to the state a subscription is in. The provider is the truth for a subscription it runs,
// so the state is set, not moved through subscription.Next.
var providerStates = map[string]subscription.State{
	"active": subscription.Active, "trialing": subscription.Trialing, "past_due": subscription.PastDue,
	"paused": subscription.Paused, "canceled": subscription.Cancelled,
}

// syncArgs is what a SyncProviderSubscription request says, checked: the state is the provider's, when it named one.
type syncArgs struct {
	id       uuid.UUID
	state    subscription.State
	known    bool
	occurred time.Time
}

func (c *Commands) parseSync(req *plansv1.SyncProviderSubscriptionRequest) (syncArgs, error) {
	var a syncArgs
	var err error
	if a.id, err = subsqry.ParseID(req.GetSubscriptionId()); err != nil {
		return a, err
	}
	if !domain.ValidProviderName(req.GetProvider()) {
		return a, domain.Invalid("provider", "a lower-case provider name such as paddle")
	}
	if req.GetProviderRef() == "" {
		return a, domain.Invalid("provider_ref", "required")
	}
	if o := req.GetOwner(); o != "quotify" && o != "provider" {
		return a, domain.Invalid("owner", `"quotify" or "provider"`)
	}
	a.state, a.known = providerStates[req.GetProviderStatus()]
	if req.GetProviderStatus() != "" && !a.known {
		return a, domain.Invalid("provider_status", "active, trialing, past_due, paused or canceled")
	}
	a.occurred = c.now()
	if req.GetOccurredAt() != nil {
		a.occurred = req.GetOccurredAt().AsTime()
	}
	return a, nil
}

// SyncProvider links a subscription to the provider that runs it and follows the state and plan the provider reports.
func (c *Commands) SyncProvider(ctx context.Context, req *plansv1.SyncProviderSubscriptionRequest) (*plansv1.Subscription, error) {
	a, err := c.parseSync(req)
	if err != nil {
		return nil, err
	}
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	var out *plansv1.Subscription
	err = c.db.Tx(ctx, func(q *sqlc.Queries) error {
		var err error
		out, err = c.syncProvider(ctx, q, ti.ID, req, a)
		return err
	})
	if err != nil {
		return nil, convert.Err(err, "subscription")
	}
	return out, nil
}

func (c *Commands) syncProvider(ctx context.Context, q *sqlc.Queries, tenantID uuid.UUID, req *plansv1.SyncProviderSubscriptionRequest, a syncArgs) (*plansv1.Subscription, error) {
	id := currentInChain(ctx, q, a.id, req.GetProvider(), req.GetProviderRef())
	cur, err := q.GetSubscriptionForUpdate(ctx, id)
	if err != nil {
		return nil, err
	}
	row := sqlc.GetSubscriptionRow(cur)
	if row.ProviderRef != nil && (*row.ProviderRef != req.GetProviderRef() || convert.Str(row.Provider) != req.GetProvider()) {
		return nil, errkey.Newf(codes.FailedPrecondition, "plans.subscription.already_linked", "subscription is already linked to %s %s", convert.Str(row.Provider), *row.ProviderRef)
	}
	n, err := q.LinkProviderSubscription(ctx, sqlc.LinkProviderSubscriptionParams{
		ID: id, Provider: new(req.GetProvider()), ProviderRef: new(req.GetProviderRef()), Owner: req.GetOwner(), OccurredAt: a.occurred,
	})
	if err != nil {
		return nil, err
	}
	linked := n == 1 && req.GetOwner() == "provider"
	if linked && a.known {
		if err := c.applyProviderState(ctx, q, tenantID, row, a); err != nil {
			return nil, err
		}
	}
	if id, err = c.followProviderPlan(ctx, q, tenantID, row, req, a, linked); err != nil {
		return nil, err
	}
	r, err := q.GetSubscription(ctx, id)
	if err != nil {
		return nil, err
	}
	return subsqry.Message(r, c.now()), nil
}

// currentInChain returns the subscription that holds the provider's id now. Events keep naming the subscription they were
// first linked to; if plan changes have replaced it, the provider's id is held by its successor.
func currentInChain(ctx context.Context, q *sqlc.Queries, id uuid.UUID, provider, ref string) uuid.UUID {
	live, err := q.GetSubscriptionIDByProviderRef(ctx, sqlc.GetSubscriptionIDByProviderRefParams{Provider: &provider, ProviderRef: &ref})
	if err != nil || live == id {
		return id
	}
	// Only the same chain: another subscription that names a provider id that is taken must still be refused.
	for cur, hops := live, 0; hops < 64; hops++ {
		prev, err := q.GetChangedFromID(ctx, cur)
		if err != nil || prev == nil {
			break
		}
		if *prev == id {
			return live
		}
		cur = *prev
	}
	return id
}

// applyProviderState sets the state the provider reports, ending the subscription when it was cancelled there. The
// provider is the truth for a subscription it runs, so the state is set, not moved through subscription.Next.
func (c *Commands) applyProviderState(ctx context.Context, q *sqlc.Queries, tenantID uuid.UUID, row sqlc.GetSubscriptionRow, a syncArgs) error {
	var pausedAt *time.Time
	if a.state == subscription.Paused {
		pausedAt = &a.occurred
	}
	if a.state == subscription.Cancelled && row.OpenEnded {
		end := a.occurred
		if !end.After(row.StartTime) {
			end = row.StartTime.Add(time.Microsecond)
		}
		if _, err := q.EndSubscription(ctx, sqlc.EndSubscriptionParams{ID: row.ID, EndTime: end}); err != nil {
			return err
		}
		if err := entitlementscmd.Publish(ctx, q, tenantID, row.CustomerID, row.PlanVersionID, row.StartTime, &end, c.now()); err != nil {
			return err
		}
	}
	_, err := q.SetSubscriptionState(ctx, sqlc.SetSubscriptionStateParams{ID: row.ID, State: string(a.state), PausedAt: pausedAt, CancelAtPeriodEnd: row.CancelAtPeriodEnd})
	return err
}

// followProviderPlan (after a link the provider owns) moves the subscription to the plan version or interval the provider bills, when it differs from ours,
// and returns the id of the subscription that is current afterwards. The provider has billed the change itself, so no money moves here.
func (c *Commands) followProviderPlan(ctx context.Context, q *sqlc.Queries, tenantID uuid.UUID, row sqlc.GetSubscriptionRow, req *plansv1.SyncProviderSubscriptionRequest, a syncArgs, linked bool) (uuid.UUID, error) {
	target := uuid.Nil
	if v := req.GetPlanVersionId(); v != "" {
		var err error
		if target, err = uuid.Parse(v); err != nil {
			return row.ID, domain.Invalid("plan_version_id", "must be a UUID")
		}
	}
	iv := subscription.Interval(row.Interval)
	switch req.GetInterval() {
	case plansv1.SubscriptionInterval_SUBSCRIPTION_INTERVAL_MONTH:
		iv = subscription.Monthly
	case plansv1.SubscriptionInterval_SUBSCRIPTION_INTERVAL_YEAR:
		iv = subscription.Yearly
	}
	newPlan := target != uuid.Nil && target != row.PlanVersionID
	newInterval := req.GetInterval() != plansv1.SubscriptionInterval_SUBSCRIPTION_INTERVAL_UNSPECIFIED && string(iv) != row.Interval
	if !linked || !row.OpenEnded || a.state == subscription.Cancelled || (!newPlan && !newInterval) {
		return row.ID, nil
	}
	if target == uuid.Nil {
		target = row.PlanVersionID
	}
	pv, err := q.GetPlanVersionByID(ctx, target)
	if err != nil {
		return row.ID, convert.Err(err, "plan version")
	}
	at := a.occurred
	if !at.After(row.StartTime) {
		at = row.StartTime.Add(time.Microsecond)
	}
	next := a.state
	if !a.known {
		next = convert.SubscriptionState(row.State, row.TrialEndsAt, row.OpenEnded, row.EndTime, at)
	}
	nr, err := changescmd.SwitchTo(ctx, q, c.now(), tenantID, row, changescmd.Successor{PlanVersion: sqlc.GetPlanVersionByCodeRow(pv), Interval: iv, State: next, TrialEnds: row.TrialEndsAt, At: at,
		Anchor: row.BillingAnchor, FeeTiming: "arrears", Owner: "provider", ProviderRef: row.ProviderRef})
	if err != nil {
		return row.ID, err
	}
	if next == subscription.Paused {
		if _, err := q.SetSubscriptionState(ctx, sqlc.SetSubscriptionStateParams{ID: nr.ID, State: string(next), PausedAt: &at}); err != nil {
			return row.ID, err
		}
	}
	return nr.ID, nil
}
