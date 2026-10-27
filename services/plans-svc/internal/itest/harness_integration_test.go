//go:build integration

// Package itest runs plans-svc against a real Postgres: migrations, both pools, the gRPC server in-process.
package itest

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/durationpb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/grpcx"
	"github.com/0furkancolak/payrudder/pkg/outbox"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/secretbox"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/pkg/tenantdir"
	"github.com/0furkancolak/payrudder/pkg/testkit"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/grpcapi"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/identity/usermail"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/placement"
	saascmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/saas/command"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/sandbox"
	sandboxapi "github.com/0furkancolak/payrudder/services/plans-svc/internal/sandbox/api"
	"github.com/0furkancolak/payrudder/services/plans-svc/migrations"
)

type recordingEvictor struct {
	mu       sync.Mutex
	prefixes []string
}

func (r *recordingEvictor) EvictAPIKey(_ context.Context, prefix string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.prefixes = append(r.prefixes, prefix)
	return nil
}

// recordingMail is the mail path of the tests: it keeps what would have been sent, so a test can follow the link in it (the database holds only hashes).
type recordingMail struct {
	mu   sync.Mutex
	sent []usermail.Mail
}

func (r *recordingMail) Send(_ context.Context, m usermail.Mail) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sent = append(r.sent, m)
}

// to returns the mails of a kind sent to an address, oldest first.
func (r *recordingMail) to(kind usermail.Kind, address string) []usermail.Mail {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []usermail.Mail
	for _, m := range r.sent {
		if m.Kind == kind && m.To == address {
			out = append(out, m)
		}
	}
	return out
}

// lastToken is the token in the link of the newest mail of a kind to an address ("" when none): links are <app>/<page>#token=<token>.
func (r *recordingMail) lastToken(kind usermail.Kind, address string) string {
	ms := r.to(kind, address)
	if len(ms) == 0 {
		return ""
	}
	_, tok, _ := strings.Cut(ms[len(ms)-1].Link, "#token=")
	return tok
}

const testAppURL = "http://app.test"

// testPepper keys the hash of one-time sign-in codes in these tests.
const testPepper = "test-pepper-not-a-secret"

type env struct {
	w      *world
	mail   *recordingMail
	db     testkit.ServiceDB
	app    *pgxpool.Pool
	system *pgxpool.Pool
	// appDB is what the app role opens a tenant's transaction on: the app pool, or in cells mode its router. places is the system role's view of where a tenant's rows are
	// (no cells in pooled mode, so it is the system pool). A test that touches a tenant's own tables goes through them, never through app and system directly.
	appDB    pgxx.Beginner
	places   *placement.Places
	plans    plansv1.PlansServiceClient
	admin    plansv1.AdminServiceClient
	ident    plansv1.IdentityServiceClient
	hooks    plansv1.WebhookServiceClient
	mailprov plansv1.MailIntegrationServiceClient
	aff      plansv1.AffiliateServiceClient
	pub      plansv1.AffiliatePublicServiceClient
	portal   plansv1.AffiliatePortalServiceClient
	box      *secretbox.Box
	evicted  *recordingEvictor
	// runner runs the sandbox resets (a test calls RunOnce itself, so nothing happens behind its back); remotes records what the other services were asked.
	runner  *sandbox.Runner
	remotes *recordingRemotes
	// saas are the SaaS commands (Bootstrap, SweepOnce) and saasAPI the gRPC client of SaasService (phase 55).
	saas    *saascmd.Commands
	saasAPI plansv1.SaasServiceClient
}

// world is one Postgres with a migrated plans_db and, in cells mode (ADR 0018), a second database holding the schemas of the tenants: every tenant lives in one of them,
// placed on first use. The global tables (accounts, tenants, apps, users, keys, the reset queue) stay in the pooled database.
type world struct {
	pg      testkit.Postgres
	db      testkit.ServiceDB
	cells   bool
	cellApp *pgxpool.Pool
	cellSys *pgxpool.Pool
	dir     *lazyCells
}

