//go:build integration

package outbox_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/0furkancolak/payrudder/pkg/outbox"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/tenantdir"
	"github.com/0furkancolak/payrudder/pkg/testkit"
)

// The outbox table the same in the pooled database and in a tenant schema (the tenant set is the pooled one, as the relay's contract is the table's shape).
var outboxSQL = fstest.MapFS{
	"00001_outbox.sql": {Data: []byte(`
-- +goose Up
CREATE TABLE outbox (
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY, tenant_id uuid NOT NULL, topic text NOT NULL, partition_key text NOT NULL,
  payload bytea NOT NULL, headers jsonb NOT NULL, ingested_at timestamptz NOT NULL);
GRANT SELECT, DELETE, UPDATE (id) ON outbox TO outt_system;
-- +goose Down
DROP TABLE outbox;
`)},
}

// sink records what the relay published, per tenant, in order.
type sink struct {
	mu   sync.Mutex
	rows []outbox.Row
	fail error
}

func (s *sink) Publish(_ context.Context, rows []outbox.Row) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail != nil {
		return s.fail
	}
	s.rows = append(s.rows, rows...)
	return nil
}

func (s *sink) payloads(tenant uuid.UUID) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, r := range s.rows {
		if r.TenantID == tenant.String() {
			out = append(out, string(r.Payload))
		}
	}
	return out
}

type env struct {
	pooled, cell testkit.ServiceDB
	pooledSys    *pgxpool.Pool
	cellSys      *pgxpool.Pool
	cellOwner    *pgxpool.Pool
	pooledOwner  *pgxpool.Pool
	dir          *testkit.Directory
	router       *pgxx.Router
	ts           pgxx.TenantSchemas
}

func newEnv(t *testing.T) *env {
	t.Helper()
	ctx := testkit.Ctx(t)
	pg := testkit.StartPostgres(t)
	e := &env{dir: testkit.NewDirectory()}
	e.pooled = testkit.CreateServiceDB(t, pg, "outt")
	if err := pgxx.Migrate(ctx, e.pooled.Migrator, outboxSQL, nil); err != nil {
		t.Fatal(err)
	}
	e.cell = testkit.CreateCellDB(t, pg, "outt", "c1")
	e.ts = pgxx.TenantSchemas{Migrations: outboxSQL, Roles: []string{"outt_system"}}
	open := func(dsn string) *pgxpool.Pool {
		p, err := pgxx.NewPool(ctx, dsn)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(p.Close)
		return p
	}
	e.pooledSys, e.cellSys = open(e.pooled.System), open(e.cell.System)
	e.pooledOwner, e.cellOwner = open(e.pooled.Migrator), open(e.cell.Migrator)
	var err error
	if e.router, err = pgxx.NewRouter(e.pooledSys, map[string]*pgxpool.Pool{"c1": e.cellSys}, e.dir); err != nil {
		t.Fatal(err)
	}
	return e
}

// place provisions a tenant in the cell and tells the directory.
func (e *env) place(t *testing.T, status tenantdir.Status) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := pgxx.ProvisionTenant(testkit.Ctx(t), e.cell.Migrator, e.ts, id, nil); err != nil {
		t.Fatal(err)
	}
	e.dir.Set(tenantdir.Placement{TenantID: id, Cell: "c1", Status: status})
	return id
}

