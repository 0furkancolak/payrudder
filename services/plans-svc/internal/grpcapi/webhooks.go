package grpcapi

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/idgen"
	"github.com/0furkancolak/quotify/pkg/safehttp"
	"github.com/0furkancolak/quotify/pkg/secretbox"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/convert"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/domain"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/store/sqlc"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/webhooks"
)

const (
	maxWebhookEndpoints = 20
	secretOverlap       = 24 * time.Hour
)

// Webhooks serves plansv1.WebhookService on top of the plans Server.
type Webhooks struct {
	plansv1.UnimplementedWebhookServiceServer
	s            *Server
	box          *secretbox.Box
	allowPrivate bool
}

// NewWebhooks returns the webhook service. box seals the signing secrets (nil: endpoints cannot be created). allowPrivate lets endpoints be http:// and
// private addresses (development and tests only).
func NewWebhooks(s *Server, box *secretbox.Box, allowPrivate bool) *Webhooks {
	return &Webhooks{s: s, box: box, allowPrivate: allowPrivate}
}

func endpointMessage(r sqlc.ListWebhookEndpointsRow) *plansv1.WebhookEndpoint {
	return &plansv1.WebhookEndpoint{Id: r.ID.String(), Url: r.Url, Description: r.Description, EventTypes: r.EventTypes, Active: r.Active, CreateTime: timestamppb.New(r.CreatedAt)}
}

func (w *Webhooks) validate(url, description string, types []string) ([]string, error) {
	var v domain.Violations
	if err := safehttp.ValidateURL(url, w.allowPrivate); err != nil {
		v.Add("url", err.Error())
	}
	if len(description) > 200 {
		v.Add("description", "at most 200 characters")
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(types))
	for i, t := range types {
		switch {
		case !webhooks.Known(t):
			v.Add("event_types["+itoa(i)+"]", "unknown event type "+t)
		case !seen[t]:
			out = append(out, t)
		}
		seen[t] = true
	}
	return out, v.Err()
}

func itoa(i int) string { return strconv.Itoa(i) }

func newSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "whsec_" + base64.RawURLEncoding.EncodeToString(b), nil
}

func (w *Webhooks) requireBox() error {
	if w.box == nil {
		return status.Error(codes.FailedPrecondition, "webhooks are not configured on this deployment (QUOTIFY_SECRETS_KEY)")
	}
	return nil
}

// CreateWebhookEndpoint implements plansv1.WebhookServiceServer.
func (w *Webhooks) CreateWebhookEndpoint(ctx context.Context, req *plansv1.CreateWebhookEndpointRequest) (*plansv1.CreateWebhookEndpointResponse, error) {
	if err := w.requireBox(); err != nil {
		return nil, err
	}
	types, err := w.validate(req.GetUrl(), req.GetDescription(), req.GetEventTypes())
	if err != nil {
		return nil, err
	}
	secret, err := newSecret()
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	id := idgen.NewUUIDv7()
	sealed, err := w.box.Seal([]byte(secret), webhooks.SecretAAD(id))
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	var out *plansv1.WebhookEndpoint
	err = w.s.tx(ctx, func(q *sqlc.Queries) error {
		n, err := q.CountWebhookEndpoints(ctx)
		if err != nil {
			return err
		}
		if n >= maxWebhookEndpoints {
			return status.Errorf(codes.FailedPrecondition, "at most %d webhook endpoints; delete one first", maxWebhookEndpoints)
		}
		r, err := q.InsertWebhookEndpoint(ctx, sqlc.InsertWebhookEndpointParams{ID: id, Url: req.GetUrl(), Description: req.GetDescription(), EventTypes: types, SecretEnc: sealed})
		if err != nil {
			return err
		}
		out = endpointMessage(sqlc.ListWebhookEndpointsRow(r))
		out.Secret = secret
		return nil
	})
	if err != nil {
		return nil, convert.Err(err, "webhook endpoint")
	}
	return &plansv1.CreateWebhookEndpointResponse{Endpoint: out}, nil
}

// ListWebhookEndpoints implements plansv1.WebhookServiceServer.
func (w *Webhooks) ListWebhookEndpoints(ctx context.Context, _ *plansv1.ListWebhookEndpointsRequest) (*plansv1.ListWebhookEndpointsResponse, error) {
	resp := &plansv1.ListWebhookEndpointsResponse{}
	err := w.s.tx(ctx, func(q *sqlc.Queries) error {
		rows, err := q.ListWebhookEndpoints(ctx)
		for _, r := range rows {
			resp.Endpoints = append(resp.Endpoints, endpointMessage(r))
		}
		return err
	})
	if err != nil {
		return nil, convert.Err(err, "webhook endpoint")
	}
	return resp, nil
}

