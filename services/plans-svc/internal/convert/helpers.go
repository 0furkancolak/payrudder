package convert

import (
	"github.com/google/uuid"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/subscription"
)

// Str is the string p points to, empty for nil.
func Str(p *string) string { return str(p) }

// UUIDStr is the text of id, empty for nil.
func UUIDStr(id *uuid.UUID) string { return logoID(id) }

// IntervalToProto maps a billing interval to its API enum.
func IntervalToProto(i subscription.Interval) plansv1.SubscriptionInterval {
	if i == subscription.Yearly {
		return plansv1.SubscriptionInterval_SUBSCRIPTION_INTERVAL_YEAR
	}
	return plansv1.SubscriptionInterval_SUBSCRIPTION_INTERVAL_MONTH
}
