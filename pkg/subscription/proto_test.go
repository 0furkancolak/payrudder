package subscription_test

import (
	"testing"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/subscription"
)

func TestIntervalProtoRoundTrip(t *testing.T) {
	cases := []struct {
		in   plansv1.SubscriptionInterval
		want subscription.Interval
	}{
		{plansv1.SubscriptionInterval_SUBSCRIPTION_INTERVAL_MONTH, subscription.Monthly},
		{plansv1.SubscriptionInterval_SUBSCRIPTION_INTERVAL_YEAR, subscription.Yearly},
		{plansv1.SubscriptionInterval_SUBSCRIPTION_INTERVAL_UNSPECIFIED, subscription.Monthly},
	}
	for _, c := range cases {
		if got := subscription.IntervalFromProto(c.in); got != c.want {
			t.Errorf("IntervalFromProto(%v) = %q, want %q", c.in, got, c.want)
		}
	}
	if subscription.Yearly.Proto() != plansv1.SubscriptionInterval_SUBSCRIPTION_INTERVAL_YEAR ||
		subscription.Monthly.Proto() != plansv1.SubscriptionInterval_SUBSCRIPTION_INTERVAL_MONTH ||
		subscription.Interval("nonsense").Proto() != plansv1.SubscriptionInterval_SUBSCRIPTION_INTERVAL_MONTH {
		t.Fatal("Proto mapping")
	}
	if subscription.Yearly.Word() != "year" || subscription.Monthly.Word() != "month" {
		t.Fatal("Word")
	}
}
