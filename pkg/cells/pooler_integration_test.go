//go:build integration

package cells_test

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/0furkancolak/payrudder/pkg/cells"
	"github.com/0furkancolak/payrudder/pkg/outbox"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/tenantdir"
	"github.com/0furkancolak/payrudder/pkg/testkit"
)

// The tests of this file put a transaction-mode PgBouncer in front of the databases of cells mode, and cells on two Postgres clusters (docs/runbooks/scaling.md).
// They start several containers (about a minute each), so they run only when ITEST_POOLER is set; the nightly workflow sets it.
func requirePooler(t *testing.T) {
	t.Helper()
	if os.Getenv(testkit.PoolerEnv) != "" {
		return
	}
	t.Skip("set ITEST_POOLER=1 to run the pooler suites (several containers each)")
}

// twoClustersBehindAPooler is the deployment of the scaling runbook in miniature: the pooled database and cell c1 on one Postgres, cell c2 on another, one
// transaction-mode PgBouncer in front of all of them, the app roles going through it and the owner roles straight to the databases.
func twoClustersBehindAPooler(poolSize int) topology {
	return topology{twoClusters: true, pooler: &testkit.PoolerOptions{MaxPreparedStatements: 200, PoolSize: poolSize}}
}

func TestMoveAcrossClustersBehindAPooler(t *testing.T) {
	requirePooler(t)
	moveLosesNoRow(t, newEnvOn(t, twoClustersBehindAPooler(2)))
}

func TestMoveGivenUpAcrossClustersBehindAPooler(t *testing.T) {
	requirePooler(t)
	moveGivenUpUndoesEverything(t, newEnvOn(t, twoClustersBehindAPooler(2)))
}

func TestDoctorAcrossClustersBehindAPooler(t *testing.T) {
	requirePooler(t)
	doctorFindsWhatDisagrees(t, newEnvOn(t, twoClustersBehindAPooler(2)))
}

// Residency: the rows of a tenant whose cell is in one region are in that region's cluster and in no other, before and after a move, and nothing of the tenant
// is left behind on the cluster it left but the schema the move set aside (closed to the service roles).
func TestTenantRowsStayInTheirRegionsCluster(t *testing.T) {
	requirePooler(t)
	e := newEnvOn(t, twoClustersBehindAPooler(2))
	ctx := testkit.Ctx(t)
	e.seed(t, e.tenantA, 12)

	// where lists the clusters ("eu" is c1's, "us" is c2's) that have a schema or a row of the tenant.
	where := func() []string {
		var out []string
		for region, cell := range map[string]string{"eu": "c1", "us": "c2"} {
			c := e.cell[cell]
			var n int
			if err := c.QueryRow(ctx, `SELECT count(*) FROM pg_namespace WHERE nspname = $1 OR nspname LIKE 'm\_'||substr($1, 3)||'\_%'`, tenantdir.Schema(e.tenantA)).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n > 0 {
				out = append(out, region)
			}
		}
		return out
	}
	if got := where(); len(got) != 0 {
		t.Fatalf("a tenant of the pooled database has a schema in %v", got)
	}
	e.move(t, e.tenantA, "c1")
	if got := where(); len(got) != 1 || got[0] != "eu" {
		t.Fatalf("after the move to c1 (eu) the tenant is in %v", got)
	}
	if p, _ := e.dir.Placement(ctx, e.tenantA); p.Region != "eu" || p.Cell != "c1" {
		t.Fatalf("directory = %+v", p)
	}
	// The second cluster has no schema of the tenant at all, and no table of the pooled database on the first has a row.
	if n := e.rows(t, "", e.tenantA); n != 0 {
		t.Fatalf("%d rows of an eu tenant are left in the pooled database", n)
	}
	if n := e.rows(t, "c2", e.tenantA); n != -1 {
		t.Fatalf("the us cluster answers for an eu tenant's tables: %d rows", n)
	}
	// Move it across clusters: the eu cluster keeps only the closed, set-aside schema, which a retirement drops.
	rep := e.move(t, e.tenantA, "c2")
	if got := where(); len(got) != 2 {
		t.Fatalf("after the move to c2 (us) the clusters with something of the tenant are %v, want us and the set-aside schema in eu", got)
	}
	if n := e.rows(t, "c2", e.tenantA); n != 12*5 {
		t.Fatalf("the us cluster has %d rows of the tenant, want %d", n, 12*5)
	}
	// The set-aside schema is closed to the service roles: the app role of the eu cell cannot read a row of it.
	app, err := pgx.Connect(ctx, e.app["c1"].App)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = app.Close(ctx) }()
	if _, err := app.Exec(ctx, `SELECT count(*) FROM `+pgx.Identifier{rep.Retired["mvt"]}.Sanitize()+`.accounts`); pgxx.ErrCode(err) != pgxx.CodeInsufficientPriv {
		t.Fatalf("the set-aside schema answered the app role: %v", err)
	}
	if err := pgxx.DropRetired(ctx, e.app["c1"].Migrator, rep.Retired["mvt"]); err != nil {
		t.Fatal(err)
	}
	if got := where(); len(got) != 1 || got[0] != "us" {
		t.Fatalf("after dropping the set-aside schema the tenant is in %v, want us only", got)
	}
}

