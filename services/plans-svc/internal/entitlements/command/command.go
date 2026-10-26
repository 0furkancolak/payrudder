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

// Publish writes, in the caller's transaction, one outbox row per entitlement of the plan version:
// the state while the subscription is (or will be) active, or a tombstone once it has ended.
//
// Publishing a plan version emits nothing: subscriptions are pinned to a version, so no customer's entitlement
// changes until a new subscription is created.
func Publish(ctx context.Context, q *sqlc.Queries, tenantID, customerID, planVersionID uuid.UUID,
	start time.Time, end *time.Time, now time.Time) error {
	ents, err := q.ListPlanEntitlements(ctx, planVersionID)
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
		key := fmt.Sprintf("%s/%s/%s", tenantID, customerID, e.MeterCode)
		var payload []byte
		if end == nil || end.After(now) {
			st := &eventsv1.EntitlementState{
				TenantId: tenantID.String(), CustomerId: customerID.String(), MeterCode: e.MeterCode,
				PlanVersionId: planVersionID.String(), LimitUnits: e.LimitUnits, AlertThresholdsPct: e.AlertThresholdsPct,
				ValidFrom: timestamppb.New(start), QuantityScale: uint32(scales[e.MeterCode]), //nolint:gosec // 0..6
			}
			if end != nil {
				st.ValidUntil = timestamppb.New(*end)
			}
			if payload, err = proto.Marshal(st); err != nil {
				return err
			}
		}
		if err := q.InsertOutbox(ctx, sqlc.InsertOutboxParams{Topic: topic, PartitionKey: key, Payload: payload}); err != nil {
			return err
		}
	}
	return q.NotifyOutbox(ctx)
}
