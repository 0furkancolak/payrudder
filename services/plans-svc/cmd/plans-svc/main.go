// Command plans-svc is the control plane: tenants, API keys, customers, meters, plans and subscriptions.
package main

import (
	"context"
	_ "embed" // config.default.yaml
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"os/signal"
	"slices"
	"syscall"
	"time"
	_ "time/tzdata" // tenant time zones must load in distroless images

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	grpchealth "google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	adminv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/admin/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/config"
	"github.com/0furkancolak/payrudder/pkg/grpcx"
	"github.com/0furkancolak/payrudder/pkg/health"
	"github.com/0furkancolak/payrudder/pkg/kafkax"
	"github.com/0furkancolak/payrudder/pkg/logx"
	"github.com/0furkancolak/payrudder/pkg/mailx"
	"github.com/0furkancolak/payrudder/pkg/otelx"
	"github.com/0furkancolak/payrudder/pkg/outbox"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/safehttp"
	"github.com/0furkancolak/payrudder/pkg/secretbox"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	_ "github.com/0furkancolak/payrudder/pkg/tzdata" // IANA zones for time.LoadLocation in distroless images
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/admin"
	affiliatescmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/affiliates/command"
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
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/webhooks"
	"github.com/0furkancolak/payrudder/services/plans-svc/migrations"
)

//go:embed config.default.yaml
var defaults []byte

type Config struct {
	config.Base
	pgxx.Settings
	GRPCAddr string `env:"PAYRUDDER_GRPC_ADDR"`
	RedisURL string `env:"PAYRUDDER_REDIS_URL"`
	// KafkaBrokers enables the outbox relay to plans.entitlements.v1. Without it, rows wait in the outbox.
	KafkaBrokers   []string `env:"PAYRUDDER_KAFKA_BROKERS" envSeparator:","`
	RelayWorkers   int      `env:"PAYRUDDER_RELAY_WORKERS"`
	RelayBatchSize int      `env:"PAYRUDDER_RELAY_BATCH_SIZE"`
	AllowedEnvs    []string `env:"PAYRUDDER_ALLOWED_ENVIRONMENTS" envSeparator:","`
	// SecretsKey (32 bytes, base64) seals the webhook signing secrets. Without it tenants cannot register webhook endpoints.
	SecretsKey string `env:"PAYRUDDER_SECRETS_KEY"`
	// WebhookAllowPrivate lets webhook endpoints be http:// and on private addresses. For development and tests; never in production.
	WebhookAllowPrivate bool `env:"PAYRUDDER_WEBHOOK_ALLOW_PRIVATE"`
	// SMTPAddr and MailFrom are the platform's own mail path: the mail Payrudder writes to people (verification, password reset, invitations). A tenant's
	// mail to its customers goes through the notifier and the tenant's mail switch instead.
	SMTPAddr string `env:"SMTP_ADDR"`
	MailFrom string `env:"MAIL_FROM"`
	// AppURL is the console's public address; the links in those mails point into it.
	AppURL string `env:"PAYRUDDER_APP_URL"`
	// The services a sandbox reset empties besides plans-svc itself (phase 45 E). An empty address skips that step, so a deployment without the service still resets
	// the rest. The gRPC ones are the services' usual addresses; payments-worker is HTTP and takes the internal token the gateway also uses.
	IngestAddr     string `env:"PAYRUDDER_INGEST_ADDR"`
	AggregatorAddr string `env:"PAYRUDDER_AGGREGATOR_ADDR"`
	AnalyticsAddr  string `env:"PAYRUDDER_ANALYTICS_ADDR"`
	BillingAddr    string `env:"PAYRUDDER_BILLING_ADDR"`
	PaymentsURL    string `env:"PAYRUDDER_PAYMENTS_URL"`
	PaymentsToken  string `env:"PAYRUDDER_PAYMENTS_INTERNAL_TOKEN"`
	// SandboxRetention is how long a sandbox may sit untouched before its data is deleted (configuration is kept); 0 switches it off. SandboxResetCooldown is the
	// least time between two resets a person asks for.
	SandboxRetention     time.Duration `env:"PAYRUDDER_SANDBOX_RETENTION"`
	SandboxResetCooldown time.Duration `env:"PAYRUDDER_SANDBOX_RESET_COOLDOWN"`
	// OTPPepper keys the hash of one-time sign-in codes (a secret, never in YAML). Without it the hash is salted but unkeyed: fine for development, set it in production.
	OTPPepper string `env:"PAYRUDDER_OTP_PEPPER"`
	// AffiliateApproveEvery is how often the job that ends the hold of affiliate commissions looks (phase 47). The holds are days long; an hour is plenty.
	AffiliateApproveEvery time.Duration `env:"PAYRUDDER_AFFILIATE_APPROVE_EVERY"`
}

