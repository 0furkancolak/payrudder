//go:build integration

// Package itest runs billing-svc against real Postgres and RabbitMQ with fake plans, ingest and aggregator services and
// the pkg/stubworker standing in for the workers.
package itest

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	aggregatorv1 "github.com/0furkancolak/quotify/gen/go/quotify/aggregator/v1"
	commandsv1 "github.com/0furkancolak/quotify/gen/go/quotify/commands/v1"
	ingestv1 "github.com/0furkancolak/quotify/gen/go/quotify/ingest/v1"
	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/outbox"
	"github.com/0furkancolak/quotify/pkg/pgxx"
	"github.com/0furkancolak/quotify/pkg/rabbitx"
	"github.com/0furkancolak/quotify/pkg/stubworker"
	"github.com/0furkancolak/quotify/pkg/tenant"
	"github.com/0furkancolak/quotify/pkg/testkit"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/replies"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/runs"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/saga"
	"github.com/0furkancolak/quotify/services/billing-svc/migrations"
)

var (
	top        *testing.T
	sharedOnce sync.Once
	shared     struct {
		pg     testkit.Postgres
		db     testkit.ServiceDB
		amqp   string
		system *pgxpool.Pool
		app    *pgxpool.Pool
	}
)

func infra(t *testing.T) {
	t.Helper()
	sharedOnce.Do(func() {
		shared.pg = testkit.StartPostgres(top)
		shared.db = testkit.CreateServiceDB(top, shared.pg, "billing")
		if err := pgxx.Migrate(context.Background(), shared.db.Migrator, migrations.FS, nil); err != nil {
			t.Fatalf("migrate: %v", err)
		}
		shared.amqp = testkit.StartRabbitMQ(top)
		var err error
		if shared.system, err = pgxx.NewPool(context.Background(), shared.db.System); err != nil {
			t.Fatal(err)
		}
		if shared.app, err = pgxx.NewPool(context.Background(), shared.db.App); err != nil {
			t.Fatal(err)
		}
	})
}

