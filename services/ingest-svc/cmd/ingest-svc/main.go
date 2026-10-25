// Command ingest-svc accepts usage events and relays them to Kafka through a transactional outbox.
// The handler and the relay run in the same binary.
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
	_ "time/tzdata" // tenant time zones must load in distroless images

	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	adminv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/admin/v1"
	ingestv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/ingest/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/config"
	"github.com/0furkancolak/payrudder/pkg/grpcx"
	"github.com/0furkancolak/payrudder/pkg/health"
	"github.com/0furkancolak/payrudder/pkg/kafkax"
	"github.com/0furkancolak/payrudder/pkg/logx"
	"github.com/0furkancolak/payrudder/pkg/otelx"
	"github.com/0furkancolak/payrudder/pkg/outbox"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/sandboxreset"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	_ "github.com/0furkancolak/payrudder/pkg/tzdata" // IANA zones for time.LoadLocation in distroless images
	"github.com/0furkancolak/payrudder/services/ingest-svc/internal/ingest"
	"github.com/0furkancolak/payrudder/services/ingest-svc/internal/lookup"
	"github.com/0furkancolak/payrudder/services/ingest-svc/internal/maintenance"
	ingestreset "github.com/0furkancolak/payrudder/services/ingest-svc/internal/sandboxreset"
	"github.com/0furkancolak/payrudder/services/ingest-svc/migrations"
)

//go:embed config.default.yaml
var defaults []byte

type Config struct {
	config.Base
	pgxx.Settings
	GRPCAddr        string   `env:"PAYRUDDER_GRPC_ADDR"`
	PlansAddr       string   `env:"PAYRUDDER_PLANS_ADDR"`
	KafkaBrokers    []string `env:"PAYRUDDER_KAFKA_BROKERS,required" envSeparator:","`
	RelayWorkers    int      `env:"PAYRUDDER_RELAY_WORKERS"`
	RelayBatchSize  int      `env:"PAYRUDDER_RELAY_BATCH_SIZE"`
	MaxOutboxDepth  int64    `env:"PAYRUDDER_MAX_OUTBOX_DEPTH"`
	PartitionPeriod string   `env:"PAYRUDDER_PARTITION_MAINTENANCE_INTERVAL"`
}

func (c *Config) Validate() error {
	errs := []error{c.Base.Validate()}
	errs = append(errs, c.CheckMigration())
	if c.RelayWorkers < 1 || c.RelayBatchSize < 1 {
		errs = append(errs, errors.New("PAYRUDDER_RELAY_WORKERS and PAYRUDDER_RELAY_BATCH_SIZE must be ≥ 1"))
	}
	if _, err := time.ParseDuration(c.PartitionPeriod); err != nil {
		errs = append(errs, fmt.Errorf("PAYRUDDER_PARTITION_MAINTENANCE_INTERVAL: %w", err))
	}
	return errors.Join(errs...)
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "ingest-svc:", err)
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

	if done, err := cfg.MigrateIfAsked(ctx, migrations.FS, cfg.MigrateOnly, log); err != nil || done {
		return err
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

	producer, err := kafkax.NewProducer(cfg.KafkaBrokers)
	if err != nil {
		return err
	}
	defer producer.Close()

	plansConn, err := grpcx.NewClient(cfg.PlansAddr,
		grpc.WithChainUnaryInterceptor(tenant.UnaryClientInterceptor()))
	if err != nil {
		return fmt.Errorf("plans-svc client: %w", err)
	}
	defer func() { _ = plansConn.Close() }()

	svc, err := ingest.New(ingest.Options{
		App: appPool, System: systemPool, Log: log, MaxOutboxDepth: cfg.MaxOutboxDepth,
		Lookup: lookup.NewPlans(plansv1.NewPlansServiceClient(plansConn), lookup.Options{}),
	})
	if err != nil {
		return err
	}
	relay, err := outbox.NewRelay(systemPool, outbox.KafkaSink{Client: producer}, outbox.Config{
		Workers: cfg.RelayWorkers, BatchSize: cfg.RelayBatchSize, AfterPublish: afterPublishFault(),
	}, log)
	if err != nil {
		return err
	}
	interval, _ := time.ParseDuration(cfg.PartitionPeriod)

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
	ingestv1.RegisterIngestServiceServer(srv, svc)
	adminv1.RegisterSandboxResetServiceServer(srv, sandboxreset.New(ingestreset.New(systemPool).Reset))

	lis, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		return err
	}
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return health.Serve(gctx, cfg.MetricsAddr, hh) })
	g.Go(func() error { return svc.Run(gctx) })
	g.Go(func() error { return relay.Run(gctx) })
	g.Go(func() error { maintenance.Run(gctx, systemPool, interval, log); return nil })
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
