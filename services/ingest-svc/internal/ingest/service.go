// Package ingest implements IngestService: validate, dedup, stamp and enqueue usage events (docs/03-services/ingest-svc.md).
package ingest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	eventsv1 "github.com/0furkancolak/quotify/gen/go/quotify/events/v1"
	ingestv1 "github.com/0furkancolak/quotify/gen/go/quotify/ingest/v1"
	"github.com/0furkancolak/quotify/pkg/idgen"
	"github.com/0furkancolak/quotify/pkg/kafkax"
	"github.com/0furkancolak/quotify/pkg/outbox"
	"github.com/0furkancolak/quotify/pkg/period"
	"github.com/0furkancolak/quotify/pkg/pgxx"
	"github.com/0furkancolak/quotify/pkg/tenant"
	"github.com/0furkancolak/quotify/services/ingest-svc/internal/domain"
	"github.com/0furkancolak/quotify/services/ingest-svc/internal/lookup"
	"github.com/0furkancolak/quotify/services/ingest-svc/internal/store/sqlc"
)

// Topic is where accepted events go.
const Topic = "usage.events.v1"

// ceType is the fully-qualified protobuf message name, carried in the ce-type header.
const ceType = "quotify.events.v1.UsageEvent"

// Lookups resolves meters and customers (cached in production).
type Lookups interface {
	Meter(ctx context.Context, tenantID, code string) (lookup.Meter, bool, error)
	Customer(ctx context.Context, tenantID, externalID string) (lookup.Customer, bool, error)
}

// Options configure a Service. Pools and Lookups are required.
type Options struct {
	App    *pgxpool.Pool // ingest_app: RLS enforced
	System *pgxpool.Pool // ingest_system: watermark and depth
	Lookup Lookups
	Log    *slog.Logger
	// MaxOutboxDepth makes IngestEvents answer RESOURCE_EXHAUSTED above this many pending rows. 0 disables it.
	MaxOutboxDepth int64
	// DepthRefresh is how often the depth estimate is refreshed (default 1s).
	DepthRefresh time.Duration
	// Now is the clock used for validation (default time.Now). The persisted ingested_at comes from NowInTx.
	Now func() time.Time
	// NowInTx reads the database clock inside the write transaction (default: SELECT clock_timestamp()).
	NowInTx func(ctx context.Context, q *sqlc.Queries) (time.Time, error)
	// Propagator injects the trace context into outbox headers (default: the global propagator).
	Propagator propagation.TextMapPropagator
}

// Service implements ingestv1.IngestServiceServer.
type Service struct {
	ingestv1.UnimplementedIngestServiceServer
	o     Options
	log   *slog.Logger
	depth atomic.Int64

	locMu sync.Mutex
	locs  map[string]*time.Location

	events metric.Int64Counter
}

