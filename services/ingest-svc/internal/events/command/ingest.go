package command

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"

	eventsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/events/v1"
	ingestv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/ingest/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/idgen"
	"github.com/0furkancolak/payrudder/pkg/period"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/ingest-svc/internal/domain"
)

// pending is an event that passed validation and awaits the write.
type pending struct {
	idx   int
	key   string // "" for auto
	hash  []byte
	event *eventsv1.UsageEvent // IngestedAt is set inside the transaction
}

// batch holds what every event of one request shares.
type batch struct {
	ti  tenant.Info
	tid string
	loc *time.Location
	now time.Time
}

// Ingest validates the events of the caller's tenant, deduplicates them by idempotency key and enqueues the accepted ones.
// It returns one result per event, in order. An error means nothing was written and the request can be retried.
func (c *Commands) Ingest(ctx context.Context, events []*ingestv1.IncomingEvent) ([]*ingestv1.EventResult, error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	n := len(events)
	if n == 0 || n > domain.MaxBatch {
		return nil, errkey.Newf(codes.InvalidArgument, "ingest.batch.bad_size", "events: need 1..%d events, got %d", domain.MaxBatch, n)
	}
	loc, err := c.location(ti.Timezone)
	if err != nil {
		return nil, errkey.Newf(codes.FailedPrecondition, "ingest.tenant.bad_timezone", "tenant time zone %q is unusable", ti.Timezone)
	}
	if max := c.o.MaxOutboxDepth; max > 0 && c.o.Depth() > max {
		return nil, errkey.New(codes.ResourceExhausted, "ingest.backlog.full", "ingest backlog is full, retry later")
	}

	b := batch{ti: ti, tid: ti.ID.String(), loc: loc, now: c.o.Now()}
	results := make([]*ingestv1.EventResult, n)
	var todo []*pending
	seen := map[string]*pending{} // first occurrence of each key within this batch

	c.warm(ctx, b.tid, events)
	for i, ev := range events {
		p, rej, err := c.prepare(ctx, b, i, ev)
		if err != nil {
			// Infrastructure failure: nothing has been written, so the whole batch can be retried safely.
			return nil, errkey.New(codes.Unavailable, "ingest.plans.unavailable", "plans-svc unavailable")
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
					results[i] = rejected(keyReused())
				}
				continue
			}
			seen[p.key] = p
		}
		todo = append(todo, p)
	}
	if len(todo) > 0 {
		if err := c.write(ctx, ti, todo, results); err != nil {
			return nil, err
		}
	}
	c.count(ctx, ti.ID.String(), results)
	return results, nil
}

// warm resolves the distinct meters and customers of a batch concurrently, so that the sequential validation
// only hits the cache. Errors are ignored here on purpose: validation asks again and reports them.
func (c *Commands) warm(ctx context.Context, tid string, events []*ingestv1.IncomingEvent) {
	meters := map[string]struct{}{}
	customers := map[string]struct{}{}
	for _, ev := range events {
		meters[ev.GetMeterCode()] = struct{}{}
		customers[ev.GetCustomerExternalId()] = struct{}{}
	}
	var g errgroup.Group
	g.SetLimit(warmConcurrency)
	for code := range meters {
		g.Go(func() error { _, _, _ = c.o.Lookup.Meter(ctx, tid, code); return nil })
	}
	for id := range customers {
		g.Go(func() error { _, _, _ = c.o.Lookup.Customer(ctx, tid, id); return nil })
	}
	_ = g.Wait() // the goroutines never return an error
}

func rejected(r *domain.Reject) *ingestv1.EventResult {
	return &ingestv1.EventResult{Status: ingestv1.EventResult_STATUS_REJECTED, ErrorCode: r.Code, ErrorMessage: r.Message}
}

// keyReused is the rejection of an idempotency key that already belongs to a different payload.
func keyReused() *domain.Reject {
	return &domain.Reject{Code: domain.CodeKeyReused, Message: "idempotency_key was already used with a different payload"}
}

// prepare runs every check that needs no database write and builds the UsageEvent.
func (c *Commands) prepare(ctx context.Context, b batch, idx int, ev *ingestv1.IncomingEvent) (*pending, *domain.Reject, error) {
	pre, rej := domain.CheckPrelim(ev, b.now, b.ti.Environment)
	if rej != nil {
		return nil, rej, nil
	}
	meter, ok, err := c.o.Lookup.Meter(ctx, b.tid, ev.GetMeterCode())
	if err != nil {
		c.log.WarnContext(ctx, "ingest: meter lookup failed", "error", err)
		return nil, nil, err
	}
	if !ok {
		return nil, &domain.Reject{Code: domain.CodeUnknownMeter, Message: fmt.Sprintf("meter %q does not exist", ev.GetMeterCode())}, nil
	}
	cust, ok, err := c.o.Lookup.Customer(ctx, b.tid, ev.GetCustomerExternalId())
	if err != nil {
		c.log.WarnContext(ctx, "ingest: customer lookup failed", "error", err)
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
		TenantId:           b.tid,
		CustomerId:         cust.ID,
		CustomerExternalId: cust.ExternalID,
		MeterCode:          meter.Code,
		MeterVersion:       meter.Version,
		QuantityUnits:      units,
		QuantityScale:      uint32(meter.Scale), //nolint:gosec // 0..6, validated by plans-svc
		EventTime:          timestamppb.New(pre.EventTime),
		PeriodId:           period.PeriodOf(pre.EventTime, b.loc),
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

// count records one ingest_events increment per event, aggregated per (result, error_code) so a 500-event request is
// at most a handful of Add calls.
func (c *Commands) count(ctx context.Context, tenantID string, results []*ingestv1.EventResult) {
	type key struct{ result, code string }
	n := map[key]int64{}
	for _, r := range results {
		n[key{strings.ToLower(strings.TrimPrefix(r.GetStatus().String(), "STATUS_")), r.GetErrorCode()}]++
	}
	for k, cnt := range n {
		c.events.Add(ctx, cnt, metric.WithAttributes(attribute.String("tenant", tenantID),
			attribute.String("result", k.result), attribute.String("error_code", k.code)))
	}
}
