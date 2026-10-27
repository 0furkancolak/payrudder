//go:build integration

// Package itest runs limits-svc against real Redis and Redpanda, in-process: the Lua script, the consumers, thresholds,
// the entitlement map and reconciliation (with a fake aggregator).
package itest

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	aggregatorv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/aggregator/v1"
	eventsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/events/v1"
	"github.com/0furkancolak/payrudder/pkg/kafkax"
	"github.com/0furkancolak/payrudder/pkg/period"
	"github.com/0furkancolak/payrudder/pkg/quantity"
	"github.com/0furkancolak/payrudder/pkg/tenantinfo"
	"github.com/0furkancolak/payrudder/pkg/testkit"
	"github.com/0furkancolak/payrudder/pkg/usage"
	"github.com/0furkancolak/payrudder/services/limits-svc/internal/engine"
	"github.com/0furkancolak/payrudder/services/limits-svc/internal/entitlements"
)

var (
	top        *testing.T
	sharedOnce sync.Once
	shared     struct {
		redisURL string
		kafka    testkit.Redpanda
		rdb      *redis.Client
	}
	seq atomic.Int64

	istanbul, _ = time.LoadLocation("Europe/Istanbul")
)

type fakeMeters map[string]usage.Meter

func (f fakeMeters) Get(_ context.Context, _ uuid.UUID, code string) (usage.Meter, error) {
	if m, ok := f[code]; ok {
		return m, nil
	}
	return usage.Meter{}, engine.ErrMeterNotFound
}

var defaultMeters = fakeMeters{
	"api":    {Code: "api", Aggregation: usage.Sum},
	"calls":  {Code: "calls", Aggregation: usage.Count},
	"peak":   {Code: "peak", Aggregation: usage.Max},
	"gb":     {Code: "gb", Aggregation: usage.Sum, Scale: 3},
	"eu_api": {Code: "eu_api", Aggregation: usage.Sum, Filter: map[string][]string{"region": {"eu"}}},
}

func fixedTenants() tenantinfo.Source {
	return tenantinfo.NewFunc(func(context.Context, uuid.UUID) (tenantinfo.Settings, error) {
		return tenantinfo.Settings{Location: istanbul, Grace: time.Hour}, nil
	}, time.Minute)
}

func infra(t *testing.T) {
	t.Helper()
	sharedOnce.Do(func() {
		shared.redisURL = testkit.StartRedis(top)
		shared.kafka = testkit.StartRedpandaContainer(top)
		opts, err := redis.ParseURL(shared.redisURL)
		if err != nil {
			t.Fatal(err)
		}
		shared.rdb = redis.NewClient(opts)
	})
}

// env is one test's isolated deployment: its own topics, consumer groups and an empty Redis.
type env struct {
	t          *testing.T
	usageTopic string
	entTopic   string
	limitTopic string
	group      string
	prod       *kgo.Client
	produced   atomic.Int64
	meters     fakeMeters
	pods       int
}

func newEnv(t *testing.T) *env {
	t.Helper()
	infra(t)
	if err := shared.rdb.FlushAll(context.Background()).Err(); err != nil {
		t.Fatal(err)
	}
	n := seq.Add(1)
	e := &env{
		t: t, usageTopic: fmt.Sprintf("usage.events.l%d", n), entTopic: fmt.Sprintf("plans.entitlements.l%d", n),
		limitTopic: fmt.Sprintf("usage.limit-exceeded.l%d", n), group: fmt.Sprintf("limits-l%d", n), meters: defaultMeters,
	}
	admin, err := kgo.NewClient(kgo.SeedBrokers(shared.kafka.Brokers))
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	adm := kadm.NewClient(admin)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for topic, parts := range map[string]int32{e.usageTopic: 4, e.entTopic: 2, e.limitTopic: 1} {
		if _, err := adm.CreateTopic(ctx, parts, 1, nil, topic); err != nil {
			t.Fatal(err)
		}
	}
	if e.prod, err = kafkax.NewProducer([]string{shared.kafka.Brokers}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.prod.Close)
	return e
}

// pod is one limits-svc replica.
type pod struct {
	e       *env
	name    string
	ents    *entitlements.Store
	applier *engine.Applier
	owned   *engine.Ownership
	stop    func()
}

type podOpts struct {
	afterLua func() error
}

