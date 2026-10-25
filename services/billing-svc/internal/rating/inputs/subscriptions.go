package inputs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/payments"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/rating"
)

// Subscription is a subscription of the period as rating sees it.
type Subscription struct {
	ID, CustomerID, External string
	PlanVersionID            string
	Start                    time.Time
	Currency                 string
	ChangedFromID            string
	TrialEnd, PausedAt       *time.Time
	Paused                   bool
	// Provider, ProviderRef and Owner: the payment provider that sells the subscription and, when it runs it, its id there. A provider-owned
	// subscription's fixed fee is billed by the provider, so it is not on Payrudder's invoice.
	Provider, ProviderRef, Owner string
	// AdvanceFee: the fixed fee is invoiced at the start of each billing period (kind FEE), not with the month's usage.
	AdvanceFee bool
	// Chain is this subscription and the ones it replaced by plan changes inside the same period, oldest first. A
	// subscription that was not changed has a chain of one.
	Chain []Subscription
}

// ProviderOwned reports whether a payment provider runs the subscription (its renewals and fee, not Payrudder's invoices).
func (s Subscription) ProviderOwned() bool { return OwnedByProvider(s.Owner) }

// OwnedByProvider reports whether owner (a subscription's owner as plans-svc names it) is a payment provider.
func OwnedByProvider(owner string) bool { return owner == string(payments.OwnerProvider) }

// Subscriptions returns, per customer, the subscription that was active last in the period, with the plan changes
// that led to it inside the period as its Chain. Usage is priced at the last plan; the fixed fee is split between the
// plans of the chain (rating.FixedFeeLines).
func (in *Inputs) Subscriptions(ctx context.Context, tenantID uuid.UUID, start, end time.Time) (map[string]Subscription, error) {
	stream, err := in.Plans.ListSubscriptionsForPeriod(TenantCtx(ctx, tenantID), &plansv1.ListSubscriptionsForPeriodRequest{
		PeriodStart: timestamppb.New(start), PeriodEnd: timestamppb.New(end)})
	if err != nil {
		return nil, fmt.Errorf("list subscriptions: %w", err)
	}
	all := map[string][]Subscription{}
	for {
		r, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("list subscriptions: %w", err)
		}
		s := r.GetSubscription()
		sub := Subscription{ID: s.GetId(), CustomerID: s.GetCustomerId(), External: s.GetCustomerExternalId(),
			PlanVersionID: s.GetPlanVersionId(), Start: s.GetStartTime().AsTime(), Currency: s.GetCurrency(),
			ChangedFromID: s.GetChangedFromId(), Paused: s.GetState() == plansv1.SubscriptionState_SUBSCRIPTION_STATE_PAUSED,
			Provider: s.GetProvider(), ProviderRef: s.GetProviderRef(), Owner: s.GetOwner(),
			AdvanceFee: s.GetFeeTiming() == plansv1.FeeTiming_FEE_TIMING_ADVANCE}
		if s.GetTrialEndTime() != nil {
			t := s.GetTrialEndTime().AsTime()
			sub.TrialEnd = &t
		}
		if s.GetPauseTime() != nil {
			t := s.GetPauseTime().AsTime()
			sub.PausedAt = &t
		}
		all[sub.CustomerID] = append(all[sub.CustomerID], sub)
	}
	out := map[string]Subscription{}
	for cid, subs := range all {
		out[cid] = lastWithChain(subs)
	}
	return out, nil
}

// lastWithChain picks the latest-started subscription and walks back through ChangedFromID while the predecessor is in
// the period too.
func lastWithChain(subs []Subscription) Subscription {
	byID := make(map[string]Subscription, len(subs))
	last := subs[0]
	for _, s := range subs {
		byID[s.ID] = s
		if s.Start.After(last.Start) {
			last = s
		}
	}
	chain := []Subscription{last}
	for cur := last; cur.ChangedFromID != ""; {
		prev, ok := byID[cur.ChangedFromID]
		if !ok {
			break
		}
		chain = append([]Subscription{prev}, chain...)
		cur = prev
	}
	last.Chain = chain
	return last
}

// FeeSegments splits the period between the plan versions of the subscription's chain. The first plan is in force from the
// start of the period even if it began inside it (a subscription pays its whole first month), each later plan from the
// instant it replaced the one before. A segment is waived while a trial runs past the period, or when the subscription was
// paused before the period began.
func (in *Inputs) FeeSegments(ctx context.Context, plans map[string]rating.PlanVersion, tenantID uuid.UUID, sub Subscription,
	start, end time.Time, tenantCurrency string) ([]rating.FeeSegment, error) {
	var segs []rating.FeeSegment
	for i, member := range sub.Chain {
		pv, err := in.PlanVersion(ctx, plans, tenantID, member.PlanVersionID, member.Currency, tenantCurrency)
		if err != nil {
			return nil, err
		}
		from := start
		if i > 0 && member.Start.After(start) {
			from = member.Start
		}
		to := end
		if i+1 < len(sub.Chain) && sub.Chain[i+1].Start.Before(end) {
			to = sub.Chain[i+1].Start
		}
		if to.Before(from) {
			to = from
		}
		segs = append(segs, rating.FeeSegment{PlanCode: pv.PlanCode, FeeMinor: pv.BaseFeeMinor, From: from, To: to, Waived: FeeWaived(member, start, end)})
	}
	return segs, nil
}

// FeeWaived reports whether the fixed fee of the period is not on the month's invoice: a trial runs past the period, the subscription was paused before it
// began, the provider bills the fee, or it is invoiced in advance on its own.
func FeeWaived(member Subscription, start, end time.Time) bool {
	trialRunsPast := member.TrialEnd != nil && member.TrialEnd.After(end)
	pausedBefore := member.Paused && member.PausedAt != nil && !member.PausedAt.After(start)
	return trialRunsPast || pausedBefore || member.ProviderOwned() || member.AdvanceFee
}
