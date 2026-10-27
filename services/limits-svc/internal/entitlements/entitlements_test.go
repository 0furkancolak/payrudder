package entitlements

import (
	"testing"

	"github.com/google/uuid"

	eventsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/events/v1"
	"github.com/0furkancolak/payrudder/pkg/period"
)

// Every window plans-svc can publish is read back as the window of the entitlement; an empty one is the billing month, and an unknown one is refused
// (the state is skipped and logged, never counted over a window nobody knows).
func TestFromProtoWindows(t *testing.T) {
	state := func(window string) *eventsv1.EntitlementState {
		return &eventsv1.EntitlementState{TenantId: uuid.NewString(), CustomerId: uuid.NewString(), MeterCode: "api", LimitUnits: 10, Window: window}
	}
	for in, want := range map[string]period.Window{
		"": period.Month, "month": period.Month, "hour": period.Hour, "day": period.Day, "week": period.Week, "year": period.Year, "lifetime": period.Lifetime,
	} {
		st, err := fromProto(state(in))
		if err != nil || st.Window != want {
			t.Errorf("window %q = %q, %v; want %q", in, st.Window, err, want)
		}
	}
	for _, bad := range []string{"yearly", "forever", "Year", "all"} {
		if _, err := fromProto(state(bad)); err == nil {
			t.Errorf("window %q accepted", bad)
		}
	}
}