// enqueue writes outbox rows the way a writer does: into the tenant's schema (or the pooled table), then NOTIFY with the tenant as payload.
func (e *env) enqueue(t *testing.T, id uuid.UUID, inCell bool, payloads ...string) {
	t.Helper()
	ctx := testkit.Ctx(t)
	pool := e.pooledOwner
	if inCell {
		pool = e.cellOwner
	}
	err := pgxx.InTx(ctx, pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if inCell {
			if _, err := tx.Exec(ctx, `SELECT set_config('search_path', $1, true)`, pgxx.SearchPath(tenantdir.Schema(id))); err != nil {
				return err
			}
		}
		for _, p := range payloads {
			if _, err := tx.Exec(ctx, `INSERT INTO outbox (tenant_id, topic, partition_key, payload, headers, ingested_at) VALUES ($1::uuid, 't', $1::text, $2, '{}', now())`, id, []byte(p)); err != nil {
				return err
			}
		}
		_, err := tx.Exec(ctx, `SELECT pg_notify($1, $2)`, outbox.NotifyChannel, id.String())
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func (e *env) left(t *testing.T, id uuid.UUID, inCell bool) int {
	t.Helper()
	ctx := testkit.Ctx(t)
	var n int
	if inCell {
		err := pgxx.InTenantSchemaTx(ctx, e.cellSys, id, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT count(*) FROM outbox`).Scan(&n)
		})
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	if err := e.pooledSys.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE tenant_id = $1`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func run(t *testing.T, r *outbox.Relay) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = r.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
}

func TestRelayInBothModes(t *testing.T) {
	e := newEnv(t)
	log := testkit.Logger()

	t.Run("pooled mode relays the shared table as it always did", func(t *testing.T) {
		s := &sink{}
		r, err := outbox.NewRelay(e.pooledSys, s, outbox.Config{BatchSize: 2}, log)
		if err != nil {
			t.Fatal(err)
		}
		a := uuid.New()
		e.enqueue(t, a, false, "1", "2", "3", "4", "5")
		total := 0
		for {
			n, err := r.RelayOnce(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if n == 0 {
				break
			}
			total += n
		}
		if got := s.payloads(a); total != 5 || len(got) != 5 || got[0] != "1" || got[4] != "5" || e.left(t, a, false) != 0 {
			t.Fatalf("published %v (total %d), left %d", got, total, e.left(t, a, false))
		}
	})

	t.Run("cells mode relays each placed tenant's own outbox, in order, and the pooled table too", func(t *testing.T) {
		s := &sink{}
		r, err := outbox.NewRelay(e.pooledSys, s, outbox.Config{BatchSize: 2, CellSweep: time.Hour}, log)
		if err != nil {
			t.Fatal(err)
		}
		r.WithCells(e.router)
		a, b := e.place(t, tenantdir.StatusReady), e.place(t, tenantdir.StatusReady)
		pooled := uuid.New()
		run(t, r)
		time.Sleep(200 * time.Millisecond) // listeners are up
		// Placed after the relay fetched its list: only a notification (a tenant it does not know) makes it ask again.
		e.enqueue(t, a, true, "a1", "a2", "a3", "a4", "a5")
		e.enqueue(t, b, true, "b1")
		e.enqueue(t, pooled, false, "p1", "p2")
		testkit.Eventually(t, 15*time.Second, "every tenant's outbox relayed", func() bool {
			return len(s.payloads(a)) == 5 && len(s.payloads(b)) == 1 && len(s.payloads(pooled)) == 2
		})
		if got := s.payloads(a); got[0] != "a1" || got[4] != "a5" {
			t.Fatalf("tenant a published out of order: %v", got)
		}
		for _, c := range []struct {
			id     uuid.UUID
			inCell bool
		}{{a, true}, {b, true}, {pooled, false}} {
			testkit.Eventually(t, 5*time.Second, "outbox emptied", func() bool { return e.left(t, c.id, c.inCell) == 0 })
		}
	})

	t.Run("without a notification the sweep still finds the rows", func(t *testing.T) {
		s := &sink{}
		r, err := outbox.NewRelay(e.pooledSys, s, outbox.Config{CellSweep: 100 * time.Millisecond}, log)
		if err != nil {
			t.Fatal(err)
		}
		r.WithCells(e.router)
		id := e.place(t, tenantdir.StatusReady)
		// A writer that never notifies.
		err = pgxx.InTx(context.Background(), e.cellOwner, pgx.TxOptions{}, func(tx pgx.Tx) error {
			if _, err := tx.Exec(context.Background(), `SELECT set_config('search_path', $1, true)`, pgxx.SearchPath(tenantdir.Schema(id))); err != nil {
				return err
			}
			_, err := tx.Exec(context.Background(), `INSERT INTO outbox (tenant_id, topic, partition_key, payload, headers, ingested_at) VALUES ($1, 't', 'k', 'quiet', '{}', now())`, id)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		run(t, r)
		testkit.Eventually(t, 10*time.Second, "swept", func() bool { return len(s.payloads(id)) == 1 })
	})

	t.Run("a tenant that is moving is not relayed until it serves again", func(t *testing.T) {
		s := &sink{}
		r, err := outbox.NewRelay(e.pooledSys, s, outbox.Config{CellSweep: 50 * time.Millisecond}, log)
		if err != nil {
			t.Fatal(err)
		}
		r.WithCells(e.router)
		id := e.place(t, tenantdir.StatusMoving)
		e.enqueue(t, id, true, "held")
		run(t, r)
		time.Sleep(600 * time.Millisecond)
		if got := s.payloads(id); len(got) != 0 || e.left(t, id, true) != 1 {
			t.Fatalf("a moving tenant was relayed: %v", got)
		}
		e.dir.Set(tenantdir.Placement{TenantID: id, Cell: "c1", Status: tenantdir.StatusReady})
		testkit.Eventually(t, 10*time.Second, "relayed after the move", func() bool { return len(s.payloads(id)) == 1 })
	})

	t.Run("a crash between publish and delete publishes again, never loses", func(t *testing.T) {
		s := &sink{}
		var crash sync.Once
		crashed := make(chan struct{})
		r, err := outbox.NewRelay(e.pooledSys, s, outbox.Config{AfterPublish: func() error {
			var err error
			crash.Do(func() { err = errors.New("crash"); close(crashed) })
			return err
		}}, log)
		if err != nil {
			t.Fatal(err)
		}
		id := e.place(t, tenantdir.StatusReady)
		e.enqueue(t, id, true, "once")
		pt := pgxx.PlacedTenant{Placement: tenantdir.Placement{TenantID: id, Cell: "c1", Status: tenantdir.StatusReady}, Pool: e.cellSys}
		if _, err := r.RelayTenantOnce(context.Background(), pt); err == nil {
			t.Fatal("the simulated crash did not surface")
		}
		<-crashed
		if e.left(t, id, true) != 1 {
			t.Fatal("the row was deleted by a batch that did not commit")
		}
		if n, err := r.RelayTenantOnce(context.Background(), pt); err != nil || n != 1 {
			t.Fatalf("second attempt = %d %v", n, err)
		}
		if got := s.payloads(id); len(got) != 2 {
			t.Fatalf("published %v, want the row twice (at-least-once)", got)
		}
		if e.left(t, id, true) != 0 {
			t.Fatal("row not deleted after the second attempt")
		}
	})

	t.Run("a row of another tenant in a tenant's schema is never published", func(t *testing.T) {
		s := &sink{}
		r, err := outbox.NewRelay(e.pooledSys, s, outbox.Config{}, log)
		if err != nil {
			t.Fatal(err)
		}
		id, other := e.place(t, tenantdir.StatusReady), uuid.New()
		e.enqueue(t, other, false, "ignore") // a pooled row, to prove it is not touched either
		err = pgxx.InTx(context.Background(), e.cellOwner, pgx.TxOptions{}, func(tx pgx.Tx) error {
			if _, err := tx.Exec(context.Background(), `SELECT set_config('search_path', $1, true)`, pgxx.SearchPath(tenantdir.Schema(id))); err != nil {
				return err
			}
			_, err := tx.Exec(context.Background(), `INSERT INTO outbox (tenant_id, topic, partition_key, payload, headers, ingested_at) VALUES ($1, 't', 'k', 'stray', '{}', now())`, other)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		pt := pgxx.PlacedTenant{Placement: tenantdir.Placement{TenantID: id, Cell: "c1", Status: tenantdir.StatusReady}, Pool: e.cellSys}
		if _, err := r.RelayTenantOnce(context.Background(), pt); err == nil {
			t.Fatal("a stray row was relayed")
		}
		if len(s.payloads(other)) != 0 {
			t.Fatal("the stray row reached the sink")
		}
	})
}

func TestDepthAndWatermarkAcrossCells(t *testing.T) {
	e := newEnv(t)
	ctx := testkit.Ctx(t)
	a, b := e.place(t, tenantdir.StatusReady), e.place(t, tenantdir.StatusReady)

	if _, ok, err := outbox.CellsWatermark(ctx, e.router, 0); err != nil || ok {
		t.Fatalf("empty cells: ok=%v err=%v", ok, err)
	}
	if d, err := outbox.CellsDepth(ctx, e.router); err != nil || d != 0 {
		t.Fatalf("empty depth = %d %v", d, err)
	}
	e.enqueue(t, a, true, "1", "2")
	time.Sleep(20 * time.Millisecond)
	e.enqueue(t, b, true, "3")

	d, err := outbox.CellsDepth(ctx, e.router)
	if err != nil || d != 3 {
		t.Fatalf("depth = %d %v, want 3", d, err)
	}
	oldest, ok, err := outbox.CellsWatermark(ctx, e.router, 0)
	if err != nil || !ok || time.Since(oldest) < 20*time.Millisecond {
		t.Fatalf("watermark = %v %v %v, want a's first row", oldest, ok, err)
	}

	// A tenant placed after the list was made: the watermark that decides something (maxAge 0) counts it, a watched number (a long maxAge) may not yet.
	late := e.place(t, tenantdir.StatusReady)
	e.enqueue(t, late, true, "late")
	oldestWithLate, _, err := outbox.CellsWatermark(ctx, e.router, 0)
	if err != nil || oldestWithLate.After(oldest) {
		t.Fatalf("watermark with the late tenant = %v %v, want no later than %v", oldestWithLate, err, oldest)
	}
	if d, err := outbox.CellsDepth(ctx, e.router); err != nil || d < 3 {
		t.Fatalf("depth = %d %v", d, err)
	}

	// A tenant that is moving makes the watermark unknown. The router's list is at most five seconds old, so the test asks for a fresh one.
	e.dir.Set(tenantdir.Placement{TenantID: b, Cell: "c1", Status: tenantdir.StatusMoving})
	if _, err := e.router.PlacedWithin(ctx, 0); err != nil {
		t.Fatal(err)
	}
	if _, _, err := outbox.CellsWatermark(ctx, e.router, 0); !errors.Is(err, tenantdir.ErrNotServing) {
		t.Fatalf("watermark with a moving tenant: %v", err)
	}
}