func parseID(field, v string) (uuid.UUID, error) {
	id, err := uuid.Parse(v)
	if err != nil {
		return uuid.Nil, domain.Invalid(field, "must be a UUID")
	}
	return id, nil
}

// UpdateWebhookEndpoint implements plansv1.WebhookServiceServer.
func (w *Webhooks) UpdateWebhookEndpoint(ctx context.Context, req *plansv1.UpdateWebhookEndpointRequest) (*plansv1.UpdateWebhookEndpointResponse, error) {
	id, err := parseID("id", req.GetId())
	if err != nil {
		return nil, err
	}
	types, err := w.validate(req.GetUrl(), req.GetDescription(), req.GetEventTypes())
	if err != nil {
		return nil, err
	}
	var out *plansv1.WebhookEndpoint
	err = w.s.tx(ctx, func(q *sqlc.Queries) error {
		r, err := q.UpdateWebhookEndpoint(ctx, sqlc.UpdateWebhookEndpointParams{ID: id, Url: req.GetUrl(), Description: req.GetDescription(), EventTypes: types, Active: req.GetActive()})
		if err != nil {
			return err
		}
		out = endpointMessage(sqlc.ListWebhookEndpointsRow(r))
		return nil
	})
	if err != nil {
		return nil, convert.Err(err, "webhook endpoint")
	}
	return &plansv1.UpdateWebhookEndpointResponse{Endpoint: out}, nil
}

// DeleteWebhookEndpoint implements plansv1.WebhookServiceServer.
func (w *Webhooks) DeleteWebhookEndpoint(ctx context.Context, req *plansv1.DeleteWebhookEndpointRequest) (*plansv1.DeleteWebhookEndpointResponse, error) {
	id, err := parseID("id", req.GetId())
	if err != nil {
		return nil, err
	}
	err = w.s.tx(ctx, func(q *sqlc.Queries) error {
		n, err := q.DeleteWebhookEndpoint(ctx, id)
		if err == nil && n == 0 {
			return pgx.ErrNoRows
		}
		return err
	})
	if err != nil {
		return nil, convert.Err(err, "webhook endpoint")
	}
	return &plansv1.DeleteWebhookEndpointResponse{}, nil
}

// RotateWebhookSecret implements plansv1.WebhookServiceServer.
func (w *Webhooks) RotateWebhookSecret(ctx context.Context, req *plansv1.RotateWebhookSecretRequest) (*plansv1.RotateWebhookSecretResponse, error) {
	if err := w.requireBox(); err != nil {
		return nil, err
	}
	id, err := parseID("id", req.GetId())
	if err != nil {
		return nil, err
	}
	secret, err := newSecret()
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	sealed, err := w.box.Seal([]byte(secret), webhooks.SecretAAD(id))
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	var out *plansv1.WebhookEndpoint
	err = w.s.tx(ctx, func(q *sqlc.Queries) error {
		r, err := q.RotateWebhookSecret(ctx, sqlc.RotateWebhookSecretParams{ID: id, SecretEnc: sealed, Until: ptr(w.s.now().Add(secretOverlap))})
		if err != nil {
			return err
		}
		out = endpointMessage(sqlc.ListWebhookEndpointsRow(r))
		out.Secret = secret
		return nil
	})
	if err != nil {
		return nil, convert.Err(err, "webhook endpoint")
	}
	return &plansv1.RotateWebhookSecretResponse{Endpoint: out}, nil
}

var deliveryStates = map[string]plansv1.WebhookDeliveryState{
	"pending":   plansv1.WebhookDeliveryState_WEBHOOK_DELIVERY_STATE_PENDING,
	"delivered": plansv1.WebhookDeliveryState_WEBHOOK_DELIVERY_STATE_DELIVERED,
	"failed":    plansv1.WebhookDeliveryState_WEBHOOK_DELIVERY_STATE_FAILED,
}