// startWorld starts a Postgres of its own; t closes it.
func startWorld(t testing.TB, cells bool) *world {
	t.Helper()
	w := &world{pg: testkit.StartPostgres(t), cells: cells}
	w.db = testkit.MigratedDB(t, w.pg, "plans", migrations.FS)
	if cells {
		w.cellApp, w.cellSys, w.dir = openCells(t, w.pg, w.db)
	}
	return w
}

var (
	top        *testing.T // set by TestPlansSvc
	sharedOnce sync.Once
	shared     *world // the world of TestPlansSvc: cells only when ITEST_CELLS is set
)

// cellsMode runs the whole suite with every tenant in a cell instead of the pooled database: ITEST_CELLS=1 go test -tags integration ./internal/itest.
func cellsMode() bool { return os.Getenv("ITEST_CELLS") != "" }

// lazyCells is the directory of the cells-mode suite: a tenant is placed in cell c1 (and its schema provisioned) the first time anything asks where it is, so the
// scenarios, which make up their tenants as they go, need no change. A test can mark a tenant as moving. Like the real directory it is the tenants table: the columns
// cell and placement_status of the pooled database say what the harness says, because the statements of plans-svc that tell pooled tenants from the others read them.
type lazyCells struct {
	mu       sync.Mutex
	migrator string
	pooled   *pgxpool.Pool // the pooled system pool, which keeps tenants.cell and placement_status in step
	placed   map[uuid.UUID]bool
	moving   map[uuid.UUID]bool
	stay     map[uuid.UUID]bool // tenants that stay in the pooled database although cells are on: a deployment half way through its placements
}

func newLazyCells(migrator string, pooled *pgxpool.Pool) *lazyCells {
	return &lazyCells{migrator: migrator, pooled: pooled, placed: map[uuid.UUID]bool{}, moving: map[uuid.UUID]bool{}, stay: map[uuid.UUID]bool{}}
}

// inCell says whether a tenant is (or will be) placed in the cell, whatever its status.
func (l *lazyCells) inCell(id uuid.UUID) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return !l.stay[id]
}

// place makes a tenant that was kept pooled a tenant of the cell (a move to the cell, the directory's side of it); the schema is the move's to build.
func (l *lazyCells) place(id uuid.UUID) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.stay, id)
	l.placed[id] = true
}

// keepPooled leaves a tenant in the pooled database (call it before anything asks where the tenant is).
func (l *lazyCells) keepPooled(id uuid.UUID) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.stay[id] = true
}

func (l *lazyCells) status(id uuid.UUID) tenantdir.Status {
	if l.moving[id] {
		return tenantdir.StatusMoving
	}
	return tenantdir.StatusReady
}

// setMoving fences (or releases) a tenant the way a move does.
func (l *lazyCells) setMoving(id uuid.UUID, moving bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.moving[id] = moving
	_, _ = l.pooled.Exec(context.Background(), `UPDATE tenants SET placement_status = $2 WHERE id = $1`, id, string(l.status(id)))
}

func (l *lazyCells) Placement(ctx context.Context, id uuid.UUID) (tenantdir.Placement, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.stay[id] {
		return tenantdir.Placement{TenantID: id, Status: l.status(id)}, nil
	}
	if !l.placed[id] {
		if _, err := pgxx.ProvisionTenant(ctx, l.migrator, tenantSchemas(), id, nil); err != nil {
			return tenantdir.Placement{}, err
		}
		l.placed[id] = true
		if _, err := l.pooled.Exec(ctx, `UPDATE tenants SET cell = 'c1' WHERE id = $1`, id); err != nil {
			return tenantdir.Placement{}, err
		}
	}
	return tenantdir.Placement{TenantID: id, Cell: "c1", Status: l.status(id)}, nil
}

func (l *lazyCells) Invalidate(uuid.UUID) {}

