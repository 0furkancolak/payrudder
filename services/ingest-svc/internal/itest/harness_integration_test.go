//go:build integration

// Package itest runs ingest-svc against real Postgres and Redpanda: migrations, both pools, the service and the
// outbox relay in-process.
package itest

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"
	"go.opentelemetry.io/otel/propagation"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	eventsv1 "github.com/0furkancolak/quotify/gen/go/quotify/events/v1"
	ingestv1 "github.com/0furkancolak/quotify/gen/go/quotify/ingest/v1"
	"github.com/0furkancolak/quotify/pkg/kafkax"
	"github.com/0furkancolak/quotify/pkg/outbox"
	"github.com/0furkancolak/quotify/pkg/pgxx"
	"github.com/0furkancolak/quotify/pkg/tenant"
	"github.com/0furkancolak/quotify/pkg/testkit"
	"github.com/0furkancolak/quotify/services/ingest-svc/internal/ingest"
	"github.com/0furkancolak/quotify/services/ingest-svc/internal/lookup"
	"github.com/0furkancolak/quotify/services/ingest-svc/internal/store/sqlc"
	"github.com/0furkancolak/quotify/services/ingest-svc/migrations"
)

var (
	top        *testing.T // set by TestIngestSvc
	sharedOnce sync.Once
	shared     struct {
		db     testkit.ServiceDB
		kafka  testkit.Redpanda
		app    *pgxpool.Pool
		system *pgxpool.Pool
	}
)

// fakeLookups stands in for plans-svc (covered by the e2e suite).
type fakeLookups struct {
	err error
}

func (f fakeLookups) Meter(_ context.Context, _, code string) (lookup.Meter, bool, error) {
	switch {
	case f.err != nil:
		return lookup.Meter{}, false, f.err
	case code == "gb_hours":
		return lookup.Meter{Code: code, Version: 2, Scale: 3}, true, nil
	case code == "api_calls":
		return lookup.Meter{Code: code, Version: 1, Scale: 0}, true, nil
	}
	return lookup.Meter{}, false, nil
}

func (f fakeLookups) Customer(_ context.Context, tenantID, ext string) (lookup.Customer, bool, error) {
	switch {
	case f.err != nil:
		return lookup.Customer{}, false, f.err
	case len(ext) > 4 && ext[:4] == "cus_":
		return lookup.Customer{ID: uuid.NewSHA1(uuid.MustParse(tenantID), []byte(ext)).String(), ExternalID: ext}, true, nil
	}
	return lookup.Customer{}, false, nil
}

// clock is a settable time source.
type clock struct{ t atomic.Pointer[time.Time] }

func newClock(t time.Time) *clock { c := &clock{}; c.Set(t); return c }
func (c *clock) Set(t time.Time)  { c.t.Store(&t) }
func (c *clock) Now() time.Time   { return *c.t.Load() }

// infra starts (once per package) Postgres with a migrated ingest_db and Redpanda.
func infra(t *testing.T) {
	t.Helper()
	sharedOnce.Do(func() {
		pg := testkit.StartPostgres(top)
		shared.db = testkit.CreateServiceDB(top, pg, "ingest")
		if err := pgxx.Migrate(context.Background(), shared.db.Migrator, migrations.FS, nil); err != nil {
			t.Fatalf("migrate: %v", err)
		}
		shared.kafka = testkit.StartRedpandaContainer(top)
		createTopic(t)
		var err error
		if shared.app, err = pgxx.NewPool(context.Background(), shared.db.App); err != nil {
			t.Fatal(err)
		}
		if shared.system, err = pgxx.NewPool(context.Background(), shared.db.System); err != nil {
			t.Fatal(err)
		}
	})
	// Tests start from an empty outbox. Idempotency keys are per tenant and every test uses fresh tenants.
	if _, err := shared.system.Exec(context.Background(), `DELETE FROM outbox`); err != nil {
		t.Fatal(err)
	}
}

// createTopic creates usage.events.v1 with its production partition count: producers never auto-create topics.
func createTopic(t *testing.T) {
	t.Helper()
	cl, err := kgo.NewClient(kgo.SeedBrokers(shared.kafka.Brokers))
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := kadm.NewClient(cl).CreateTopic(ctx, 12, 1, nil, ingest.Topic); err != nil {
		t.Fatalf("create topic: %v", err)
	}
}

type testOpts struct {
	clock          *clock
	lookups        ingest.Lookups
	maxOutboxDepth int64
	propagator     propagation.TextMapPropagator
}

