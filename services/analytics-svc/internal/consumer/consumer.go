// Package consumer feeds ClickHouse from Kafka: usage events, limit alerts and invoice facts. It batches (10k rows or
// 1 s), inserts, and only then commits the offsets, so a crash re-inserts a batch rather than losing it. The tables absorb
// the duplicates (ReplacingMergeTree), which is why this consumer needs no inbox (docs/03-services/analytics-svc.md).
package consumer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/google/uuid"
	"github.com/twmb/franz-go/pkg/kgo"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"google.golang.org/protobuf/proto"

	eventsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/events/v1"
	"github.com/0furkancolak/payrudder/pkg/kafkax"
	"github.com/0furkancolak/payrudder/pkg/usage"
)

// Topics and their dead-letter topics.
const (
	UsageTopic    = "usage.events.v1"
	LimitTopic    = "usage.limit-exceeded.v1"
	InvoiceTopic  = "billing.invoices.v1"
	UsageDLQ      = "usage.events.dlq.v1"
	LimitDLQ      = "usage.limit-exceeded.dlq.v1"
	InvoiceDLQ    = "billing.invoices.dlq.v1"
	redriveHeader = "x-redrive-target"
)

var dlqOf = map[string]string{UsageTopic: UsageDLQ, LimitTopic: LimitDLQ, InvoiceTopic: InvoiceDLQ}

// Config configures a Runner.
type Config struct {
	Brokers []string
	Group   string
	Conn    driver.Conn
	// Producer sends dead letters.
	Producer *kgo.Client
	// BatchRows and BatchWait bound a batch (defaults 10 000 rows, 1 s).
	BatchRows int
	BatchWait time.Duration
	// Backoff between insert retries while ClickHouse is unavailable (default 1 s, doubling to 10 s).
	Backoff time.Duration
	Log     *slog.Logger
}

// Runner is the consumer.
type Runner struct {
	cfg Config
	log *slog.Logger

	rows    metric.Int64Counter
	dead    metric.Int64Counter
	retries metric.Int64Counter
}

