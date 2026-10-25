//go:build integration

// Package itest runs the aggregator against real Postgres and Redpanda: migrations, the consumer, the batch
// transaction and the read APIs, in-process.
package itest

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"
	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	aggregatorv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/aggregator/v1"
	eventsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/events/v1"
	"github.com/0furkancolak/payrudder/pkg/grpcx"
	"github.com/0furkancolak/payrudder/pkg/kafkax"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/pkg/tenantinfo"
	"github.com/0furkancolak/payrudder/pkg/testkit"
	"github.com/0furkancolak/payrudder/pkg/usage"
	"github.com/0furkancolak/payrudder/services/aggregator/internal/apply"
	"github.com/0furkancolak/payrudder/services/aggregator/internal/grpcapi"
	"github.com/0furkancolak/payrudder/services/aggregator/internal/settings"
	"github.com/0furkancolak/payrudder/services/aggregator/migrations"
)

var (
	top        *testing.T // set by TestAggregator
	sharedOnce sync.Once
	shared     struct {
		pg     testkit.Postgres
		db     testkit.ServiceDB
		kafka  testkit.Redpanda
		system *pgxpool.Pool
		app    *pgxpool.Pool
		reader *sdkmetric.ManualReader
	}
	seq atomic.Int64
)

// fakeMeters stands in for plans-svc.
type fakeMeters map[string]usage.Meter

func (f fakeMeters) Get(_ context.Context, _ uuid.UUID, code string) (usage.Meter, error) {
	if m, ok := f[code]; ok {
		return m, nil
	}
	return usage.Meter{}, apply.ErrMeterNotFound
}

var defaultMeters = fakeMeters{
	"sum_m":   {Code: "sum_m", Aggregation: usage.Sum},
	"count_m": {Code: "count_m", Aggregation: usage.Count},
	"max_m":   {Code: "max_m", Aggregation: usage.Max},
	"eu_only": {Code: "eu_only", Aggregation: usage.Sum, Filter: map[string][]string{"region": {"eu"}}},
}

func infra(t *testing.T) {
	t.Helper()
	sharedOnce.Do(func() {
		shared.reader = sdkmetric.NewManualReader()
		otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(shared.reader)))

		shared.pg = testkit.StartPostgres(top)
		shared.db = testkit.MigratedDB(top, shared.pg, "aggregator", migrations.FS)
		shared.kafka = testkit.StartRedpandaContainer(top)
		var err error
		if shared.system, err = pgxx.NewPool(context.Background(), shared.db.System); err != nil {
			t.Fatal(err)
		}
		if shared.app, err = pgxx.NewPool(context.Background(), shared.db.App); err != nil {
			t.Fatal(err)
		}
	})
}

// counterTotal sums an OTel counter (by instrument name) across all attribute sets.
func counterTotal(t *testing.T, name string) int64 {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := shared.reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	var total int64
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			if sum, ok := m.Data.(metricdata.Sum[int64]); ok {
				for _, dp := range sum.DataPoints {
					total += dp.Value
				}
			}
		}
	}
	return total
}

// run is one consumer deployment under test: its own topics and offsets group, so tests don't see each other's events.
type run struct {
	t        *testing.T
	topic    string
	dlqTopic string
	group    string
	handler  *apply.Handler
	src      *settings.Source
	prod     *kgo.Client
	produced atomic.Int64
	stops    []func()
}

type runOpts struct {
	meters      apply.MeterSource                 // default defaultMeters
	tenants     map[uuid.UUID]tenantinfo.Settings // default: Europe/Istanbul, 1 h grace
	afterCommit func() error
	partitions  int32
}