func (c *Config) Validate() error {
	errs := []error{c.Base.Validate()}
	errs = append(errs, c.CheckMigration())
	for _, e := range c.AllowedEnvs {
		if e != tenant.EnvSandbox && e != tenant.EnvLive {
			errs = append(errs, fmt.Errorf("PAYRUDDER_ALLOWED_ENVIRONMENTS: unknown environment %q", e))
		}
	}
	if c.SecretsKey != "" {
		if _, err := secretbox.ParseKey(c.SecretsKey); err != nil {
			errs = append(errs, fmt.Errorf("PAYRUDDER_SECRETS_KEY: %w", err))
		}
	}
	if c.AffiliateApproveEvery < time.Minute {
		errs = append(errs, errors.New("PAYRUDDER_AFFILIATE_APPROVE_EVERY must be at least a minute"))
	}
	if !slices.Contains(c.AllowedEnvs, tenant.EnvSandbox) {
		errs = append(errs, errors.New("PAYRUDDER_ALLOWED_ENVIRONMENTS must include sandbox"))
	}
	return errors.Join(errs...)
}

// redisEvictor deletes gateway API-key cache entries.
type redisEvictor struct{ c *redis.Client }

func (r redisEvictor) EvictAPIKey(ctx context.Context, prefix string) error {
	return r.c.Del(ctx, "gw:apikey:"+prefix).Err()
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "plans-svc:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load[Config](defaults)
	if err != nil {
		return err
	}
	log, err := logx.New(logx.Options{Service: cfg.ServiceName, Version: cfg.Version, Level: cfg.LogLevel, Writer: os.Stdout})
	if err != nil {
		return err
	}
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	reg := health.NewRegistry()
	providers, err := otelx.Setup(ctx, otelx.Options{
		ServiceName: cfg.ServiceName, Version: cfg.Version, Environment: string(cfg.Env), OTLPEndpoint: cfg.OTLPEndpoint, Registry: reg,
	})
	if err != nil {
		return err
	}
	defer func() {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = providers.Shutdown(c)
	}()

	return start(ctx, &cfg, log, health.New(reg, 2*time.Second))
}

// start wires plans-svc's dependencies and runs it until ctx is cancelled.
func start(ctx context.Context, cfg *Config, log *slog.Logger, hh *health.Handler) error {
	if done, err := cfg.MigrateIfAsked(ctx, migrations.FS, cfg.MigrateOnly, log); err != nil || done {
		return err
	}
	appPool, systemPool, err := cfg.OpenPools(ctx)
	if err != nil {
		return err
	}
	defer appPool.Close()
	defer systemPool.Close()

	hh.AddCheck("postgres", appPool.Ping)
	evictor, closeRedis, err := newEvictor(cfg, log)
	if err != nil {
		return err
	}
	defer closeRedis()
	relay, closeRelay, err := newRelay(cfg, log, systemPool, hh)
	if err != nil {
		return err
	}
	defer closeRelay()

	sbx, sbxRunner, closeSbx, err := newSandbox(cfg, log, appPool, systemPool)
	if err != nil {
		return err
	}
	defer closeSbx()
	srv, grpcHealth, box, err := newGRPCServer(cfg, log, appPool, systemPool, evictor, sbx)
	if err != nil {
		return err
	}
	lis, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		return err
	}
	w := &workers{cfg: cfg, log: log, pool: systemPool, relay: relay, box: box, health: hh, srv: srv, grpcHealth: grpcHealth, lis: lis, sandbox: sbxRunner}
	return w.run(ctx)
}

