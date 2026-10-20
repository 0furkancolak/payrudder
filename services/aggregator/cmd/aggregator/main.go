// Command aggregator consumes usage.events.v1 and maintains the authoritative usage aggregates.
package main

import (
	"context"
	_ "embed" // config.default.yaml
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	grpchealth "google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	aggregatorv1 "github.com/0furkancolak/quotify/gen/go/quotify/aggregator/v1"
	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/config"
	"github.com/0furkancolak/quotify/pkg/grpcx"
	"github.com/0furkancolak/quotify/pkg/health"
	"github.com/0furkancolak/quotify/pkg/kafkax"
	"github.com/0furkancolak/quotify/pkg/logx"
	"github.com/0furkancolak/quotify/pkg/otelx"
	"github.com/0furkancolak/quotify/pkg/pgxx"
	"github.com/0furkancolak/quotify/pkg/tenant"
	"github.com/0furkancolak/quotify/services/aggregator/internal/apply"
	"github.com/0furkancolak/quotify/services/aggregator/internal/maintenance"
	"github.com/0furkancolak/quotify/services/aggregator/internal/meters"
	"github.com/0furkancolak/quotify/services/aggregator/internal/query"
	"github.com/0furkancolak/quotify/services/aggregator/internal/replay"
	"github.com/0furkancolak/quotify/services/aggregator/internal/settings"
	"github.com/0furkancolak/quotify/services/aggregator/migrations"
)

//go:embed config.default.yaml
var defaults []byte

type Config struct {
	config.Base
	GRPCAddr       string        `env:"QUOTIFY_GRPC_ADDR"`
	PGAppDSN       string        `env:"QUOTIFY_PG_APP_DSN,required"`
	PGSystemDSN    string        `env:"QUOTIFY_PG_SYSTEM_DSN,required"`
	PGMigratorDSN  string        `env:"QUOTIFY_PG_MIGRATOR_DSN"`
	MigrateOnStart bool          `env:"QUOTIFY_MIGRATE_ON_START"`
	PlansAddr      string        `env:"QUOTIFY_PLANS_ADDR"`
	KafkaBrokers   []string      `env:"QUOTIFY_KAFKA_BROKERS,required" envSeparator:","`
	DLQTopic       string        `env:"QUOTIFY_DLQ_TOPIC"`
	PollRecords    int           `env:"QUOTIFY_POLL_RECORDS"`
	Backoff        time.Duration `env:"QUOTIFY_CONSUMER_BACKOFF"`
	SessionTimeout time.Duration `env:"QUOTIFY_CONSUMER_SESSION_TIMEOUT"`
	GroupID        string        `env:"QUOTIFY_CONSUMER_GROUP"`
}

func (c *Config) Validate() error {
	errs := []error{c.Base.Validate()}
	if c.MigrateOnStart && c.PGMigratorDSN == "" {
		errs = append(errs, errors.New("QUOTIFY_PG_MIGRATOR_DSN is required with QUOTIFY_MIGRATE_ON_START"))
	}
	if c.PollRecords < 1 {
		errs = append(errs, errors.New("QUOTIFY_POLL_RECORDS must be ≥ 1"))
	}
	return errors.Join(errs...)
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "aggregator:", err)
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

// migrate applies the migrations when asked to; done reports that the process should exit afterwards (QUOTIFY_MIGRATE_ONLY).
func migrate(ctx context.Context, cfg *Config, log *slog.Logger) (done bool, err error) {
	if cfg.MigrateOnStart {
		if err := pgxx.Migrate(ctx, cfg.PGMigratorDSN, migrations.FS, log); err != nil {
			return false, err
		}
	}
	if cfg.MigrateOnly {
		log.Info("migrations applied; exiting (QUOTIFY_MIGRATE_ONLY)")
		return true, nil
	}
	return false, nil
}

// openPools opens the application-role and system-role Postgres pools.
func openPools(ctx context.Context, cfg *Config) (appPool, systemPool *pgxpool.Pool, err error) {
	appPool, err = pgxx.NewPool(ctx, cfg.PGAppDSN)
	if err != nil {
		return nil, nil, err
	}
	systemPool, err = pgxx.NewPool(ctx, cfg.PGSystemDSN)
	if err != nil {
		appPool.Close()
		return nil, nil, err
	}
	return appPool, systemPool, nil
}

