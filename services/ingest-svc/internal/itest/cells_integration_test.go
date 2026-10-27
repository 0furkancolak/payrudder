//go:build integration

package itest

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	ingestv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/ingest/v1"
	"github.com/0furkancolak/payrudder/pkg/outbox"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/tenantdir"
	"github.com/0furkancolak/payrudder/pkg/testkit"
	"github.com/0furkancolak/payrudder/services/ingest-svc/internal/grpcapi"
	"github.com/0furkancolak/payrudder/services/ingest-svc/internal/maintenance"
	"github.com/0furkancolak/payrudder/services/ingest-svc/internal/sandboxreset"
	"github.com/0furkancolak/payrudder/services/ingest-svc/internal/store/sqlc"
	"github.com/0furkancolak/payrudder/services/ingest-svc/migrations"
)

// Phase 28 stage 3: ingest-svc in both isolation modes. The same scenarios run with every tenant in the pooled database (RLS) and with every tenant in a cell (its
// own schema), over the same service code; they need only Postgres, so a recording sink stands in for Kafka (the Kafka suite, TestIngestSvc, is the pooled mode's
// end to end).

// recSink records what the relay published.
type recSink struct {
	mu   sync.Mutex
	rows []outbox.Row
}

func (s *recSink) Publish(_ context.Context, rows []outbox.Row) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows = append(s.rows, rows...)
	return nil
}

func (s *recSink) count(tenantID uuid.UUID) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, r := range s.rows {
		if r.TenantID == tenantID.String() {
			n++
		}
	}
	return n
}

// dual is one isolation mode's environment.
type dual struct {
	cells  bool
	db     testkit.ServiceDB
	cell   testkit.ServiceDB
	app    *pgxpool.Pool // ingest_app in the pooled database
	system *pgxpool.Pool // ingest_system in the pooled database
	cellAp *pgxpool.Pool
	cellSy *pgxpool.Pool
	dir    *testkit.Directory
	appDB  pgxx.Beginner
	router *pgxx.Router // the system-role router; nil in pooled mode
	clk    *clock
	sink   *recSink
	relay  *outbox.Relay
}

func newDual(t *testing.T, cells bool) *dual {
	t.Helper()
	ctx := testkit.Ctx(t)
	pg := testkit.StartPostgres(t)
	d := &dual{cells: cells, dir: testkit.NewDirectory(), clk: newClock(time.Now()), sink: &recSink{}}
	d.db = testkit.MigratedDB(t, pg, "ingest", migrations.FS)
	open := func(dsn string) *pgxpool.Pool {
		p, err := pgxx.NewPool(ctx, dsn)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(p.Close)
		return p
	}
	d.app, d.system = open(d.db.App), open(d.db.System)
	d.appDB = d.app
	if cells {
		d.cell = testkit.CreateCellDB(t, pg, "ingest", "c1")
		d.cellAp, d.cellSy = open(d.cell.App), open(d.cell.System)
		appRouter, err := pgxx.NewRouter(d.app, map[string]*pgxpool.Pool{"c1": d.cellAp}, d.dir)
		if err != nil {
			t.Fatal(err)
		}
		if d.router, err = pgxx.NewRouter(d.system, map[string]*pgxpool.Pool{"c1": d.cellSy}, d.dir); err != nil {
			t.Fatal(err)
		}
		d.appDB = appRouter
	}
	return d
}

func (d *dual) schemas() pgxx.TenantSchemas {
	return pgxx.TenantSchemas{Migrations: migrations.Tenant(), Roles: []string{"ingest_app", "ingest_system"}}
}

