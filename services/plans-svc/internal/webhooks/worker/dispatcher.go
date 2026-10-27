// Package worker delivers the queued webhook deliveries of every tenant, signed, to the tenants' endpoints.
package worker

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
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/0furkancolak/payrudder/pkg/brand"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/secretbox"
	"github.com/0furkancolak/payrudder/pkg/webhooksig"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/placement"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/webhooks/model"
)

const (
	lease     = 2 * time.Minute // a claimed delivery is not claimed again before this, so a crashed dispatcher's work is picked up again
	retention = 30 * 24 * time.Hour

	pollEvery = 2 * time.Second // how often the dispatcher looks for due deliveries
	batchSize = 50              // deliveries claimed per pass
	workers   = 8               // concurrent HTTP calls per pass
	// maxErrorText bounds the error a failed delivery keeps, and the snippet of the endpoint's answer in it.
	maxErrorText = 300
	maxSnippet   = 200
)

// Dispatcher delivers the due webhook deliveries of every tenant. Several may run: a delivery is claimed with SKIP LOCKED and a lease.
//
// The deliveries of the tenants in the pooled database are claimed in one statement, as ever. With cells on (ADR 0018) each tenant placed in a cell is visited too, in a
// transaction of its own in its schema: the claim, and later the target, the retry and the result of each of its deliveries, are statements of that tenant's schema.
type Dispatcher struct {
	Pool   *pgxpool.Pool // plans_system
	Places *placement.Places
	Box    *secretbox.Box
	Client *http.Client
	Log    *slog.Logger
	Now    func() time.Time

	lastPrune time.Time
}

func (d *Dispatcher) defaults() {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Log == nil {
		d.Log = slog.Default()
	}
}