func (l *lazyCells) Placed(context.Context, string) ([]tenantdir.Placement, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []tenantdir.Placement
	for id := range l.placed {
		out = append(out, tenantdir.Placement{TenantID: id, Cell: "c1", Status: l.status(id)})
	}
	return out, nil
}

// tenantSchemas is what a tenant's schema in a cell is built from.
func tenantSchemas() pgxx.TenantSchemas {
	return pgxx.TenantSchemas{Migrations: migrations.Tenant(), Roles: []string{"plans_app", "plans_system"}}
}

// openCells opens the pools of the cell c1 of a world and its lazy directory (cells mode only).
func openCells(t testing.TB, pg testkit.Postgres, pooled testkit.ServiceDB) (cellApp, cellSys *pgxpool.Pool, dir *lazyCells) {
	t.Helper()
	cell := testkit.CreateCellDB(t, pg, "plans", "c1")
	cellApp, err := pgxx.NewPool(context.Background(), cell.App)
	if err != nil {
		t.Fatal(err)
	}
	cellSys, err = pgxx.NewPool(context.Background(), cell.System)
	if err != nil {
		t.Fatal(err)
	}
	sys, err := pgxx.NewPool(context.Background(), pooled.System)
	if err != nil {
		t.Fatal(err)
	}
	return cellApp, cellSys, newLazyCells(cell.Migrator, sys)
}

// routers builds the app-role and system-role routers of a process over its pooled pools and the pools of the cell.
func routers(t testing.TB, app, system, cellApp, cellSys *pgxpool.Pool, dir tenantdir.Source) (appR, sysR *pgxx.Router) {
	t.Helper()
	appR, err := pgxx.NewRouter(app, map[string]*pgxpool.Pool{"c1": cellApp}, dir)
	if err != nil {
		t.Fatal(err)
	}
	sysR, err = pgxx.NewRouter(system, map[string]*pgxpool.Pool{"c1": cellSys}, dir)
	if err != nil {
		t.Fatal(err)
	}
	return appR, sysR
}

// newEnv starts (once per package) Postgres with a migrated plans_db and returns a server with the given environments.
func newEnv(t *testing.T, allowedEnvs ...string) *env {
	t.Helper()
	return newEnvWith(t, saascmd.Config{}, allowedEnvs...)
}

// newEnvWith is newEnv with the SaaS mode (phase 55) set as given; the zero Config is off, as in every other test.
func newEnvWith(t *testing.T, saasCfg saascmd.Config, allowedEnvs ...string) *env {
	t.Helper()
	sharedOnce.Do(func() {
		// Started with the top-level test's t (see TestPlansSvc), so it lives for the whole package run.
		shared = startWorld(top, cellsMode())
	})
	return newEnvIn(t, shared, saasCfg, allowedEnvs...)
}

