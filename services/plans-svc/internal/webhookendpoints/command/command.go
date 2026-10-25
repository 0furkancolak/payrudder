// Package command changes webhook endpoints and queues deliveries to them.
package command

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/errkey"
	"github.com/0furkancolak/quotify/pkg/idgen"
	"github.com/0furkancolak/quotify/pkg/safehttp"
	"github.com/0furkancolak/quotify/pkg/secretbox"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/convert"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/domain"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/store"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/store/sqlc"
	endpointsqry "github.com/0furkancolak/quotify/services/plans-svc/internal/webhookendpoints/query"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/webhooks"
)

const (
	maxWebhookEndpoints = 20
	secretOverlap       = 24 * time.Hour
)

// Commands changes webhook endpoints.
type Commands struct {
	db           *store.DB
	now          func() time.Time
	box          *secretbox.Box
	allowPrivate bool
}

// New returns the webhook commands. box seals the signing secrets (nil: endpoints cannot be created). allowPrivate lets endpoints be http:// and
// private addresses (development and tests only).
func New(db *store.DB, now func() time.Time, box *secretbox.Box, allowPrivate bool) *Commands {
	return &Commands{db: db, now: now, box: box, allowPrivate: allowPrivate}
}

func (c *Commands) validate(url, description string, types []string) ([]string, error) {
	var v domain.Violations
	if err := safehttp.ValidateURL(url, c.allowPrivate); err != nil {
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
			v.Add("event_types["+strconv.Itoa(i)+"]", "unknown event type "+t)
		case !seen[t]:
			out = append(out, t)
		}
		seen[t] = true
	}
	return out, v.Err()
}

func newSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "whsec_" + base64.RawURLEncoding.EncodeToString(b), nil
}

func (c *Commands) requireBox() error {
	if c.box == nil {
		return errkey.New(codes.FailedPrecondition, "plans.webhook.not_configured", "webhooks are not configured on this deployment (QUOTIFY_SECRETS_KEY)")
	}
	return nil
}

// CreateEndpoint adds an endpoint and returns it with its signing secret, which is shown this once.
func (c *Commands) CreateEndpoint(ctx context.Context, req *plansv1.CreateWebhookEndpointRequest) (*plansv1.WebhookEndpoint, error) {
	if err := c.requireBox(); err != nil {
		return nil, err
	}
	types, err := c.validate(req.GetUrl(), req.GetDescription(), req.GetEventTypes())
	if err != nil {
		return nil, err
	}
	secret, err := newSecret()
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	id := idgen.NewUUIDv7()
	sealed, err := c.box.Seal([]byte(secret), webhooks.SecretAAD(id))
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	var out *plansv1.WebhookEndpoint
	err = c.db.Tx(ctx, func(q *sqlc.Queries) error {
		n, err := q.CountWebhookEndpoints(ctx)
		if err != nil {
			return err
		}
		if n >= maxWebhookEndpoints {
			return errkey.Newf(codes.FailedPrecondition, "plans.webhook.too_many_endpoints", "at most %d webhook endpoints; delete one first", maxWebhookEndpoints)
		}
		r, err := q.InsertWebhookEndpoint(ctx, sqlc.InsertWebhookEndpointParams{ID: id, Url: req.GetUrl(), Description: req.GetDescription(), EventTypes: types, SecretEnc: sealed})
		if err != nil {
			return err
		}
		out = endpointsqry.EndpointMessage(sqlc.ListWebhookEndpointsRow(r))
		out.Secret = secret
		return nil
	})
	if err != nil {
		return nil, convert.Err(err, "webhook endpoint")
	}
	return out, nil
}

// UpdateEndpoint replaces an endpoint's url, description, event types and active flag.
func (c *Commands) UpdateEndpoint(ctx context.Context, req *plansv1.UpdateWebhookEndpointRequest) (*plansv1.WebhookEndpoint, error) {
	id, err := endpointsqry.ParseID("id", req.GetId())
	if err != nil {
		return nil, err
	}
	types, err := c.validate(req.GetUrl(), req.GetDescription(), req.GetEventTypes())
	if err != nil {
		return nil, err
	}
	var out *plansv1.WebhookEndpoint
	err = c.db.Tx(ctx, func(q *sqlc.Queries) error {
		r, err := q.UpdateWebhookEndpoint(ctx, sqlc.UpdateWebhookEndpointParams{ID: id, Url: req.GetUrl(), Description: req.GetDescription(), EventTypes: types, Active: req.GetActive()})
		if err != nil {
			return err
		}
		out = endpointsqry.EndpointMessage(sqlc.ListWebhookEndpointsRow(r))
		return nil
	})
	if err != nil {
		return nil, convert.Err(err, "webhook endpoint")
	}
	return out, nil
}