// New applies defaults and registers metrics.
func New(cfg Config) (*Runner, error) {
	if cfg.BatchRows <= 0 {
		cfg.BatchRows = 10000
	}
	if cfg.BatchWait <= 0 {
		cfg.BatchWait = time.Second
	}
	if cfg.Backoff <= 0 {
		cfg.Backoff = time.Second
	}
	if cfg.Group == "" {
		cfg.Group = "analytics"
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	m := otel.Meter("github.com/0furkancolak/payrudder/services/analytics-svc")
	r := &Runner{cfg: cfg, log: cfg.Log}
	var err error
	if r.rows, err = m.Int64Counter("analytics_rows_inserted", metric.WithDescription("Rows inserted into ClickHouse, by table")); err != nil {
		return nil, err
	}
	if r.dead, err = m.Int64Counter("analytics_dead_letters", metric.WithDescription("Records sent to a DLQ, by topic and reason")); err != nil {
		return nil, err
	}
	if r.retries, err = m.Int64Counter("analytics_insert_retries", metric.WithDescription("Failed ClickHouse inserts that were retried")); err != nil {
		return nil, err
	}
	return r, nil
}

type usageRow struct {
	Tenant, Customer      uuid.UUID
	EventID, ExternalID   string
	Meter, Period         string
	Units                 int64
	Scale                 uint8
	EventTime, IngestedAt time.Time
	Properties            map[string]string
}

type limitRow struct {
	Tenant, Customer uuid.UUID
	EventID          string
	Meter, Period    string
	Threshold        int32
	Limit, Used      int64
	Scale            uint8
	DetectedAt       time.Time
}

type invoiceRow struct {
	Tenant, Invoice, Customer uuid.UUID
	Version                   int64
	Period, Kind              string
	TotalMinor                int64
	Currency, Number          string
	OccurredAt                time.Time
}

type batch struct {
	usage    []usageRow
	limits   []limitRow
	invoices []invoiceRow
	records  []*kgo.Record // everything to commit once the batch is inserted
	first    time.Time
}

func (b *batch) size() int { return len(b.usage) + len(b.limits) + len(b.invoices) }

// decodeError marks a record that can never be processed.
type decodeError struct {
	reason string
	err    error
}

func (e *decodeError) Error() string { return e.reason + ": " + e.err.Error() }

// decodeUsage applies the one usage-event rule of pkg/usage, the same as the aggregator and limits-svc, so reporting never
// shows an event billing refused.
func decodeUsage(rec *kgo.Record) (usageRow, error) {
	ev, err := usage.Decode(rec)
	if err != nil {
		var ue *usage.DecodeError
		if errors.As(err, &ue) {
			return usageRow{}, &decodeError{ue.Reason, ue.Err}
		}
		return usageRow{}, err
	}
	props := ev.Props
	if props == nil {
		props = map[string]string{}
	}
	return usageRow{Tenant: ev.TenantID, Customer: ev.CustomerID, EventID: ev.EventID, ExternalID: ev.CustomerExternalID, Meter: ev.Meter,
		Period: ev.Period, Units: ev.Units, Scale: uint8(ev.Scale), //nolint:gosec // 0..6
		EventTime: ev.EventTime, IngestedAt: ev.IngestedAt, Properties: props}, nil
}

func decodeLimit(rec *kgo.Record) (limitRow, error) {
	var pb eventsv1.LimitExceeded
	if err := proto.Unmarshal(rec.Value, &pb); err != nil {
		return limitRow{}, &decodeError{"DECODE_ERROR", err}
	}
	t, err1 := uuid.Parse(pb.GetTenantId())
	c, err2 := uuid.Parse(pb.GetCustomerId())
	if err := errors.Join(err1, err2); err != nil || pb.GetEventId() == "" || pb.GetDetectedAt() == nil || pb.GetQuantityScale() > 6 {
		return limitRow{}, &decodeError{"INVALID_EVENT", errors.Join(fmt.Errorf("incomplete limit event %q", pb.GetEventId()), err)}
	}
	return limitRow{Tenant: t, Customer: c, EventID: pb.GetEventId(), Meter: pb.GetMeterCode(), Period: pb.GetPeriodId(), Threshold: pb.GetThresholdPct(),
		Limit: pb.GetLimitUnits(), Used: pb.GetUsedUnits(), Scale: uint8(pb.GetQuantityScale()), DetectedAt: pb.GetDetectedAt().AsTime()}, nil //nolint:gosec // 0..6
}

func decodeInvoice(rec *kgo.Record) (invoiceRow, error) {
	var pb eventsv1.InvoiceFact
	if err := proto.Unmarshal(rec.Value, &pb); err != nil {
		return invoiceRow{}, &decodeError{"DECODE_ERROR", err}
	}
	t, err1 := uuid.Parse(pb.GetTenantId())
	i, err2 := uuid.Parse(pb.GetInvoiceId())
	c, err3 := uuid.Parse(pb.GetCustomerId())
	if err := errors.Join(err1, err2, err3); err != nil || pb.GetOccurredAt() == nil || pb.GetKind() == nil {
		return invoiceRow{}, &decodeError{"INVALID_EVENT", errors.Join(errors.New("incomplete invoice fact"), err)}
	}
	kind := ""
	switch pb.GetKind().(type) {
	case *eventsv1.InvoiceFact_Finalized_:
		kind = "finalized"
	case *eventsv1.InvoiceFact_Paid_:
		kind = "paid"
	case *eventsv1.InvoiceFact_Voided_:
		kind = "voided"
	case *eventsv1.InvoiceFact_Corrected_:
		kind = "corrected"
	}
	return invoiceRow{Tenant: t, Invoice: i, Customer: c, Version: pb.GetVersion(), Period: pb.GetPeriodId(), Kind: kind, TotalMinor: pb.GetTotalMinor(),
		Currency: pb.GetCurrency(), Number: pb.GetNumber(), OccurredAt: pb.GetOccurredAt().AsTime()}, nil
}

func redriveSkips(rec *kgo.Record, group string) bool {
	for _, h := range rec.Headers {
		if h.Key == redriveHeader {
			return string(h.Value) != group
		}
	}
	return false
}

// add decodes a record into the batch. An undecodable record is dead-lettered (and so counts as handled).
func (r *Runner) add(ctx context.Context, b *batch, rec *kgo.Record) error {
	b.records = append(b.records, rec)
	if redriveSkips(rec, r.cfg.Group) {
		return nil // a redrive aimed at another consumer group
	}
	var err error
	switch rec.Topic {
	case UsageTopic:
		var row usageRow
		if row, err = decodeUsage(rec); err == nil {
			b.usage = append(b.usage, row)
		}
	case LimitTopic:
		var row limitRow
		if row, err = decodeLimit(rec); err == nil {
			b.limits = append(b.limits, row)
		}
	case InvoiceTopic:
		var row invoiceRow
		if row, err = decodeInvoice(rec); err == nil {
			b.invoices = append(b.invoices, row)
		}
	default:
		return fmt.Errorf("unexpected topic %q", rec.Topic)
	}
	if err == nil {
		return nil
	}
	var de *decodeError
	reason := "DECODE_ERROR"
	if errors.As(err, &de) {
		reason = de.reason
	}
	r.dead.Add(ctx, 1, metric.WithAttributes(attribute.String("topic", rec.Topic), attribute.String("reason", reason)))
	dlq := kafkax.DLQ{Client: r.cfg.Producer, Topic: dlqOf[rec.Topic], Consumer: r.cfg.Group}
	return dlq.Send(ctx, rec, reason, err)
}

// insert writes the batch, retrying until ClickHouse takes it (or ctx ends): the offsets are not committed meanwhile, so
// nothing is lost while it is down.
func (r *Runner) insert(ctx context.Context, b *batch) error {
	backoff := r.cfg.Backoff
	for {
		err := r.insertOnce(ctx, b)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		r.retries.Add(ctx, 1)
		r.log.WarnContext(ctx, "clickhouse insert failed, retrying", "rows", b.size(), "error", err, "backoff", backoff)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 10*time.Second)
	}
}