// startPod runs the two consumers of a replica. The pod is ready when its entitlement map has caught up.
func (e *env) startPod(o podOpts) *pod {
	e.t.Helper()
	e.pods++
	p := &pod{e: e, name: fmt.Sprintf("pod%d", e.pods), owned: engine.NewOwnership()}
	p.ents = entitlements.New(shared.rdb, testkit.Logger())
	var err error
	p.applier, err = engine.NewApplier(engine.Config{
		Redis: shared.rdb, Meters: e.meters, Tenants: fixedTenants(), Ents: p.ents, Producer: e.prod, LimitTopic: e.limitTopic,
		Log: testkit.Logger(), AfterLua: o.afterLua,
	})
	if err != nil {
		e.t.Fatal(err)
	}
	if err := p.applier.Load(context.Background()); err != nil {
		e.t.Fatal(err)
	}
	usage, err := kafkax.NewConsumer(kafkax.ConsumerConfig{
		Brokers: []string{shared.kafka.Brokers}, GroupID: e.group, Topic: e.usageTopic, LoadOffsets: p.applier.LoadOffsets,
		Handler: p.applier, MaxPollRecords: 1000, Backoff: 20 * time.Millisecond, SessionTimeout: 6 * time.Second,
		Log: testkit.Logger(), Extra: p.owned.Opts(e.usageTopic),
	})
	if err != nil {
		e.t.Fatal(err)
	}
	entC, err := kafkax.NewConsumer(kafkax.ConsumerConfig{
		Brokers: []string{shared.kafka.Brokers}, GroupID: "limits-ent-" + e.group + "-" + p.name, Topic: e.entTopic,
		LoadOffsets: entitlements.OffsetLoader, Handler: p.ents, MaxPollRecords: 1000, Backoff: 20 * time.Millisecond, Log: testkit.Logger(),
	})
	if err != nil {
		e.t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _ = entC.Run(ctx) }()
	go func() {
		defer wg.Done()
		// Like main: usage is applied once the entitlement map has caught up.
		adm := kadm.NewClient(entC.Client())
		for ctx.Err() == nil {
			ends, err := adm.ListEndOffsets(ctx, e.entTopic)
			if err == nil && ends.Error() == nil {
				targets := map[int32]int64{}
				ends.Each(func(o kadm.ListedOffset) { targets[o.Partition] = o.Offset })
				p.ents.SetTargets(targets)
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		for ctx.Err() == nil && !p.ents.Ready() {
			time.Sleep(20 * time.Millisecond)
		}
		_ = usage.Run(ctx)
	}()
	var once sync.Once
	p.stop = func() { once.Do(func() { cancel(); wg.Wait(); usage.Close(); entC.Close() }) }
	e.t.Cleanup(p.stop)
	return p
}

// event builds a usage event ingested now, in the current period of the test tenants' time zone.
func (e *env) event(tenantID, customer uuid.UUID, meter string, units int64, scale uint32, props map[string]string) *eventsv1.UsageEvent {
	now := time.Now().UTC()
	id := fmt.Sprintf("ev%d", e.produced.Add(1))
	return &eventsv1.UsageEvent{
		EventId: id, TenantId: tenantID.String(), IdempotencyKey: id, CustomerId: customer.String(), MeterCode: meter,
		EventTime: timestamppb.New(now), IngestedAt: timestamppb.New(now), Properties: props, QuantityUnits: units,
		QuantityScale: scale, PeriodId: period.PeriodOf(now, istanbul), MeterVersion: 1,
	}
}

func (e *env) produce(events ...*eventsv1.UsageEvent) {
	e.t.Helper()
	recs := make([]*kgo.Record, len(events))
	for i, ev := range events {
		b, err := proto.MarshalOptions{Deterministic: true}.Marshal(ev)
		if err != nil {
			e.t.Fatal(err)
		}
		recs[i] = &kgo.Record{Topic: e.usageTopic, Key: []byte(ev.TenantId), Value: b}
	}
	if err := e.prod.ProduceSync(context.Background(), recs...).FirstErr(); err != nil {
		e.t.Fatal(err)
	}
}

// setEntitlement publishes an entitlement the way plans-svc's relay does.
func (e *env) setEntitlement(tenantID, customer uuid.UUID, meter string, limit int64, scale uint32, thresholds ...int32) {
	e.t.Helper()
	st := &eventsv1.EntitlementState{
		TenantId: tenantID.String(), CustomerId: customer.String(), MeterCode: meter, PlanVersionId: uuid.NewString(),
		LimitUnits: limit, AlertThresholdsPct: thresholds, ValidFrom: timestamppb.New(time.Now().Add(-time.Hour)), QuantityScale: scale,
	}
	b, err := proto.Marshal(st)
	if err != nil {
		e.t.Fatal(err)
	}
	e.produceEntitlement(fmt.Sprintf("%s/%s/%s", tenantID, customer, meter), b)
}

func (e *env) produceEntitlement(key string, value []byte) {
	e.t.Helper()
	if err := e.prod.ProduceSync(context.Background(), &kgo.Record{Topic: e.entTopic, Key: []byte(key), Value: value}).FirstErr(); err != nil {
		e.t.Fatal(err)
	}
}

// counter reads a counter in scaled units (0 when absent).
func counter(t *testing.T, tenantID, customer uuid.UUID, meter string) int64 {
	t.Helper()
	k := engine.CounterKey(tenantID, customer, meter, period.PeriodOf(time.Now(), istanbul))
	v, err := shared.rdb.Get(context.Background(), k).Int64()
	if err == redis.Nil {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// readLimitEvents returns the LimitExceeded events on the test's topic, waiting for at least min of them and then
// settle longer for any extra.
func (e *env) readLimitEvents(min int, settle time.Duration) []*eventsv1.LimitExceeded {
	e.t.Helper()
	cl, err := kgo.NewClient(kgo.SeedBrokers(shared.kafka.Brokers), kgo.ConsumeTopics(e.limitTopic),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()))
	if err != nil {
		e.t.Fatal(err)
	}
	defer cl.Close()
	var out []*eventsv1.LimitExceeded
	deadline := time.Now().Add(30 * time.Second)
	quiet := time.Time{}
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		fs := cl.PollFetches(ctx)
		cancel()
		fs.EachRecord(func(r *kgo.Record) {
			var le eventsv1.LimitExceeded
			if err := proto.Unmarshal(r.Value, &le); err != nil {
				e.t.Fatal(err)
			}
			out = append(out, &le)
			quiet = time.Time{}
		})
		if len(out) >= min {
			if quiet.IsZero() {
				quiet = time.Now()
			}
			if time.Since(quiet) >= settle {
				return out
			}
		}
	}
	return out
}

// fakeAggregator answers GetUsageBatch from a table of totals, with chosen consumer offsets.
type fakeAggregator struct {
	aggregatorv1.UsageQueryServiceClient
	mu      sync.Mutex
	totals  map[string]int64 // counter key -> scaled units
	scale   map[string]int
	offsets map[int32]int64
}

func (f *fakeAggregator) GetUsageBatch(_ context.Context, req *aggregatorv1.GetUsageBatchRequest, _ ...grpc.CallOption) (*aggregatorv1.GetUsageBatchResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	resp := &aggregatorv1.GetUsageBatchResponse{ConsumerOffsets: map[int32]int64{}}
	for p, o := range f.offsets {
		resp.ConsumerOffsets[p] = o
	}
	for _, k := range req.GetKeys() {
		key := f.keyFor(k)
		units := f.totals[key]
		sc := f.scale[key]
		resp.Usage = append(resp.Usage, &aggregatorv1.UsageAggregate{
			CustomerId: k.GetCustomerId(), MeterCode: k.GetMeterCode(), PeriodId: k.GetPeriodId(),
			Quantity: quantity.FormatUnits(units, sc), QuantityScale: uint32(sc), //nolint:gosec // 0..6
		})
	}
	return resp, nil
}

// keyFor finds the table entry of an aggregator key regardless of tenant (tests use one tenant per fake).
func (f *fakeAggregator) keyFor(k *aggregatorv1.UsageKey) string {
	for key := range f.totals {
		if ref, ok := engine.ParseKey(key); ok && ref.Customer.String() == k.GetCustomerId() && ref.Meter == k.GetMeterCode() && ref.Period == k.GetPeriodId() {
			return key
		}
	}
	return ""
}

// startPodNoConsume builds a replica without consumers: tests feed Apply directly. The entitlement map is fed from
// the topic through a consumer, so setEntitlement works.
func (e *env) startPodNoConsume(o podOpts) *pod {
	e.t.Helper()
	e.pods++
	p := &pod{e: e, name: fmt.Sprintf("pod%d", e.pods), owned: engine.NewOwnership()}
	p.ents = entitlements.New(shared.rdb, testkit.Logger())
	var err error
	p.applier, err = engine.NewApplier(engine.Config{
		Redis: shared.rdb, Meters: e.meters, Tenants: fixedTenants(), Ents: p.ents, Producer: e.prod, LimitTopic: e.limitTopic,
		Log: testkit.Logger(), AfterLua: o.afterLua,
	})
	if err != nil {
		e.t.Fatal(err)
	}
	if err := p.applier.Load(context.Background()); err != nil {
		e.t.Fatal(err)
	}
	entC, err := kafkax.NewConsumer(kafkax.ConsumerConfig{
		Brokers: []string{shared.kafka.Brokers}, GroupID: "limits-ent-" + e.group + "-" + p.name, Topic: e.entTopic,
		LoadOffsets: entitlements.OffsetLoader, Handler: p.ents, MaxPollRecords: 1000, Backoff: 20 * time.Millisecond, Log: testkit.Logger(),
	})
	if err != nil {
		e.t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = entC.Run(ctx); close(done) }()
	var once sync.Once
	p.stop = func() { once.Do(func() { cancel(); <-done; entC.Close() }) }
	e.t.Cleanup(p.stop)
	return p
}

// eventuallyEnts waits for the pod's entitlement map to hold n entitlements.
func eventuallyEnts(t *testing.T, _ *env, p *pod, n int) {
	t.Helper()
	testkit.Eventually(t, 20*time.Second, "entitlements", func() bool { return p.ents.Len() == n })
}

// reconciler builds a Reconciler over the fake aggregator for the given partitions.
func (e *env) reconciler(p *pod, agg *fakeAggregator, parts ...int32) *engine.Reconciler {
	return engine.NewReconciler(engine.ReconcilerConfig{
		Applier: p.applier, Redis: shared.rdb, Aggregator: agg, Meters: e.meters,
		Owned: func() []int32 { return parts }, CheckpointTimeout: 5 * time.Second, Log: testkit.Logger(),
	})
}
