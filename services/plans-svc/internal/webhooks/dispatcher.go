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

	"github.com/0furkancolak/quotify/pkg/brand"
	"github.com/0furkancolak/quotify/pkg/secretbox"
	"github.com/0furkancolak/quotify/pkg/webhooksig"
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
	rows, err := d.Pool.Query(ctx, `
		WITH due AS (
			SELECT tenant_id, id FROM webhook_deliveries WHERE state = 'pending' AND next_attempt_at <= $1
			ORDER BY next_attempt_at LIMIT $2 FOR UPDATE SKIP LOCKED)
		UPDATE webhook_deliveries d SET attempts = d.attempts + 1, next_attempt_at = $1::timestamptz + make_interval(secs => $3), last_attempt_at = $1
		FROM due WHERE d.tenant_id = due.tenant_id AND d.id = due.id
		RETURNING d.tenant_id, d.id, d.event_id, d.endpoint_id, d.attempts`, now, d.Batch, lease.Seconds())
	if err != nil {
		return 0, err
	}
	var batch []claimed
	for rows.Next() {
		var c claimed
		if err := rows.Scan(&c.tenant, &c.id, &c.event, &c.endpoint, &c.attempt); err != nil {
			rows.Close()
			return 0, err
		}
		batch = append(batch, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
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
	var (
		url, typ          string
		active            bool
		secretEnc, prevEn []byte
		prevUntil         *time.Time
		data              []byte
		occurred          time.Time
	)
	err := d.Pool.QueryRow(ctx, `
		SELECT e.url, e.active, e.secret_enc, e.prev_secret_enc, e.prev_secret_until, ev.type, ev.data, ev.occurred_at
		FROM webhook_deliveries d
		JOIN webhook_endpoints e ON e.tenant_id = d.tenant_id AND e.id = d.endpoint_id
		JOIN webhook_events ev ON ev.tenant_id = d.tenant_id AND ev.id = d.event_id
		WHERE d.tenant_id = $1 AND d.id = $2`, c.tenant, c.id).Scan(&url, &active, &secretEnc, &prevEn, &prevUntil, &typ, &data, &occurred)
	if err != nil {
		return err
	}
	if !active {
		return d.finish(ctx, c, "failed", 0, "the endpoint is not active")
	}
	secrets, err := d.secrets(c.endpoint, secretEnc, prevEn, prevUntil)
	if err != nil {
		return d.finish(ctx, c, "failed", 0, "the signing secret cannot be read: "+err.Error())
	}
	body, err := json.Marshal(Envelope{ID: c.event.String(), Type: typ, Created: occurred.UTC().Format(time.RFC3339), TenantID: c.tenant.String(), Data: data})
	if err != nil {
		return err
	}
	status, errText := d.post(ctx, url, c, typ, body, secrets)
	if errText == "" {
		return d.finish(ctx, c, "delivered", status, "")
	}
	if c.attempt > len(Backoff) {
		return d.finish(ctx, c, "failed", status, errText)
	}
	wait := Backoff[c.attempt-1]
	_, err = d.Pool.Exec(ctx, `UPDATE webhook_deliveries SET next_attempt_at = $3, last_status = NULLIF($4, 0), last_error = $5 WHERE tenant_id = $1 AND id = $2`,
		c.tenant, c.id, d.Now().Add(wait), status, errText)
	return err
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
	_, err := d.Pool.Exec(ctx, `UPDATE webhook_deliveries SET state = $3, last_status = NULLIF($4, 0), last_error = NULLIF($5, ''),
		delivered_at = CASE WHEN $3 = 'delivered' THEN $6::timestamptz ELSE NULL END WHERE tenant_id = $1 AND id = $2`, c.tenant, c.id, state, status, errText, d.Now())
	return err
}

func (d *Dispatcher) prune(ctx context.Context, now time.Time) error {
	if _, err := d.Pool.Exec(ctx, `DELETE FROM webhook_events WHERE created_at < $1`, now.Add(-retention)); err != nil {
		return err
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
