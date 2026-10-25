package webhooks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	neturl "net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/0furkancolak/payrudder/pkg/brand"
	"github.com/0furkancolak/payrudder/pkg/secretbox"
	"github.com/0furkancolak/payrudder/pkg/webhooksig"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// Backoff is the wait before attempt 2, 3, ...; a delivery that fails after the last one is failed (7 attempts in about 32 hours).
var Backoff = []time.Duration{time.Minute, 5 * time.Minute, 30 * time.Minute, 2 * time.Hour, 6 * time.Hour, 24 * time.Hour}

const (
	lease     = 2 * time.Minute // a claimed delivery is not claimed again before this, so a crashed dispatcher's work is picked up again
	retention = 30 * 24 * time.Hour
)

// SecretAAD is the associated data an endpoint's secret is sealed with.
func SecretAAD(endpoint uuid.UUID) []byte { return []byte("webhook:" + endpoint.String()) }

// Dispatcher delivers the due webhook deliveries of every tenant. Several may run: a delivery is claimed with SKIP LOCKED and a lease.
type Dispatcher struct {
	Pool    *pgxpool.Pool // plans_system
	Box     *secretbox.Box
	Client  *http.Client
	Log     *slog.Logger
	Now     func() time.Time
	Every   time.Duration // poll interval (default 2 s)
	Batch   int           // deliveries claimed per pass (default 50)
	Workers int           // concurrent HTTP calls per pass (default 8)

	lastPrune time.Time
}

func (d *Dispatcher) defaults() {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Every <= 0 {
		d.Every = 2 * time.Second
	}
	if d.Batch <= 0 {
		d.Batch = 50
	}
	if d.Workers <= 0 {
		d.Workers = 8
	}
	if d.Log == nil {
		d.Log = slog.Default()
	}
}

