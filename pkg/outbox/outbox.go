// Package outbox relays rows of a service's transactional outbox table to a sink (Kafka today, AMQP in Phase 05).
//
// Rows are claimed with FOR UPDATE SKIP LOCKED, published, then deleted in the same transaction. A crash after the
// publish and before the commit leaves the rows in place, so they are published again: at-least-once publish.
// Consumers dedup on the event id (docs/07-correctness.md §3).
//
// The table must have the shape of docs/05-data-model.md#ingest_db: id, tenant_id, topic, partition_key, payload,
// headers (jsonb) and ingested_at.
package outbox

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// The relay's statements. The table is fixed: every service names its outbox "outbox".
const (
	claimRows = `SELECT id, tenant_id::text, topic, partition_key, payload, headers, ingested_at
		FROM outbox ORDER BY id LIMIT $1 FOR UPDATE SKIP LOCKED`
	deleteRows = `DELETE FROM outbox WHERE id = ANY($1)`
	oldestRow  = `SELECT min(ingested_at) FROM outbox`
	queueDepth = `SELECT COALESCE((SELECT max(id) FROM outbox) - (SELECT min(id) FROM outbox) + 1, 0)`
)

// NotifyChannel is the LISTEN/NOTIFY channel writers signal after committing outbox rows.
const NotifyChannel = "outbox_new"

// Row is one outbox row.
type Row struct {
	ID           int64
	TenantID     string
	Topic        string
	PartitionKey string
	Payload      []byte
	Headers      map[string]string
	IngestedAt   time.Time
}

// Sink publishes rows. Publish returns nil only when every row is durably acknowledged by the destination.
type Sink interface {
	Publish(ctx context.Context, rows []Row) error
}

// Config tunes a Relay. Zero values take the defaults noted.
type Config struct {
	Workers      int           // default 2
	BatchSize    int           // default 1000
	PollInterval time.Duration // default 50ms: fallback when no NOTIFY arrives
	ErrorBackoff time.Duration // default 1s
	// AfterPublish runs after the sink acked and before the delete commits. Returning an error aborts the batch
	// without deleting, which is what a crash at that point looks like. Used by chaos tests only.
	AfterPublish func() error
}

func (c *Config) defaults() {
	if c.Workers <= 0 {
		c.Workers = 2
	}
	if c.BatchSize <= 0 {
		c.BatchSize = 1000
	}
	if c.PollInterval <= 0 {
		c.PollInterval = 50 * time.Millisecond
	}
	if c.ErrorBackoff <= 0 {
		c.ErrorBackoff = time.Second
	}
}

// Relay moves outbox rows to a Sink. Run it with the service's system pool (BYPASSRLS, SELECT and DELETE on the table).
type Relay struct {
	pool *pgxpool.Pool
	sink Sink
	cfg  Config
	log  *slog.Logger

	published metric.Int64Counter
	failures  metric.Int64Counter
	batchSecs metric.Float64Histogram
	pubSecs   metric.Float64Histogram
	lag       metric.Float64Histogram
}

// NewRelay registers metrics.
func NewRelay(pool *pgxpool.Pool, sink Sink, cfg Config, log *slog.Logger) (*Relay, error) {
	cfg.defaults()
	if log == nil {
		log = slog.Default()
	}
	m := otel.Meter("github.com/0furkancolak/payrudder/pkg/outbox")
	r := &Relay{pool: pool, sink: sink, cfg: cfg, log: log}
	var err error
	if r.published, err = m.Int64Counter("outbox_published", metric.WithDescription("Rows published to the sink")); err != nil {
		return nil, err
	}
	if r.failures, err = m.Int64Counter("outbox_batch_failures", metric.WithDescription("Relay batches that failed and will be retried")); err != nil {
		return nil, err
	}
	if r.batchSecs, err = m.Float64Histogram("outbox_batch_seconds", metric.WithUnit("s"), metric.WithDescription("Claim, publish and delete time of one batch")); err != nil {
		return nil, err
	}
	if r.pubSecs, err = m.Float64Histogram("relay_publish_duration", metric.WithUnit("s"), metric.WithDescription("Time the sink took to publish a batch, by topic")); err != nil {
		return nil, err
	}
	if r.lag, err = m.Float64Histogram("outbox_publish_lag_seconds", metric.WithUnit("s"), metric.WithDescription("Time from ingested_at to publish")); err != nil {
		return nil, err
	}
	return r, nil
}

// Run blocks until ctx is done. It starts the NOTIFY listener and the workers.
func (r *Relay) Run(ctx context.Context) error {
	wake := make(chan struct{}, 1)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		r.listen(ctx, wake)
	}()
	for i := 0; i < r.cfg.Workers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			r.work(ctx, id, wake)
		}(i)
	}
	<-ctx.Done()
	wg.Wait()
	return nil
}

// listen turns NOTIFY into a non-blocking wakeup. Losing the connection is harmless: workers still poll.
func (r *Relay) listen(ctx context.Context, wake chan<- struct{}) {
	for ctx.Err() == nil {
		if err := r.listenOnce(ctx, wake); err != nil && ctx.Err() == nil {
			r.log.WarnContext(ctx, "outbox: listener lost, polling only until it reconnects", "error", err)
			sleep(ctx, r.cfg.ErrorBackoff)
		}
	}
}