// TestTransactionPooling is what a service sees of a transaction-mode PgBouncer with one server connection per user and database: any state a client leaves on
// that connection is certain to reach the next client, so a leak cannot hide.
func TestTransactionPooling(t *testing.T) {
	requirePooler(t)
	e := newEnvOn(t, twoClustersBehindAPooler(1))
	fresh := func() *env { c := *e; c.tenantA, c.tenantB = uuid.New(), uuid.New(); return &c }
	t.Run("the tenant scope does not leak between clients", func(t *testing.T) { tenantScopeDoesNotLeakBetweenClients(t, fresh()) })
	t.Run("transaction-local flags work", func(t *testing.T) { transactionLocalFlagsWorkBehindAPooler(t, fresh()) })
	t.Run("session state is not kept", func(t *testing.T) { sessionStateIsNotKeptByTransactionPooling(t, fresh()) })
	t.Run("the owner role is refused", func(t *testing.T) { ownerRoleIsRefusedByATransactionPooler(t, fresh()) })
}

// The tenant's scope is two set_config(…, true): app.tenant_id (row-level security) and, in a cell, search_path. Both are transaction-local, so they must not
// outlive the transaction on the server connection a pooler hands to somebody else. One server connection per database and user makes any such leak certain.
func tenantScopeDoesNotLeakBetweenClients(t *testing.T, e *env) {
	ctx := testkit.Ctx(t)
	other := uuid.New() // a second tenant in the pooled database, so that row-level security has something to hide
	tenants := map[uuid.UUID]struct {
		cell string
		rows int
	}{e.tenantA: {"c1", 9}, e.tenantB: {"c2", 7}, uuid.New(): {"", 11}, other: {"", 5}}
	for id, p := range tenants {
		e.seed(t, id, p.rows)
		if p.cell != "" {
			e.move(t, id, p.cell)
		}
	}

	pools := map[string]*pgxpool.Pool{}
	for cell, db := range e.app {
		p, err := pgxx.NewPool(ctx, db.App)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(p.Close)
		pools[cell] = p
	}
	router, err := pgxx.NewRouter(pools[""], map[string]*pgxpool.Pool{"c1": pools["c1"], "c2": pools["c2"]}, e.dir)
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for w := range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 30 {
				for id, p := range tenants {
					if (w+i)%3 != 0 && p.cell == "" {
						continue
					}
					err := pgxx.InTenantTx(ctx, router, id, func(tx pgx.Tx) error {
						var gotTenant, path string
						var n int
						if err := tx.QueryRow(ctx, `SELECT current_setting('app.tenant_id'), current_setting('search_path'), (SELECT count(*) FROM accounts)`).Scan(&gotTenant, &path, &n); err != nil {
							return err
						}
						if gotTenant != id.String() || n != p.rows || (p.cell != "") != strings.Contains(path, tenantdir.Schema(id)) {
							return fmt.Errorf("tenant %s (cell %q) saw app.tenant_id=%s search_path=%s and %d accounts, want %d", id, p.cell, gotTenant, path, n, p.rows)
						}
						return nil
					})
					if err != nil {
						errs <- err
						return
					}
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}

	// Afterwards nothing is set on the server connection any client gets next: a plain transaction on each place sees neither setting.
	for cell, p := range pools {
		var tenantSetting, path string
		if err := pgxx.InTx(ctx, p, pgx.TxOptions{}, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT coalesce(current_setting('app.tenant_id', true), ''), current_setting('search_path')`).Scan(&tenantSetting, &path)
		}); err != nil {
			t.Fatal(err)
		}
		if tenantSetting != "" || strings.Contains(path, "t_") {
			t.Errorf("place %q: a later transaction sees app.tenant_id=%q search_path=%q", cell, tenantSetting, path)
		}
	}

	// It really was multiplexed: more client connections than server connections.
	clients, servers := e.poolerCount(t, "SHOW CLIENTS"), e.poolerCount(t, "SHOW SERVERS")
	if clients <= servers {
		t.Errorf("PgBouncer shows %d clients and %d servers: the test did not share server connections", clients, servers)
	}
}

// poolerCount is the number of rows the admin console answers to a SHOW command, ignoring its own connection.
func (e *env) poolerCount(t *testing.T, show string) int {
	t.Helper()
	ctx := testkit.Ctx(t)
	c, err := pgx.Connect(ctx, e.pooler.AdminDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close(ctx) }()
	rows, err := c.Query(ctx, show)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			t.Fatal(err)
		}
		if user, _ := vals[1].(string); user != "payrudder" { // column "user" of SHOW CLIENTS and SHOW SERVERS
			n++
		}
	}
	return n
}

// plans-svc, billing-svc and payments-worker refuse every delete of a financial row unless the transaction names the tenant in payrudder.tenant_move or
// payrudder.sandbox_reset (set_config(…, true)). A flag that is local to the transaction is what a transaction pooler keeps apart; this is the same trigger on a
// small table.
func transactionLocalFlagsWorkBehindAPooler(t *testing.T, e *env) {
	ctx := testkit.Ctx(t)
	for _, s := range []string{
		`CREATE TABLE financial (tenant_id uuid NOT NULL, id int NOT NULL, PRIMARY KEY (tenant_id, id))`,
		`CREATE FUNCTION financial_row_no_delete() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
		   IF current_setting('payrudder.tenant_move', true) = OLD.tenant_id::text THEN RETURN OLD; END IF;
		   RAISE EXCEPTION 'rows of % are never deleted', TG_TABLE_NAME USING ERRCODE = 'P0001', HINT = 'financial_row_delete'; END $$`,
		`CREATE TRIGGER no_delete BEFORE DELETE ON financial FOR EACH ROW EXECUTE FUNCTION financial_row_no_delete()`,
		`GRANT SELECT, INSERT, DELETE ON financial TO mvt_system`,
	} {
		if _, err := e.pooled.Exec(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	// The system role (the one that deletes) behind the pooler, as in the worst case: two clients, one server connection.
	dsn := e.pooler.DSN(e.app[""].System)
	a, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	b, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close(ctx); _ = b.Close(ctx) })
	id := e.tenantA
	for i := range 3 {
		if _, err := a.Exec(ctx, `INSERT INTO financial VALUES ($1, $2)`, id, i); err != nil {
			t.Fatal(err)
		}
	}
	del := func(c *pgx.Conn, flag string, local bool, i int) error {
		tx, err := c.Begin(ctx)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if flag != "" {
			if _, err := tx.Exec(ctx, `SELECT set_config('payrudder.tenant_move', $1, $2)`, flag, local); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `DELETE FROM financial WHERE tenant_id = $1 AND id = $2`, id, i); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	refused := func(err error) bool { return pgxx.ErrCode(err) == pgxx.CodeRaiseException }

	if err := del(a, "", true, 0); !refused(err) {
		t.Fatalf("a delete with no flag: %v", err)
	}
	if err := del(a, id.String(), true, 0); err != nil {
		t.Fatalf("a delete in a transaction that names the tenant: %v", err)
	}
	// The flag ended with its transaction: the next client on the same server connection is refused again, and so is the same client.
	if err := del(b, "", true, 1); !refused(err) {
		t.Fatalf("another client's delete after the flag's transaction: %v", err)
	}
	if err := del(a, "", true, 1); !refused(err) {
		t.Fatalf("the same client's next delete: %v", err)
	}
	// Two clients at once: b's transaction waits for the server connection a holds, and does not see a's flag.
	tx, err := a.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('payrudder.tenant_move', $1, true)`, id.String()); err != nil {
		t.Fatal(err)
	}
	blocked := make(chan error, 1)
	go func() { blocked <- del(b, "", true, 1) }()
	select {
	case err := <-blocked:
		t.Fatalf("b ran while a's transaction held the only server connection: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	if _, err := tx.Exec(ctx, `DELETE FROM financial WHERE tenant_id = $1 AND id = 1`, id); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	// b's delete of the row a just deleted finds nothing (and, flagless, would have been refused had it found one).
	if err := <-blocked; err != nil && !refused(err) {
		t.Fatalf("b after a: %v", err)
	}
	// A session-level flag (is_local false) is the mistake: it stays on the server connection and the next client deletes with it.
	if _, err := a.Exec(ctx, `SELECT set_config('payrudder.tenant_move', $1, false)`, id.String()); err != nil {
		t.Fatal(err)
	}
	if err := del(b, "", true, 2); err != nil {
		t.Fatalf("expected the leaked session-level flag to let b delete (this documents the hazard): %v", err)
	}
}

// What a transaction pooler does not keep, and so what the system and owner roles must not go through it for. Each case needs the one server connection two clients
// share to show the effect for certain; with more they would only be unlucky some of the time.
func sessionStateIsNotKeptByTransactionPooling(t *testing.T, e *env) {
	ctx := testkit.Ctx(t)
	dsn := e.pooler.DSN(e.app[""].System)
	connect := func() *pgx.Conn {
		c, err := pgx.Connect(ctx, dsn)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Close(ctx) })
		return c
	}

	t.Run("a session setting is seen by the next client", func(t *testing.T) {
		a, b := connect(), connect()
		if _, err := a.Exec(ctx, `SET payrudder.leak = 'from a'`); err != nil {
			t.Fatal(err)
		}
		var got string
		if err := b.QueryRow(ctx, `SELECT coalesce(current_setting('payrudder.leak', true), '')`).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != "from a" {
			t.Fatalf("b sees %q: PgBouncer reset the session, so the runbook's warning about SET is stale", got)
		}
	})

	t.Run("a session advisory lock does not exclude a second holder", func(t *testing.T) {
		// aggregator's replay jobs and billing-svc's runs take pg_try_advisory_lock on a connection they hold: one replica per job. Behind the pooler both replicas win.
		a, b := connect(), connect()
		var gotA, gotB bool
		if err := a.QueryRow(ctx, `SELECT pg_try_advisory_lock(4242)`).Scan(&gotA); err != nil {
			t.Fatal(err)
		}
		if err := b.QueryRow(ctx, `SELECT pg_try_advisory_lock(4242)`).Scan(&gotB); err != nil {
			t.Fatal(err)
		}
		if !gotA || !gotB {
			t.Fatalf("lock answers %v, %v: expected both clients to get the lock behind the pooler", gotA, gotB)
		}
		// Released directly on the server's side of things: two unlocks for two locks.
		for range 2 {
			if _, err := a.Exec(ctx, `SELECT pg_advisory_unlock(4242)`); err != nil {
				t.Fatal(err)
			}
		}
	})

	t.Run("LISTEN never delivers", func(t *testing.T) {
		// The outbox relay wakes on NOTIFY and sweeps every few milliseconds otherwise (pkg/outbox: PollInterval). Behind the pooler it is the sweep alone.
		a, b := connect(), connect()
		if _, err := a.Exec(ctx, `LISTEN `+outbox.NotifyChannel); err != nil {
			t.Fatal(err)
		}
		if _, err := b.Exec(ctx, `SELECT pg_notify($1, 'x')`, outbox.NotifyChannel); err != nil {
			t.Fatal(err)
		}
		wctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		if n, err := a.WaitForNotification(wctx); err == nil {
			t.Fatalf("a was notified (%+v): LISTEN works behind this pooler, so the runbook's warning is stale", n)
		}
	})
}

// The owner role sets search_path as a start-up parameter (a tenant migration must create its tables in the tenant's schema and nowhere else), which PgBouncer
// refuses unless it is told to ignore it, and which it could not honour in transaction mode anyway. The tool must be pointed at the databases themselves.
func ownerRoleIsRefusedByATransactionPooler(t *testing.T, e *env) {
	ctx := testkit.Ctx(t)

	_, err := pgxx.ProvisionTenant(ctx, e.pooler.DSN(e.app["c1"].Migrator), e.schemas, e.tenantA, nil)
	if err == nil || !strings.Contains(err.Error(), "unsupported startup parameter: search_path") {
		t.Fatalf("a tenant migration through the pooler: %v", err)
	}
	// A move through it fails before it touches anything, says so, and undoes what it did.
	e.seed(t, e.tenantB, 4)
	cfg := e.cfg
	svc := cfg.Services["mvt"]
	svc.Pooled = e.pooler.DSN(svc.Pooled)
	svc.Cells = map[string]string{"c1": e.pooler.DSN(svc.Cells["c1"]), "c2": e.pooler.DSN(svc.Cells["c2"])}
	cfg.Services = map[string]cells.Service{"mvt": svc}
	_, err = cells.MoveTenant(ctx, cfg, e.dir, e.tenantB, "c1", cells.MoveOptions{Settle: e.settle, FenceWait: time.Second, Log: testkit.Logger()})
	if err == nil || !strings.Contains(err.Error(), "everything undone") || !strings.Contains(err.Error(), "unsupported startup parameter: search_path") {
		t.Fatalf("a move through the pooler: %v", err)
	}
	if p, _ := e.dir.Placement(ctx, e.tenantB); !p.Pooled() || !p.Serving() {
		t.Fatalf("directory = %+v", p)
	}
	if n := e.rows(t, "", e.tenantB); n != 4*5 {
		t.Fatalf("the failed move left %d rows in the pooled database, want %d", n, 4*5)
	}
	// And the same tenant moves fine with the owner DSNs the runbook prescribes.
	e.move(t, e.tenantB, "c1")
}

// pgx names a prepared statement after its text and keeps it on the connection. A pooler that does not track them (PgBouncer before 1.21, or
// max_prepared_statements = 0) sends a client's statement to a server connection that has another client's, or none. The run books give three ways out; each is
// checked here against a pooler that tracks nothing.
func TestPreparedStatementsBehindAPooler(t *testing.T) {
	requirePooler(t)
	ctx := testkit.Ctx(t)
	nw := testkit.StartNetwork(t)
	pg := testkit.StartPostgresOn(t, nw, "pg1")
	db := testkit.MigratedDB(t, pg, "mvt", fstest.MapFS{"00001_init.sql": {Data: []byte(fixtureSQL)}})
	tracking := testkit.StartPgBouncer(t, nw, testkit.PoolerOptions{Routes: map[string]testkit.Postgres{"*": pg}, MaxPreparedStatements: 200})
	blind := testkit.StartPgBouncer(t, nw, testkit.PoolerOptions{Routes: map[string]testkit.Postgres{"*": pg}, MaxPreparedStatements: 0})

	// run is 8 clients, 40 transactions each, over a pool that has two server connections.
	run := func(dsn string) error {
		pool, err := pgxx.NewPool(ctx, dsn)
		if err != nil {
			return err
		}
		defer pool.Close()
		var wg sync.WaitGroup
		errs := make(chan error, 8)
		for range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := range 40 {
					err := pgxx.InTenantTx(ctx, pool, uuid.New(), func(tx pgx.Tx) error {
						var n int
						if err := tx.QueryRow(ctx, `SELECT count(*) FROM accounts WHERE id = $1`, i).Scan(&n); err != nil {
							return err
						}
						_, err := tx.Exec(ctx, `SELECT $1::int + 1, $2::text`, i, "x")
						return err
					})
					if err != nil {
						errs <- err
						return
					}
				}
			}()
		}
		wg.Wait()
		close(errs)
		var all []error
		for err := range errs {
			all = append(all, err)
		}
		return errors.Join(all...)
	}
	with := func(p testkit.Pooler, mode string) string {
		u, err := url.Parse(p.DSN(db.App))
		if err != nil {
			t.Fatal(err)
		}
		if mode != "" {
			q := u.Query()
			q.Set("default_query_exec_mode", mode)
			u.RawQuery = q.Encode()
		}
		return u.String()
	}

	if err := run(with(tracking, "")); err != nil {
		t.Errorf("a pooler that tracks prepared statements, the driver's default mode: %v", err)
	}
	err := run(with(blind, ""))
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || !strings.Contains(pgErr.Message, "prepared statement") {
		t.Errorf("a pooler that tracks nothing, the driver's default mode: want a prepared statement error, got %v", err)
	}
	// The modes of the driver that do not leave a named statement behind. (describe_exec, cache_describe and exec all use the extended protocol with the unnamed
	// statement; simple_protocol sends the arguments as text.)
	for _, mode := range []string{"exec", "cache_describe", "describe_exec", "simple_protocol"} {
		if err := run(with(blind, mode)); err != nil {
			t.Errorf("a pooler that tracks nothing, default_query_exec_mode=%s: %v", mode, err)
		}
	}
}

// Invalidate makes the in-memory directory a tenantdir.Source for a router: it has no cache to drop.
func (d *memDir) Invalidate(uuid.UUID) {}