// newEnvIn starts a plans-svc over a world: its two pools of the pooled database, and in a world with cells their routers.
func newEnvIn(t *testing.T, w *world, saasCfg saascmd.Config, allowedEnvs ...string) *env {
	t.Helper()
	if len(allowedEnvs) == 0 {
		allowedEnvs = []string{"sandbox", "live"}
	}
	ctx := context.Background()
	app, err := pgxx.NewPool(ctx, w.db.App)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Close)
	system, err := pgxx.NewPool(ctx, w.db.System)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(system.Close)

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if os.Getenv("ITEST_VERBOSE") != "" {
		log = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	var appDB pgxx.Beginner = app
	places := placement.NewPlaces(system, nil)
	if w.cells {
		appR, sysR := routers(t, app, system, w.cellApp, w.cellSys, w.dir)
		appDB, places = placement.AppDB(app, appR, system), placement.NewPlaces(system, sysR)
	}
	ev := &recordingEvictor{}
	srv, _ := grpcx.NewServer(grpcx.ServerOptions{Logger: log, Extra: []grpc.ServerOption{
		grpc.ChainUnaryInterceptor(tenant.UnaryServerInterceptor()),
		grpc.ChainStreamInterceptor(tenant.StreamServerInterceptor()),
	}})
	box, err := secretbox.New(make([]byte, 32)) // an all-zero key is fine for a test database
	if err != nil {
		t.Fatal(err)
	}
	mail := &recordingMail{}
	remotes := &recordingRemotes{}
	sbx, runner := sandboxapi.Wire(places, remotes.steps(), time.Hour, sandbox.Settings{Retention: time.Hour}, log)
	saas := grpcapi.Register(srv, grpcapi.Deps{
		AppDB: appDB, SystemPool: system, Places: places, Log: log, Evict: ev, AllowedEnvs: allowedEnvs, Box: box, WebhookAllowPrivate: true,
		Mail: mail, AppURL: testAppURL, OTPPepper: testPepper, Sandbox: sbx, Saas: saasCfg,
	})
	lis := bufconn.Listen(1 << 20)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	cc, err := grpcx.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cc.Close() })
	return &env{w: w, saas: saas, saasAPI: plansv1.NewSaasServiceClient(cc), runner: runner, remotes: remotes, db: w.db, app: app, system: system, appDB: appDB, places: places, plans: plansv1.NewPlansServiceClient(cc),
		admin: plansv1.NewAdminServiceClient(cc), ident: plansv1.NewIdentityServiceClient(cc), hooks: plansv1.NewWebhookServiceClient(cc), mailprov: plansv1.NewMailIntegrationServiceClient(cc), aff: plansv1.NewAffiliateServiceClient(cc), pub: plansv1.NewAffiliatePublicServiceClient(cc), portal: plansv1.NewAffiliatePortalServiceClient(cc), box: box, evicted: ev, mail: mail}
}

func operator() context.Context {
	return metadata.AppendToOutgoingContext(context.Background(), tenant.MDOperator, "true")
}

func as(tenantID string) context.Context {
	return metadata.AppendToOutgoingContext(context.Background(), tenant.MDTenantID, tenantID)
}

// account creates an account and returns its tenants by environment, the live one already activated (an operator's decision, as in support): most
// tests are about what a working live tenant does. inactiveAccount keeps the live tenant as it is created.
func (e *env) account(t *testing.T, name string) map[plansv1.Environment]*plansv1.Tenant {
	t.Helper()
	out := e.inactiveAccount(t, name)
	if live := out[plansv1.Environment_ENVIRONMENT_LIVE]; live != nil {
		out[plansv1.Environment_ENVIRONMENT_LIVE] = e.activate(t, live.GetId())
	}
	return out
}

// inactiveAccount creates an account whose live tenant is inactive, as every new one is.
func (e *env) inactiveAccount(t *testing.T, name string) map[plansv1.Environment]*plansv1.Tenant {
	t.Helper()
	resp, err := e.admin.CreateAccount(operator(), &plansv1.CreateAccountRequest{
		Name: name, Currency: "TRY", Timezone: "Europe/Istanbul", GracePeriod: durationpb.New(24 * time.Hour),
		Tier: plansv1.TenantTier_TENANT_TIER_STANDARD,
	})
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	out := map[plansv1.Environment]*plansv1.Tenant{}
	for _, tn := range resp.GetAccount().GetTenants() {
		out[tn.GetEnvironment()] = tn
	}
	return out
}

// activate makes a live tenant active without its checklist, the way an operator does.
func (e *env) activate(t *testing.T, tenantID string) *plansv1.Tenant {
	t.Helper()
	resp, err := e.admin.SetTenantStatus(operator(), &plansv1.SetTenantStatusRequest{TenantId: tenantID, Status: plansv1.TenantStatus_TENANT_STATUS_ACTIVE, Reason: "itest"})
	if err != nil {
		t.Fatalf("SetTenantStatus: %v", err)
	}
	return resp.GetTenant()
}