func (r *Relay) listenOnce(ctx context.Context, wake chan<- struct{}) error {
	conn, err := r.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "LISTEN "+NotifyChannel); err != nil {
		return err
	}
	for {
		if _, err := conn.Conn().WaitForNotification(ctx); err != nil {
			// The connection is in an unknown state after an error: drop it instead of returning it to the pool.
			_ = conn.Conn().Close(context.WithoutCancel(ctx))
			return err
		}
		select {
		case wake <- struct{}{}:
		default:
		}
	}
}

func (r *Relay) work(ctx context.Context, id int, wake chan struct{}) {
	log := r.log.With("relay_worker", id)
	timer := time.NewTimer(0)
	defer timer.Stop()
	for ctx.Err() == nil {
		n, err := r.RelayOnce(ctx)
		switch {
		case err != nil && ctx.Err() == nil:
			r.failures.Add(ctx, 1)
			log.WarnContext(ctx, "outbox: batch failed, will retry", "error", err)
			sleep(ctx, r.cfg.ErrorBackoff)
		case n >= r.cfg.BatchSize:
			// More is probably waiting: go again immediately.
		default:
			resetTimer(timer, r.cfg.PollInterval)
			select {
			case <-ctx.Done():
			case <-wake:
				// A wakeup is consumed by one worker. The others stay on their poll interval, which is fine:
				// SKIP LOCKED means the woken worker takes the batch and the rest find nothing.
			case <-timer.C:
			}
		}
	}
}

// RelayOnce claims, publishes and deletes at most one batch and returns the number of rows published.
func (r *Relay) RelayOnce(ctx context.Context) (int, error) {
	start := time.Now()
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("outbox: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	rows, err := r.claim(ctx, tx)
	if err != nil || len(rows) == 0 {
		return 0, err
	}
	pubStart := time.Now()
	if err := r.sink.Publish(ctx, rows); err != nil {
		return 0, fmt.Errorf("outbox: publish %d rows: %w", len(rows), err)
	}
	topics := map[string]bool{}
	for _, row := range rows {
		topics[row.Topic] = true
	}
	for t := range topics {
		r.pubSecs.Record(ctx, time.Since(pubStart).Seconds(), metric.WithAttributes(attribute.String("topic", t)))
	}
	if r.cfg.AfterPublish != nil {
		if err := r.cfg.AfterPublish(); err != nil {
			return 0, fmt.Errorf("outbox: after publish: %w", err)
		}
	}
	ids := make([]int64, len(rows))
	for i, row := range rows {
		ids[i] = row.ID
	}
	if _, err := tx.Exec(ctx, deleteRows, ids); err != nil {
		return 0, fmt.Errorf("outbox: delete: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("outbox: commit: %w", err)
	}

	now := time.Now()
	r.published.Add(ctx, int64(len(rows)))
	r.batchSecs.Record(ctx, now.Sub(start).Seconds())
	r.lag.Record(ctx, now.Sub(rows[0].IngestedAt).Seconds(), metric.WithAttributes(attribute.String("table", "outbox")))
	return len(rows), nil
}

func (r *Relay) claim(ctx context.Context, tx pgx.Tx) ([]Row, error) {
	rs, err := tx.Query(ctx, claimRows, r.cfg.BatchSize)
	if err != nil {
		return nil, fmt.Errorf("outbox: claim: %w", err)
	}
	defer rs.Close()
	var out []Row
	for rs.Next() {
		var row Row
		var hdr []byte
		if err := rs.Scan(&row.ID, &row.TenantID, &row.Topic, &row.PartitionKey, &row.Payload, &hdr, &row.IngestedAt); err != nil {
			return nil, fmt.Errorf("outbox: scan: %w", err)
		}
		if err := json.Unmarshal(hdr, &row.Headers); err != nil {
			return nil, fmt.Errorf("outbox: row %d headers: %w", row.ID, err)
		}
		out = append(out, row)
	}
	return out, rs.Err()
}

// Watermark returns the oldest unpublished ingested_at, or ok=false when the table is empty.
func Watermark(ctx context.Context, db interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}) (oldest time.Time, ok bool, err error) {
	var t *time.Time
	if err := db.QueryRow(ctx, oldestRow).Scan(&t); err != nil {
		return time.Time{}, false, err
	}
	if t == nil {
		return time.Time{}, false, nil
	}
	return *t, true, nil
}

// Depth is a cheap upper-bound estimate of the number of rows: max(id) - min(id) + 1, two index probes. Gaps left by
// concurrent relays make it overestimate slightly, which only makes back-pressure kick in a little early.
func Depth(ctx context.Context, db interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}) (int64, error) {
	var d int64
	err := db.QueryRow(ctx, queueDepth).Scan(&d)
	return d, err
}

func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

func resetTimer(t *time.Timer, d time.Duration) {
	if !t.Stop() {
		select {
		case <-t.C:
		default:
		}
	}
	t.Reset(d)
}