// New validates the options and registers metrics. Call Run to keep the depth estimate fresh.
func New(o Options) (*Service, error) {
	if o.App == nil || o.System == nil || o.Lookup == nil {
		return nil, errors.New("ingest: App, System and Lookup are required")
	}
	if o.Log == nil {
		o.Log = slog.Default()
	}
	if o.DepthRefresh <= 0 {
		o.DepthRefresh = time.Second
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.NowInTx == nil {
		o.NowInTx = func(ctx context.Context, q *sqlc.Queries) (time.Time, error) { return q.ClockNow(ctx) }
	}
	if o.Propagator == nil {
		o.Propagator = otel.GetTextMapPropagator()
	}
	ctr, err := otel.Meter("github.com/0furkancolak/quotify/services/ingest-svc").Int64Counter("ingest_events",
		metric.WithDescription("Ingested events by outcome"))
	if err != nil {
		return nil, err
	}
	return &Service{o: o, log: o.Log, locs: map[string]*time.Location{}, events: ctr}, nil
}

// Run refreshes the outbox depth estimate until ctx is done.
func (s *Service) Run(ctx context.Context) error {
	t := time.NewTicker(s.o.DepthRefresh)
	defer t.Stop()
	for {
		d, err := outbox.Depth(ctx, s.o.System, "outbox")
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			s.log.WarnContext(ctx, "ingest: outbox depth unavailable", "error", err)
		} else {
			s.depth.Store(d)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

// OutboxDepth is the last refreshed depth estimate.
func (s *Service) OutboxDepth() int64 { return s.depth.Load() }

func (s *Service) location(tz string) (*time.Location, error) {
	s.locMu.Lock()
	defer s.locMu.Unlock()
	if l, ok := s.locs[tz]; ok {
		return l, nil
	}
	l, err := time.LoadLocation(tz)
	if err != nil {
		return nil, err
	}
	s.locs[tz] = l
	return l, nil
}

// pending is an event that passed validation and awaits the write.
type pending struct {
	idx   int
	key   string // "" for auto
	hash  []byte
	event *eventsv1.UsageEvent // IngestedAt is set inside the transaction
}

// IngestEvents implements ingestv1.IngestServiceServer.
func (s *Service) IngestEvents(ctx context.Context, req *ingestv1.IngestEventsRequest) (*ingestv1.IngestEventsResponse, error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	n := len(req.GetEvents())
	if n == 0 || n > domain.MaxBatch {
		return nil, status.Errorf(codes.InvalidArgument, "events: need 1..%d events, got %d", domain.MaxBatch, n)
	}
	loc, err := s.location(ti.Timezone)
	if err != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "tenant time zone %q is unusable", ti.Timezone)
	}
	if max := s.o.MaxOutboxDepth; max > 0 && s.depth.Load() > max {
		return nil, status.Error(codes.ResourceExhausted, "ingest backlog is full, retry later")
	}

	results := make([]*ingestv1.EventResult, n)
	var todo []*pending
	seen := map[string]*pending{} // first occurrence of each key within this batch
	now := s.o.Now()
	tid := ti.ID.String()

	s.warm(ctx, tid, req.GetEvents())
	for i, ev := range req.GetEvents() {
		p, rej, err := s.prepare(ctx, ti, loc, tid, now, i, ev)
		if err != nil {
			// Infrastructure failure: nothing has been written, so the whole batch can be retried safely.
			return nil, status.Error(codes.Unavailable, "plans-svc unavailable")
		}
		if rej != nil {
			results[i] = rejected(rej)
			continue
		}
		if p.key != "" {
			if first, ok := seen[p.key]; ok {
				// Same key twice in one request: the second is a duplicate of the first, or a reuse.
				if bytes.Equal(first.hash, p.hash) {
					results[i] = &ingestv1.EventResult{Status: ingestv1.EventResult_STATUS_DUPLICATE, EventId: first.event.EventId}
				} else {
					results[i] = rejected(&domain.Reject{Code: domain.CodeKeyReused, Message: "idempotency_key was already used with a different payload"})
				}
				continue
			}
			seen[p.key] = p
		}
		todo = append(todo, p)
	}
	if len(todo) > 0 {
		if err := s.write(ctx, ti, todo, results); err != nil {
			return nil, err
		}
	}
	s.count(ctx, results)
	return &ingestv1.IngestEventsResponse{Results: results}, nil
}

// warm resolves the distinct meters and customers of a batch concurrently, so that the sequential validation below
// only hits the cache. Errors are ignored here: validation asks again and reports them.
func (s *Service) warm(ctx context.Context, tid string, events []*ingestv1.IncomingEvent) {
	meters := map[string]struct{}{}
	customers := map[string]struct{}{}
	for _, ev := range events {
		meters[ev.GetMeterCode()] = struct{}{}
		customers[ev.GetCustomerExternalId()] = struct{}{}
	}
	var g errgroup.Group
	g.SetLimit(16)
	for code := range meters {
		g.Go(func() error { _, _, _ = s.o.Lookup.Meter(ctx, tid, code); return nil })
	}
	for id := range customers {
		g.Go(func() error { _, _, _ = s.o.Lookup.Customer(ctx, tid, id); return nil })
	}
	_ = g.Wait()
}

func rejected(r *domain.Reject) *ingestv1.EventResult {
	return &ingestv1.EventResult{Status: ingestv1.EventResult_STATUS_REJECTED, ErrorCode: r.Code, ErrorMessage: r.Message}
}

// prepare runs every check that needs no database write and builds the UsageEvent.
func (s *Service) prepare(ctx context.Context, ti tenant.Info, loc *time.Location, tid string, now time.Time, idx int, ev *ingestv1.IncomingEvent) (*pending, *domain.Reject, error) {
	pre, rej := domain.CheckPrelim(ev, now, ti.Environment)
	if rej != nil {
		return nil, rej, nil
	}
	meter, ok, err := s.o.Lookup.Meter(ctx, tid, ev.GetMeterCode())
	if err != nil {
		s.log.WarnContext(ctx, "ingest: meter lookup failed", "error", err)
		return nil, nil, err
	}
	if !ok {
		return nil, &domain.Reject{Code: domain.CodeUnknownMeter, Message: fmt.Sprintf("meter %q does not exist", ev.GetMeterCode())}, nil
	}
	cust, ok, err := s.o.Lookup.Customer(ctx, tid, ev.GetCustomerExternalId())
	if err != nil {
		s.log.WarnContext(ctx, "ingest: customer lookup failed", "error", err)
		return nil, nil, err
	}
	if !ok {
		return nil, &domain.Reject{Code: domain.CodeUnknownCustomer, Message: fmt.Sprintf("customer %q does not exist", ev.GetCustomerExternalId())}, nil
	}
	units, rej := domain.ParseQuantity(ev.GetQuantity(), meter.Scale)
	if rej != nil {
		return nil, rej, nil
	}

	eventID := idgen.NewULID()
	e := &eventsv1.UsageEvent{
		EventId:            eventID,
		TenantId:           tid,
		CustomerId:         cust.ID,
		CustomerExternalId: cust.ExternalID,
		MeterCode:          meter.Code,
		MeterVersion:       meter.Version,
		QuantityUnits:      units,
		QuantityScale:      uint32(meter.Scale), //nolint:gosec // 0..6, validated by plans-svc
		EventTime:          timestamppb.New(pre.EventTime),
		PeriodId:           period.PeriodOf(pre.EventTime, loc),
		Properties:         ev.GetProperties(),
	}
	if pre.Auto {
		e.IdempotencyKey = domain.AutoKeyPrefix + eventID
	} else {
		e.IdempotencyKey = pre.Key
	}
	return &pending{
		idx: idx, key: pre.Key,
		hash:  domain.PayloadHash(cust.ExternalID, meter.Code, units, meter.Scale, pre.EventTime, ev.GetProperties()),
		event: e,
	}, nil, nil
}

// write persists the batch in one transaction and fills results for every pending event.
func (s *Service) write(ctx context.Context, ti tenant.Info, todo []*pending, results []*ingestv1.EventResult) error {
	traceHeaders := map[string]string{}
	kafkax.InjectTrace(ctx, traceHeaders, s.o.Propagator)

	err := pgxx.InTenantTx(ctx, s.o.App, ti.ID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('transaction_timeout', '5s', true)`); err != nil {
			return fmt.Errorf("set transaction_timeout: %w", err)
		}
		q := sqlc.New(tx)
		now, err := s.o.NowInTx(ctx, q)
		if err != nil {
			return fmt.Errorf("clock: %w", err)
		}
		bucket := domain.BucketOf(now)
		prev := bucket.AddDate(0, -1, 0)

		accepted, err := s.dedup(ctx, q, ti.ID, bucket, prev, todo, results)
		if err != nil {
			return err
		}
		if len(accepted) == 0 {
			return nil
		}

		payloads := make([][]byte, len(accepted))
		headers := make([]string, len(accepted))
		for i, p := range accepted {
			p.event.IngestedAt = timestamppb.New(now)
			b, err := proto.MarshalOptions{Deterministic: true}.Marshal(p.event)
			if err != nil {
				return fmt.Errorf("marshal event: %w", err)
			}
			payloads[i] = b
			h := kafkax.Standard(ceType, p.event.EventId, ti.ID.String())
			for k, v := range traceHeaders {
				h[k] = v
			}
			hb, err := json.Marshal(h)
			if err != nil {
				return err
			}
			headers[i] = string(hb)
		}
		if err := q.EnqueueOutbox(ctx, sqlc.EnqueueOutboxParams{
			TenantID: ti.ID, Topic: Topic, PartitionKey: ti.ID.String(),
			Payloads: payloads, Headers: headers, IngestedAt: now,
		}); err != nil {
			return fmt.Errorf("enqueue outbox: %w", err)
		}
		for _, p := range accepted {
			results[p.idx] = &ingestv1.EventResult{Status: ingestv1.EventResult_STATUS_ACCEPTED, EventId: p.event.EventId}
		}
		return nil
	})
	if err != nil {
		return mapDBError(err)
	}
	return nil
}

// dedup claims the idempotency keys and returns the events that are new. Duplicates and reuses are written to results.
func (s *Service) dedup(ctx context.Context, q *sqlc.Queries, tenantID uuid.UUID, bucket, prev time.Time, todo []*pending, results []*ingestv1.EventResult) ([]*pending, error) {
	var keyed []*pending
	var accepted []*pending
	for _, p := range todo {
		if p.key == "" {
			accepted = append(accepted, p) // sandbox without a key: no dedup
		} else {
			keyed = append(keyed, p)
		}
	}
	if len(keyed) == 0 {
		return accepted, nil
	}
	// Sorted so that concurrent batches claim keys in the same order and cannot deadlock on each other.
	sort.Slice(keyed, func(i, j int) bool { return keyed[i].key < keyed[j].key })

	known := map[string]sqlc.FindIdempotencyKeysRow{}
	keys := make([]string, len(keyed))
	for i, p := range keyed {
		keys[i] = p.key
	}
	// The previous month's bucket is not covered by ON CONFLICT: check it explicitly.
	rows, err := q.FindIdempotencyKeys(ctx, sqlc.FindIdempotencyKeysParams{TenantID: tenantID, Keys: keys, Buckets: []time.Time{prev}})
	if err != nil {
		return nil, fmt.Errorf("find previous bucket: %w", err)
	}
	for _, r := range rows {
		known[r.IdempotencyKey] = r
	}

	var fresh []*pending
	for _, p := range keyed {
		if r, ok := known[p.key]; ok {
			results[p.idx] = existingResult(r, p)
		} else {
			fresh = append(fresh, p)
		}
	}
	if len(fresh) == 0 {
		return accepted, nil
	}

	ins := sqlc.InsertIdempotencyKeysParams{TenantID: tenantID, CreatedAt: bucket}
	for _, p := range fresh {
		ins.Keys = append(ins.Keys, p.key)
		ins.EventIds = append(ins.EventIds, p.event.EventId)
		ins.Hashes = append(ins.Hashes, p.hash)
	}
	won, err := q.InsertIdempotencyKeys(ctx, ins)
	if err != nil {
		return nil, fmt.Errorf("insert idempotency keys: %w", err)
	}
	isNew := make(map[string]bool, len(won))
	for _, k := range won {
		isNew[k] = true
	}
	var lost []string
	for _, p := range fresh {
		if isNew[p.key] {
			accepted = append(accepted, p)
		} else {
			lost = append(lost, p.key)
		}
	}
	if len(lost) > 0 {
		// Another transaction claimed these keys first (it has committed by now, because ON CONFLICT waited for it).
		rows, err := q.FindIdempotencyKeys(ctx, sqlc.FindIdempotencyKeysParams{TenantID: tenantID, Keys: lost, Buckets: []time.Time{bucket}})
		if err != nil {
			return nil, fmt.Errorf("find conflicting keys: %w", err)
		}
		byKey := map[string]sqlc.FindIdempotencyKeysRow{}
		for _, r := range rows {
			byKey[r.IdempotencyKey] = r
		}
		for _, p := range fresh {
			if isNew[p.key] {
				continue
			}
			r, ok := byKey[p.key]
			if !ok {
				return nil, fmt.Errorf("idempotency key %q conflicted but cannot be read back", p.key)
			}
			results[p.idx] = existingResult(r, p)
		}
	}
	sort.Slice(accepted, func(i, j int) bool { return accepted[i].idx < accepted[j].idx })
	return accepted, nil
}

func existingResult(r sqlc.FindIdempotencyKeysRow, p *pending) *ingestv1.EventResult {
	if bytes.Equal(r.PayloadHash, p.hash) {
		return &ingestv1.EventResult{Status: ingestv1.EventResult_STATUS_DUPLICATE, EventId: r.EventID}
	}
	return rejected(&domain.Reject{Code: domain.CodeKeyReused, Message: "idempotency_key was already used with a different payload"})
}

func mapDBError(err error) error {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return status.Error(codes.DeadlineExceeded, "ingest timed out; nothing was accepted, retry is safe")
	case errors.Is(err, context.Canceled):
		return status.Error(codes.Canceled, "canceled")
	}
	switch pgxx.ErrCode(err) {
	case "57014", "25P03": // query_canceled (transaction_timeout), idle_in_transaction_session_timeout
		return status.Error(codes.Unavailable, "database too slow; nothing was accepted, retry is safe")
	case "40001", "40P01":
		return status.Error(codes.Aborted, "conflict; retry is safe")
	}
	slog.Error("ingest: write failed", "error", err)
	return status.Error(codes.Unavailable, "database unavailable; nothing was accepted, retry is safe")
}

func (s *Service) count(ctx context.Context, results []*ingestv1.EventResult) {
	for _, r := range results {
		s.events.Add(ctx, 1, metric.WithAttributes(
			attribute.String("status", r.GetStatus().String()), attribute.String("error_code", r.GetErrorCode())))
	}
}

// GetOutboxWatermark implements ingestv1.IngestServiceServer.
func (s *Service) GetOutboxWatermark(ctx context.Context, _ *ingestv1.GetOutboxWatermarkRequest) (*ingestv1.GetOutboxWatermarkResponse, error) {
	oldest, ok, err := outbox.Watermark(ctx, s.o.System, "outbox")
	if err != nil {
		return nil, status.Error(codes.Unavailable, "outbox unavailable")
	}
	resp := &ingestv1.GetOutboxWatermarkResponse{ServerNow: timestamppb.Now()}
	if ok {
		resp.OldestUnpublishedIngestedAt = timestamppb.New(oldest)
	}
	return resp, nil
}