// newEvictor connects to Redis for gateway API-key cache eviction; without PAYRUDDER_REDIS_URL it returns a nil evictor.
// The returned func releases the connection.
func newEvictor(cfg *Config, log *slog.Logger) (admin.CacheEvictor, func(), error) {
	if cfg.RedisURL == "" {
		log.Warn("PAYRUDDER_REDIS_URL unset: revoked keys stay valid in the gateway cache until its TTL")
		return nil, func() {}, nil
	}
	opts, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		return nil, nil, fmt.Errorf("PAYRUDDER_REDIS_URL: %w", err)
	}
	rc := redis.NewClient(opts)
	return redisEvictor{c: rc}, func() { _ = rc.Close() }, nil
}

// newRelay builds the outbox relay and registers its Kafka readiness check; without PAYRUDDER_KAFKA_BROKERS it returns a nil relay.
// The returned func closes the Kafka producer.
func newRelay(cfg *Config, log *slog.Logger, systemPool *pgxpool.Pool, hh *health.Handler) (*outbox.Relay, func(), error) {
	if len(cfg.KafkaBrokers) == 0 {
		log.Warn("PAYRUDDER_KAFKA_BROKERS unset: entitlement changes stay in the outbox and limits-svc will not see them")
		return nil, func() {}, nil
	}
	producer, err := kafkax.NewProducer(cfg.KafkaBrokers)
	if err != nil {
		return nil, nil, err
	}
	hh.AddCheck("kafka", producer.Ping)
	relay, err := outbox.NewRelay(systemPool, outbox.KafkaSink{Client: producer},
		outbox.Config{Workers: cfg.RelayWorkers, BatchSize: cfg.RelayBatchSize}, log)
	if err != nil {
		producer.Close()
		return nil, nil, err
	}
	return relay, producer.Close, nil
}

// newSecretBox opens the box that seals webhook signing secrets; without PAYRUDDER_SECRETS_KEY it returns nil.
func newSecretBox(cfg *Config, log *slog.Logger) (*secretbox.Box, error) {
	if cfg.SecretsKey == "" {
		log.Warn("PAYRUDDER_SECRETS_KEY is not set: tenants cannot register webhook endpoints")
		return nil, nil
	}
	key, _ := secretbox.ParseKey(cfg.SecretsKey) // validated
	return secretbox.New(key)
}

// newSandbox builds the sandbox reset module: the steps for the other services, the handler for the two calls, and the runner that executes the jobs. The returned func
// closes the connections.
func newSandbox(cfg *Config, log *slog.Logger, _, systemPool *pgxpool.Pool) (*sandboxapi.Handler, *sandbox.Runner, func(), error) {
	var conns []*grpc.ClientConn
	closeAll := func() {
		for _, c := range conns {
			_ = c.Close()
		}
	}
	remote := func(name, addr string) (sandbox.Step, error) {
		if addr == "" {
			return sandbox.Step{Name: name}, nil
		}
		conn, err := grpcx.NewClient(addr, grpc.WithChainUnaryInterceptor(tenant.UnaryClientInterceptor()))
		if err != nil {
			return sandbox.Step{}, fmt.Errorf("%s client: %w", name, err)
		}
		conns = append(conns, conn)
		return sandbox.GRPC(name, adminv1.NewSandboxResetServiceClient(conn)), nil
	}
	var steps []sandbox.Step
	// The order: usage first (the source, then what was built from it), then invoices, then payments.
	for _, r := range []struct{ name, addr string }{
		{"ingest", cfg.IngestAddr}, {"aggregator", cfg.AggregatorAddr}, {"analytics", cfg.AnalyticsAddr}, {"billing", cfg.BillingAddr},
	} {
		s, err := remote(r.name, r.addr)
		if err != nil {
			closeAll()
			return nil, nil, nil, err
		}
		steps = append(steps, s)
	}
	var payments *url.URL
	if cfg.PaymentsURL != "" {
		u, err := url.Parse(cfg.PaymentsURL)
		if err != nil {
			closeAll()
			return nil, nil, nil, fmt.Errorf("PAYRUDDER_PAYMENTS_URL: %w", err)
		}
		payments = u
	}
	steps = append(steps, sandbox.HTTP("payments", payments, cfg.PaymentsToken, nil))
	h, runner := sandboxapi.Wire(systemPool, steps, cfg.SandboxResetCooldown, sandbox.Settings{Retention: cfg.SandboxRetention}, log)
	return h, runner, closeAll, nil
}