// start wires aggregator's dependencies and runs it until ctx is cancelled.
func start(ctx context.Context, cfg *Config, log *slog.Logger, hh *health.Handler) error {
	if done, err := migrate(ctx, cfg, log); err != nil || done {
		return err
	}
	appPool, systemPool, err := openPools(ctx, cfg)
	if err != nil {
		return err
	}
	defer appPool.Close()
	defer systemPool.Close()

	plansConn, err := grpcx.NewClient(cfg.PlansAddr, grpc.WithChainUnaryInterceptor(tenant.UnaryClientInterceptor()))
	if err != nil {
		return fmt.Errorf("plans-svc client: %w", err)
	}
	defer func() { _ = plansConn.Close() }()
	src := settings.NewSource(plansv1.NewAdminServiceClient(plansConn), time.Minute)

	producer, err := kafkax.NewProducer(cfg.KafkaBrokers)
	if err != nil {
		return err
	}
	defer producer.Close()

	handler := &apply.Handler{
		Pool: systemPool, Meters: meters.New(plansv1.NewPlansServiceClient(plansConn), time.Minute), Settings: src,
		DLQ: kafkax.DLQ{Client: producer, Topic: cfg.DLQTopic, Consumer: cfg.GroupID}, Log: log,
		ConsumerGroup: cfg.GroupID, AfterCommit: afterCommitFault(),
	}
	consumer, err := newConsumer(cfg, log, handler)
	if err != nil {
		return err
	}
	defer consumer.Close()

	hh.AddCheck("postgres", appPool.Ping)
	hh.AddCheck("kafka", producer.Ping)

	srv, grpcHealth, replays := newGRPCServer(cfg, log, appPool, systemPool, handler, src)
	lis, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		return err
	}
	w := &workers{cfg: cfg, log: log, pool: systemPool, consumer: consumer, replays: replays, health: hh, srv: srv, grpcHealth: grpcHealth, lis: lis}
	return w.run(ctx)
}

// newConsumer builds the usage-event consumer that feeds handler.
func newConsumer(cfg *Config, log *slog.Logger, handler *apply.Handler) (*kafkax.Consumer, error) {
	return kafkax.NewConsumer(kafkax.ConsumerConfig{
		Brokers: cfg.KafkaBrokers, GroupID: cfg.GroupID, Topic: apply.Topic, LoadOffsets: handler.LoadOffsets,
		Handler: handler, MaxPollRecords: cfg.PollRecords, Backoff: cfg.Backoff, SessionTimeout: cfg.SessionTimeout, Log: log,
		RecordTime: kafkax.UsageRecordTime,
	})
}

// newGRPCServer builds the gRPC server with the usage query and replay services registered; the replay runner is returned to be run.
func newGRPCServer(cfg *Config, log *slog.Logger, appPool, systemPool *pgxpool.Pool, handler *apply.Handler, src *settings.Source) (*grpc.Server, *grpchealth.Server, *replay.Manager) {
	srv, grpcHealth := grpcx.NewServer(grpcx.ServerOptions{
		Logger: log, Reflection: cfg.Env == config.EnvLocal,
		Extra: []grpc.ServerOption{
			grpc.ChainUnaryInterceptor(tenant.UnaryServerInterceptor()),
			grpc.ChainStreamInterceptor(tenant.StreamServerInterceptor()),
		},
	})
	aggregatorv1.RegisterUsageQueryServiceServer(srv, query.New(appPool, src, cfg.GroupID, apply.Topic))
	replays := replay.New(replay.Config{
		Pool: systemPool, Brokers: cfg.KafkaBrokers, Topic: apply.Topic, LiveGroup: cfg.GroupID,
		Meters: handler.Meters, Settings: src, Log: log,
	})
	aggregatorv1.RegisterReplayServiceServer(srv, replay.NewService(replays))
	return srv, grpcHealth, replays
}

// workers are the long-running parts of aggregator: the usage consumer, maintenance, replay jobs and the gRPC server.
type workers struct {
	cfg        *Config
	log        *slog.Logger
	pool       *pgxpool.Pool
	consumer   *kafkax.Consumer
	replays    *replay.Manager
	health     *health.Handler
	srv        *grpc.Server
	grpcHealth *grpchealth.Server
	lis        net.Listener
}

// run starts everything and blocks until ctx is cancelled or one of them fails, then drains the gRPC server.
func (w *workers) run(ctx context.Context) error {
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return health.Serve(gctx, w.cfg.MetricsAddr, w.health) })
	g.Go(func() error { return w.consumer.Run(gctx) })
	g.Go(func() error { maintenance.Run(gctx, w.pool, time.Hour, w.log); return nil })
	g.Go(func() error { return w.replays.Run(gctx) })
	g.Go(func() error {
		w.log.Info("grpc listening", "addr", w.cfg.GRPCAddr, "metrics", w.cfg.MetricsAddr, "kafka", w.cfg.KafkaBrokers)
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