// DeleteEndpoint removes an endpoint.
func (c *Commands) DeleteEndpoint(ctx context.Context, rawID string) error {
	id, err := endpointsqry.ParseID("id", rawID)
	if err != nil {
		return err
	}
	err = c.db.Tx(ctx, func(q *sqlc.Queries) error {
		n, err := q.DeleteWebhookEndpoint(ctx, id)
		if err == nil && n == 0 {
			return pgx.ErrNoRows
		}
		return err
	})
	if err != nil {
		return convert.Err(err, "webhook endpoint")
	}
	return nil
}

// RotateSecret gives an endpoint a new signing secret; the old one keeps working for a day.
func (c *Commands) RotateSecret(ctx context.Context, rawID string) (*plansv1.WebhookEndpoint, error) {
	if err := c.requireBox(); err != nil {
		return nil, err
	}
	id, err := endpointsqry.ParseID("id", rawID)
	if err != nil {
		return nil, err
	}
	secret, err := newSecret()
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	sealed, err := c.box.Seal([]byte(secret), webhooks.SecretAAD(id))
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	var out *plansv1.WebhookEndpoint
	err = c.db.Tx(ctx, func(q *sqlc.Queries) error {
		until := c.now().Add(secretOverlap)
		r, err := q.RotateWebhookSecret(ctx, sqlc.RotateWebhookSecretParams{ID: id, SecretEnc: sealed, Until: &until})
		if err != nil {
			return err
		}
		out = endpointsqry.EndpointMessage(sqlc.ListWebhookEndpointsRow(r))
		out.Secret = secret
		return nil
	})
	if err != nil {
		return nil, convert.Err(err, "webhook endpoint")
	}
	return out, nil
}

// SendTest queues a ping event to one endpoint.
func (c *Commands) SendTest(ctx context.Context, rawID string) (*plansv1.WebhookDelivery, error) {
	id, err := endpointsqry.ParseID("id", rawID)
	if err != nil {
		return nil, err
	}
	var out *plansv1.WebhookDelivery
	err = c.db.Tx(ctx, func(q *sqlc.Queries) error {
		if _, err := q.GetWebhookEndpoint(ctx, id); err != nil {
			return err
		}
		event := idgen.NewUUIDv7()
		if _, err := q.InsertWebhookEvent(ctx, sqlc.InsertWebhookEventParams{ID: event, Type: webhooks.Ping, Data: json.RawMessage(`{"message":"This is a test event from Payrudder."}`), OccurredAt: c.now()}); err != nil {
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
		out = endpointsqry.DeliveryMessage(row)
		return nil
	})
	if err != nil {
		return nil, convert.Err(err, "webhook endpoint")
	}
	return out, nil
}

// Redeliver puts a delivery back in the queue.
func (c *Commands) Redeliver(ctx context.Context, rawID string) (*plansv1.WebhookDelivery, error) {
	id, err := endpointsqry.ParseID("delivery_id", rawID)
	if err != nil {
		return nil, err
	}
	var out *plansv1.WebhookDelivery
	err = c.db.Tx(ctx, func(q *sqlc.Queries) error {
		if _, err := q.RedeliverWebhook(ctx, id); err != nil {
			return err
		}
		row, err := q.GetWebhookDelivery(ctx, id)
		if err != nil {
			return err
		}
		out = endpointsqry.DeliveryMessage(row)
		return nil
	})
	if err != nil {
		return nil, convert.Err(err, "webhook delivery")
	}
	return out, nil
}

// RecordEvent records an event another service produced and queues it to the endpoints that want its type; it returns how many deliveries.
func (c *Commands) RecordEvent(ctx context.Context, req *plansv1.RecordTenantEventRequest) (int32, error) {
	id, err := endpointsqry.ParseID("event_id", req.GetEventId())
	if err != nil {
		return 0, err
	}
	if !webhooks.Known(req.GetType()) {
		return 0, domain.Invalid("type", "unknown event type")
	}
	if !json.Valid([]byte(req.GetDataJson())) || !strings.HasPrefix(strings.TrimSpace(req.GetDataJson()), "{") {
		return 0, domain.Invalid("data_json", "must be a JSON object")
	}
	at := c.now()
	if req.GetOccurredTime() != nil {
		at = req.GetOccurredTime().AsTime()
	}
	var queued int64
	err = c.db.Tx(ctx, func(q *sqlc.Queries) error {
		var err error
		queued, err = webhooks.Emit(ctx, q, id, req.GetType(), json.RawMessage(req.GetDataJson()), at)
		return err
	})
	if err != nil {
		return 0, convert.Err(err, "webhook event")
	}
	return int32(queued), nil //nolint:gosec // at most 20 endpoints
}
