//go:build integration

package cells_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/0furkancolak/payrudder/pkg/cells"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/tenantdir"
	"github.com/0furkancolak/payrudder/pkg/testkit"
)

// The fixture service: a parent and a child table (foreign key), a table partitioned by month, and a queue with an identity column. The same file builds the
// pooled database (public) and a tenant schema, which is what a service's public and tenant migrations are to each other.
const fixtureSQL = `
-- +goose Up
CREATE FUNCTION app_tenant_id() RETURNS uuid LANGUAGE sql STABLE AS $$ SELECT NULLIF(current_setting('app.tenant_id', true), '')::uuid $$;

CREATE TABLE accounts (tenant_id uuid NOT NULL, id int NOT NULL, name text NOT NULL, meta jsonb, created_at timestamptz NOT NULL, PRIMARY KEY (tenant_id, id));
CREATE TABLE orders (
  tenant_id uuid NOT NULL, id int NOT NULL, account_id int NOT NULL, amount numeric(20,6) NOT NULL, blob bytea, note text,
  PRIMARY KEY (tenant_id, id), FOREIGN KEY (tenant_id, account_id) REFERENCES accounts (tenant_id, id));
CREATE TABLE events (tenant_id uuid NOT NULL, at timestamptz NOT NULL, n int NOT NULL, PRIMARY KEY (tenant_id, at, n)) PARTITION BY RANGE (at);
CREATE TABLE events_2026_08 PARTITION OF events FOR VALUES FROM ('2026-08-01 00:00:00+00') TO ('2026-09-01 00:00:00+00');
CREATE TABLE events_2026_09 PARTITION OF events FOR VALUES FROM ('2026-09-01 00:00:00+00') TO ('2026-10-01 00:00:00+00');
CREATE TABLE queue (id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY, tenant_id uuid NOT NULL, payload bytea NOT NULL);

ALTER TABLE accounts ENABLE ROW LEVEL SECURITY; ALTER TABLE accounts FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON accounts USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());
ALTER TABLE orders ENABLE ROW LEVEL SECURITY; ALTER TABLE orders FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON orders USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());
ALTER TABLE events ENABLE ROW LEVEL SECURITY; ALTER TABLE events FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON events USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());

GRANT SELECT, INSERT, UPDATE ON accounts, orders, events TO mvt_app;
GRANT SELECT, INSERT ON queue TO mvt_app;
GRANT SELECT, DELETE ON queue, accounts, orders, events TO mvt_system;
GRANT EXECUTE ON FUNCTION app_tenant_id() TO mvt_app;
-- +goose Down
DROP TABLE queue, events, orders, accounts;
DROP FUNCTION app_tenant_id();
`

type env struct {
	cfg      cells.Config
	dir      *memDir
	pooled   *pgx.Conn // owner of the pooled database
	cell     map[string]*pgx.Conn
	app      map[string]testkit.ServiceDB // by cell name; "" is the pooled database
	schemas  pgxx.TenantSchemas
	tenantA  uuid.UUID
	tenantB  uuid.UUID
	settle   time.Duration
	migrDir  string
	services []string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	ctx := testkit.Ctx(t)
	pg := testkit.StartPostgres(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "00001_init.sql"), []byte(fixtureSQL), 0o600); err != nil {
		t.Fatal(err)
	}
	pooled := testkit.CreateServiceDB(t, pg, "mvt")
	if err := pgxx.Migrate(ctx, pooled.Migrator, fstest.MapFS{"00001_init.sql": {Data: []byte(fixtureSQL)}}, nil); err != nil {
		t.Fatal(err)
	}
	c1, c2 := testkit.CreateCellDB(t, pg, "mvt", "c1"), testkit.CreateCellDB(t, pg, "mvt", "c2")
	e := &env{
		dir: newMemDir(), settle: time.Nanosecond, migrDir: dir, cell: map[string]*pgx.Conn{}, app: map[string]testkit.ServiceDB{"": pooled, "c1": c1, "c2": c2},
		tenantA: uuid.New(), tenantB: uuid.New(),
		cfg: cells.Config{
			Cells: map[string]cells.Cell{"c1": {Region: "eu"}, "c2": {Region: "us"}},
			Services: map[string]cells.Service{"mvt": {
				Migrations: dir, Roles: []string{"mvt_app", "mvt_system"}, Pooled: pooled.Migrator, Cells: map[string]string{"c1": c1.Migrator, "c2": c2.Migrator},
			}},
		},
	}
	if err := e.cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	var err error
	if e.schemas, err = e.cfg.Schemas("mvt"); err != nil {
		t.Fatal(err)
	}
	connect := func(dsn string) *pgx.Conn {
		c, err := pgx.Connect(ctx, dsn)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Close(context.Background()) })
		return c
	}
	e.pooled = connect(pooled.Migrator)
	for _, n := range []string{"c1", "c2"} {
		e.cell[n] = connect(e.app[n].Migrator)
	}
	return e
}

