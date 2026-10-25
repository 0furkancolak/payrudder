//go:build integration

package itest

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/dunning"
	sagacommand "github.com/0furkancolak/payrudder/services/billing-svc/internal/sagas/command"
)

// A tenant that set no dunning delays is shown the delays the service is configured with (PAYRUDDER_DUNNING_LIVE and _SANDBOX), the ones its sagas retry on,
// not the built-in ones.
func testDunningDefaultsAreTheConfiguredOnes(t *testing.T) {
	infra(t)
	eng, err := sagacommand.NewEngine(sagacommand.Config{
		DunningLive:    []time.Duration{2 * time.Hour, 5 * time.Hour, 48 * time.Hour},
		DunningSandbox: []time.Duration{2 * time.Minute, 4 * time.Minute, 9 * time.Minute},
	})
	if err != nil {
		t.Fatal(err)
	}
	h := dunning.New(shared.app, eng)
	for _, c := range []struct {
		environment string
		want        []int32
	}{{"live", []int32{2, 5, 48}}, {"sandbox", []int32{2, 4, 9}}} {
		ctx := tenant.NewContext(context.Background(), tenant.Info{ID: uuid.New(), Environment: c.environment})
		got, err := h.GetDunningSettings(ctx, &billingv1.GetDunningSettingsRequest{})
		if err != nil || !got.GetSettings().GetDefaultsInUse() || !slices.Equal(got.GetSettings().GetRetryHours(), c.want) {
			t.Errorf("%s tenant: %v, %v; want the configured delays %v", c.environment, got, err, c.want)
		}
	}
}
