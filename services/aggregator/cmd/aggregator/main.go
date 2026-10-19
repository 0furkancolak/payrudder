// Command aggregator consumes usage.events.v1 and maintains the authoritative usage aggregates.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
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

type Config struct {
	config.Base
	GRPCAddr       string        `env:"QUOTIFY_GRPC_ADDR" envDefault:":7003"`
	PGAppDSN       string        `env:"QUOTIFY_PG_APP_DSN,required"`
	PGSystemDSN    string        `env:"QUOTIFY_PG_SYSTEM_DSN,required"`
	PGMigratorDSN  string        `env:"QUOTIFY_PG_MIGRATOR_DSN"`
	MigrateOnStart bool          `env:"QUOTIFY_MIGRATE_ON_START" envDefault:"false"`
	PlansAddr      string        `env:"QUOTIFY_PLANS_ADDR" envDefault:"dns:///localhost:7001"`
	KafkaBrokers   []string      `env:"QUOTIFY_KAFKA_BROKERS,required" envSeparator:","`
	DLQTopic       string        `env:"QUOTIFY_DLQ_TOPIC" envDefault:"usage.events.dlq.v1"`
	PollRecords    int           `env:"QUOTIFY_POLL_RECORDS" envDefault:"1000"`
	Backoff        time.Duration `env:"QUOTIFY_CONSUMER_BACKOFF" envDefault:"1s"`
	SessionTimeout time.Duration `env:"QUOTIFY_CONSUMER_SESSION_TIMEOUT" envDefault:"10s"`
	GroupID        string        `env:"QUOTIFY_CONSUMER_GROUP" envDefault:"aggregator"`
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
	if cfg.MigrateOnly {
		log.Info("migrations applied; exiting (QUOTIFY_MIGRATE_ONLY)")
		return nil
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
	consumer, err := kafkax.NewConsumer(kafkax.ConsumerConfig{
		Brokers: cfg.KafkaBrokers, GroupID: cfg.GroupID, Topic: apply.Topic, LoadOffsets: handler.LoadOffsets,
		Handler: handler, MaxPollRecords: cfg.PollRecords, Backoff: cfg.Backoff, SessionTimeout: cfg.SessionTimeout, Log: log,
		RecordTime: kafkax.UsageRecordTime,
	})
	if err != nil {
		return err
	}
	defer consumer.Close()

	hh := health.New(reg, 2*time.Second)
	hh.AddCheck("postgres", appPool.Ping)
	hh.AddCheck("kafka", producer.Ping)

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

	lis, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		return err
	}
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return health.Serve(gctx, cfg.MetricsAddr, hh) })
	g.Go(func() error { return consumer.Run(gctx) })
	g.Go(func() error { maintenance.Run(gctx, systemPool, time.Hour, log); return nil })
	g.Go(func() error { return replays.Run(gctx) })
	g.Go(func() error {
		log.Info("grpc listening", "addr", cfg.GRPCAddr, "metrics", cfg.MetricsAddr, "kafka", cfg.KafkaBrokers)
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
