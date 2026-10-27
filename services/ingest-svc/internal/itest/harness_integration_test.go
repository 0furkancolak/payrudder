//go:build integration

// Package itest runs ingest-svc against real Postgres and Redpanda: migrations, both pools, the service and the
// outbox relay in-process.
package itest

import (
	"context"
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
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"
	"go.opentelemetry.io/otel/propagation"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	eventsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/events/v1"
	ingestv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/ingest/v1"
	"github.com/0furkancolak/payrudder/pkg/kafkax"
	"github.com/0furkancolak/payrudder/pkg/outbox"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/pkg/tenantdir"
	"github.com/0furkancolak/payrudder/pkg/testkit"
	"github.com/0furkancolak/payrudder/services/ingest-svc/internal/events/command"
	"github.com/0furkancolak/payrudder/services/ingest-svc/internal/grpcapi"
	"github.com/0furkancolak/payrudder/services/ingest-svc/internal/lookup"
	"github.com/0furkancolak/payrudder/services/ingest-svc/internal/maintenance"
	"github.com/0furkancolak/payrudder/services/ingest-svc/internal/store/sqlc"
	"github.com/0furkancolak/payrudder/services/ingest-svc/migrations"
)

var (
	top        *testing.T // set by TestIngestSvc
	sharedOnce sync.Once
	shared     struct {
		db     testkit.ServiceDB
		kafka  testkit.Redpanda
		app    *pgxpool.Pool
		system *pgxpool.Pool
		// Cells mode (ITEST_CELLS=1, ADR 0018): every tenant of the suite lives in a schema of a second database, placed on first use. appDB is the app
		// pool's router then, router the system pool's.
		appDB   pgxx.Beginner
		router  *pgxx.Router
		cellSys *pgxpool.Pool
		dir     *lazyCells
	}
)

// cellsMode runs the whole suite with every tenant in a cell instead of the pooled database: ITEST_CELLS=1 go test -tags integration ./internal/itest.
func cellsMode() bool { return os.Getenv("ITEST_CELLS") != "" }

// lazyCells is the directory of the cells-mode suite: a tenant is placed in cell c1 (and its schema provisioned) the first time anything asks where it is, so the
// scenarios, which make up their tenants as they go, need no change.
type lazyCells struct {
	mu       sync.Mutex
	migrator string
	placed   map[uuid.UUID]bool
	fail     error
}

func (l *lazyCells) Placement(ctx context.Context, id uuid.UUID) (tenantdir.Placement, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.placed[id] {
		ts := pgxx.TenantSchemas{Migrations: migrations.Tenant(), Roles: []string{"ingest_app", "ingest_system"}}
		if _, err := pgxx.ProvisionTenant(ctx, l.migrator, ts, id, nil); err != nil {
			return tenantdir.Placement{}, err
		}
		l.placed[id] = true
	}
	return tenantdir.Placement{TenantID: id, Cell: "c1", Status: tenantdir.StatusReady}, nil
}

func (l *lazyCells) Invalidate(uuid.UUID) {}