// service builds the service the way main does for the mode.
func (d *dual) service(t *testing.T, maxDepth int64) *grpcapi.Server {
	t.Helper()
	svc, err := grpcapi.New(grpcapi.Options{
		App: d.appDB, System: d.system, Cells: d.router, Lookup: fakeLookups{}, MaxOutboxDepth: maxDepth,
		DepthRefresh: 50 * time.Millisecond, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now: d.clk.Now, NowInTx: func(context.Context, *sqlc.Queries) (time.Time, error) { return d.clk.Now(), nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = svc.Run(ctx) }()
	return svc
}

// tenant is a new tenant, placed where the mode puts every tenant.
func (d *dual) tenant(t *testing.T) tenantCtx {
	t.Helper()
	tc := newTenant("live")
	d.place(t, tc.id)
	return tc
}

func (d *dual) place(t *testing.T, id uuid.UUID) {
	t.Helper()
	if !d.cells {
		return
	}
	if _, err := pgxx.ProvisionTenant(testkit.Ctx(t), d.cell.Migrator, d.schemas(), id, nil); err != nil {
		t.Fatal(err)
	}
	d.dir.Place(id, "c1")
}

// startRelay runs a relay into the recording sink for the mode.
func (d *dual) startRelay(t *testing.T) {
	t.Helper()
	r, err := outbox.NewRelay(d.system, d.sink, outbox.Config{CellSweep: 100 * time.Millisecond}, testkit.Logger())
	if err != nil {
		t.Fatal(err)
	}
	r.WithCells(d.router)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = r.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
}

// system runs fn as the system role where the tenant's data is.
func (d *dual) inSystem(t *testing.T, id uuid.UUID, fn func(ctx context.Context, tx pgx.Tx) error) {
	t.Helper()
	ctx := context.Background()
	var err error
	if d.cells {
		err = pgxx.InTenantSchemaTx(ctx, d.cellSy, id, func(tx pgx.Tx) error { return fn(ctx, tx) })
	} else {
		err = pgxx.InTx(ctx, d.system, pgx.TxOptions{}, func(tx pgx.Tx) error { return fn(ctx, tx) })
	}
	if err != nil {
		t.Fatal(err)
	}
}

func (d *dual) outboxRows(t *testing.T, id uuid.UUID) int {
	t.Helper()
	var n int
	d.inSystem(t, id, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE tenant_id = $1`, id).Scan(&n)
	})
	return n
}

func (d *dual) keys(t *testing.T, id uuid.UUID) int {
	t.Helper()
	var n int
	d.inSystem(t, id, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM idempotency_keys WHERE tenant_id = $1`, id).Scan(&n)
	})
	return n
}

// TestIngestInBothModes: every scenario, pooled and then cells.
func TestIngestInBothModes(t *testing.T) {
	for _, cells := range []bool{false, true} {
		name := "pooled"
		if cells {
			name = "cells"
		}
		t.Run(name, func(t *testing.T) {
			d := newDual(t, cells)
			t.Run("events are deduplicated per tenant and enqueued", d.testIngest)
			t.Run("the relay publishes each accepted event once and empties the outbox", d.testRelay)
			t.Run("depth, back-pressure and the watermark cover every tenant", d.testWatermarkAndBackPressure)
			t.Run("dedup holds across a month boundary", d.testMonthBoundary)
			t.Run("a sandbox reset deletes one tenant's rows and no one else's", d.testSandboxReset)
			t.Run("roles and row-level security still hold", d.testRoles)
			t.Run("partition maintenance keeps every tenant's partitions", d.testMaintenance) // last: it drops the partitions of today
		})
	}
}

