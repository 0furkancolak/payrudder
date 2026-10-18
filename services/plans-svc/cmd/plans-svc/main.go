// Command plans-svc is the control plane: tenants, API keys, customers, meters, plans and subscriptions.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"slices"
	"syscall"
	"time"
	_ "time/tzdata" // tenant time zones must load in distroless images

	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/config"
	"github.com/0furkancolak/quotify/pkg/grpcx"
	"github.com/0furkancolak/quotify/pkg/health"
	"github.com/0furkancolak/quotify/pkg/kafkax"
	"github.com/0furkancolak/quotify/pkg/logx"
	"github.com/0furkancolak/quotify/pkg/otelx"
	"github.com/0furkancolak/quotify/pkg/outbox"
	"github.com/0furkancolak/quotify/pkg/pgxx"
	"github.com/0furkancolak/quotify/pkg/tenant"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/admin"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/grpcapi"
	"github.com/0furkancolak/quotify/services/plans-svc/migrations"
)

type Config struct {
	config.Base
	GRPCAddr       string `env:"QUOTIFY_GRPC_ADDR" envDefault:":7001"`
	PGAppDSN       string `env:"QUOTIFY_PG_APP_DSN,required"`
	PGSystemDSN    string `env:"QUOTIFY_PG_SYSTEM_DSN,required"`
	PGMigratorDSN  string `env:"QUOTIFY_PG_MIGRATOR_DSN"`
	MigrateOnStart bool   `env:"QUOTIFY_MIGRATE_ON_START" envDefault:"false"`
	RedisURL       string `env:"QUOTIFY_REDIS_URL"`
	// KafkaBrokers enables the outbox relay to plans.entitlements.v1. Without it, rows wait in the outbox.
	KafkaBrokers   []string `env:"QUOTIFY_KAFKA_BROKERS" envSeparator:","`
	RelayWorkers   int      `env:"QUOTIFY_RELAY_WORKERS" envDefault:"1"`
	RelayBatchSize int      `env:"QUOTIFY_RELAY_BATCH_SIZE" envDefault:"200"`
	AllowedEnvs    []string `env:"QUOTIFY_ALLOWED_ENVIRONMENTS" envSeparator:"," envDefault:"sandbox,live"`
}

func (c *Config) Validate() error {
	errs := []error{c.Base.Validate()}
	if c.MigrateOnStart && c.PGMigratorDSN == "" {
		errs = append(errs, errors.New("QUOTIFY_PG_MIGRATOR_DSN is required with QUOTIFY_MIGRATE_ON_START"))
	}
	for _, e := range c.AllowedEnvs {
		if e != tenant.EnvSandbox && e != tenant.EnvLive {
			errs = append(errs, fmt.Errorf("QUOTIFY_ALLOWED_ENVIRONMENTS: unknown environment %q", e))
		}
	}
	if !slices.Contains(c.AllowedEnvs, tenant.EnvSandbox) {
		errs = append(errs, errors.New("QUOTIFY_ALLOWED_ENVIRONMENTS must include sandbox"))
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
	cfg, err := config.Load[Config]()
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

	if cfg.MigrateOnStart {
		if err := pgxx.Migrate(ctx, cfg.PGMigratorDSN, migrations.FS, log); err != nil {
			return err
		}
	}
	appPool, err := pgxx.NewPool(ctx, cfg.PGAppDSN)
	if err != nil {
		return err
	}
	defer appPool.Close()
	systemPool, err := pgxx.NewPool(ctx, cfg.PGSystemDSN)
	if err != nil {
		return err
	}
	defer systemPool.Close()

	var evictor admin.CacheEvictor
	hh := health.New(reg, 2*time.Second)
	hh.AddCheck("postgres", appPool.Ping)
	if cfg.RedisURL != "" {
		opts, err := redis.ParseURL(cfg.RedisURL)
		if err != nil {
			return fmt.Errorf("QUOTIFY_REDIS_URL: %w", err)
		}
		rc := redis.NewClient(opts)
		defer func() { _ = rc.Close() }()
		evictor = redisEvictor{c: rc}
	} else {
		log.Warn("QUOTIFY_REDIS_URL unset: revoked keys stay valid in the gateway cache until its TTL")
	}

	var relay *outbox.Relay
	if len(cfg.KafkaBrokers) > 0 {
		producer, err := kafkax.NewProducer(cfg.KafkaBrokers)
		if err != nil {
			return err
		}
		defer producer.Close()
		hh.AddCheck("kafka", producer.Ping)
		if relay, err = outbox.NewRelay(systemPool, outbox.KafkaSink{Client: producer},
			outbox.Config{Workers: cfg.RelayWorkers, BatchSize: cfg.RelayBatchSize}, log); err != nil {
			return err
		}
	} else {
		log.Warn("QUOTIFY_KAFKA_BROKERS unset: entitlement changes stay in the outbox and limits-svc will not see them")
	}

	srv, grpcHealth := grpcx.NewServer(grpcx.ServerOptions{
		Logger: log, Reflection: cfg.Env == config.EnvLocal,
		Extra: []grpc.ServerOption{
			grpc.ChainUnaryInterceptor(tenant.UnaryServerInterceptor()),
			grpc.ChainStreamInterceptor(tenant.StreamServerInterceptor()),
		},
	})
	plansv1.RegisterPlansServiceServer(srv, grpcapi.New(appPool, log))
	plansv1.RegisterAdminServiceServer(srv, admin.New(systemPool, evictor, cfg.AllowedEnvs, log))

	lis, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		return err
	}
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return health.Serve(gctx, cfg.MetricsAddr, hh) })
	if relay != nil {
		g.Go(func() error { return relay.Run(gctx) })
	}
	g.Go(func() error {
		log.Info("grpc listening", "addr", cfg.GRPCAddr, "metrics", cfg.MetricsAddr, "environments", cfg.AllowedEnvs)
		return srv.Serve(lis)
	})
	g.Go(func() error {
		<-gctx.Done()
		grpcHealth.SetServingStatus("", healthpb.HealthCheckResponse_NOT_SERVING)
		srv.GracefulStop()
		return nil
	})
	if err := g.Wait(); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	log.Info("stopped")
	return nil
}
