// Package model holds the rules of the trial-ending reminder that need no database: which trials are due one, the window, and the fact that goes to the
// notifier.
package model

import (
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	eventsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/events/v1"
	"github.com/0furkancolak/payrudder/pkg/idgen"
	"github.com/0furkancolak/payrudder/pkg/subscription"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
)

// Topic is the Kafka topic of the TrialWillEnd fact (docs/04-messaging.md), written to the outbox and relayed like plans.entitlements.v1.
const Topic = "subscription.trial-will-end.v1"

// DefaultDays is how many days before its end a trial is reminded (PAYRUDDER_TRIAL_REMINDER_DAYS). It is one value for the whole service: a trial is a plan
// version's trial_days, which the customer may have overridden, and a reminder date of its own would need a column on every subscription.
const DefaultDays = 3

// Window is how long before its end a trial is reminded. days below 1 is the default.
func Window(days int) time.Duration {
	if days < 1 {
		days = DefaultDays
	}
	return time.Duration(days) * 24 * time.Hour
}

// Trial is what the reminder decides on: a subscription row as it is now, read under its lock.
type Trial struct {
	State             string
	OpenEnded         bool
	CancelAtPeriodEnd bool
	Owner             string
	EndsAt            *time.Time
}

// Remindable reports whether the trial is to be reminded at now: it is trialing and open-ended (not ended, not scheduled to end), will not be cancelled at the
// end of the period, is run by Payrudder (a provider reminds its own customers), and its end falls after now and within window. A trial that ends at or before
// now is too late to remind.
func (t Trial) Remindable(now time.Time, window time.Duration) bool {
	if t.State != string(subscription.Trialing) || !t.OpenEnded || t.CancelAtPeriodEnd || t.Owner != domain.OwnerPayrudder || t.EndsAt == nil {
		return false
	}
	return t.EndsAt.After(now) && !t.EndsAt.After(now.Add(window))
}

// EventID is the id of the reminder of one end of one customer's trial: the same trial end always gives the same id, a new end (an extended or restarted trial)
// another.
func EventID(tenantID, customerID uuid.UUID, trialEnds time.Time) uuid.UUID {
	return idgen.Deterministic("trial_will_end", tenantID.String(), customerID.String(), trialEnds.UTC().Format(time.RFC3339Nano))
}

// Event is the fact the notifier turns into the customer's e-mail.
func Event(tenantID, customerID, subscriptionID uuid.UUID, planCode string, trialEnds, now time.Time) *eventsv1.TrialWillEnd {
	return &eventsv1.TrialWillEnd{
		EventId: EventID(tenantID, customerID, trialEnds).String(), TenantId: tenantID.String(), CustomerId: customerID.String(), SubscriptionId: subscriptionID.String(),
		PlanCode: planCode, TrialEndsAt: timestamppb.New(trialEnds), DetectedAt: timestamppb.New(now),
	}
}

// PartitionKey is the Kafka key of the fact.
func PartitionKey(tenantID, customerID uuid.UUID) string {
	return tenantID.String() + "/" + customerID.String()
}