// Run delivers until ctx is done.
func (d *Dispatcher) Run(ctx context.Context) error {
	d.defaults()
	t := time.NewTicker(d.Every)
	defer t.Stop()
	for {
		n, err := d.Once(ctx)
		if err != nil && ctx.Err() == nil {
			d.Log.WarnContext(ctx, "webhook dispatcher pass failed", "error", err)
		}
		if n >= d.Batch { // a full batch: there is probably more waiting
			continue
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

type claimed struct {
	tenant, id, event, endpoint uuid.UUID
	attempt                     int
}

// Once claims and delivers one batch of due deliveries and returns how many it claimed.
func (d *Dispatcher) Once(ctx context.Context) (int, error) {
	d.defaults()
	now := d.Now()
	rows, err := sqlc.New(d.Pool).ClaimDueWebhookDeliveries(ctx, sqlc.ClaimDueWebhookDeliveriesParams{Now: now, Batch: int32(d.Batch), LeaseSecs: lease.Seconds()}) //nolint:gosec // a small batch size
	if err != nil {
		return 0, err
	}
	batch := make([]claimed, len(rows))
	for i, r := range rows {
		batch[i] = claimed{tenant: r.TenantID, id: r.ID, event: r.EventID, endpoint: r.EndpointID, attempt: int(r.Attempts)}
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, d.Workers)
	for _, c := range batch {
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer func() { <-sem; wg.Done() }()
			if err := d.deliver(ctx, c); err != nil && ctx.Err() == nil {
				d.Log.WarnContext(ctx, "webhook delivery not recorded", "delivery", c.id, "error", err)
			}
		}()
	}
	wg.Wait()
	if now.Sub(d.lastPrune) > time.Hour {
		d.lastPrune = now
		if err := d.prune(ctx, now); err != nil {
			d.Log.WarnContext(ctx, "webhook retention failed", "error", err)
		}
	}
	return len(batch), nil
}

// Envelope is the body of a delivery.
type Envelope struct {
	ID       string          `json:"id"`
	Type     string          `json:"type"`
	Created  string          `json:"created"`
	TenantID string          `json:"tenant_id"`
	Data     json.RawMessage `json:"data"`
}

func (d *Dispatcher) deliver(ctx context.Context, c claimed) error {
	t, err := sqlc.New(d.Pool).GetWebhookDeliveryTarget(ctx, sqlc.GetWebhookDeliveryTargetParams{TenantID: c.tenant, ID: c.id})
	if err != nil {
		return err
	}
	if !t.Active {
		return d.finish(ctx, c, "failed", 0, "the endpoint is not active")
	}
	secrets, err := d.secrets(c.endpoint, t.SecretEnc, t.PrevSecretEnc, t.PrevSecretUntil)
	if err != nil {
		return d.finish(ctx, c, "failed", 0, "the signing secret cannot be read: "+err.Error())
	}
	body, err := json.Marshal(Envelope{ID: c.event.String(), Type: t.Type, Created: t.OccurredAt.UTC().Format(time.RFC3339), TenantID: c.tenant.String(), Data: t.Data})
	if err != nil {
		return err
	}
	status, errText := d.post(ctx, t.Url, c, t.Type, body, secrets)
	if errText == "" {
		return d.finish(ctx, c, "delivered", status, "")
	}
	if c.attempt > len(Backoff) {
		return d.finish(ctx, c, "failed", status, errText)
	}
	wait := Backoff[c.attempt-1]
	return sqlc.New(d.Pool).RescheduleWebhookDelivery(ctx, sqlc.RescheduleWebhookDeliveryParams{
		TenantID: c.tenant, ID: c.id, NextAttemptAt: d.Now().Add(wait), LastStatus: int32(status), LastError: errText, //nolint:gosec // an HTTP status
	})
}

func (d *Dispatcher) secrets(endpoint uuid.UUID, current, previous []byte, until *time.Time) ([]string, error) {
	cur, err := d.Box.Open(current, SecretAAD(endpoint))
	if err != nil {
		return nil, err
	}
	out := []string{string(cur)}
	if previous != nil && until != nil && until.After(d.Now()) {
		if prev, err := d.Box.Open(previous, SecretAAD(endpoint)); err == nil {
			out = append(out, string(prev))
		}
	}
	return out, nil
}

// post makes one call and returns the HTTP status (0 if none) and an error text when the delivery did not succeed.
func (d *Dispatcher) post(ctx context.Context, url string, c claimed, typ string, body []byte, secrets []string) (int, string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return 0, "the URL cannot be requested: " + err.Error()
	}
	now := d.Now()
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", brand.UserAgent("Webhooks", 1))
	req.Header.Set(brand.Header("Event-Id"), c.event.String())
	req.Header.Set(brand.Header("Event-Type"), typ)
	req.Header.Set(brand.Header("Delivery-Attempt"), fmt.Sprint(c.attempt))
	req.Header.Set(webhooksig.Header(), webhooksig.Sign(now, body, secrets...))
	resp, err := d.Client.Do(req)
	if err != nil {
		var ue *neturl.Error // its text repeats the URL, which the tenant knows
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return 0, truncate("no answer: "+err.Error(), 300)
	}
	defer func() { _ = resp.Body.Close() }()
	snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return resp.StatusCode, ""
	}
	return resp.StatusCode, truncate(fmt.Sprintf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet))), 300)
}

func (d *Dispatcher) finish(ctx context.Context, c claimed, state string, status int, errText string) error {
	return sqlc.New(d.Pool).FinishWebhookDelivery(ctx, sqlc.FinishWebhookDeliveryParams{
		TenantID: c.tenant, ID: c.id, State: state, LastStatus: int32(status), LastError: errText, DeliveredAt: d.Now(), //nolint:gosec // an HTTP status
	})
}

func (d *Dispatcher) prune(ctx context.Context, now time.Time) error {
	return sqlc.New(d.Pool).PruneWebhookEvents(ctx, now.Add(-retention))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