func (l *lazyCells) Placed(ctx context.Context, _ string) ([]tenantdir.Placement, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []tenantdir.Placement
	for id := range l.placed {
		out = append(out, tenantdir.Placement{TenantID: id, Cell: "c1", Status: tenantdir.StatusReady})
	}
	return out, nil
}

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
		shared.db = testkit.MigratedDB(top, pg, "ingest", migrations.FS)
		shared.kafka = testkit.StartRedpandaContainer(top)
		createTopic(t)
		var err error
		if shared.app, err = pgxx.NewPool(context.Background(), shared.db.App); err != nil {
			t.Fatal(err)
		}
		if shared.system, err = pgxx.NewPool(context.Background(), shared.db.System); err != nil {
			t.Fatal(err)
		}
		shared.appDB = shared.app
		if cellsMode() {
			cell := testkit.CreateCellDB(top, pg, "ingest", "c1")
			cellApp, err := pgxx.NewPool(context.Background(), cell.App)
			if err != nil {
				t.Fatal(err)
			}
			cellSystem, err := pgxx.NewPool(context.Background(), cell.System)
			if err != nil {
				t.Fatal(err)
			}
			shared.cellSys = cellSystem
			shared.dir = &lazyCells{migrator: cell.Migrator, placed: map[uuid.UUID]bool{}}
			appRouter, err := pgxx.NewRouter(shared.app, map[string]*pgxpool.Pool{"c1": cellApp}, shared.dir)
			if err != nil {
				t.Fatal(err)
			}
			if shared.router, err = pgxx.NewRouter(shared.system, map[string]*pgxpool.Pool{"c1": cellSystem}, shared.dir); err != nil {
				t.Fatal(err)
			}
			shared.appDB = appRouter
		}
	})
	// Tests start from an empty outbox. Idempotency keys are per tenant and every test uses fresh tenants.
	if _, err := shared.system.Exec(context.Background(), `DELETE FROM outbox`); err != nil {
		t.Fatal(err)
	}
	if shared.router != nil {
		err := shared.router.EachReady(context.Background(), func(ctx context.Context, p pgxx.PlacedTenant) error {
			return pgxx.InTenantSchemaTx(ctx, p.Pool, p.TenantID, func(tx pgx.Tx) error {
				_, err := tx.Exec(ctx, `DELETE FROM outbox`)
				return err
			})
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

// prepareTenant makes the partitions of the (fake) month the tenant is about to write in exist: the shared table's in the pooled database, and in cells mode the
// tenant's own, which first needs its schema.
func prepareTenant(t *testing.T, tc tenantCtx, now time.Time) {
	t.Helper()
	ctx := context.Background()
	if _, _, err := maintenance.Once(ctx, shared.system, now); err != nil {
		t.Fatal(err)
	}
	if shared.router != nil {
		if _, err := shared.dir.Placement(ctx, tc.id); err != nil {
			t.Fatal(err)
		}
		if _, _, err := maintenance.OnceCells(ctx, shared.router, now); err != nil {
			t.Fatal(err)
		}
	}
}

// oldestIngested is min(ingested_at) over every outbox of the mode.
func oldestIngested(t *testing.T) time.Time {
	t.Helper()
	ctx := context.Background()
	var pooled *time.Time
	if err := shared.system.QueryRow(ctx, `SELECT min(ingested_at) FROM outbox`).Scan(&pooled); err != nil {
		t.Fatal(err)
	}
	var out time.Time
	if pooled != nil {
		out = *pooled
	}
	if shared.router != nil {
		oldest, ok, err := outbox.CellsWatermark(ctx, shared.router, 0)
		if err != nil {
			t.Fatal(err)
		}
		if ok && (pooled == nil || oldest.Before(out)) {
			out = oldest
		}
	}
	return out
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
	if _, err := kadm.NewClient(cl).CreateTopic(ctx, 12, 1, nil, command.Topic); err != nil {
		t.Fatalf("create topic: %v", err)
	}
}

type testOpts struct {
	clock          *clock
	lookups        command.Lookups
	maxOutboxDepth int64
	propagator     propagation.TextMapPropagator
}

func newService(t *testing.T, o testOpts) *grpcapi.Server {
	t.Helper()
	infra(t)
	opts := grpcapi.Options{
		App: shared.appDB, System: shared.system, Cells: shared.router, Lookup: o.lookups, MaxOutboxDepth: o.maxOutboxDepth,
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
	svc, err := grpcapi.New(opts)
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

func ingestOne(t *testing.T, svc *grpcapi.Server, tc tenantCtx, ev *ingestv1.IncomingEvent) *ingestv1.EventResult {
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
	count := func(ctx context.Context, q interface {
		QueryRow(context.Context, string, ...any) pgx.Row
	}) error {
		return q.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE tenant_id = $1`, tenantID).Scan(&n)
	}
	if shared.router != nil {
		// A tenant that was never placed (nothing of it got past the lookup) has no schema and no rows.
		var reg *string
		if err := shared.cellSys.QueryRow(context.Background(), `SELECT to_regclass($1)::text`, tenantdir.Schema(tenantID)+".outbox").Scan(&reg); err != nil {
			t.Fatal(err)
		}
		if reg == nil {
			return 0
		}
		err := pgxx.InTenantSchemaTx(context.Background(), shared.cellSys, tenantID, func(tx pgx.Tx) error { return count(context.Background(), tx) })
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	if err := count(context.Background(), shared.system); err != nil {
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
	if shared.router != nil && cfg.CellSweep == 0 {
		cfg.CellSweep = 100 * time.Millisecond
	}
	r, err := outbox.NewRelay(shared.system, outbox.KafkaSink{Client: producer}, cfg, log)
	if err != nil {
		t.Fatal(err)
	}
	r.WithCells(shared.router)
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
	cl, err := kgo.NewClient(kgo.SeedBrokers(shared.kafka.Brokers), kgo.ConsumeTopics(command.Topic),
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

func timestamppbAt(t time.Time) *timestamppb.Timestamp { return timestamppb.New(t) }