// newGRPCServer builds the gRPC server with the plans, webhook and admin services registered.
func newGRPCServer(cfg *Config, log *slog.Logger, appPool, systemPool *pgxpool.Pool, evictor admin.CacheEvictor, sbx *sandboxapi.Handler) (*grpc.Server, *grpchealth.Server, *secretbox.Box, error) {
	srv, grpcHealth := grpcx.NewServer(grpcx.ServerOptions{
		Logger: log, Reflection: cfg.Env == config.EnvLocal,
		Extra: []grpc.ServerOption{
			grpc.ChainUnaryInterceptor(tenant.UnaryServerInterceptor()),
			grpc.ChainStreamInterceptor(tenant.StreamServerInterceptor()),
		},
	})
	plansSrv := grpcapi.New(appPool, log)
	plansv1.RegisterPlansServiceServer(srv, plansSrv)
	box, err := newSecretBox(cfg, log)
	if err != nil {
		return nil, nil, nil, err
	}
	plansv1.RegisterWebhookServiceServer(srv, grpcapi.NewWebhooks(plansSrv, box, cfg.WebhookAllowPrivate))
	plansv1.RegisterAffiliateServiceServer(srv, grpcapi.NewAffiliates(plansSrv))
	plansv1.RegisterAffiliatePublicServiceServer(srv, grpcapi.NewAffiliatesPublic(systemPool, log))
	plansv1.RegisterAffiliatePortalServiceServer(srv, grpcapi.NewAffiliatePortal(plansSrv, systemPool))
	adminSrv := admin.New(systemPool, evictor, cfg.AllowedEnvs, log)
	plansv1.RegisterAdminServiceServer(srv, adminSrv)
	plansv1.RegisterIdentityServiceServer(srv, identity.New(identitycmd.New(systemPool, adminSrv, adminSrv, usermail.NewSMTP(mailx.SMTP{Addr: cfg.SMTPAddr}, cfg.MailFrom, log), cfg.AppURL, log).WithPepper(cfg.OTPPepper), identityqry.New(systemPool),
		golive.New(golivecmd.New(systemPool, log), goliveqry.New(systemPool)),
		promote.Wire(appPool, systemPool, box, cfg.WebhookAllowPrivate, log), sbx))
	return srv, grpcHealth, box, nil
}

// workers are the long-running parts of plans-svc: the outbox relay, the webhook dispatcher and the gRPC server.
type workers struct {
	cfg        *Config
	log        *slog.Logger
	pool       *pgxpool.Pool
	relay      *outbox.Relay  // nil without Kafka
	box        *secretbox.Box // nil without a secrets key
	health     *health.Handler
	srv        *grpc.Server
	grpcHealth *grpchealth.Server
	lis        net.Listener
	sandbox    *sandbox.Runner // runs the sandbox resets and the retention sweep
}

// run starts everything and blocks until ctx is cancelled or one of them fails, then drains the gRPC server.
func (w *workers) run(ctx context.Context) error {
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return health.Serve(gctx, w.cfg.MetricsAddr, w.health) })
	if w.relay != nil {
		g.Go(func() error { return w.relay.Run(gctx) })
	}
	if w.box != nil {
		disp := &webhooks.Dispatcher{Pool: w.pool, Box: w.box, Client: safehttp.NewClient(10*time.Second, w.cfg.WebhookAllowPrivate), Log: w.log}
		g.Go(func() error { return disp.Run(gctx) })
	}
	if w.sandbox != nil {
		g.Go(func() error { return w.sandbox.Run(gctx) })
	}
	g.Go(func() error { return affiliatescmd.NewApprover(w.pool, w.cfg.AffiliateApproveEvery, w.log).Run(gctx) })
	g.Go(func() error {
		w.log.Info("grpc listening", "addr", w.cfg.GRPCAddr, "metrics", w.cfg.MetricsAddr, "environments", w.cfg.AllowedEnvs)
		return w.srv.Serve(w.lis)
	})
	g.Go(func() error {
		<-gctx.Done()
		w.grpcHealth.SetServingStatus("", healthpb.HealthCheckResponse_NOT_SERVING)
		w.srv.GracefulStop()
		return nil
	})
	if err := g.Wait(); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	w.log.Info("stopped")
	return nil
}
