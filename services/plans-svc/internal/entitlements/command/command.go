// Package command publishes entitlements: what each customer may use of each meter, for limits-svc.
package command

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	eventsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/events/v1"
	metersqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/meters/query"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// topic is the compacted topic limits-svc builds its entitlement map from.
const topic = "plans.entitlements.v1"

// entitlement is one limit as it is published: the plan version's, or the customer's override in its place.
type entitlement struct {
	meter      string
	limit      int64
	thresholds []int32
	window     string
	mode       string
	override   bool
}

// inForce merges the plan version's entitlements with the customer's overrides: an override replaces the plan's entitlement on its meter and adds one on a
// meter the plan does not limit. The result is ordered by meter code.
func inForce(ctx context.Context, q *sqlc.Queries, customerID, planVersionID uuid.UUID) ([]entitlement, error) {
	ents, err := q.ListPlanEntitlements(ctx, planVersionID)
	if err != nil {
		return nil, err
	}
	overrides, err := q.ListEntitlementOverrides(ctx, customerID)
	if err != nil {
		return nil, err
	}
	byMeter := map[string]entitlement{}
	var order []string
	for _, e := range ents {
		byMeter[e.MeterCode] = entitlement{meter: e.MeterCode, limit: e.LimitUnits, thresholds: e.AlertThresholdsPct, window: e.QuotaWindow, mode: e.Mode}
		order = append(order, e.MeterCode)
	}
	for _, o := range overrides {
		if _, ok := byMeter[o.MeterCode]; !ok {
			order = append(order, o.MeterCode)
		}
		byMeter[o.MeterCode] = entitlement{meter: o.MeterCode, limit: o.LimitUnits, thresholds: o.AlertThresholdsPct, window: o.QuotaWindow, mode: o.Mode, override: true}
	}
	out := make([]entitlement, 0, len(order))
	for _, m := range order {
		out = append(out, byMeter[m])
	}
	return out, nil
}

// Publish writes, in the caller's transaction, one outbox row per entitlement in force for the subscription (the plan version's, with the customer's
// overrides in their place): the state while the subscription is (or will be) active, or a tombstone once it has ended.
//
// Publishing a plan version emits nothing: subscriptions are pinned to a version, so no customer's entitlement
// changes until a new subscription is created or the customer's overrides change.
func Publish(ctx context.Context, q *sqlc.Queries, tenantID, customerID, planVersionID uuid.UUID,
	start time.Time, end *time.Time, now time.Time) error {
	ents, err := inForce(ctx, q, customerID, planVersionID)
	if err != nil {
		return err
	}
	if len(ents) == 0 {
		return nil
	}
	scales, err := metersqry.Scales(ctx, q)
	if err != nil {
		return err
	}
	for _, e := range ents {
		var payload []byte
		if end == nil || end.After(now) {
			st := &eventsv1.EntitlementState{
				TenantId: tenantID.String(), CustomerId: customerID.String(), MeterCode: e.meter,
				PlanVersionId: planVersionID.String(), LimitUnits: e.limit, AlertThresholdsPct: e.thresholds,
				ValidFrom: timestamppb.New(start), QuantityScale: uint32(scales[e.meter]), //nolint:gosec // 0..6
				Window: e.window, Mode: e.mode, Override: e.override,
			}
			if end != nil {
				st.ValidUntil = timestamppb.New(*end)
			}
			if payload, err = proto.Marshal(st); err != nil {
				return err
			}
		}
		if err := emit(ctx, q, tenantID, customerID, e.meter, payload); err != nil {
			return err
		}
	}
	return q.NotifyOutbox(ctx)
}

// Republish publishes again the entitlements of every subscription of the customer that has not ended, after its overrides changed. removed is the meter of
// an override that was just deleted, or "": when no plan version of those subscriptions limits it, its entitlement ends (a tombstone).
func Republish(ctx context.Context, q *sqlc.Queries, tenantID, customerID uuid.UUID, removed string, now time.Time) error {
	subs, err := q.ListUnendedSubscriptionsOfCustomer(ctx, sqlc.ListUnendedSubscriptionsOfCustomerParams{CustomerID: customerID, Now: now})
	if err != nil {
		return err
	}
	stillLimited := removed == ""
	for _, s := range subs {
		if stillLimited {
			break
		}
		ents, err := q.ListPlanEntitlements(ctx, s.PlanVersionID)
		if err != nil {
			return err
		}
		for _, e := range ents {
			stillLimited = stillLimited || e.MeterCode == removed
		}
	}
	if !stillLimited && len(subs) > 0 {
		if err := emit(ctx, q, tenantID, customerID, removed, nil); err != nil {
			return err
		}
	}
	// Oldest start first: where two subscriptions limit the same meter, the newest one's state is the last word on the compacted key.
	for _, s := range subs {
		var end *time.Time
		if !s.OpenEnded {
			e := s.EndTime
			end = &e
		}
		if err := Publish(ctx, q, tenantID, customerID, s.PlanVersionID, s.StartTime, end, now); err != nil {
			return err
		}
	}
	return q.NotifyOutbox(ctx)
}

// emit writes one outbox row on the entitlement key of (customer, meter); a nil payload is a tombstone.
func emit(ctx context.Context, q *sqlc.Queries, tenantID, customerID uuid.UUID, meter string, payload []byte) error {
	key := fmt.Sprintf("%s/%s/%s", tenantID, customerID, meter)
	return q.InsertOutbox(ctx, sqlc.InsertOutboxParams{Topic: topic, PartitionKey: key, Payload: payload})
}