func (d *dual) testIngest(t *testing.T) {
	svc := d.service(t, 0)
	a, b := d.tenant(t), d.tenant(t)
	at := time.Now().Add(-time.Hour)
	first := ingestOne(t, svc, a, event("k1", "5", at))
	status1(t, first, ingestv1.EventResult_STATUS_ACCEPTED, "")
	again := ingestOne(t, svc, a, event("k1", "5", at))
	status1(t, again, ingestv1.EventResult_STATUS_DUPLICATE, "")
	if again.EventId != first.EventId {
		t.Fatalf("duplicate id %q != %q", again.EventId, first.EventId)
	}
	status1(t, ingestOne(t, svc, a, event("k1", "6", at)), ingestv1.EventResult_STATUS_REJECTED, "IDEMPOTENCY_KEY_REUSED")
	// The same key for another tenant is a different event.
	status1(t, ingestOne(t, svc, b, event("k1", "5", at)), ingestv1.EventResult_STATUS_ACCEPTED, "")
	if na, nb := d.outboxRows(t, a.id), d.outboxRows(t, b.id); na != 1 || nb != 1 {
		t.Fatalf("outbox rows a=%d b=%d, want 1 each", na, nb)
	}
	if d.keys(t, a.id) != 1 || d.keys(t, b.id) != 1 {
		t.Fatal("dedup keys are not one per tenant")
	}
}

func (d *dual) testRelay(t *testing.T) {
	svc := d.service(t, 0)
	a, b := d.tenant(t), d.tenant(t)
	at := time.Now().Add(-time.Hour)
	for i := range 20 {
		status1(t, ingestOne(t, svc, a, event(fmt.Sprintf("r-%d", i), "1", at)), ingestv1.EventResult_STATUS_ACCEPTED, "")
	}
	status1(t, ingestOne(t, svc, b, event("r-b", "1", at)), ingestv1.EventResult_STATUS_ACCEPTED, "")
	d.startRelay(t)
	testkit.Eventually(t, 15*time.Second, "both tenants published", func() bool { return d.sink.count(a.id) >= 20 && d.sink.count(b.id) >= 1 })
	testkit.Eventually(t, 5*time.Second, "outboxes empty", func() bool { return d.outboxRows(t, a.id) == 0 && d.outboxRows(t, b.id) == 0 })
	if got := d.sink.count(a.id); got != 20 {
		t.Fatalf("tenant a published %d rows, want exactly 20", got)
	}
}

func (d *dual) testWatermarkAndBackPressure(t *testing.T) {
	svc := d.service(t, 5)
	ctx := context.Background()
	// Earlier scenarios may have left rows; take what is there as the baseline of the depth and make the oldest ours.
	a, b := d.tenant(t), d.tenant(t)
	at := time.Now().Add(-time.Hour)
	var batch []*ingestv1.IncomingEvent
	for i := range 10 {
		batch = append(batch, event(fmt.Sprintf("w-a-%d", i), "1", at))
	}
	if _, err := svc.IngestEvents(a.ctx, &ingestv1.IngestEventsRequest{Events: batch}); err != nil {
		t.Fatal(err)
	}
	ingestOne(t, svc, b, event("w-b", "1", at))
	after := time.Now().Add(time.Second)

	w, err := svc.GetOutboxWatermark(ctx, &ingestv1.GetOutboxWatermarkRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if w.OldestUnpublishedIngestedAt == nil || w.OldestUnpublishedIngestedAt.AsTime().After(after) {
		t.Fatalf("watermark = %v, want set and no later than the ingests just made (older leftovers of this test run are fine)", w.OldestUnpublishedIngestedAt)
	}
	// The depth is the sum over the tenants; back-pressure answers above the limit, whichever tenant asks.
	testkit.Eventually(t, 5*time.Second, "depth above the limit", func() bool { return svc.OutboxDepth() > 5 })
	_, err = svc.IngestEvents(b.ctx, &ingestv1.IngestEventsRequest{Events: batch[:1]})
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("err = %v, want ResourceExhausted", err)
	}
	// Drained, it resumes and the watermark is empty.
	d.startRelay(t)
	testkit.Eventually(t, 15*time.Second, "outboxes drained", func() bool { return d.outboxRows(t, a.id) == 0 && d.outboxRows(t, b.id) == 0 })
	testkit.Eventually(t, 5*time.Second, "depth back under the limit", func() bool { return svc.OutboxDepth() <= 5 })
	status1(t, ingestOne(t, svc, a, event("w-after", "1", at)), ingestv1.EventResult_STATUS_ACCEPTED, "")
	testkit.Eventually(t, 15*time.Second, "everything published", func() bool {
		w, err := svc.GetOutboxWatermark(ctx, &ingestv1.GetOutboxWatermarkRequest{})
		return err == nil && w.OldestUnpublishedIngestedAt == nil
	})
}