func logger() *slog.Logger {
	if os.Getenv("ITEST_VERBOSE") != "" {
		return slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// clock is a test clock shared by the run manager and the saga engine.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// sliceStream is a grpc.ServerStreamingClient over a slice.
type sliceStream[T any] struct {
	grpc.ClientStream
	items []*T
}

func (s *sliceStream[T]) Recv() (*T, error) {
	if len(s.items) == 0 {
		return nil, io.EOF
	}
	it := s.items[0]
	s.items = s.items[1:]
	return it, nil
}

// world is everything the fakes serve: one tenant with customers, a plan and usage.
type world struct {
	mu sync.Mutex

	tenant    *plansv1.Tenant
	customers map[string]*plansv1.Customer // by external id
	meters    []*plansv1.Meter
	versions  map[string]*plansv1.PlanVersion
	subs      []*plansv1.Subscription // active over the test periods
	feeDue    []*plansv1.FeeDue       // advance fee periods waiting to be invoiced
	advanced  []string                // subscription id|period start (RFC 3339) of the fee periods advanced
	adjusts   []*plansv1.Adjustment   // what plan changes moved, until marked billed
	billedAdj []string
	applyCall int
	taxRates  []*plansv1.TaxRate

	usage     map[string][]*aggregatorv1.UsageAggregate // by period id
	late      map[string][]*aggregatorv1.LateUsage      // by billed period id
	oldest    *timestamppb.Timestamp                    // ingest outbox watermark
	caughtUp  bool
	frozen    []string
	earlyCut  time.Time
	closeCall int
}

type fakeAll struct {
	w *world
}

// Plans.
func (f fakeAll) GetCustomer(_ context.Context, in *plansv1.GetCustomerRequest, _ ...grpc.CallOption) (*plansv1.GetCustomerResponse, error) {
	f.w.mu.Lock()
	defer f.w.mu.Unlock()
	return &plansv1.GetCustomerResponse{Customer: f.w.customers[in.GetExternalId()]}, nil
}

func (f fakeAll) ListMeters(context.Context, *plansv1.ListMetersRequest, ...grpc.CallOption) (*plansv1.ListMetersResponse, error) {
	return &plansv1.ListMetersResponse{Meters: f.w.meters}, nil
}

func (f fakeAll) GetPlanVersion(_ context.Context, in *plansv1.GetPlanVersionRequest, _ ...grpc.CallOption) (*plansv1.GetPlanVersionResponse, error) {
	f.w.mu.Lock()
	defer f.w.mu.Unlock()
	pv, ok := f.w.versions[in.GetPlanVersionId()]
	if !ok {
		return nil, status.Error(codes.NotFound, "plan version not found")
	}
	return &plansv1.GetPlanVersionResponse{PlanVersion: pv}, nil
}

func (f fakeAll) ListSubscriptionsForPeriod(context.Context, *plansv1.ListSubscriptionsForPeriodRequest, ...grpc.CallOption) (grpc.ServerStreamingClient[plansv1.ListSubscriptionsForPeriodResponse], error) {
	f.w.mu.Lock()
	defer f.w.mu.Unlock()
	st := &sliceStream[plansv1.ListSubscriptionsForPeriodResponse]{}
	for _, s := range f.w.subs {
		st.items = append(st.items, &plansv1.ListSubscriptionsForPeriodResponse{Subscription: s})
	}
	return st, nil
}

// ListSubscriptionsFeeDue serves the periods not yet advanced.
func (f fakeAll) ListSubscriptionsFeeDue(context.Context, *plansv1.ListSubscriptionsFeeDueRequest, ...grpc.CallOption) (*plansv1.ListSubscriptionsFeeDueResponse, error) {
	f.w.mu.Lock()
	defer f.w.mu.Unlock()
	out := &plansv1.ListSubscriptionsFeeDueResponse{}
	for _, d := range f.w.feeDue {
		done := false
		for _, a := range f.w.advanced {
			if a == d.GetSubscription().GetId()+"|"+d.GetPeriodStart().AsTime().UTC().Format(time.RFC3339) {
				done = true
			}
		}
		if !done {
			out.Due = append(out.Due, d)
		}
	}
	return out, nil
}

func (f fakeAll) AdvanceSubscriptionFee(_ context.Context, in *plansv1.AdvanceSubscriptionFeeRequest, _ ...grpc.CallOption) (*plansv1.AdvanceSubscriptionFeeResponse, error) {
	f.w.mu.Lock()
	defer f.w.mu.Unlock()
	f.w.advanced = append(f.w.advanced, in.GetSubscriptionId()+"|"+in.GetPeriodStart().AsTime().UTC().Format(time.RFC3339))
	return &plansv1.AdvanceSubscriptionFeeResponse{}, nil
}

func (f fakeAll) ApplyScheduledChanges(context.Context, *plansv1.ApplyScheduledChangesRequest, ...grpc.CallOption) (*plansv1.ApplyScheduledChangesResponse, error) {
	f.w.mu.Lock()
	defer f.w.mu.Unlock()
	f.w.applyCall++
	return &plansv1.ApplyScheduledChangesResponse{}, nil
}

func (f fakeAll) ListSubscriptionAdjustments(context.Context, *plansv1.ListSubscriptionAdjustmentsRequest, ...grpc.CallOption) (*plansv1.ListSubscriptionAdjustmentsResponse, error) {
	f.w.mu.Lock()
	defer f.w.mu.Unlock()
	out := &plansv1.ListSubscriptionAdjustmentsResponse{}
	for _, a := range f.w.adjusts {
		done := false
		for _, b := range f.w.billedAdj {
			done = done || b == a.GetId()
		}
		if !done {
			out.Adjustments = append(out.Adjustments, a)
		}
	}
	return out, nil
}

func (f fakeAll) MarkSubscriptionAdjustmentBilled(_ context.Context, in *plansv1.MarkSubscriptionAdjustmentBilledRequest, _ ...grpc.CallOption) (*plansv1.MarkSubscriptionAdjustmentBilledResponse, error) {
	f.w.mu.Lock()
	defer f.w.mu.Unlock()
	f.w.billedAdj = append(f.w.billedAdj, in.GetAdjustmentId())
	return &plansv1.MarkSubscriptionAdjustmentBilledResponse{}, nil
}

func (f fakeAll) GetTaxRates(context.Context, *plansv1.GetTaxRatesRequest, ...grpc.CallOption) (*plansv1.GetTaxRatesResponse, error) {
	f.w.mu.Lock()
	defer f.w.mu.Unlock()
	return &plansv1.GetTaxRatesResponse{Rates: f.w.taxRates}, nil
}

// Admin.
func (f fakeAll) GetTenant(context.Context, *plansv1.GetTenantRequest, ...grpc.CallOption) (*plansv1.GetTenantResponse, error) {
	return &plansv1.GetTenantResponse{Tenant: f.w.tenant}, nil
}

func (f fakeAll) ListTenants(context.Context, *plansv1.ListTenantsRequest, ...grpc.CallOption) (*plansv1.ListTenantsResponse, error) {
	return &plansv1.ListTenantsResponse{Tenants: []*plansv1.Tenant{f.w.tenant}}, nil
}

// Ingest.
func (f fakeAll) GetOutboxWatermark(context.Context, *ingestv1.GetOutboxWatermarkRequest, ...grpc.CallOption) (*ingestv1.GetOutboxWatermarkResponse, error) {
	f.w.mu.Lock()
	defer f.w.mu.Unlock()
	return &ingestv1.GetOutboxWatermarkResponse{OldestUnpublishedIngestedAt: f.w.oldest}, nil
}

// Aggregator.
func (f fakeAll) StreamPeriodUsage(_ context.Context, in *aggregatorv1.StreamPeriodUsageRequest, _ ...grpc.CallOption) (grpc.ServerStreamingClient[aggregatorv1.StreamPeriodUsageResponse], error) {
	f.w.mu.Lock()
	defer f.w.mu.Unlock()
	st := &sliceStream[aggregatorv1.StreamPeriodUsageResponse]{}
	for _, u := range f.w.usage[in.GetPeriodId()] {
		st.items = append(st.items, &aggregatorv1.StreamPeriodUsageResponse{Usage: u})
	}
	return st, nil
}

func (f fakeAll) StreamLateUsage(_ context.Context, in *aggregatorv1.StreamLateUsageRequest, _ ...grpc.CallOption) (grpc.ServerStreamingClient[aggregatorv1.StreamLateUsageResponse], error) {
	f.w.mu.Lock()
	defer f.w.mu.Unlock()
	st := &sliceStream[aggregatorv1.StreamLateUsageResponse]{}
	for _, l := range f.w.late[in.GetBilledPeriodId()] {
		st.items = append(st.items, &aggregatorv1.StreamLateUsageResponse{LateUsage: l})
	}
	return st, nil
}

func (f fakeAll) GetUsageBatch(_ context.Context, in *aggregatorv1.GetUsageBatchRequest, _ ...grpc.CallOption) (*aggregatorv1.GetUsageBatchResponse, error) {
	f.w.mu.Lock()
	defer f.w.mu.Unlock()
	resp := &aggregatorv1.GetUsageBatchResponse{}
	for _, k := range in.GetKeys() {
		agg := &aggregatorv1.UsageAggregate{CustomerId: k.GetCustomerId(), MeterCode: k.GetMeterCode(), PeriodId: k.GetPeriodId(), Quantity: "0"}
		for _, u := range f.w.usage[k.GetPeriodId()] {
			if u.GetCustomerId() == k.GetCustomerId() && u.GetMeterCode() == k.GetMeterCode() {
				agg = u
			}
		}
		resp.Usage = append(resp.Usage, agg)
	}
	return resp, nil
}

func (f fakeAll) GetIngestWatermark(context.Context, *aggregatorv1.GetIngestWatermarkRequest, ...grpc.CallOption) (*aggregatorv1.GetIngestWatermarkResponse, error) {
	f.w.mu.Lock()
	defer f.w.mu.Unlock()
	return &aggregatorv1.GetIngestWatermarkResponse{CaughtUp: f.w.caughtUp}, nil
}

func (f fakeAll) FreezePeriod(_ context.Context, in *aggregatorv1.FreezePeriodRequest, _ ...grpc.CallOption) (*aggregatorv1.FreezePeriodResponse, error) {
	f.w.mu.Lock()
	defer f.w.mu.Unlock()
	f.w.frozen = append(f.w.frozen, in.GetPeriodId())
	return &aggregatorv1.FreezePeriodResponse{}, nil
}

func (f fakeAll) ClosePeriodEarly(context.Context, *aggregatorv1.ClosePeriodEarlyRequest, ...grpc.CallOption) (*aggregatorv1.ClosePeriodEarlyResponse, error) {
	f.w.mu.Lock()
	defer f.w.mu.Unlock()
	f.w.closeCall++
	return &aggregatorv1.ClosePeriodEarlyResponse{CutoffAt: timestamppb.New(f.w.earlyCut)}, nil
}

// kafkaSink captures facts.
type kafkaSink struct {
	mu   sync.Mutex
	rows []outbox.Row
}

func (k *kafkaSink) Publish(_ context.Context, rows []outbox.Row) error {
	k.mu.Lock()
	k.rows = append(k.rows, rows...)
	k.mu.Unlock()
	return nil
}

func (k *kafkaSink) count() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return len(k.rows)
}

// env is one test's deployment under test.
type env struct {
	t      *testing.T
	clk    *clock
	w      *world
	eng    *saga.Engine
	mgr    *runs.Manager
	events *fakeEvents
	kafka  *kafkaSink
	tid    uuid.UUID
	stubs  []*stubworker.Worker
	cancel []context.CancelFunc
}

type envOpts struct {
	sandbox      bool
	stepTimeout  time.Duration
	watermarkGap time.Duration
}

var seq atomic.Int64

// newEnv creates a tenant world with two customers on one plan: base fee 1000, gb_hours at 12.5 per unit (scale 3),
// plus a tiered meter "calls" (0–100 free, then 2 per call).
func newEnv(t *testing.T, o envOpts) *env {
	t.Helper()
	infra(t)
	seq.Add(1)
	tid := uuid.New()
	envName := plansv1.Environment_ENVIRONMENT_LIVE
	if o.sandbox {
		envName = plansv1.Environment_ENVIRONMENT_SANDBOX
	}
	// "Now" is the 5th of the month after the billed period: the period 2026-10 closed on the 1st (grace 0).
	clk := &clock{t: time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)}
	pv := &plansv1.PlanVersion{Id: uuid.NewString(), PlanCode: "pro", Version: 1, BaseFeeMinor: 1000, Prices: []*plansv1.Price{
		{MeterCode: "gb_hours", Model: &plansv1.Price_PerUnit{PerUnit: &plansv1.PerUnitPrice{UnitPrice: "12.5"}}},
		{MeterCode: "calls", Model: &plansv1.Price_Tiered{Tiered: &plansv1.TieredGraduatedPrice{Tiers: []*plansv1.PriceTier{
			{UpTo: "100", UnitPrice: "0"}, {UnitPrice: "2"}}}}},
	}}
	w := &world{
		tenant: &plansv1.Tenant{Id: tid.String(), Environment: envName, Name: "Acme", Currency: "USD", Timezone: "UTC", Address: &plansv1.Address{Line1: "1 Market St", City: "London", Country: "GB"},
			GracePeriod: durationpb.New(0), CreateTime: timestamppb.New(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))},
		customers: map[string]*plansv1.Customer{},
		meters: []*plansv1.Meter{
			{Code: "gb_hours", QuantityScale: 3, UnitLabel: "GB-h"}, {Code: "calls", QuantityScale: 0, UnitLabel: "calls"}},
		versions: map[string]*plansv1.PlanVersion{pv.Id: pv},
		usage:    map[string][]*aggregatorv1.UsageAggregate{}, late: map[string][]*aggregatorv1.LateUsage{}, caughtUp: true,
		earlyCut: clk.t.Add(5 * time.Second),
	}
	e := &env{t: t, clk: clk, w: w, tid: tid, kafka: &kafkaSink{}}
	for _, name := range []string{"cus_a", "cus_b"} {
		c := &plansv1.Customer{Id: uuid.NewString(), ExternalId: name, Name: "Customer " + name, Email: name + "@example.com", PaymentMethod: "pm_sandbox_success",
			Address: &plansv1.Address{Line1: name + " street 1", City: "Berlin", PostalCode: "10115", Country: "DE"}}
		w.customers[name] = c
		w.subs = append(w.subs, &plansv1.Subscription{Id: uuid.NewString(), CustomerId: c.Id, CustomerExternalId: name, PlanVersionId: pv.Id,
			StartTime: timestamppb.New(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))})
	}
	var err error
	if o.stepTimeout == 0 {
		o.stepTimeout = time.Hour
	}
	e.eng, err = saga.NewEngine(saga.Config{Now: clk.Now, StepTimeout: o.stepTimeout, Log: logger()})
	if err != nil {
		t.Fatal(err)
	}
	if o.watermarkGap == 0 {
		o.watermarkGap = 30 * time.Second
	}
	f := fakeAll{w: w}
	e.events = &fakeEvents{}
	e.mgr, err = runs.NewManager(runs.Config{
		Pool: shared.system, Engine: e.eng, Plans: f, Admin: f, Ingest: f, Aggregator: f, Events: e.events,
		EndOffsets: func(context.Context) (map[int32]int64, error) { return map[int32]int64{0: 10, 1: 20}, nil },
		Now:        clk.Now, WatermarkDelay: o.watermarkGap, PageSize: 2, Log: logger(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *env) customer(name string) *plansv1.Customer { return e.w.customers[name] }

func (e *env) pv() *plansv1.PlanVersion {
	for _, v := range e.w.versions {
		return v
	}
	return nil
}

// addUsage sets the on-time usage of a customer in a period.
func (e *env) addUsage(period, customer, meter, qty string, scale uint32) {
	e.w.mu.Lock()
	defer e.w.mu.Unlock()
	e.w.usage[period] = append(e.w.usage[period], &aggregatorv1.UsageAggregate{
		CustomerId: e.customer(customer).Id, MeterCode: meter, PeriodId: period, Quantity: qty, QuantityScale: scale, Generation: 1})
}

func (e *env) addLate(billed, origin, customer, meter, qty string, scale uint32) {
	e.w.mu.Lock()
	defer e.w.mu.Unlock()
	e.w.late[billed] = append(e.w.late[billed], &aggregatorv1.LateUsage{
		CustomerId: e.customer(customer).Id, MeterCode: meter, OriginPeriodId: origin, BilledPeriodId: billed, Quantity: qty, QuantityScale: scale, Generation: 1})
}

// startWorkers runs the stub worker, the reply consumer and the outbox relay (with a Kafka capture sink).
func (e *env) startWorkers(script stubworker.Script) *stubworker.Worker {
	e.t.Helper()
	stub := stubworker.New(shared.amqp, script)
	stub.Log = logger()
	ctx, cancel := context.WithCancel(context.Background())
	e.cancel = append(e.cancel, cancel)
	e.t.Cleanup(cancel)
	go func() { _ = stub.Run(ctx) }()
	e.startConsumers(ctx)
	e.stubs = append(e.stubs, stub)
	return stub
}

func (e *env) startConsumers(ctx context.Context) {
	rc, err := replies.New(shared.system, e.eng, shared.amqp, logger())
	if err != nil {
		e.t.Fatal(err)
	}
	go func() { _ = rc.Run(ctx) }()
	e.startRelay(ctx)
}

func (e *env) startRelay(ctx context.Context) {
	pub := rabbitx.NewPublisher(shared.amqp)
	e.t.Cleanup(pub.Close)
	relay, err := outbox.NewRelay(shared.system, outbox.Router{Kafka: e.kafka, AMQP: outbox.AMQPSink{Publisher: pub}},
		outbox.Config{Workers: 1, BatchSize: 50}, logger())
	if err != nil {
		e.t.Fatal(err)
	}
	go func() { _ = relay.Run(ctx) }()
}

// closeOctober runs the October run to completion of the rating/invoicing stages.
func (e *env) driveRun(period string) *runs.Run {
	e.t.Helper()
	_, end, _ := periodBounds(period)
	if _, err := e.mgr.EnsureRun(context.Background(), e.tid, period, end, false); err != nil {
		e.t.Fatal(err)
	}
	run, err := e.mgr.Drive(context.Background(), e.tid, period)
	if err != nil {
		e.t.Fatal(err)
	}
	return run
}

func periodBounds(period string) (time.Time, time.Time, error) {
	var y, m int
	if _, err := fmt.Sscanf(period, "%d-%d", &y, &m); err != nil {
		return time.Time{}, time.Time{}, err
	}
	start := time.Date(y, time.Month(m), 1, 0, 0, 0, 0, time.UTC)
	return start, start.AddDate(0, 1, 0), nil
}

// eventually polls cond.
func eventually(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out after %s waiting for %s", timeout, what)
}

// sagaStates returns the saga state per customer external id for a period.
func (e *env) sagaStates(period string) map[string]string {
	e.t.Helper()
	rows, err := shared.system.Query(context.Background(), `SELECT i.snapshot->>'customer_external_id', s.state FROM invoices i
		JOIN invoice_sagas s ON s.tenant_id = i.tenant_id AND s.invoice_id = i.id WHERE i.tenant_id = $1 AND i.period_id = $2`, e.tid, period)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var c, s string
		if err := rows.Scan(&c, &s); err != nil {
			e.t.Fatal(err)
		}
		out[c] = s
	}
	return out
}

func (e *env) invoiceTotal(period, customer string) int64 {
	e.t.Helper()
	var total int64
	err := shared.system.QueryRow(context.Background(), `SELECT total_minor FROM invoices WHERE tenant_id = $1 AND period_id = $2 AND snapshot->>'customer_external_id' = $3`,
		e.tid, period, customer).Scan(&total)
	if err != nil {
		e.t.Fatal(err)
	}
	return total
}

func countCommands(w *stubworker.Worker, kind string) int { return w.Count(kind) }

var _ = commandsv1.PaymentStatus_STATE_SUCCEEDED

type timestamppbT = timestamppb.Timestamp

func newTS(t time.Time) *timestamppbT { return timestamppb.New(t) }

type pgxTx = pgx.Tx

func pgxxTx(fn func(pgx.Tx) error) error {
	return pgxx.InTx(context.Background(), shared.system, pgx.TxOptions{}, fn)
}

// startConsumersWith starts the reply consumer and a relay on the given publisher with a crash hook.
func (e *env) startConsumersWith(ctx context.Context, pub *rabbitx.Publisher, after func() error) {
	rc, err := replies.New(shared.system, e.eng, shared.amqp, logger())
	if err != nil {
		e.t.Fatal(err)
	}
	go func() { _ = rc.Run(ctx) }()
	relay, err := outbox.NewRelay(shared.system, outbox.Router{Kafka: e.kafka, AMQP: outbox.AMQPSink{Publisher: pub}},
		outbox.Config{Workers: 1, BatchSize: 50, AfterPublish: after}, logger())
	if err != nil {
		e.t.Fatal(err)
	}
	go func() { _ = relay.Run(ctx) }()
}

// fakeEvents stands in for plans-svc's webhook service: it records what billing hands over, or fails when told to.
type fakeEvents struct {
	mu      sync.Mutex
	got     []*plansv1.RecordTenantEventRequest
	tenants []string // the tenant each recorded event was handed over for
	fail    error
}

func (f *fakeEvents) RecordTenantEvent(ctx context.Context, in *plansv1.RecordTenantEventRequest, _ ...grpc.CallOption) (*plansv1.RecordTenantEventResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return nil, f.fail
	}
	md, _ := metadata.FromOutgoingContext(ctx)
	if t := md.Get(tenant.MDTenantID); len(t) > 0 {
		f.tenants = append(f.tenants, t[0])
	} else {
		f.tenants = append(f.tenants, "")
	}
	f.got = append(f.got, in)
	return &plansv1.RecordTenantEventResponse{Queued: 1}, nil
}

// recorded is what was handed over for one tenant: the hand-over works across tenants, and other tests' sagas may still be finishing.
func (f *fakeEvents) recorded(tenantID uuid.UUID) []*plansv1.RecordTenantEventRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*plansv1.RecordTenantEventRequest
	for i, r := range f.got {
		if f.tenants[i] == tenantID.String() {
			out = append(out, r)
		}
	}
	return out
}

func (f *fakeEvents) failWith(err error) { f.mu.Lock(); f.fail = err; f.mu.Unlock() }