func deliveryMessage(r sqlc.GetWebhookDeliveryRow) *plansv1.WebhookDelivery {
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

// SendTestWebhook implements plansv1.WebhookServiceServer.
func (w *Webhooks) SendTestWebhook(ctx context.Context, req *plansv1.SendTestWebhookRequest) (*plansv1.SendTestWebhookResponse, error) {
	id, err := parseID("id", req.GetId())
	if err != nil {
		return nil, err
	}
	var out *plansv1.WebhookDelivery
	err = w.s.tx(ctx, func(q *sqlc.Queries) error {
		if _, err := q.GetWebhookEndpoint(ctx, id); err != nil {
			return err
		}
		event := idgen.NewUUIDv7()
		if _, err := q.InsertWebhookEvent(ctx, sqlc.InsertWebhookEventParams{ID: event, Type: webhooks.Ping, Data: json.RawMessage(`{"message":"This is a test event from Quotify."}`), OccurredAt: w.s.now()}); err != nil {
			return err
		}
		did, err := q.QueueWebhookDeliveryFor(ctx, sqlc.QueueWebhookDeliveryForParams{ID: idgen.NewUUIDv7(), EventID: event, EndpointID: id})
		if err != nil {
			return err
		}
		row, err := q.GetWebhookDelivery(ctx, did)
		if err != nil {
			return err
		}
		out = deliveryMessage(row)
		return nil
	})
	if err != nil {
		return nil, convert.Err(err, "webhook endpoint")
	}
	return &plansv1.SendTestWebhookResponse{Delivery: out}, nil
}

// ListWebhookDeliveries implements plansv1.WebhookServiceServer: newest first.
func (w *Webhooks) ListWebhookDeliveries(ctx context.Context, req *plansv1.ListWebhookDeliveriesRequest) (*plansv1.ListWebhookDeliveriesResponse, error) {
	size := req.GetPageSize()
	switch {
	case size == 0:
		size = 50
	case size < 0 || size > 200:
		return nil, domain.Invalid("page_size", "1..200")
	}
	endpoint := uuid.Nil
	if v := req.GetEndpointId(); v != "" {
		id, err := parseID("endpoint_id", v)
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
	err := w.s.tx(ctx, func(q *sqlc.Queries) error {
		rows, err := q.ListWebhookDeliveries(ctx, sqlc.ListWebhookDeliveriesParams{EndpointID: endpoint, State: state, AfterTime: afterTime, AfterID: afterID, PageSize: size + 1})
		for _, r := range rows {
			resp.Deliveries = append(resp.Deliveries, deliveryMessage(sqlc.GetWebhookDeliveryRow(r)))
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

// RedeliverWebhook implements plansv1.WebhookServiceServer.
func (w *Webhooks) RedeliverWebhook(ctx context.Context, req *plansv1.RedeliverWebhookRequest) (*plansv1.RedeliverWebhookResponse, error) {
	id, err := parseID("delivery_id", req.GetDeliveryId())
	if err != nil {
		return nil, err
	}
	var out *plansv1.WebhookDelivery
	err = w.s.tx(ctx, func(q *sqlc.Queries) error {
		if _, err := q.RedeliverWebhook(ctx, id); err != nil {
			return err
		}
		row, err := q.GetWebhookDelivery(ctx, id)
		if err != nil {
			return err
		}
		out = deliveryMessage(row)
		return nil
	})
	if err != nil {
		return nil, convert.Err(err, "webhook delivery")
	}
	return &plansv1.RedeliverWebhookResponse{Delivery: out}, nil
}

// RecordTenantEvent implements plansv1.WebhookServiceServer.
func (w *Webhooks) RecordTenantEvent(ctx context.Context, req *plansv1.RecordTenantEventRequest) (*plansv1.RecordTenantEventResponse, error) {
	id, err := parseID("event_id", req.GetEventId())
	if err != nil {
		return nil, err
	}
	if !webhooks.Known(req.GetType()) {
		return nil, domain.Invalid("type", "unknown event type")
	}
	if !json.Valid([]byte(req.GetDataJson())) || !strings.HasPrefix(strings.TrimSpace(req.GetDataJson()), "{") {
		return nil, domain.Invalid("data_json", "must be a JSON object")
	}
	at := w.s.now()
	if req.GetOccurredTime() != nil {
		at = req.GetOccurredTime().AsTime()
	}
	var queued int64
	err = w.s.tx(ctx, func(q *sqlc.Queries) error {
		var err error
		queued, err = webhooks.Emit(ctx, q, id, req.GetType(), json.RawMessage(req.GetDataJson()), at)
		return err
	})
	if err != nil {
		return nil, convert.Err(err, "webhook event")
	}
	return &plansv1.RecordTenantEventResponse{Queued: int32(queued)}, nil //nolint:gosec // at most 20 endpoints
}

var eventJSON = protojson.MarshalOptions{UseProtoNames: true}

// emit records a subscription event in the caller's transaction: {"subscription": <as the API returns it>} plus extra fields.
func (s *Server) emit(ctx context.Context, q *sqlc.Queries, typ string, sub *plansv1.Subscription, extra map[string]any) error {
	ok, err := q.HasWebhookTargets(ctx, typ)
	if err != nil || !ok {
		return err
	}
	raw, err := eventJSON.Marshal(sub)
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
	_, err = webhooks.Emit(ctx, q, idgen.NewUUIDv7(), typ, b, s.now())
	return err
}
