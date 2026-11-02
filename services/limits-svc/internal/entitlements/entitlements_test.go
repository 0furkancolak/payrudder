package entitlements

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

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

// The customer's suspension is a record on the empty meter: it holds from valid_from until valid_until (open-ended when unset), a record that does not say
// suspended lifts it, and so does a tombstone on that key. It is not an entitlement: the customer's limits are untouched and none is added.
func TestSuspension(t *testing.T) {
	tn, cust, other := uuid.New(), uuid.New(), uuid.New()
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	st := New(nil, nil)
	apply := func(key string, pb *eventsv1.EntitlementState) {
		t.Helper()
		var raw []byte
		if pb != nil {
			var err error
			if raw, err = proto.Marshal(pb); err != nil {
				t.Fatal(err)
			}
		}
		if err := st.Apply(context.Background(), 0, []*kgo.Record{{Key: []byte(key), Value: raw, Offset: 0}}); err != nil {
			t.Fatal(err)
		}
	}
	record := func(suspended bool, until time.Time) *eventsv1.EntitlementState {
		pb := &eventsv1.EntitlementState{TenantId: tn.String(), CustomerId: cust.String(), Suspended: suspended, ValidFrom: timestamppb.New(now.Add(-time.Hour))}
		if !until.IsZero() {
			pb.ValidUntil = timestamppb.New(until)
		}
		return pb
	}
	key := fmt.Sprintf("%s/%s/", tn, cust)
	if st.Suspended(tn, cust, now) {
		t.Fatal("suspended before any record")
	}
	apply(key, record(true, time.Time{}))
	if !st.Suspended(tn, cust, now) || st.Suspended(tn, other, now) || st.Suspended(uuid.New(), cust, now) {
		t.Fatal("an open-ended suspension holds for its customer in its tenant, and for nobody else")
	}
	if st.Suspended(tn, cust, now.Add(-2*time.Hour)) {
		t.Error("holds before valid_from")
	}
	if st.Len() != 0 {
		t.Errorf("the suspension was kept as an entitlement (%d held)", st.Len())
	}
	apply(key, record(true, now.Add(time.Hour)))
	if !st.Suspended(tn, cust, now) || st.Suspended(tn, cust, now.Add(time.Hour)) {
		t.Error("a suspension with an end holds until the end, and not at it")
	}
	apply(key, record(false, time.Time{}))
	if st.Suspended(tn, cust, now) {
		t.Error("a record that does not say suspended lifts it")
	}
	apply(key, record(true, time.Time{}))
	apply(key, nil)
	if st.Suspended(tn, cust, now) {
		t.Error("a tombstone lifts it")
	}
}