// tx runs fn as the system role in a transaction at the tenant's place: the pooled database, or the tenant's schema in cells mode. It is how a test looks at, or changes,
// a table that has a tenant_id; the tables without one (accounts, tenants, apps, users, api keys, the reset queue) are read on e.system.
func (e *env) tx(ctx context.Context, tenantID string, fn func(pgx.Tx) error) error {
	return e.places.Tx(ctx, uuid.MustParse(tenantID), fn)
}

// scan runs a query that returns one row, as the system role at the tenant's place, into dest.
func (e *env) scan(ctx context.Context, tenantID, query string, args []any, dest ...any) error {
	return e.tx(ctx, tenantID, func(tx pgx.Tx) error { return tx.QueryRow(ctx, query, args...).Scan(dest...) })
}

// exec runs a statement as the system role at the tenant's place.
func (e *env) exec(ctx context.Context, tenantID, stmt string, args ...any) error {
	return e.tx(ctx, tenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, stmt, args...)
		return err
	})
}

// relayAll relays the outbox into sink until nothing is left: the pooled table, and in cells mode the outbox of every tenant's schema.
func (e *env) relayAll(t testing.TB, sink outbox.Sink) {
	t.Helper()
	r, err := outbox.NewRelay(e.system, sink, outbox.Config{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for {
		n, err := r.RelayOnce(context.Background())
		if err != nil {
			t.Fatalf("relay: %v", err)
		}
		if n == 0 {
			break
		}
	}
	err = e.places.EachCell(context.Background(), func(ctx context.Context, p pgxx.PlacedTenant) error {
		for {
			n, err := r.RelayTenantOnce(ctx, p)
			if err != nil || n == 0 {
				return err
			}
		}
	})
	if err != nil {
		t.Fatalf("relay (cells): %v", err)
	}
}

// ownerDB is what a tenant transaction of the owner of the tables (the migrator role, which row-level security and the grants do not bind, the triggers do) is opened
// on: the pooled database, or in cells mode a router over the owner's pools of both places.
func (e *env) ownerDB(t testing.TB) pgxx.Beginner {
	t.Helper()
	ctx := context.Background()
	pooled, err := pgxx.NewPool(ctx, e.db.Migrator)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pooled.Close)
	if !e.w.cells {
		return pooled
	}
	cell, err := pgxx.NewPool(ctx, e.w.dir.migrator)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cell.Close)
	r, err := pgxx.NewRouter(pooled, map[string]*pgxpool.Pool{"c1": cell}, e.w.dir)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// clearDeliveries deletes every queued webhook delivery, wherever it is: the dispatcher works across tenants, and other tests' deliveries (to addresses that mean nothing
// to the test) must not be in its way.
func (e *env) clearDeliveries(t testing.TB) {
	t.Helper()
	ctx := context.Background()
	if _, err := e.system.Exec(ctx, `DELETE FROM webhook_deliveries`); err != nil {
		t.Fatal(err)
	}
	err := e.places.EachCell(ctx, func(ctx context.Context, p pgxx.PlacedTenant) error {
		return e.places.CellTx(ctx, p, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `DELETE FROM webhook_deliveries`)
			return err
		})
	})
	if err != nil {
		t.Fatal(err)
	}
}

// countWithoutTenant counts the rows a statement of the app role sees in a table when it names no tenant: the pooled table, or in cells mode the tenant's schema,
// where row-level security is the second guard. Zero is the right answer in both.
func (e *env) countWithoutTenant(ctx context.Context, tenantID, table string) (int, error) {
	q := fmt.Sprintf(`SELECT count(*) FROM %s`, table)
	var n int
	if !e.w.cells {
		return n, e.app.QueryRow(ctx, q).Scan(&n)
	}
	err := pgxx.InTx(ctx, e.w.cellApp, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('search_path', $1, true)`, pgxx.SearchPath(tenantdir.Schema(uuid.MustParse(tenantID)))); err != nil {
			return err
		}
		return tx.QueryRow(ctx, q).Scan(&n)
	})
	return n, err
}
