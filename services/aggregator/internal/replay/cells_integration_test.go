//go:build integration

package replay

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/tenantdir"
	"github.com/0furkancolak/payrudder/pkg/testkit"
	"github.com/0furkancolak/payrudder/services/aggregator/internal/store/sqlc"
	"github.com/0furkancolak/payrudder/services/aggregator/migrations"
)

// Phase 28 stage 3: the replay manager in cells mode. The Kafka side (reading the log) is covered by the Kafka suite with ITEST_CELLS=1; these scenarios need only
// Postgres and exercise what is new: where a tenant's jobs are looked for and run, and that promote and discard work in the tenant's own schema.

type cellsEnv struct {
	t       *testing.T
	cell    testkit.ServiceDB
	pooled  *pgxpool.Pool
	cellSys *pgxpool.Pool
	dir     *testkit.Directory
	router  *pgxx.Router
	inCell  map[uuid.UUID]bool
}

func newCellsEnv(t *testing.T) *cellsEnv {
	t.Helper()
	ctx := testkit.Ctx(t)
	pg := testkit.StartPostgres(t)
	db := testkit.MigratedDB(t, pg, "aggregator", migrations.FS)
	e := &cellsEnv{t: t, dir: testkit.NewDirectory(), inCell: map[uuid.UUID]bool{}}
	e.cell = testkit.CreateCellDB(t, pg, "aggregator", "c1")
	var err error
	if e.pooled, err = pgxx.NewPool(ctx, db.System); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.pooled.Close)
	if e.cellSys, err = pgxx.NewPool(ctx, e.cell.System); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.cellSys.Close)
	if e.router, err = pgxx.NewRouter(e.pooled, map[string]*pgxpool.Pool{"c1": e.cellSys}, e.dir); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *cellsEnv) manager() *Manager {
	return New(Config{Pool: e.pooled, Cells: e.router, Topic: "t", LiveGroup: "g", Log: testkit.Logger()})
}

func (e *cellsEnv) tenant(cell bool) uuid.UUID {
	e.t.Helper()
	id := uuid.New()
	if cell {
		ts := pgxx.TenantSchemas{Migrations: migrations.Tenant(), Roles: []string{"aggregator_app", "aggregator_system"}}
		if _, err := pgxx.ProvisionTenant(testkit.Ctx(e.t), e.cell.Migrator, ts, id, nil); err != nil {
			e.t.Fatal(err)
		}
		e.dir.Place(id, "c1")
		e.inCell[id] = true
	}
	return id
}

// at runs fn with queries as the system role where the tenant's data is.
func (e *cellsEnv) at(tn uuid.UUID, fn func(ctx context.Context, tx pgx.Tx) error) {
	e.t.Helper()
	ctx := context.Background()
	var err error
	if e.inCell[tn] {
		err = pgxx.InTenantSchemaTx(ctx, e.cellSys, tn, func(tx pgx.Tx) error { return fn(ctx, tx) })
	} else {
		err = pgxx.InTx(ctx, e.pooled, pgx.TxOptions{}, func(tx pgx.Tx) error { return fn(ctx, tx) })
	}
	if err != nil {
		e.t.Fatal(err)
	}
}

// job opens a running replay of the tenant in its place, the way Start does after it has read the log.
func (e *cellsEnv) job(tn uuid.UUID) uuid.UUID {
	e.t.Helper()
	id := uuid.New()
	e.at(tn, func(ctx context.Context, tx pgx.Tx) error {
		return insertJob(ctx, sqlc.New(tx), newJob{id: id, tenantID: tn, from: "2026-11", to: "2026-11", reason: "test", start: map[int32]int64{0: 0}, end: map[int32]int64{0: 10}})
	})
	return id
}

// Every open job is run once, wherever its tenant is: the pooled database's, each cell tenant's, not a tenant that is moving, and not a pooled row of a tenant the
// directory has placed elsewhere. A job already running is not started again by the next scan.
func TestScanRunsEachOpenJobOnceWhereverItsTenantIs(t *testing.T) {
	e := newCellsEnv(t)
	pooled, a, b, moving := e.tenant(false), e.tenant(true), e.tenant(true), e.tenant(true)
	leftover := e.tenant(false) // has a pooled job row, then the directory places it in the cell
	jobs := map[uuid.UUID]uuid.UUID{}
	for _, tn := range []uuid.UUID{pooled, a, b, moving, leftover} {
		jobs[tn] = e.job(tn)
	}
	e.dir.Place(leftover, "c1")
	e.dir.Set(tenantdir.Placement{TenantID: moving, Cell: "c1", Status: tenantdir.StatusMoving})

	m := e.manager()
	var mu sync.Mutex
	runs := map[uuid.UUID]int{}
	release := make(chan struct{})
	m.run = func(ctx context.Context, tenantID, jobID uuid.UUID) error {
		mu.Lock()
		runs[jobID]++
		mu.Unlock()
		<-release
		return nil
	}
	ctx := context.Background()
	m.scan(ctx)
	m.scan(ctx) // the jobs are still running: nothing starts twice
	time.Sleep(100 * time.Millisecond)
	close(release)
	m.jobs.Wait()

	ran := func(id uuid.UUID) int {
		mu.Lock()
		defer mu.Unlock()
		return runs[id]
	}
	for tn, id := range jobs {
		want := 1
		if tn == moving || tn == leftover {
			want = 0
		}
		if ran(id) != want {
			t.Errorf("job of tenant %s (cells: %v) ran %d times, want %d", tn, e.inCell[tn], ran(id), want)
		}
	}
	// The moving tenant's job is picked up when it serves again.
	e.dir.Place(moving, "c1")
	m.run = func(ctx context.Context, tenantID, jobID uuid.UUID) error {
		mu.Lock()
		runs[jobID]++
		mu.Unlock()
		return nil
	}
	m.scan(ctx)
	m.jobs.Wait()
	if ran(jobs[moving]) != 1 {
		t.Errorf("the job of the tenant that moved ran %d times after it served again, want 1", ran(jobs[moving]))
	}
}

