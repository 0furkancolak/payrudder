package command

import (
	"context"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"

	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/idgen"
	"github.com/0furkancolak/payrudder/pkg/subscription"
	changesqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/changes/query"
	couponscmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/coupons/command"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
	entitlementscmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/entitlements/command"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// Successor is the subscription that takes over from another: at a plan or interval change, at a provider sync, at a provider
// cutover. The three share SwitchTo, so they end the old subscription, start the new one, carry the coupon and publish the
// entitlements by one rule.
type Successor struct {
	PlanVersionID uuid.UUID
	Interval      subscription.Interval
	State         subscription.State
	TrialEnds     *time.Time
	At, Anchor    time.Time
	// FeeTiming and FeeDue: subscription.FeeInAdvance with the start of the next period to invoice, or subscription.FeeInArrears with none.
	FeeTiming string
	FeeDue    *time.Time
	// Owner and ProviderRef carry a provider-run subscription's link over. Owner "" is Payrudder.
	Owner       string
	ProviderRef *string
	// Provider is the successor's payment provider; nil keeps the old subscription's.
	Provider *string
	// Quantity is the successor's seats; 0 keeps the old subscription's.
	Quantity int32
	// LeaveOldOpen ends the old subscription at At without marking it cancelled yet: a cutover dated in the future, where the
	// old one is still live until then.
	LeaveOldOpen bool
}

// CarriesCoupon is the one rule for the coupon of a subscription that is replaced: it follows the subscription, with the
// periods it has used, as long as the successor can hold one. A coupon takes something off a fee Payrudder invoices in
// advance (coupons.Redeem refuses any other subscription), so a successor that a provider runs, or that is billed in
// arrears, leaves it on the ended subscription.
func CarriesCoupon(next Successor) bool {
	return (next.Owner == "" || next.Owner == domain.OwnerPayrudder) && next.FeeTiming == subscription.FeeInAdvance
}

// SwitchTo ends old at next.At and starts its successor in the same transaction, with the entitlements of both.
func SwitchTo(ctx context.Context, q *sqlc.Queries, now time.Time, tenantID uuid.UUID, old sqlc.GetSubscriptionRow, next Successor) (sqlc.InsertSubscriptionRow, error) {
	at := next.At
	if err := End(ctx, q, old.ID, at); err != nil {
		return sqlc.InsertSubscriptionRow{}, err
	}
	if !next.LeaveOldOpen {
		if _, err := q.SetSubscriptionState(ctx, sqlc.SetSubscriptionStateParams{ID: old.ID, State: string(subscription.Cancelled), PausedAt: old.PausedAt}); err != nil {
			return sqlc.InsertSubscriptionRow{}, err
		}
	}
	if next.ProviderRef != nil && old.ProviderRef != nil && *next.ProviderRef == *old.ProviderRef {
		if _, err := q.ReleaseProviderLink(ctx, old.ID); err != nil {
			return sqlc.InsertSubscriptionRow{}, err
		}
	}
	oldID := old.ID
	owner := next.Owner
	if owner == "" {
		owner = domain.OwnerPayrudder
	}
	provider := old.Provider
	if next.Provider != nil {
		provider = next.Provider
	}
	quantity := next.Quantity
	if quantity == 0 {
		quantity = old.Quantity
	}
	nr, err := q.InsertSubscription(ctx, sqlc.InsertSubscriptionParams{
		ID: idgen.NewUUIDv7(), CustomerID: old.CustomerID, PlanVersionID: next.PlanVersionID, StartTime: at,
		State: string(next.State), Interval: string(next.Interval), Currency: old.Currency, TrialEndsAt: next.TrialEnds,
		BillingAnchor: next.Anchor, ChangedFromID: &oldID, Provider: provider, ProviderRef: next.ProviderRef, Owner: owner, FeeTiming: next.FeeTiming, FeeDueAt: next.FeeDue, Quantity: quantity,
	})
	if err != nil {
		return nr, err
	}
	if CarriesCoupon(next) {
		if err := couponscmd.Move(ctx, q, old.ID, nr.ID); err != nil {
			return nr, err
		}
	}
	// Old entitlements first, new ones last: the topic is compacted per customer and meter, so the last write wins.
	if err := entitlementscmd.Publish(ctx, q, tenantID, old.CustomerID, old.PlanVersionID, old.StartTime, &at, now); err != nil {
		return nr, err
	}
	return nr, entitlementscmd.Publish(ctx, q, tenantID, nr.CustomerID, nr.PlanVersionID, at, nil, now)
}

// CreditUnusedFee records the unused prepaid fee as an adjustment on the subscription, which billing turns into credit balance.
func CreditUnusedFee(ctx context.Context, q *sqlc.Queries, now time.Time, row sqlc.GetSubscriptionRow, at time.Time) (int64, error) {
	credit, pe, ok, err := changesqry.UnusedFee(ctx, q, now, row, at)
	if err != nil || !ok {
		return 0, err
	}
	return credit, q.InsertSubscriptionAdjustment(ctx, sqlc.InsertSubscriptionAdjustmentParams{
		ID: idgen.NewUUIDv7(), SubscriptionID: row.ID, OldPlanCode: row.PlanCode, FromAt: at, ToAt: pe, CreditMinor: credit, ChargeMinor: 0})
}

// AdvanceFee moves fee_due_at of a subscription to next when start is the fee period that is due now. It is a compare-and-set:
// false means the period was dealt with already or is not the next one, and nothing changed.
func AdvanceFee(ctx context.Context, q *sqlc.Queries, id uuid.UUID, start, next time.Time) (bool, error) {
	n, err := q.AdvanceSubscriptionFee(ctx, sqlc.AdvanceSubscriptionFeeParams{ID: id, PeriodStart: start, NextDue: next})
	return n > 0, err
}

// changedConcurrently is the answer to a change that found the subscription no longer as it was read: somebody else changed it first.
func changedConcurrently() error {
	return errkey.New(codes.Aborted, "plans.subscription.changed_concurrently", "subscription changed concurrently; retry")
}

// End ends the subscription at the given time. It is the one way a subscription's range is closed; a row that was changed since it was read is the
// "changed concurrently" answer.
func End(ctx context.Context, q *sqlc.Queries, id uuid.UUID, at time.Time) error {
	n, err := q.EndSubscription(ctx, sqlc.EndSubscriptionParams{ID: id, EndTime: at})
	if err != nil {
		return err
	}
	if n != 1 {
		return changedConcurrently()
	}
	return nil
}

// Reopen undoes End: the subscription runs on, its range open again.
func Reopen(ctx context.Context, q *sqlc.Queries, id uuid.UUID, now time.Time) error {
	n, err := q.ReopenSubscription(ctx, sqlc.ReopenSubscriptionParams{ID: id, Now: now})
	if err != nil {
		return err
	}
	if n != 1 {
		return changedConcurrently()
	}
	return nil
}
