package webhooks

import (
	"context"
	"encoding/json"
	"time"

	"google.golang.org/protobuf/encoding/protojson"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/idgen"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// EventJSON writes API messages the way the API returns them, for the data of an event.
var EventJSON = protojson.MarshalOptions{UseProtoNames: true}

// EmitSubscription records a subscription event in the caller's transaction: {"subscription": <as the API returns it>} plus extra fields.
func EmitSubscription(ctx context.Context, q *sqlc.Queries, now time.Time, typ string, sub *plansv1.Subscription, extra map[string]any) error {
	ok, err := q.HasWebhookTargets(ctx, typ)
	if err != nil || !ok {
		return err
	}
	raw, err := EventJSON.Marshal(sub)
	if err != nil {
		return err
	}
	data := map[string]any{"subscription": json.RawMessage(raw)}
	for k, v := range extra {
		data[k] = v
	}
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = Emit(ctx, q, idgen.NewUUIDv7(), typ, b, now)
	return err
}