// Promote and discard run in the tenant's schema: the rows of the periods outside the replay are carried over, the pooled database is not touched, a tenant that
// is moving is refused with an error the API answers as unavailable, and a move is not counted as a failure of the job.
func TestPromoteAndDiscardInATenantSchema(t *testing.T) {
	e := newCellsEnv(t)
	ctx := context.Background()
	m := e.manager()
	tn := e.tenant(true)
	other := e.tenant(false)

	jobID := e.job(tn)
	e.at(tn, func(ctx context.Context, tx pgx.Tx) error {
		// Generation 1 holds October (outside the replay) and November; generation 2 is the rebuilt November, catching up, and the job is ready.
		for _, q := range []string{
			`INSERT INTO usage_aggregates (tenant_id, period_id, customer_id, meter_code, generation, quantity, quantity_scale, event_count, updated_at) VALUES ($1, '2026-10', gen_random_uuid(), 'm', 1, 7, 0, 3, now())`,
			`INSERT INTO usage_aggregates (tenant_id, period_id, customer_id, meter_code, generation, quantity, quantity_scale, event_count, updated_at) VALUES ($1, '2026-11', gen_random_uuid(), 'm', 2, 9, 0, 4, now())`,
			`UPDATE aggregate_generations SET status = 'catching_up' WHERE tenant_id = $1 AND generation = 2`,
			`UPDATE replay_jobs SET status = 'ready' WHERE tenant_id = $1`,
		} {
			if _, err := tx.Exec(ctx, q, tn); err != nil {
				return err
			}
		}
		return nil
	})

	// Not in the pooled database, whatever the manager's pool says.
	pooledOnly := New(Config{Pool: e.pooled, Topic: "t", LiveGroup: "g", Log: testkit.Logger()})
	if _, err := pooledOnly.Get(ctx, tn, jobID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("the pooled database knows the cell tenant's job: %v", err)
	}

	e.dir.Set(tenantdir.Placement{TenantID: tn, Cell: "c1", Status: tenantdir.StatusMoving})
	if _, err := m.Promote(ctx, tn, jobID); !errors.Is(err, tenantdir.ErrNotServing) {
		t.Fatalf("promote while moving = %v, want ErrNotServing", err)
	}
	if got := mapErr(fmt.Errorf("x: %w", tenantdir.ErrNotServing)); got == nil || got.Error() == "" {
		t.Fatal("a move is not mapped to an error")
	}
	job := &Job{ID: jobID, TenantID: tn, TargetGeneration: 2}
	for range maxFailures + 2 {
		m.afterRun(ctx, job, fmt.Errorf("interrupted: %w", tenantdir.ErrNotServing))
	}
	m.mu.Lock()
	failures := m.failures[jobID]
	m.mu.Unlock()
	e.dir.Place(tn, "c1")
	if got, err := m.Get(ctx, tn, jobID); err != nil || got.Status != Ready || failures != 0 {
		t.Fatalf("after the move: job %+v, %v, %d failures counted; want ready and none", got, err, failures)
	}

	promoted, err := m.Promote(ctx, tn, jobID)
	if err != nil || promoted.Status != Promoted {
		t.Fatalf("promote = %+v, %v", promoted, err)
	}
	e.at(tn, func(ctx context.Context, tx pgx.Tx) error {
		var active int32
		if err := tx.QueryRow(ctx, `SELECT generation FROM aggregate_generations WHERE tenant_id = $1 AND status = 'active'`, tn).Scan(&active); err != nil || active != 2 {
			return fmt.Errorf("active generation = %d, %v", active, err)
		}
		var carried int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM usage_aggregates WHERE tenant_id = $1 AND generation = 2 AND period_id = '2026-10'`, tn).Scan(&carried); err != nil || carried != 1 {
			return fmt.Errorf("%d rows of the period outside the replay were carried over (%v), want 1", carried, err)
		}
		return nil
	})

	// A second replay is discarded: its rows go, the live generation stays, and the pooled tenant's own job is not mixed up with it.
	second := e.job(tn)
	e.at(tn, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO usage_aggregates (tenant_id, period_id, customer_id, meter_code, generation, quantity, quantity_scale, event_count, updated_at) VALUES ($1, '2026-11', gen_random_uuid(), 'm', 3, 1, 0, 1, now())`, tn)
		return err
	})
	pooledJob := e.job(other)
	d, err := m.Discard(ctx, tn, second)
	if err != nil || d.Status != Discarded {
		t.Fatalf("discard = %+v, %v", d, err)
	}
	e.at(tn, func(ctx context.Context, tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM usage_aggregates WHERE tenant_id = $1 AND generation = 3`, tn).Scan(&n); err != nil || n != 0 {
			return fmt.Errorf("%d rows of the discarded generation remain (%v)", n, err)
		}
		return nil
	})
	if got, err := m.Get(ctx, other, pooledJob); err != nil || got.Status != Running {
		t.Fatalf("the pooled tenant's job = %+v, %v", got, err)
	}
	if _, err := m.Get(ctx, tn, pooledJob); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a job of another tenant is visible: %v", err)
	}
}