func (d *dual) testMonthBoundary(t *testing.T) {
	d.clk.Set(time.Date(2026, 11, 30, 23, 59, 30, 0, time.UTC))
	t.Cleanup(func() { d.clk.Set(time.Now()) })
	svc := d.service(t, 0)
	tc := d.tenant(t)
	d.maintain(t, d.clk.Now())
	at := d.clk.Now().Add(-time.Minute)

	first := ingestOne(t, svc, tc, event("boundary", "7", at))
	status1(t, first, ingestv1.EventResult_STATUS_ACCEPTED, "")
	d.clk.Set(time.Date(2026, 12, 1, 0, 0, 10, 0, time.UTC))
	d.maintain(t, d.clk.Now())
	retry := ingestOne(t, svc, tc, event("boundary", "7", at))
	status1(t, retry, ingestv1.EventResult_STATUS_DUPLICATE, "")
	if retry.EventId != first.EventId {
		t.Fatalf("cross-partition duplicate id %q != %q", retry.EventId, first.EventId)
	}
	status1(t, ingestOne(t, svc, tc, event("december", "1", at)), ingestv1.EventResult_STATUS_ACCEPTED, "")
	var buckets []time.Time
	err := pgxx.InTenantTx(context.Background(), d.appDB, tc.id, func(tx pgx.Tx) error {
		rows, err := tx.Query(context.Background(), `SELECT created_at FROM idempotency_keys ORDER BY idempotency_key`)
		if err != nil {
			return err
		}
		buckets, err = pgx.CollectRows(rows, pgx.RowTo[time.Time])
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []time.Time{time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)}
	if len(buckets) != 2 || !buckets[0].Equal(want[0]) || !buckets[1].Equal(want[1]) {
		t.Fatalf("buckets = %v, want %v", buckets, want)
	}
}

// maintain runs the maintenance job for the mode: the pooled table, and the placed tenants' tables in cells mode.
func (d *dual) maintain(t *testing.T, now time.Time) (created, dropped []string) {
	t.Helper()
	ctx := context.Background()
	created, dropped, err := maintenance.Once(ctx, d.system, now)
	if err != nil {
		t.Fatal(err)
	}
	if d.router != nil {
		cc, cd, err := maintenance.OnceCells(ctx, d.router, now)
		if err != nil {
			t.Fatal(err)
		}
		created, dropped = append(created, cc...), append(dropped, cd...)
	}
	return created, dropped
}

func (d *dual) testMaintenance(t *testing.T) {
	a, b := d.tenant(t), d.tenant(t)
	now := time.Date(2030, 3, 15, 12, 0, 0, 0, time.UTC)
	created, dropped := d.maintain(t, now)
	if len(created) == 0 || len(dropped) == 0 {
		t.Fatalf("created %v dropped %v: expected partitions made and the old ones dropped", created, dropped)
	}
	if d.cells {
		// Every placed tenant got its own: the three months from March 2030, for each of the tenants of this mode so far.
		for _, id := range []uuid.UUID{a.id, b.id} {
			n := 0
			for _, c := range created {
				if len(c) > 36 && c[:36] == id.String() {
					n++
				}
			}
			if n != 3 {
				t.Fatalf("tenant %s got %d partitions, want 3 (created %v)", id, n, created)
			}
		}
	}
	// Idempotent: a second run does nothing.
	created, dropped = d.maintain(t, now)
	if len(created) != 0 || len(dropped) != 0 {
		t.Fatalf("second run created %v dropped %v", created, dropped)
	}
}

func (d *dual) testSandboxReset(t *testing.T) {
	svc := d.service(t, 0)
	a, b := d.tenant(t), d.tenant(t)
	at := time.Now().Add(-time.Hour)
	for _, tc := range []tenantCtx{a, b} {
		for i := range 3 {
			status1(t, ingestOne(t, svc, tc, event(fmt.Sprintf("s-%d", i), "1", at)), ingestv1.EventResult_STATUS_ACCEPTED, "")
		}
	}
	rs := sandboxreset.New(d.system).WithCells(d.router)
	got, err := rs.Reset(context.Background(), a.id)
	if err != nil {
		t.Fatal(err)
	}
	if got["idempotency_keys"] != 3 || got["outbox"] != 3 {
		t.Fatalf("reset a = %v, want 3 keys and 3 queued events", got)
	}
	if d.keys(t, a.id) != 0 || d.outboxRows(t, a.id) != 0 {
		t.Fatal("a still has rows")
	}
	if d.keys(t, b.id) != 3 || d.outboxRows(t, b.id) != 3 {
		t.Fatal("b lost rows to a's reset")
	}
	// After a reset the same key is a new event again.
	status1(t, ingestOne(t, svc, a, event("s-0", "1", at)), ingestv1.EventResult_STATUS_ACCEPTED, "")
}

func (d *dual) testRoles(t *testing.T) {
	svc := d.service(t, 0)
	ctx := context.Background()
	a, b := d.tenant(t), d.tenant(t)
	at := time.Now().Add(-time.Hour)
	ingestOne(t, svc, a, event("rls-a", "1", at))
	ingestOne(t, svc, b, event("rls-b", "1", at))

	// Tenant A sees only its own keys, even when it asks for B's.
	err := pgxx.InTenantTx(ctx, d.appDB, a.id, func(tx pgx.Tx) error {
		var own, others int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM idempotency_keys`).Scan(&own); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM idempotency_keys WHERE tenant_id = $1`, b.id).Scan(&others); err != nil {
			return err
		}
		if own != 1 || others != 0 {
			return fmt.Errorf("tenant A sees %d own and %d foreign keys", own, others)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// Forging a row for another tenant is blocked by the WITH CHECK policy.
	err = pgxx.InTenantTx(ctx, d.appDB, a.id, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO idempotency_keys VALUES ($1, 'forged', 'e', '\x00', now())`, b.id)
		return err
	})
	if pgxx.ErrCode(err) != pgxx.CodeInsufficientPriv {
		t.Fatalf("forged insert: %v", err)
	}
	// The app role can't enqueue for another tenant.
	err = pgxx.InTenantTx(ctx, d.appDB, a.id, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `SELECT enqueue_outbox($1, 'usage.events.v1', 'k', ARRAY['\x00'::bytea], ARRAY['{}'], now())`, b.id)
		return err
	})
	if pgxx.ErrCode(err) != pgxx.CodeInsufficientPriv {
		t.Fatalf("enqueue for another tenant: %v", err)
	}
	// Nor read the outbox directly, nor call the maintenance function.
	err = pgxx.InTenantTx(ctx, d.appDB, a.id, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `SELECT count(*) FROM outbox`)
		return err
	})
	if pgxx.ErrCode(err) != pgxx.CodeInsufficientPriv {
		t.Fatalf("app read of outbox: %v", err)
	}
	err = pgxx.InTenantTx(ctx, d.appDB, a.id, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `SELECT maintain_idempotency_partitions(now())`)
		return err
	})
	if pgxx.ErrCode(err) != pgxx.CodeInsufficientPriv {
		t.Fatalf("app call of maintenance: %v", err)
	}

	if !d.cells {
		return
	}
	// In a cell, tenant A can name B's schema (the role may use every tenant schema) and still reads none of its rows and writes none.
	err = pgxx.InTenantTx(ctx, d.appDB, a.id, func(tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM `+tenantdir.Schema(b.id)+`.idempotency_keys`).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			return fmt.Errorf("tenant A read %d rows of tenant B's schema", n)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	err = pgxx.InTenantTx(ctx, d.appDB, a.id, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO `+tenantdir.Schema(b.id)+`.idempotency_keys VALUES ($1, 'forged', 'e', '\x00', now())`, b.id)
		return err
	})
	if pgxx.ErrCode(err) != pgxx.CodeInsufficientPriv {
		t.Fatalf("write into B's schema as A: %v", err)
	}
	// The pooled database has none of the cell tenants' tables at all.
	if _, err := d.app.Exec(ctx, `SELECT 1 FROM `+tenantdir.Schema(a.id)+`.idempotency_keys`); err == nil {
		t.Fatal("a tenant schema exists in the pooled database")
	}
}

// A tenant that is moving gets an unavailable answer and nothing is written.
func TestIngestRefusesATenantThatIsMoving(t *testing.T) {
	d := newDual(t, true)
	svc := d.service(t, 0)
	tc := d.tenant(t)
	at := time.Now().Add(-time.Hour)
	status1(t, ingestOne(t, svc, tc, event("before", "1", at)), ingestv1.EventResult_STATUS_ACCEPTED, "")
	d.dir.Set(tenantdir.Placement{TenantID: tc.id, Cell: "c1", Status: tenantdir.StatusMoving})
	_, err := svc.IngestEvents(tc.ctx, &ingestv1.IngestEventsRequest{Events: []*ingestv1.IncomingEvent{event("during", "1", at)}})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("err = %v, want Unavailable", err)
	}
	d.dir.Place(tc.id, "c1")
	if n := d.outboxRows(t, tc.id); n != 1 {
		t.Fatalf("outbox rows = %d, want only the first event", n)
	}
	status1(t, ingestOne(t, svc, tc, event("after", "1", at)), ingestv1.EventResult_STATUS_ACCEPTED, "")
}

// The tenant migration set builds exactly the schema the public migrations build (tables, columns, constraints, indexes, policies, privileges, functions).
func TestTenantSchemaMatchesPublic(t *testing.T) {
	ctx := testkit.Ctx(t)
	pg := testkit.StartPostgres(t)
	pub := testkit.MigratedDB(t, pg, "ingest", migrations.FS)
	cell := testkit.CreateCellDB(t, pg, "ingest", "c1")
	id := uuid.New()
	ts := pgxx.TenantSchemas{Migrations: migrations.Tenant(), Roles: []string{"ingest_app", "ingest_system"}}
	if _, err := pgxx.ProvisionTenant(ctx, cell.Migrator, ts, id, nil); err != nil {
		t.Fatal(err)
	}
	pubConn, err := pgx.Connect(ctx, pub.Migrator)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pubConn.Close(ctx) }()
	cellConn, err := pgx.Connect(ctx, cell.Migrator)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cellConn.Close(ctx) }()
	want, err := pgxx.SchemaShape(ctx, pubConn, "public")
	if err != nil {
		t.Fatal(err)
	}
	got, err := pgxx.SchemaShape(ctx, cellConn, tenantdir.Schema(id))
	if err != nil {
		t.Fatal(err)
	}
	if len(want) < 20 {
		t.Fatalf("the public shape has only %d lines: the comparison is not looking at the schema (%q)", len(want), want)
	}
	if diff := pgxx.Diff(want, got); len(diff) > 0 {
		for _, l := range diff {
			t.Error(l)
		}
		t.Fatal("the tenant migrations and the public migrations of ingest-svc have drifted apart (- public only, + tenant only)")
	}
	if latest, err := pgxx.LatestTenantVersion(migrations.Tenant()); err != nil || latest != 1 {
		t.Fatalf("latest tenant version = %d %v", latest, err)
	}
	// Migrating every schema twice is a no-op.
	rep, err := pgxx.MigrateTenants(ctx, cell.Migrator, ts, nil)
	if err != nil || len(rep.Applied) != 0 || rep.Schemas != 1 {
		t.Fatalf("migrate all = %+v %v", rep, err)
	}
}
