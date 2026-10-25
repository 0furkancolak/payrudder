// Package query reads webhook endpoints and their deliveries.
package query

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/convert"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// Queries reads webhook endpoints and deliveries.
type Queries struct{ db *store.DB }

// New returns the webhook queries.
func New(db *store.DB) *Queries { return &Queries{db: db} }

// EndpointMessage maps an endpoint row to its API message (without the secret).
func EndpointMessage(r sqlc.ListWebhookEndpointsRow) *plansv1.WebhookEndpoint {
	return &plansv1.WebhookEndpoint{Id: r.ID.String(), Url: r.Url, Description: r.Description, EventTypes: r.EventTypes, Active: r.Active, CreateTime: timestamppb.New(r.CreatedAt)}
}

var deliveryStates = map[string]plansv1.WebhookDeliveryState{
	"pending":   plansv1.WebhookDeliveryState_WEBHOOK_DELIVERY_STATE_PENDING,
	"delivered": plansv1.WebhookDeliveryState_WEBHOOK_DELIVERY_STATE_DELIVERED,
	"failed":    plansv1.WebhookDeliveryState_WEBHOOK_DELIVERY_STATE_FAILED,
}

// DeliveryMessage maps a delivery row to its API message.
func DeliveryMessage(r sqlc.GetWebhookDeliveryRow) *plansv1.WebhookDelivery {
	out := &plansv1.WebhookDelivery{
		Id: r.ID.String(), EventId: r.EventID.String(), EventType: r.EventType, EndpointId: r.EndpointID.String(), State: deliveryStates[r.State],
		Attempts: r.Attempts, CreateTime: timestamppb.New(r.CreatedAt),
	}
	if r.State == "pending" {
		out.NextAttemptTime = timestamppb.New(r.NextAttemptAt)
	}
	if r.LastStatus != nil {
		out.LastStatus = *r.LastStatus
	}
	if r.LastError != nil {
		out.LastError = *r.LastError
	}
	if r.DeliveredAt != nil {
		out.DeliveredTime = timestamppb.New(*r.DeliveredAt)
	}
	return out
}

// Endpoints lists the tenant's webhook endpoints.
func (r *Queries) Endpoints(ctx context.Context) ([]*plansv1.WebhookEndpoint, error) {
	var out []*plansv1.WebhookEndpoint
	err := r.db.Tx(ctx, func(q *sqlc.Queries) error {
		rows, err := q.ListWebhookEndpoints(ctx)
		for _, row := range rows {
			out = append(out, EndpointMessage(row))
		}
		return err
	})
	if err != nil {
		return nil, convert.Err(err, "webhook endpoint")
	}
	return out, nil
}

// Deliveries lists deliveries, newest first.
func (r *Queries) Deliveries(ctx context.Context, req *plansv1.ListWebhookDeliveriesRequest) (*plansv1.ListWebhookDeliveriesResponse, error) {
	size := req.GetPageSize()
	switch {
	case size == 0:
		size = 50
	case size < 0 || size > 200:
		return nil, domain.Invalid("page_size", "1..200")
	}
	endpoint := uuid.Nil
	if v := req.GetEndpointId(); v != "" {
		id, err := domain.ParseID("endpoint_id", v)
		if err != nil {
			return nil, err
		}
		endpoint = id
	}
	state := ""
	switch req.GetState() {
	case plansv1.WebhookDeliveryState_WEBHOOK_DELIVERY_STATE_UNSPECIFIED:
	case plansv1.WebhookDeliveryState_WEBHOOK_DELIVERY_STATE_PENDING:
		state = "pending"
	case plansv1.WebhookDeliveryState_WEBHOOK_DELIVERY_STATE_DELIVERED:
		state = "delivered"
	case plansv1.WebhookDeliveryState_WEBHOOK_DELIVERY_STATE_FAILED:
		state = "failed"
	}
	afterTime, afterID := time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC), uuid.MustParse("ffffffff-ffff-ffff-ffff-ffffffffffff")
	if tok := req.GetPageToken(); tok != "" {
		at, id, ok := strings.Cut(tok, "|")
		var perr error
		if afterTime, perr = time.Parse(time.RFC3339Nano, at); !ok || perr != nil {
			return nil, domain.Invalid("page_token", "not one this API gave")
		}
		if afterID, perr = uuid.Parse(id); perr != nil {
			return nil, domain.Invalid("page_token", "not one this API gave")
		}
	}
	resp := &plansv1.ListWebhookDeliveriesResponse{}
	err := r.db.Tx(ctx, func(q *sqlc.Queries) error {
		rows, err := q.ListWebhookDeliveries(ctx, sqlc.ListWebhookDeliveriesParams{EndpointID: endpoint, State: state, AfterTime: afterTime, AfterID: afterID, PageSize: size + 1})
		for _, row := range rows {
			resp.Deliveries = append(resp.Deliveries, DeliveryMessage(sqlc.GetWebhookDeliveryRow(row)))
		}
		return err
	})
	if err != nil {
		return nil, convert.Err(err, "webhook delivery")
	}
	if len(resp.Deliveries) > int(size) {
		resp.Deliveries = resp.Deliveries[:size]
		last := resp.Deliveries[size-1]
		resp.NextPageToken = last.CreateTime.AsTime().UTC().Format(time.RFC3339Nano) + "|" + last.Id
	}
	return resp, nil
}