func newRun(t *testing.T, o runOpts) *run {
	t.Helper()
	infra(t)
	n := seq.Add(1)
	r := &run{t: t, topic: fmt.Sprintf("usage.events.t%d", n), dlqTopic: fmt.Sprintf("usage.events.dlq.t%d", n), group: fmt.Sprintf("agg-t%d", n)}
	if o.partitions == 0 {
		o.partitions = 4
	}
	if o.meters == nil {
		o.meters = defaultMeters
	}
	admin, err := kgo.NewClient(kgo.SeedBrokers(shared.kafka.Brokers))
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	adm := kadm.NewClient(admin)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := adm.CreateTopic(ctx, o.partitions, 1, nil, r.topic); err != nil {
		t.Fatal(err)
	}
	if _, err := adm.CreateTopic(ctx, 1, 1, nil, r.dlqTopic); err != nil {
		t.Fatal(err)
	}
	if r.prod, err = kafkax.NewProducer([]string{shared.kafka.Brokers}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.prod.Close)

	loader := func(_ context.Context, id uuid.UUID) (tenantinfo.Settings, error) {
		if tn, ok := o.tenants[id]; ok {
			return tn, nil
		}
		loc, _ := time.LoadLocation("Europe/Istanbul")
		return tenantinfo.Settings{Timezone: "Europe/Istanbul", Location: loc, Grace: time.Hour}, nil
	}
	r.src = settings.NewSourceFunc(loader, time.Minute)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if os.Getenv("ITEST_VERBOSE") != "" {
		log = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	r.handler = &apply.Handler{
		Pool: shared.system, Meters: o.meters, Settings: r.src, Log: log, ConsumerGroup: r.group, Topic: r.topic,
		DLQ:              kafkax.DLQ{Client: r.prod, Topic: r.dlqTopic, Consumer: r.group},
		MeterRetryDelays: []time.Duration{time.Millisecond},
		AfterCommit:      o.afterCommit,
	}
	return r
}

// start launches a consumer replica. kafkaGroup lets a test run two replicas as independent readers (a forced zombie).
func (r *run) start(kafkaGroup string) {
	r.t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if os.Getenv("ITEST_VERBOSE") != "" {
		log = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	c, err := kafkax.NewConsumer(kafkax.ConsumerConfig{
		Brokers: []string{shared.kafka.Brokers}, GroupID: kafkaGroup, Topic: r.topic, LoadOffsets: r.handler.LoadOffsets,
		Handler: r.handler, MaxPollRecords: 1000, Backoff: 20 * time.Millisecond, Log: log,
	})
	if err != nil {
		r.t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = c.Run(ctx); close(done) }()
	var once sync.Once
	stop := func() { once.Do(func() { cancel(); <-done; c.Close() }) }
	r.stops = append(r.stops, stop)
	r.t.Cleanup(stop)
}

func (r *run) stop() {
	for _, s := range r.stops {
		s()
	}
}

// produce publishes events (key = tenant id, like the ingest relay) and counts them.
func (r *run) produce(events ...*eventsv1.UsageEvent) {
	r.t.Helper()
	recs := make([]*kgo.Record, len(events))
	for i, e := range events {
		b, err := proto.MarshalOptions{Deterministic: true}.Marshal(e)
		if err != nil {
			r.t.Fatal(err)
		}
		recs[i] = &kgo.Record{Topic: r.topic, Key: []byte(e.TenantId), Value: b}
	}
	r.produceRecords(recs...)
}

func (r *run) produceRecords(recs ...*kgo.Record) {
	r.t.Helper()
	for i := 0; i < len(recs); i += 5000 {
		end := min(i+5000, len(recs))
		if err := r.prod.ProduceSync(context.Background(), recs[i:end]...).FirstErr(); err != nil {
			r.t.Fatal(err)
		}
	}
	r.produced.Add(int64(len(recs)))
}

// consumedOffsets is Σ next_offset over the partitions: how many records the aggregator has moved past.
func (r *run) consumedOffsets() int64 {
	var n int64
	err := shared.system.QueryRow(context.Background(),
		`SELECT COALESCE(sum(next_offset), 0)::bigint FROM consumer_offsets WHERE consumer_group = $1 AND topic = $2`, r.group, r.topic).Scan(&n)
	if err != nil {
		r.t.Fatal(err)
	}
	return n
}

// drain waits until every produced record is behind the DB offsets.
func (r *run) drain(timeout time.Duration) {
	r.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if r.consumedOffsets() >= r.produced.Load() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	r.t.Fatalf("not drained after %s: offsets %d of %d produced", timeout, r.consumedOffsets(), r.produced.Load())
}

type totals struct {
	qty    string
	count  int64
	scale  int
	lateOf string // origin period for late rows
}

type rowKey struct {
	tenant   uuid.UUID
	customer uuid.UUID
	meter    string
	period   string // on-time: period; late: billed|origin
}

// onTimeRows reads usage_aggregates (generation 1) for the given tenants.
func onTimeRows(t *testing.T, tenants ...uuid.UUID) map[rowKey]totals {
	t.Helper()
	rows, err := shared.system.Query(context.Background(),
		`SELECT tenant_id, customer_id, meter_code, period_id, quantity::text, event_count, quantity_scale FROM usage_aggregates WHERE tenant_id = ANY($1) AND generation = 1`, tenants)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[rowKey]totals{}
	for rows.Next() {
		var k rowKey
		var tt totals
		if err := rows.Scan(&k.tenant, &k.customer, &k.meter, &k.period, &tt.qty, &tt.count, &tt.scale); err != nil {
			t.Fatal(err)
		}
		out[k] = tt
	}
	return out
}

func lateRows(t *testing.T, tenants ...uuid.UUID) map[rowKey]totals {
	t.Helper()
	rows, err := shared.system.Query(context.Background(),
		`SELECT tenant_id, customer_id, meter_code, billed_period_id, origin_period_id, quantity::text, event_count, quantity_scale FROM late_usage WHERE tenant_id = ANY($1) AND generation = 1`, tenants)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[rowKey]totals{}
	for rows.Next() {
		var k rowKey
		var billed string
		var tt totals
		if err := rows.Scan(&k.tenant, &k.customer, &k.meter, &billed, &tt.lateOf, &tt.qty, &tt.count, &tt.scale); err != nil {
			t.Fatal(err)
		}
		k.period = billed + "|" + tt.lateOf
		out[k] = tt
	}
	return out
}

// ledger accumulates the expected totals of a test.
type ledger struct {
	onTime map[rowKey]*expect
	late   map[rowKey]*expect
}

type expect struct {
	qty   *big.Int
	count int64
	max   bool
}

func newLedger() *ledger { return &ledger{onTime: map[rowKey]*expect{}, late: map[rowKey]*expect{}} }

func (l *ledger) add(m map[rowKey]*expect, k rowKey, agg usage.Aggregation, units int64) {
	e := m[k]
	v := units
	if agg == usage.Count {
		v = 1
	}
	if e == nil {
		m[k] = &expect{qty: big.NewInt(v), count: 1, max: agg == usage.Max}
		return
	}
	e.count++
	if e.max {
		if n := big.NewInt(v); n.Cmp(e.qty) > 0 {
			e.qty = n
		}
		return
	}
	e.qty.Add(e.qty, big.NewInt(v))
}

// compare fails on any difference, in either direction.
func compare(t *testing.T, what string, want map[rowKey]*expect, got map[rowKey]totals) {
	t.Helper()
	bad := 0
	for k, w := range want {
		g, ok := got[k]
		if !ok || g.qty != w.qty.String() || g.count != w.count {
			if bad++; bad <= 10 {
				t.Errorf("%s %v: got %+v (present=%v), want qty=%s count=%d", what, k, g, ok, w.qty, w.count)
			}
		}
	}
	for k, g := range got {
		if _, ok := want[k]; !ok {
			if bad++; bad <= 10 {
				t.Errorf("%s %v: unexpected row %+v", what, k, g)
			}
		}
	}
	if bad > 0 {
		t.Fatalf("%s: %d mismatching keys (of %d expected rows)", what, bad, len(want))
	}
}

// ev builds a UsageEvent.
func ev(id string, tenantID, customer uuid.UUID, meter string, units int64, scale uint32, period string, ingestedAt time.Time) *eventsv1.UsageEvent {
	return &eventsv1.UsageEvent{
		EventId: id, TenantId: tenantID.String(), IdempotencyKey: "k-" + id, CustomerId: customer.String(),
		CustomerExternalId: "cus", MeterCode: meter, EventTime: timestamppb.New(ingestedAt.Add(-time.Second)),
		IngestedAt: timestamppb.New(ingestedAt), QuantityUnits: units, QuantityScale: scale, PeriodId: period, MeterVersion: 1,
	}
}

// queryClient serves grpcapi.Server over bufconn, with the tenant metadata the gateway would send.
func queryClient(t *testing.T, r *run) aggregatorv1.UsageQueryServiceClient {
	t.Helper()
	svc := grpcapi.New(shared.app, r.src, r.group, r.topic)
	srv, _ := grpcx.NewServer(grpcx.ServerOptions{Extra: []grpc.ServerOption{
		grpc.ChainUnaryInterceptor(tenant.UnaryServerInterceptor()),
		grpc.ChainStreamInterceptor(tenant.StreamServerInterceptor()),
	}})
	aggregatorv1.RegisterUsageQueryServiceServer(srv, svc)
	lis := bufconn.Listen(1 << 20)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	cc, err := grpcx.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cc.Close() })
	return aggregatorv1.NewUsageQueryServiceClient(cc)
}

func as(tenantID uuid.UUID, environment string) context.Context {
	return metadata.NewOutgoingContext(context.Background(), metadata.Pairs(
		tenant.MDTenantID, tenantID.String(), tenant.MDEnvironment, environment))
}

// consumeTopic reads every record of a topic from the start (used for the DLQ).
func consumeTopic(t *testing.T, topic string, wait time.Duration) []*kgo.Record {
	t.Helper()
	cl, err := kgo.NewClient(kgo.SeedBrokers(shared.kafka.Brokers), kgo.ConsumeTopics(topic),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()))
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	var out []*kgo.Record
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		cl.PollFetches(ctx).EachRecord(func(r *kgo.Record) { out = append(out, r) })
		cancel()
	}
	return out
}

var _ = pgx.ErrNoRows

func marshal(e *eventsv1.UsageEvent) ([]byte, error) {
	return proto.MarshalOptions{Deterministic: true}.Marshal(e)
}
