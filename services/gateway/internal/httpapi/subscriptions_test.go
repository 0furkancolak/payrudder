package httpapi

import (
	"context"
	"net/http"
	"testing"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
)

func (f *fakePlans) CancelSubscription(_ context.Context, req *plansv1.CancelSubscriptionRequest) (*plansv1.CancelSubscriptionResponse, error) {
	resp := &plansv1.CancelSubscriptionResponse{Subscription: &plansv1.Subscription{Id: req.GetSubscriptionId(), State: plansv1.SubscriptionState_SUBSCRIPTION_STATE_CANCELLED}}
	if req.GetCreditUnused() {
		resp.CreditedMinor = 1250
	}
	return resp, nil
}

// DELETE /v1/subscriptions/{id} answers {subscription, credited_minor} whether or not credit_unused was asked for.
func TestCancelSubscriptionAlwaysAnswersTheSameShape(t *testing.T) {
	ts, _ := newTestServer(t)
	for query, wantCredited := range map[string]string{"": "0", "?credit_unused=false": "0", "?credit_unused=true": "1250"} {
		resp, m := do(t, ts, "DELETE", "/v1/subscriptions/sub_1"+query, "live-key", "")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("cancel%s: %d %v", query, resp.StatusCode, m)
		}
		sub, _ := m["subscription"].(map[string]any)
		if sub["id"] != "sub_1" || m["credited_minor"] != wantCredited {
			t.Fatalf("cancel%s = %v, want a subscription and credited_minor %q", query, m, wantCredited)
		}
		if _, bare := m["state"]; bare {
			t.Fatalf("cancel%s answered a bare subscription: %v", query, m)
		}
	}
}