// Run delivers until ctx is done.
func (d *Dispatcher) Run(ctx context.Context) error {
	d.defaults()
	t := time.NewTicker(pollEvery)
	defer t.Stop()
	for {
		n, err := d.Once(ctx)
		if err != nil && ctx.Err() == nil {
			d.Log.WarnContext(ctx, "webhook dispatcher pass failed", "error", err)
		}
		if n >= batchSize { // a full batch: there is probably more waiting
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
	cell                        *pgxx.PlacedTenant // where the tenant is when it is in a cell; nil: the pooled database
}

// queries runs fn on the statements of the place the delivery's tenant was claimed in.
func (d *Dispatcher) queries(ctx context.Context, c claimed, fn func(q *sqlc.Queries) error) error {
	if c.cell == nil {
		return fn(sqlc.New(d.Pool))
	}
	return d.Places.CellTx(ctx, *c.cell, func(tx pgx.Tx) error { return fn(sqlc.New(tx)) })
}

// Once claims and delivers one batch of due deliveries and returns how many it claimed.
func (d *Dispatcher) Once(ctx context.Context) (int, error) {
	d.defaults()
	now := d.Now()
	rows, err := sqlc.New(d.Pool).ClaimDueWebhookDeliveries(ctx, sqlc.ClaimDueWebhookDeliveriesParams{Now: now, Batch: int32(batchSize), LeaseSecs: lease.Seconds()}) //nolint:gosec // a small batch size
	if err != nil {
		return 0, err
	}
	batch := make([]claimed, len(rows))
	for i, r := range rows {
		batch[i] = claimed{tenant: r.TenantID, id: r.ID, event: r.EventID, endpoint: r.EndpointID, attempt: int(r.Attempts)}
	}
	err = d.Places.EachCell(ctx, func(ctx context.Context, t pgxx.PlacedTenant) error {
		got, err := d.claimCell(ctx, t, now)
		batch = append(batch, got...)
		return err
	})
	if err != nil {
		d.Log.WarnContext(ctx, "webhook dispatcher: a tenant in a cell could not be claimed", "error", err)
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)
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

// claimCell claims up to a batch of the due deliveries of one tenant placed in a cell, in a transaction of its own in its schema. A delivery of another tenant in that
// schema is a bug that must not be delivered under the wrong tenant: the transaction is rolled back, so nothing of it is leased.
func (d *Dispatcher) claimCell(ctx context.Context, t pgxx.PlacedTenant, now time.Time) ([]claimed, error) {
	var out []claimed
	err := d.Places.CellTx(ctx, t, func(tx pgx.Tx) error {
		rows, err := sqlc.New(tx).ClaimDueWebhookDeliveries(ctx, sqlc.ClaimDueWebhookDeliveriesParams{Now: now, Batch: int32(batchSize), LeaseSecs: lease.Seconds()}) //nolint:gosec // a small batch size
		if err != nil {
			return err
		}
		out = out[:0]
		for _, r := range rows {
			if r.TenantID != t.TenantID {
				return fmt.Errorf("webhook delivery %s of tenant %s is in the schema of tenant %s", r.ID, r.TenantID, t.TenantID)
			}
			out = append(out, claimed{tenant: r.TenantID, id: r.ID, event: r.EventID, endpoint: r.EndpointID, attempt: int(r.Attempts), cell: &t})
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("claim webhook deliveries: %w", err)
	}
	return out, nil
}

func (d *Dispatcher) deliver(ctx context.Context, c claimed) error {
	var t sqlc.GetWebhookDeliveryTargetRow
	err := d.queries(ctx, c, func(q *sqlc.Queries) (err error) {
		t, err = q.GetWebhookDeliveryTarget(ctx, sqlc.GetWebhookDeliveryTargetParams{TenantID: c.tenant, ID: c.id})
		return err
	})
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
	body, err := json.Marshal(model.Envelope{ID: c.event.String(), Type: t.Type, Created: t.OccurredAt.UTC().Format(time.RFC3339), TenantID: c.tenant.String(), Data: t.Data})
	if err != nil {
		return err
	}
	status, errText := d.post(ctx, t.Url, c, t.Type, body, secrets)
	if errText == "" {
		return d.finish(ctx, c, "delivered", status, "")
	}
	if c.attempt > len(model.Backoff) {
		return d.finish(ctx, c, "failed", status, errText)
	}
	wait := model.Backoff[c.attempt-1]
	return d.queries(ctx, c, func(q *sqlc.Queries) error {
		return q.RescheduleWebhookDelivery(ctx, sqlc.RescheduleWebhookDeliveryParams{
			TenantID: c.tenant, ID: c.id, NextAttemptAt: d.Now().Add(wait), LastStatus: int32(status), LastError: errText, //nolint:gosec // an HTTP status
		})
	})
}

func (d *Dispatcher) secrets(endpoint uuid.UUID, current, previous []byte, until *time.Time) ([]string, error) {
	cur, err := d.Box.Open(current, model.SecretAAD(endpoint))
	if err != nil {
		return nil, err
	}
	out := []string{string(cur)}
	if previous != nil && until != nil && until.After(d.Now()) {
		if prev, err := d.Box.Open(previous, model.SecretAAD(endpoint)); err == nil {
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
		return 0, truncate("no answer: "+err.Error(), maxErrorText)
	}
	defer func() { _ = resp.Body.Close() }()
	snippet, _ := io.ReadAll(io.LimitReader(resp.Body, maxSnippet))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return resp.StatusCode, ""
	}
	return resp.StatusCode, truncate(fmt.Sprintf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet))), maxErrorText)
}

func (d *Dispatcher) finish(ctx context.Context, c claimed, state string, status int, errText string) error {
	return d.queries(ctx, c, func(q *sqlc.Queries) error {
		return q.FinishWebhookDelivery(ctx, sqlc.FinishWebhookDeliveryParams{
			TenantID: c.tenant, ID: c.id, State: state, LastStatus: int32(status), LastError: errText, DeliveredAt: d.Now(), //nolint:gosec // an HTTP status
		})
	})
}

func (d *Dispatcher) prune(ctx context.Context, now time.Time) error {
	before := now.Add(-retention)
	if err := sqlc.New(d.Pool).PruneWebhookEvents(ctx, before); err != nil {
		return err
	}
	return d.Places.EachCell(ctx, func(ctx context.Context, t pgxx.PlacedTenant) error {
		return d.Places.CellTx(ctx, t, func(tx pgx.Tx) error { return sqlc.New(tx).PruneWebhookEvents(ctx, before) })
	})
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