// insertOnce writes what is left of the batch, table by table. A table that went in is cleared, so a retry of the rest does not insert it again.
func (r *Runner) insertOnce(ctx context.Context, b *batch) error {
	if err := insertTable(ctx, r, "usage_events", "INSERT INTO usage_events", &b.usage, func(batch driver.Batch, u usageRow) error {
		return batch.Append(u.Tenant, u.EventID, u.Customer, u.ExternalID, u.Meter, u.Units, u.Scale, u.Period, u.EventTime, u.IngestedAt, u.Properties)
	}); err != nil {
		return err
	}
	if err := insertTable(ctx, r, "limit_events", "INSERT INTO limit_events", &b.limits, func(batch driver.Batch, l limitRow) error {
		return batch.Append(l.Tenant, l.EventID, l.Customer, l.Meter, l.Period, l.Threshold, l.Limit, l.Used, l.Scale, l.DetectedAt)
	}); err != nil {
		return err
	}
	return insertTable(ctx, r, "invoice_facts", "INSERT INTO invoice_facts", &b.invoices, func(batch driver.Batch, f invoiceRow) error {
		return batch.Append(f.Tenant, f.Invoice, f.Version, f.Customer, f.Period, f.Kind, f.TotalMinor, f.Currency, f.Number, f.OccurredAt)
	})
}

// insertTable sends rows (statement stmt, metric label table) in one ClickHouse batch and empties rows once the batch is in.
func insertTable[T any](ctx context.Context, r *Runner, table, stmt string, rows *[]T, appendRow func(driver.Batch, T) error) error {
	if len(*rows) == 0 {
		return nil
	}
	batch, err := r.cfg.Conn.PrepareBatch(ctx, stmt)
	if err != nil {
		return err
	}
	for _, row := range *rows {
		if err := appendRow(batch, row); err != nil {
			return err
		}
	}
	if err := batch.Send(); err != nil {
		return err
	}
	r.rows.Add(ctx, int64(len(*rows)), metric.WithAttributes(attribute.String("table", table)))
	*rows = nil
	return nil
}

// Run consumes until ctx is done.
func (r *Runner) Run(ctx context.Context) error {
	cl, err := kgo.NewClient(
		kgo.SeedBrokers(r.cfg.Brokers...), kgo.ConsumerGroup(r.cfg.Group), kgo.ConsumeTopics(UsageTopic, LimitTopic, InvoiceTopic),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()), kgo.DisableAutoCommit(), kgo.BlockRebalanceOnPoll(),
		kgo.FetchMaxWait(500*time.Millisecond), kgo.SessionTimeout(10*time.Second),
	)
	if err != nil {
		return fmt.Errorf("consumer: new client: %w", err)
	}
	defer cl.Close()

	b := &batch{}
	for ctx.Err() == nil {
		wait := r.cfg.BatchWait
		if b.size() > 0 || len(b.records) > 0 {
			wait = max(time.Until(b.first.Add(r.cfg.BatchWait)), time.Millisecond)
		}
		pctx, cancel := context.WithTimeout(ctx, wait)
		fetches := cl.PollRecords(pctx, r.cfg.BatchRows)
		cancel()
		if fetches.IsClientClosed() {
			return nil
		}
		fetches.EachRecord(func(rec *kgo.Record) {
			if len(b.records) == 0 {
				b.first = time.Now()
			}
			if err := r.add(ctx, b, rec); err != nil && ctx.Err() == nil {
				r.log.ErrorContext(ctx, "record not processed", "topic", rec.Topic, "offset", rec.Offset, "error", err)
			}
		})
		if len(b.records) > 0 && (b.size() >= r.cfg.BatchRows || time.Since(b.first) >= r.cfg.BatchWait) {
			if err := r.flush(ctx, cl, b); err != nil {
				return nil // ctx ended mid-retry
			}
		}
		cl.AllowRebalance()
	}
	if len(b.records) > 0 {
		fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		if err := r.flush(fctx, cl, b); err != nil {
			r.log.WarnContext(ctx, "last batch not flushed on shutdown, it is read again after a restart", "rows", b.size(), "error", err)
		}
	}
	return nil
}

func (r *Runner) flush(ctx context.Context, cl *kgo.Client, b *batch) error {
	// One consumer span per topic-partition in the batch, linked to the producers of its records.
	type tp struct {
		topic string
		part  int32
	}
	var order []tp
	groups := map[tp][]*kgo.Record{}
	for _, rec := range b.records {
		k := tp{rec.Topic, rec.Partition}
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], rec)
	}
	var ends []func(error)
	for _, k := range order {
		_, end := kafkax.StartBatch(ctx, k.topic, r.cfg.Group, k.part, groups[k])
		ends = append(ends, end)
	}
	err := r.insert(ctx, b)
	for _, end := range ends {
		end(err)
	}
	if err != nil {
		return err
	}
	if err := cl.CommitRecords(ctx, b.records...); err != nil {
		// The rows are in; an uncommitted offset only means the batch is read (and merged away) again after a restart.
		r.log.WarnContext(ctx, "offset commit failed", "error", err)
	}
	*b = batch{}
	return nil
}
