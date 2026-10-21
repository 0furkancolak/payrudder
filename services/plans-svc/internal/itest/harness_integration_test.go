//go:build integration

// Package itest runs plans-svc against a real Postgres: migrations, both pools, the gRPC server in-process.
package itest

import (
	"context"
	"io"
	"log/slog"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/durationpb"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/grpcx"
	"github.com/0furkancolak/quotify/pkg/pgxx"
	"github.com/0furkancolak/quotify/pkg/secretbox"
	"github.com/0furkancolak/quotify/pkg/tenant"
	"github.com/0furkancolak/quotify/pkg/testkit"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/admin"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/grpcapi"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/identity"
	identitycmd "github.com/0furkancolak/quotify/services/plans-svc/internal/identity/command"
	identityqry "github.com/0furkancolak/quotify/services/plans-svc/internal/identity/query"
	"github.com/0furkancolak/quotify/services/plans-svc/migrations"
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

type env struct {
	db      testkit.ServiceDB
	app     *pgxpool.Pool
	system  *pgxpool.Pool
	plans   plansv1.PlansServiceClient
	admin   plansv1.AdminServiceClient
	ident   plansv1.IdentityServiceClient
	hooks   plansv1.WebhookServiceClient
	box     *secretbox.Box
	evicted *recordingEvictor
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
		shared.db = testkit.CreateServiceDB(top, shared.pg, "plans")
		if err := pgxx.Migrate(context.Background(), shared.db.Migrator, migrations.FS, nil); err != nil {
			t.Fatalf("migrate: %v", err)
		}
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
	adminSrv := admin.New(system, ev, allowedEnvs, log)
	plansv1.RegisterAdminServiceServer(srv, adminSrv)
	plansv1.RegisterIdentityServiceServer(srv, identity.New(identitycmd.New(system, adminSrv, adminSrv, log), identityqry.New(system)))
	lis := bufconn.Listen(1 << 20)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	cc, err := grpcx.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cc.Close() })
	return &env{db: shared.db, app: app, system: system, plans: plansv1.NewPlansServiceClient(cc),
		admin: plansv1.NewAdminServiceClient(cc), ident: plansv1.NewIdentityServiceClient(cc), hooks: plansv1.NewWebhookServiceClient(cc), box: box, evicted: ev}
}

func operator() context.Context {
	return metadata.AppendToOutgoingContext(context.Background(), tenant.MDOperator, "true")
}

func as(tenantID string) context.Context {
	return metadata.AppendToOutgoingContext(context.Background(), tenant.MDTenantID, tenantID)
}

// account creates an account and returns its tenants by environment.
func (e *env) account(t *testing.T, name string) map[plansv1.Environment]*plansv1.Tenant {
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