func newService(t *testing.T, o testOpts) *ingest.Service {
	t.Helper()
	infra(t)
	opts := ingest.Options{
		App: shared.app, System: shared.system, Lookup: o.lookups, MaxOutboxDepth: o.maxOutboxDepth,
		DepthRefresh: 50 * time.Millisecond, Propagator: o.propagator,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if os.Getenv("ITEST_VERBOSE") != "" {
		opts.Log = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	if opts.Lookup == nil {
		opts.Lookup = fakeLookups{}
	}
	if o.clock != nil {
		opts.Now = o.clock.Now
		opts.NowInTx = func(context.Context, *sqlc.Queries) (time.Time, error) { return o.clock.Now(), nil }
	}
	svc, err := ingest.New(opts)
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

type tenantCtx struct {
	id  uuid.UUID
	ctx context.Context
}

func newTenant(environment string) tenantCtx {
	id := uuid.New()
	return tenantCtx{id: id, ctx: tenant.NewContext(context.Background(),
		tenant.Info{ID: id, Environment: environment, Timezone: "Europe/Istanbul"})}
}

func event(key, qty string, at time.Time) *ingestv1.IncomingEvent {
	return &ingestv1.IncomingEvent{
		IdempotencyKey: key, CustomerExternalId: "cus_1", MeterCode: "gb_hours", Quantity: qty,
		EventTime: timestamppb.New(at),
	}
}

func ingestOne(t *testing.T, svc *ingest.Service, tc tenantCtx, ev *ingestv1.IncomingEvent) *ingestv1.EventResult {
	t.Helper()
	resp, err := svc.IngestEvents(tc.ctx, &ingestv1.IngestEventsRequest{Events: []*ingestv1.IncomingEvent{ev}})
	if err != nil {
		t.Fatalf("IngestEvents: %v", err)
	}
	return resp.Results[0]
}

func outboxRows(t *testing.T, tenantID uuid.UUID) int {
	t.Helper()
	var n int
	if err := shared.system.QueryRow(context.Background(), `SELECT count(*) FROM outbox WHERE tenant_id = $1`, tenantID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// relay starts a relay (to the shared Redpanda) and returns a stop function that waits for it to exit.
func startRelay(t *testing.T, cfg outbox.Config) (stop func()) {
	t.Helper()
	producer, err := kafkax.NewProducer([]string{shared.kafka.Brokers})
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if os.Getenv("ITEST_VERBOSE") != "" {
		log = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	r, err := outbox.NewRelay(shared.system, outbox.KafkaSink{Client: producer}, cfg, log)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = r.Run(ctx); close(done) }()
	var once sync.Once
	stop = func() {
		once.Do(func() {
			cancel()
			<-done
			producer.Close()
		})
	}
	t.Cleanup(stop)
	return stop
}

// waitOutboxEmpty blocks until the tenant has no unpublished rows.
func waitOutboxEmpty(t *testing.T, tenantID uuid.UUID, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if outboxRows(t, tenantID) == 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("outbox of %s not drained after %s (%d rows left)", tenantID, timeout, outboxRows(t, tenantID))
}

// consume reads usage.events.v1 from the start and returns the records of the tenant (partition key = tenant id).
// It stops when want records were seen and quiet has passed without news, or at timeout.
func consume(t *testing.T, tenantID uuid.UUID, want int, quiet, timeout time.Duration) []*kgo.Record {
	t.Helper()
	cl, err := kgo.NewClient(kgo.SeedBrokers(shared.kafka.Brokers), kgo.ConsumeTopics(ingest.Topic),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()))
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	var out []*kgo.Record
	deadline := time.Now().Add(timeout)
	lastNew := time.Now()
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		fetches := cl.PollFetches(ctx)
		cancel()
		fetches.EachRecord(func(r *kgo.Record) {
			if string(r.Key) == tenantID.String() {
				out = append(out, r)
				lastNew = time.Now()
			}
		})
		if len(out) >= want && time.Since(lastNew) >= quiet {
			break
		}
	}
	return out
}

func decode(t *testing.T, r *kgo.Record) *eventsv1.UsageEvent {
	t.Helper()
	e := &eventsv1.UsageEvent{}
	if err := proto.Unmarshal(r.Value, e); err != nil {
		t.Fatal(err)
	}
	return e
}

// dockerPause freezes or thaws the Redpanda container.
func dockerPause(t *testing.T, pause bool) {
	t.Helper()
	verb := "unpause"
	if pause {
		verb = "pause"
	}
	if out, err := exec.Command("docker", verb, shared.kafka.ContainerID).CombinedOutput(); err != nil {
		t.Fatalf("docker %s: %v: %s", verb, err, out)
	}
}

func timestamppbAt(t time.Time) *timestamppb.Timestamp { return timestamppb.New(t) }
