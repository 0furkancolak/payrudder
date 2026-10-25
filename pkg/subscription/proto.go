package subscription

import plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"

// IntervalFromProto maps the wire enum to an Interval. Anything but YEAR (MONTH, UNSPECIFIED) is Monthly: the plans API
// treats a missing interval as monthly, and callers that must reject UNSPECIFIED check for it first.
func IntervalFromProto(i plansv1.SubscriptionInterval) Interval {
	if i == plansv1.SubscriptionInterval_SUBSCRIPTION_INTERVAL_YEAR {
		return Yearly
	}
	return Monthly
}

// Proto maps the Interval to the wire enum; anything but Yearly is MONTH.
func (i Interval) Proto() plansv1.SubscriptionInterval {
	if i == Yearly {
		return plansv1.SubscriptionInterval_SUBSCRIPTION_INTERVAL_YEAR
	}
	return plansv1.SubscriptionInterval_SUBSCRIPTION_INTERVAL_MONTH
}

// Word is the interval as it appears in provider price keys, URLs and fee labels: "month" or "year".
func (i Interval) Word() string { return string(i) }
