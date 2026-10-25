//go:build integration

// Package itest runs plans-svc against a real Postgres: migrations, both pools, the gRPC server in-process.
package itest

import (
	"context"
	"io"
	"log/slog"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/durationpb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/grpcx"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/secretbox"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/pkg/testkit"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/admin"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/golive"
	golivecmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/golive/command"
	goliveqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/golive/query"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/grpcapi"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/identity"
	identitycmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/identity/command"
	identityqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/identity/query"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/identity/usermail"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/promote"
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
	mail    *recordingMail
	db      testkit.ServiceDB
	app     *pgxpool.Pool
	system  *pgxpool.Pool
	plans   plansv1.PlansServiceClient
	admin   plansv1.AdminServiceClient
	ident   plansv1.IdentityServiceClient
	hooks   plansv1.WebhookServiceClient
	aff     plansv1.AffiliateServiceClient
	pub     plansv1.AffiliatePublicServiceClient
	box     *secretbox.Box
	evicted *recordingEvictor
	// runner runs the sandbox resets (a test calls RunOnce itself, so nothing happens behind its back); remotes records what the other services were asked.
	runner  *sandbox.Runner
	remotes *recordingRemotes
}

var (
	top        *testing.T // set by TestPlansSvc
	sharedOnce sync.Once
	shared     struct {
		pg testkit.Postgres
		db testkit.ServiceDB
	}
)

// newEnv starts (once per package) Postgres with a migrated plans_db and returns a server with the given environments.
func newEnv(t *testing.T, allowedEnvs ...string) *env {
	t.Helper()
	if len(allowedEnvs) == 0 {
		allowedEnvs = []string{"sandbox", "live"}
	}
	sharedOnce.Do(func() {
		// Started with the top-level test's t (see TestPlansSvc), so it lives for the whole package run.
		shared.pg = testkit.StartPostgres(top)
		shared.db = testkit.MigratedDB(top, shared.pg, "plans", migrations.FS)
	})
	ctx := context.Background()
	app, err := pgxx.NewPool(ctx, shared.db.App)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Close)
	system, err := pgxx.NewPool(ctx, shared.db.System)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(system.Close)

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if os.Getenv("ITEST_VERBOSE") != "" {
		log = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	ev := &recordingEvictor{}
	srv, _ := grpcx.NewServer(grpcx.ServerOptions{Logger: log, Extra: []grpc.ServerOption{
		grpc.ChainUnaryInterceptor(tenant.UnaryServerInterceptor()),
		grpc.ChainStreamInterceptor(tenant.StreamServerInterceptor()),
	}})
	plansSrv := grpcapi.New(app, log)
	plansv1.RegisterPlansServiceServer(srv, plansSrv)
	box, err := secretbox.New(make([]byte, 32)) // an all-zero key is fine for a test database
	if err != nil {
		t.Fatal(err)
	}
	plansv1.RegisterWebhookServiceServer(srv, grpcapi.NewWebhooks(plansSrv, box, true))
	plansv1.RegisterAffiliateServiceServer(srv, grpcapi.NewAffiliates(plansSrv))
	plansv1.RegisterAffiliatePublicServiceServer(srv, grpcapi.NewAffiliatesPublic(system, log))
	adminSrv := admin.New(system, ev, allowedEnvs, log)
	plansv1.RegisterAdminServiceServer(srv, adminSrv)
	mail := &recordingMail{}
	remotes := &recordingRemotes{}
	sbx, runner := sandboxapi.Wire(system, remotes.steps(), time.Hour, sandbox.Settings{Retention: time.Hour}, log)
	plansv1.RegisterIdentityServiceServer(srv, identity.New(identitycmd.New(system, adminSrv, adminSrv, mail, testAppURL, log).WithPepper(testPepper), identityqry.New(system),
		golive.New(golivecmd.New(system, log), goliveqry.New(system)),
		promote.Wire(app, system, box, true, log), sbx))
	lis := bufconn.Listen(1 << 20)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	cc, err := grpcx.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cc.Close() })
	return &env{runner: runner, remotes: remotes, db: shared.db, app: app, system: system, plans: plansv1.NewPlansServiceClient(cc),
		admin: plansv1.NewAdminServiceClient(cc), ident: plansv1.NewIdentityServiceClient(cc), hooks: plansv1.NewWebhookServiceClient(cc), aff: plansv1.NewAffiliateServiceClient(cc), pub: plansv1.NewAffiliatePublicServiceClient(cc), box: box, evicted: ev, mail: mail}
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