// conn is the owner's connection to a place, and the schema the tenant's tables are in there.
func (e *env) conn(cell string) *pgx.Conn {
	if cell == "" {
		return e.pooled
	}
	return e.cell[cell]
}

func schemaOf(cell string, id uuid.UUID) string {
	if cell == "" {
		return "public"
	}
	return tenantdir.Schema(id)
}

// seed fills the pooled database with a tenant's data: enough variety to show a copy that mangles anything.
func (e *env) seed(t *testing.T, id uuid.UUID, scale int) {
	t.Helper()
	ctx := context.Background()
	tx, err := e.pooled.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT set_config('app.tenant_id', $1, true)`, id.String()); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= scale; i++ {
		if _, err := tx.Exec(ctx, `INSERT INTO accounts VALUES ($1, $2, $3, $4, $5)`, id, i, fmt.Sprintf("acct é ü 日本 %d", i), fmt.Sprintf(`{"n": %d, "tags": ["a", "b"], "nested": {"x": null}}`, i), time.Date(2026, 9, 1, 12, 30, i, 123456000, time.UTC)); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO orders VALUES ($1, $2, $3, $4, $5, $6)`, id, i, i, fmt.Sprintf("%d.%06d", i*1000, i), []byte{0, 1, 2, byte(i), 255}, nil); err != nil {
			t.Fatal(err)
		}
		month := 8 + i%2
		if _, err := tx.Exec(ctx, `INSERT INTO events VALUES ($1, $2, $3)`, id, time.Date(2026, time.Month(month), 1+i%27, 3, 4, 5, 0, time.UTC), i); err != nil {
			t.Fatal(err)
		}
	}
	for i := range scale * 2 {
		if _, err := tx.Exec(ctx, `INSERT INTO queue (tenant_id, payload) VALUES ($1, $2)`, id, []byte(fmt.Sprintf("p%04d", i))); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

// snapshot is every row of a tenant, as text, by table, in a form that does not depend on where it is: the queue is the payloads in id order (ids are not kept
// into the pooled database).
func (e *env) snapshot(t *testing.T, cell string, id uuid.UUID) map[string]string {
	t.Helper()
	ctx := context.Background()
	c := e.conn(cell)
	tx, err := c.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT set_config('app.tenant_id', $1, true)`, id.String()); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"SET LOCAL TimeZone = 'UTC'", "SET LOCAL bytea_output = 'hex'"} {
		if _, err := tx.Exec(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	sch := schemaOf(cell, id)
	out := map[string]string{}
	for table, q := range map[string]string{
		"accounts": `SELECT coalesce(string_agg(t::text, '|' ORDER BY id), '') FROM %s.accounts t WHERE tenant_id = $1`,
		"orders":   `SELECT coalesce(string_agg(t::text, '|' ORDER BY id), '') FROM %s.orders t WHERE tenant_id = $1`,
		"events":   `SELECT coalesce(string_agg(t::text, '|' ORDER BY at, n), '') FROM %s.events t WHERE tenant_id = $1`,
		"queue":    `SELECT coalesce(string_agg(encode(payload, 'hex'), '|' ORDER BY id), '') FROM %s.queue WHERE tenant_id = $1`,
	} {
		var s string
		if err := tx.QueryRow(ctx, fmt.Sprintf(q, pgx.Identifier{sch}.Sanitize()), id).Scan(&s); err != nil {
			t.Fatalf("snapshot %s in %q: %v", table, cell, err)
		}
		out[table] = s
	}
	return out
}

func (e *env) rows(t *testing.T, cell string, id uuid.UUID) int {
	t.Helper()
	n := 0
	for _, tbl := range []string{"accounts", "orders", "events", "queue"} {
		var c int
		ctx := context.Background()
		tx, err := e.conn(cell).Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, `SELECT set_config('app.tenant_id', $1, true)`, id.String()); err != nil {
			t.Fatal(err)
		}
		if err := tx.QueryRow(ctx, fmt.Sprintf(`SELECT count(*) FROM %s.%s WHERE tenant_id = $1`, pgx.Identifier{schemaOf(cell, id)}.Sanitize(), tbl), id).Scan(&c); err != nil {
			_ = tx.Rollback(ctx)
			return -1
		}
		_ = tx.Rollback(ctx)
		n += c
	}
	return n
}

func (e *env) move(t *testing.T, id uuid.UUID, to string) cells.MoveReport {
	t.Helper()
	rep, err := cells.MoveTenant(testkit.Ctx(t), e.cfg, e.dir, id, to, cells.MoveOptions{Settle: e.settle, FenceWait: 2 * time.Second, Log: testkit.Logger()})
	if err != nil {
		t.Fatalf("move to %q: %v", to, err)
	}
	if len(rep.Problems) > 0 {
		t.Fatalf("problems after the move: %v", rep.Problems)
	}
	return rep
}

// memDir is the directory as a state machine, the way plans-svc keeps it.
type memDir struct {
	mu         sync.Mutex
	p          map[uuid.UUID]tenantdir.Placement
	toCell     map[uuid.UUID]string
	failFinish func() error // when it returns an error, FinishMove fails; if the error is a *lostReply the directory has moved all the same
}

type lostReply struct{ error }

func newMemDir() *memDir {
	return &memDir{p: map[uuid.UUID]tenantdir.Placement{}, toCell: map[uuid.UUID]string{}}
}

func (d *memDir) Placement(_ context.Context, id uuid.UUID) (tenantdir.Placement, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if p, ok := d.p[id]; ok {
		return p, nil
	}
	return tenantdir.Placement{TenantID: id, Status: tenantdir.StatusReady}, nil
}

func (d *memDir) Placed(ctx context.Context, cell string) ([]tenantdir.Placement, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []tenantdir.Placement
	for _, p := range d.p {
		if !p.Pooled() && (cell == "" || p.Cell == cell) {
			out = append(out, p)
		}
	}
	return out, nil
}

func (d *memDir) BeginMove(_ context.Context, id uuid.UUID, toCell string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	cur := d.p[id]
	cur.TenantID = id
	if cur.Status == tenantdir.StatusMoving {
		return errors.New("move_in_progress")
	}
	if cur.Cell == toCell {
		return errors.New("same_place")
	}
	cur.Status = tenantdir.StatusMoving
	d.p[id], d.toCell[id] = cur, toCell
	return nil
}

func (d *memDir) FinishMove(_ context.Context, id uuid.UUID, cell, region string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.p[id].Status != tenantdir.StatusMoving {
		return errors.New("not_moving")
	}
	if d.failFinish != nil {
		if err := d.failFinish(); err != nil {
			var lost *lostReply
			if errors.As(err, &lost) {
				d.p[id] = tenantdir.Placement{TenantID: id, Cell: cell, Region: region, Status: tenantdir.StatusReady}
			}
			return err
		}
	}
	d.p[id] = tenantdir.Placement{TenantID: id, Cell: cell, Region: region, Status: tenantdir.StatusReady}
	return nil
}

func (d *memDir) AbortMove(_ context.Context, id uuid.UUID) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	cur := d.p[id]
	if cur.Status != tenantdir.StatusMoving {
		return errors.New("not_moving")
	}
	cur.Status = tenantdir.StatusReady
	d.p[id] = cur
	return nil
}

func (d *memDir) set(p tenantdir.Placement) { d.mu.Lock(); d.p[p.TenantID] = p; d.mu.Unlock() }

func (l *lostReply) Unwrap() error { return l.error }

func equalSnapshots(t *testing.T, what string, want, got map[string]string) {
	t.Helper()
	for table, w := range want {
		if got[table] != w {
			t.Errorf("%s: table %s differs\n want %.200q\n  got %.200q", what, table, w, got[table])
		}
	}
	if t.Failed() {
		t.FailNow()
	}
}

func TestMoveLosesNoRow(t *testing.T) {
	e := newEnv(t)
	const scale = 40
	e.seed(t, e.tenantA, scale)
	e.seed(t, e.tenantB, 7)
	want := e.snapshot(t, "", e.tenantA)
	wantB := e.snapshot(t, "", e.tenantB)
	total := e.rows(t, "", e.tenantA)
	if total != scale*5 {
		t.Fatalf("seeded %d rows, want %d", total, scale*5)
	}

	t.Run("pooled to a cell", func(t *testing.T) {
		rep := e.move(t, e.tenantA, "c1")
		if r := rep.Copied["mvt"].Rows(); r != int64(total) {
			t.Fatalf("copied %d rows, want %d", r, total)
		}
		equalSnapshots(t, "in c1", want, e.snapshot(t, "c1", e.tenantA))
		if n := e.rows(t, "", e.tenantA); n != 0 {
			t.Fatalf("%d rows of the tenant are left in the pooled database", n)
		}
		equalSnapshots(t, "other tenant", wantB, e.snapshot(t, "", e.tenantB))
		if p, _ := e.dir.Placement(context.Background(), e.tenantA); p.Cell != "c1" || p.Region != "eu" || !p.Serving() {
			t.Fatalf("directory = %+v", p)
		}
		// The identity sequence continues after the highest id: the next row gets a larger one.
		var maxID, next int64
		if err := e.cell["c1"].QueryRow(context.Background(), `SELECT max(id) FROM `+tenantdir.Schema(e.tenantA)+`.queue`).Scan(&maxID); err != nil {
			t.Fatal(err)
		}
		if err := e.cell["c1"].QueryRow(context.Background(), `INSERT INTO `+tenantdir.Schema(e.tenantA)+`.queue (tenant_id, payload) VALUES ($1, 'x') RETURNING id`, e.tenantA).Scan(&next); err != nil {
			t.Fatal(err)
		}
		if next <= maxID {
			t.Fatalf("next queue id %d <= max %d", next, maxID)
		}
		if _, err := e.cell["c1"].Exec(context.Background(), `DELETE FROM `+tenantdir.Schema(e.tenantA)+`.queue WHERE id = $1`, next); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("cell to cell sets the source aside", func(t *testing.T) {
		rep := e.move(t, e.tenantA, "c2")
		equalSnapshots(t, "in c2", want, e.snapshot(t, "c2", e.tenantA))
		retired := rep.Retired["mvt"]
		if !strings.HasPrefix(retired, "m_") {
			t.Fatalf("retired = %q", retired)
		}
		if _, err := e.cell["c1"].Exec(context.Background(), `SELECT 1 FROM `+tenantdir.Schema(e.tenantA)+`.accounts`); err == nil {
			t.Fatal("the tenant's schema is still in c1 under its own name")
		}
		set, err := pgxx.RetiredSchemas(context.Background(), e.cell["c1"])
		if err != nil || len(set) != 1 || set[0].Name != retired || set[0].TenantID != e.tenantA {
			t.Fatalf("retired schemas = %+v %v", set, err)
		}
		// The rows are still there to be had back.
		var n int
		if err := e.cell["c1"].QueryRow(context.Background(), `SELECT count(*) FROM `+pgx.Identifier{retired}.Sanitize()+`.queue`).Scan(&n); err != nil || n != scale*2 {
			t.Fatalf("set-aside queue rows = %d %v", n, err)
		}
		if err := pgxx.DropRetired(context.Background(), e.app["c1"].Migrator, retired); err != nil {
			t.Fatal(err)
		}
		if set, _ := pgxx.RetiredSchemas(context.Background(), e.cell["c1"]); len(set) != 0 {
			t.Fatalf("still set aside: %+v", set)
		}
	})

	t.Run("a cell back to the pooled database", func(t *testing.T) {
		e.move(t, e.tenantA, "")
		equalSnapshots(t, "back in the pooled database", want, e.snapshot(t, "", e.tenantA))
		equalSnapshots(t, "other tenant", wantB, e.snapshot(t, "", e.tenantB))
		if p, _ := e.dir.Placement(context.Background(), e.tenantA); !p.Pooled() || !p.Serving() {
			t.Fatalf("directory = %+v", p)
		}
	})

	t.Run("a refused or pointless move does nothing", func(t *testing.T) {
		if _, err := cells.MoveTenant(context.Background(), e.cfg, e.dir, e.tenantA, "", cells.MoveOptions{Settle: e.settle}); err == nil {
			t.Fatal("moved a tenant to where it is")
		}
		if _, err := cells.MoveTenant(context.Background(), e.cfg, e.dir, e.tenantA, "c9", cells.MoveOptions{Settle: e.settle}); err == nil {
			t.Fatal("moved a tenant to a cell that is not in the config")
		}
		e.dir.set(tenantdir.Placement{TenantID: e.tenantB, Status: tenantdir.StatusMoving})
		if _, err := cells.MoveTenant(context.Background(), e.cfg, e.dir, e.tenantB, "c1", cells.MoveOptions{Settle: e.settle}); err == nil {
			t.Fatal("moved a tenant that is already moving")
		}
		e.dir.set(tenantdir.Placement{TenantID: e.tenantB, Status: tenantdir.StatusReady})
		equalSnapshots(t, "other tenant", wantB, e.snapshot(t, "", e.tenantB))
	})
}

func TestMoveGivenUpUndoesEverything(t *testing.T) {
	e := newEnv(t)
	ctx := testkit.Ctx(t)
	e.seed(t, e.tenantA, 10)
	want := e.snapshot(t, "", e.tenantA)
	e.move(t, e.tenantA, "c1") // now in c1

	t.Run("a writer that holds a transaction open keeps the fence from closing", func(t *testing.T) {
		app, err := pgx.Connect(ctx, e.app["c1"].App)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = app.Close(ctx) }()
		tx, err := app.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, `SELECT set_config('app.tenant_id', $1, true)`, e.tenantA.String()); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO `+tenantdir.Schema(e.tenantA)+`.accounts VALUES ($1, 9000, 'late', null, now())`, e.tenantA); err != nil {
			t.Fatal(err)
		}
		_, err = cells.MoveTenant(ctx, e.cfg, e.dir, e.tenantA, "c2", cells.MoveOptions{Settle: e.settle, FenceWait: 300 * time.Millisecond, Log: testkit.Logger()})
		if err == nil || !strings.Contains(err.Error(), "everything undone") {
			t.Fatalf("err = %v, want the move given up and undone", err)
		}
		// Undone: the directory serves the tenant from c1 again, the writer's roles can use the schema again, nothing is left in c2.
		if p, _ := e.dir.Placement(ctx, e.tenantA); p.Cell != "c1" || !p.Serving() {
			t.Fatalf("directory = %+v", p)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO `+tenantdir.Schema(e.tenantA)+`.accounts VALUES ($1, 9001, 'later', null, now())`, e.tenantA); err != nil {
			t.Fatalf("the writer's transaction cannot go on after the undo: %v", err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := e.cell["c2"].Exec(ctx, `SELECT 1 FROM `+tenantdir.Schema(e.tenantA)+`.accounts`); err == nil {
			t.Fatal("the failed move left a schema in c2")
		}
		// Clean up the two rows the writer added so the next case compares the original data.
		// The tables are under forced row-level security even for their owner: the tenant has to be named.
		tx2, err := e.cell["c1"].Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx2.Exec(ctx, `SELECT set_config('app.tenant_id', $1, true)`, e.tenantA.String()); err != nil {
			t.Fatal(err)
		}
		if ct, err := tx2.Exec(ctx, `DELETE FROM `+tenantdir.Schema(e.tenantA)+`.accounts WHERE id >= 9000`); err != nil || ct.RowsAffected() != 2 {
			t.Fatalf("cleanup deleted %v rows (err %v), want the writer's 2", ct, err)
		}
		if err := tx2.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("a writer is refused once the source is fenced", func(t *testing.T) {
		m := pgxx.Move{TenantID: e.tenantA, From: pgxx.Endpoint{Cell: "c1", MigratorDSN: e.app["c1"].Migrator}, To: pgxx.Endpoint{Cell: "c2", MigratorDSN: e.app["c2"].Migrator}, Schemas: e.schemas}
		if err := m.Fence(ctx); err != nil {
			t.Fatal(err)
		}
		app, err := pgx.Connect(ctx, e.app["c1"].App)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = app.Close(ctx) }()
		_, err = app.Exec(ctx, `SELECT count(*) FROM `+tenantdir.Schema(e.tenantA)+`.accounts`)
		if pgxx.ErrCode(err) != pgxx.CodeInsufficientPriv {
			t.Fatalf("a fenced schema answered the app role: %v", err)
		}
		if err := m.Unfence(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := app.Exec(ctx, `SELECT count(*) FROM `+tenantdir.Schema(e.tenantA)+`.accounts`); err != nil {
			t.Fatalf("unfenced but still refused: %v", err)
		}
	})

	t.Run("a copy that does not match the source is not committed", func(t *testing.T) {
		// c2 already holds a schema for the tenant whose orders table changes what is inserted: the checksum must catch it.
		if _, err := pgxx.ProvisionTenant(ctx, e.app["c2"].Migrator, e.schemas, e.tenantA, nil); err != nil {
			t.Fatal(err)
		}
		for _, s := range []string{
			`CREATE FUNCTION ` + tenantdir.Schema(e.tenantA) + `.tamper() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN NEW.amount := NEW.amount + 1; RETURN NEW; END $$`,
			`CREATE TRIGGER tamper BEFORE INSERT ON ` + tenantdir.Schema(e.tenantA) + `.orders FOR EACH ROW EXECUTE FUNCTION ` + tenantdir.Schema(e.tenantA) + `.tamper()`,
		} {
			if _, err := e.cell["c2"].Exec(ctx, s); err != nil {
				t.Fatal(err)
			}
		}
		m := pgxx.Move{TenantID: e.tenantA, From: pgxx.Endpoint{Cell: "c1", MigratorDSN: e.app["c1"].Migrator}, To: pgxx.Endpoint{Cell: "c2", MigratorDSN: e.app["c2"].Migrator}, Schemas: e.schemas}
		_, err := m.Copy(ctx)
		if err == nil || !strings.Contains(err.Error(), "differs after the copy") {
			t.Fatalf("err = %v, want the checksum to refuse", err)
		}
		if n := e.rows(t, "c2", e.tenantA); n != 0 {
			t.Fatalf("a copy that failed its check left %d rows in c2", n)
		}
		if err := m.DiscardDestination(ctx); err != nil {
			t.Fatal(err)
		}
		equalSnapshots(t, "source untouched", want, e.snapshot(t, "c1", e.tenantA))
	})

	t.Run("the directory does not answer the end of the move", func(t *testing.T) {
		// The reply is lost but the directory moved: the move goes on, nothing is undone.
		e.dir.failFinish = func() error { return &lostReply{errors.New("deadline exceeded")} }
		defer func() { e.dir.failFinish = nil }()
		rep, err := cells.MoveTenant(ctx, e.cfg, e.dir, e.tenantA, "c2", cells.MoveOptions{Settle: e.settle, Log: testkit.Logger()})
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if rep.Retired["mvt"] == "" {
			t.Fatalf("the source was not retired: %+v", rep)
		}
		equalSnapshots(t, "in c2", want, e.snapshot(t, "c2", e.tenantA))
	})

	t.Run("a refusal by the directory at the end undoes the copy", func(t *testing.T) {
		e.dir.failFinish = func() error { return errors.New("cells_not_ready: a job was added") }
		defer func() { e.dir.failFinish = nil }()
		_, err := cells.MoveTenant(ctx, e.cfg, e.dir, e.tenantA, "c1", cells.MoveOptions{Settle: e.settle, Log: testkit.Logger()})
		if err == nil || !strings.Contains(err.Error(), "everything undone") {
			t.Fatalf("err = %v", err)
		}
		if p, _ := e.dir.Placement(ctx, e.tenantA); p.Cell != "c2" || !p.Serving() {
			t.Fatalf("directory = %+v", p)
		}
		equalSnapshots(t, "still in c2", want, e.snapshot(t, "c2", e.tenantA))
		if _, err := e.cell["c1"].Exec(ctx, `SELECT 1 FROM `+tenantdir.Schema(e.tenantA)+`.accounts`); err == nil {
			t.Fatal("the undone move left the tenant's schema in c1")
		}
	})
}

func TestDoctorFindsWhatDisagrees(t *testing.T) {
	e := newEnv(t)
	ctx := testkit.Ctx(t)
	healthy, missing, orphan, behind, stuck := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()

	// healthy: placed in c1 with a current schema. missing: placed in c1, never built. orphan: has a schema in c1 but the directory says pooled.
	// behind: placed in c2 with a schema one migration behind. stuck: placed in c2 and moving.
	for _, p := range []struct {
		id   uuid.UUID
		cell string
		st   tenantdir.Status
	}{{healthy, "c1", tenantdir.StatusReady}, {missing, "c1", tenantdir.StatusReady}, {behind, "c2", tenantdir.StatusReady}, {stuck, "c2", tenantdir.StatusMoving}} {
		e.dir.set(tenantdir.Placement{TenantID: p.id, Cell: p.cell, Status: p.st})
	}
	// The tenant migrations have two files; all but `behind` are built from both.
	if err := os.WriteFile(filepath.Join(e.migrDir, "00002_more.sql"), []byte("-- +goose Up\nSELECT 1;\n-- +goose Down\nSELECT 1;\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, p := range []struct {
		id   uuid.UUID
		cell string
	}{{healthy, "c1"}, {orphan, "c1"}, {stuck, "c2"}} {
		if _, err := pgxx.ProvisionTenant(ctx, e.app[p.cell].Migrator, e.schemas, p.id, nil); err != nil {
			t.Fatal(err)
		}
	}
	// A schema one migration behind: built from the first file only.
	first := pgxx.TenantSchemas{Migrations: fstest.MapFS{"00001_init.sql": {Data: []byte(fixtureSQL)}}, Roles: e.schemas.Roles}
	if _, err := pgxx.ProvisionTenant(ctx, e.app["c2"].Migrator, first, behind, nil); err != nil {
		t.Fatal(err)
	}
	// And one set aside by a move, long ago.
	if _, err := e.cell["c1"].Exec(ctx, fmt.Sprintf(`CREATE SCHEMA m_%s_%d`, strings.ReplaceAll(uuid.NewString(), "-", ""), time.Now().Add(-30*24*time.Hour).Unix())); err != nil {
		t.Fatal(err)
	}

	findings, err := cells.Doctor(ctx, e.cfg, e.dir, cells.DoctorOptions{})
	if err != nil {
		t.Fatal(err)
	}
	has := func(l cells.Level, tenant uuid.UUID, part string) bool {
		for _, f := range findings {
			if f.Level == l && (tenant == uuid.Nil || f.Tenant == tenant) && strings.Contains(f.Message, part) {
				return true
			}
		}
		return false
	}
	for _, c := range []struct {
		l      cells.Level
		tenant uuid.UUID
		part   string
	}{
		{cells.Error, missing, "does not exist"},
		{cells.Warn, orphan, "a leftover"},
		{cells.Warn, behind, "behind"},
		{cells.Warn, stuck, "is moving"},
		{cells.Warn, uuid.Nil, "was set aside by a move"},
	} {
		if !has(c.l, c.tenant, c.part) {
			t.Errorf("no %s finding about %v containing %q", c.l, c.tenant, c.part)
		}
	}
	for _, f := range findings {
		if f.Tenant == healthy && f.Level != cells.Info {
			t.Errorf("a healthy tenant is reported: %s", f)
		}
	}
	// Errors sort first.
	if len(findings) == 0 || findings[0].Level != cells.Error {
		t.Errorf("findings are not sorted by level: %v", findings)
	}

	// After migrating, nothing is behind.
	tsAll := pgxx.TenantSchemas{Migrations: os.DirFS(e.migrDir), Roles: e.schemas.Roles}
	if _, err := pgxx.MigrateTenants(ctx, e.app["c2"].Migrator, tsAll, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := pgxx.MigrateTenants(ctx, e.app["c1"].Migrator, tsAll, nil); err != nil {
		t.Fatal(err)
	}
	findings, err = cells.Doctor(ctx, e.cfg, e.dir, cells.DoctorOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if has(cells.Warn, behind, "behind") {
		t.Error("still behind after migrating")
	}
}

func TestParseConfigExpandsTheEnvironment(t *testing.T) {
	t.Setenv("CELLS_TEST_DSN", "postgres://u:p@h/db")
	c, err := cells.Parse([]byte(`{"plans_addr": "p:1", "cells": {"c1": {"region": "eu"}}, "services": {"x": {"migrations": "m", "roles": ["r"], "pooled": "${CELLS_TEST_DSN}", "cells": {"c1": "${CELLS_TEST_DSN}"}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if c.Services["x"].Pooled != "postgres://u:p@h/db" {
		t.Fatalf("pooled = %q", c.Services["x"].Pooled)
	}
	for _, bad := range []string{
		`{"services": {}}`,
		`{"cells": {"C1": {}}, "services": {"x": {"migrations": "m", "roles": ["r"], "pooled": "p", "cells": {"C1": "d"}}}}`,
		`{"cells": {"c1": {}}, "services": {"x": {"migrations": "m", "roles": ["r"], "pooled": "p", "cells": {}}}}`,
		`{"cells": {}, "services": {"x": {"migrations": "m", "roles": ["r"], "pooled": "p", "cells": {"c9": "d"}}}}`,
		`{"unknown": 1, "services": {"x": {"migrations": "m", "roles": ["r"], "pooled": "p"}}}`,
	} {
		if _, err := cells.Parse([]byte(bad)); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
}
