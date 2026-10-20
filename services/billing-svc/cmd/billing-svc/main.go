// Command billing-svc closes billing periods, rates usage into invoices and drives each invoice through its saga.
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

	"github.com/twmb/franz-go/pkg/kadm"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	aggregatorv1 "github.com/0furkancolak/quotify/gen/go/quotify/aggregator/v1"
	billingv1 "github.com/0furkancolak/quotify/gen/go/quotify/billing/v1"
	ingestv1 "github.com/0furkancolak/quotify/gen/go/quotify/ingest/v1"
	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/blob"
	"github.com/0furkancolak/quotify/pkg/config"
	"github.com/0furkancolak/quotify/pkg/grpcx"
	"github.com/0furkancolak/quotify/pkg/health"
	"github.com/0furkancolak/quotify/pkg/kafkax"
	"github.com/0furkancolak/quotify/pkg/logx"
	"github.com/0furkancolak/quotify/pkg/otelx"
	"github.com/0furkancolak/quotify/pkg/outbox"
	"github.com/0furkancolak/quotify/pkg/pgxx"
	"github.com/0furkancolak/quotify/pkg/rabbitx"
	"github.com/0furkancolak/quotify/pkg/tenant"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/api"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/dlq"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/replies"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/runs"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/saga"
	"github.com/0furkancolak/quotify/services/billing-svc/migrations"
)

const usageTopic = "usage.events.v1"

//go:embed config.default.yaml
var defaults []byte

type Config struct {
	config.Base
	GRPCAddr          string        `env:"QUOTIFY_GRPC_ADDR"`
	PGAppDSN          string        `env:"QUOTIFY_PG_APP_DSN,required"`
	PGSystemDSN       string        `env:"QUOTIFY_PG_SYSTEM_DSN,required"`
	PGMigratorDSN     string        `env:"QUOTIFY_PG_MIGRATOR_DSN"`
	MigrateOnStart    bool          `env:"QUOTIFY_MIGRATE_ON_START"`
	PlansAddr         string        `env:"QUOTIFY_PLANS_ADDR"`
	IngestAddr        string        `env:"QUOTIFY_INGEST_ADDR"`
	AggregatorAddr    string        `env:"QUOTIFY_AGGREGATOR_ADDR"`
	KafkaBrokers      []string      `env:"QUOTIFY_KAFKA_BROKERS,required" envSeparator:","`
	RabbitMQURL       string        `env:"QUOTIFY_RABBITMQ_URL,required"`
	SchedulerInterval time.Duration `env:"QUOTIFY_SCHEDULER_INTERVAL"`
	SweepInterval     time.Duration `env:"QUOTIFY_SWEEP_INTERVAL"`
	StepTimeout       time.Duration `env:"QUOTIFY_STEP_TIMEOUT"`
	WatermarkDelay    time.Duration `env:"QUOTIFY_WATERMARK_DELAY"`
	// Dunning delays before payment retry 1..3 (ADR 0011: sandbox tenants run on minutes instead of days).
	DunningLive    []time.Duration `env:"QUOTIFY_DUNNING_LIVE" envSeparator:","`
	DunningSandbox []time.Duration `env:"QUOTIFY_DUNNING_SANDBOX" envSeparator:","`
	RelayWorkers   int             `env:"QUOTIFY_RELAY_WORKERS"`
	RelayBatchSize int             `env:"QUOTIFY_RELAY_BATCH_SIZE"`
	// Object storage, for presigned invoice PDF links (optional: without S3_ACCESS_KEY the PDF RPC is unimplemented).
	S3Endpoint       string `env:"S3_ENDPOINT"`
	S3PublicEndpoint string `env:"S3_PUBLIC_ENDPOINT"`
	S3Region         string `env:"S3_REGION"`
	S3Bucket         string `env:"S3_BUCKET"`
	S3AccessKey      string `env:"S3_ACCESS_KEY"`
	S3SecretKey      string `env:"S3_SECRET_KEY"`
	S3ForcePath      bool   `env:"S3_FORCE_PATH_STYLE"`
}

func (c *Config) Validate() error {
	errs := []error{c.Base.Validate()}
	if c.MigrateOnStart && c.PGMigratorDSN == "" {
		errs = append(errs, errors.New("QUOTIFY_PG_MIGRATOR_DSN is required with QUOTIFY_MIGRATE_ON_START"))
	}
	if len(c.DunningLive) != saga.MaxDunning || len(c.DunningSandbox) != saga.MaxDunning {
		errs = append(errs, fmt.Errorf("QUOTIFY_DUNNING_LIVE and QUOTIFY_DUNNING_SANDBOX need exactly %d delays", saga.MaxDunning))
	}
	if c.SchedulerInterval < time.Second || c.SweepInterval < time.Second {
		errs = append(errs, errors.New("QUOTIFY_SCHEDULER_INTERVAL and QUOTIFY_SWEEP_INTERVAL must be ≥ 1s"))
	}
	return errors.Join(errs...)
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "billing-svc:", err)
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

	plansConn, err := grpcx.NewClient(cfg.PlansAddr, grpc.WithChainUnaryInterceptor(tenant.UnaryClientInterceptor()), grpc.WithChainStreamInterceptor(tenant.StreamClientInterceptor()))
	if err != nil {
		return fmt.Errorf("plans-svc client: %w", err)
	}
	defer func() { _ = plansConn.Close() }()
	ingestConn, err := grpcx.NewClient(cfg.IngestAddr, grpc.WithChainUnaryInterceptor(tenant.UnaryClientInterceptor()))
	if err != nil {
		return fmt.Errorf("ingest-svc client: %w", err)
	}
	defer func() { _ = ingestConn.Close() }()
	aggConn, err := grpcx.NewClient(cfg.AggregatorAddr, grpc.WithChainUnaryInterceptor(tenant.UnaryClientInterceptor()), grpc.WithChainStreamInterceptor(tenant.StreamClientInterceptor()))
	if err != nil {
		return fmt.Errorf("aggregator client: %w", err)
	}
	defer func() { _ = aggConn.Close() }()

	producer, err := kafkax.NewProducer(cfg.KafkaBrokers)
	if err != nil {
		return err
	}
	defer producer.Close()
	if err := rabbitx.DeclareTopology(cfg.RabbitMQURL); err != nil {
		return err
	}
	pub := rabbitx.NewPublisher(cfg.RabbitMQURL)
	defer pub.Close()

	eng, err := saga.NewEngine(saga.Config{StepTimeout: cfg.StepTimeout, DunningLive: cfg.DunningLive, DunningSandbox: cfg.DunningSandbox, Log: log})
	if err != nil {
		return err
	}
	mgr, err := runs.NewManager(runs.Config{
		Pool: systemPool, Engine: eng,
		Plans: plansv1.NewPlansServiceClient(plansConn), Events: plansv1.NewWebhookServiceClient(plansConn), Admin: plansv1.NewAdminServiceClient(plansConn),
		Ingest: ingestv1.NewIngestServiceClient(ingestConn), Aggregator: aggregatorv1.NewUsageQueryServiceClient(aggConn),
		EndOffsets: func(ctx context.Context) (map[int32]int64, error) {
			ends, err := kadm.NewClient(producer).ListEndOffsets(ctx, usageTopic)
			if err != nil {
				return nil, err
			}
			if err := ends.Error(); err != nil {
				return nil, err
			}
			out := map[int32]int64{}
			ends.Each(func(o kadm.ListedOffset) { out[o.Partition] = o.Offset })
			return out, nil
		},
		WatermarkDelay: cfg.WatermarkDelay, Log: log,
	})
	if err != nil {
		return err
	}
	rc, err := replies.New(systemPool, eng, cfg.RabbitMQURL, log)
	if err != nil {
		return err
	}
	relay, err := outbox.NewRelay(systemPool, outbox.Router{Kafka: outbox.KafkaSink{Client: producer}, AMQP: outbox.AMQPSink{Publisher: pub}},
		outbox.Config{Workers: cfg.RelayWorkers, BatchSize: cfg.RelayBatchSize, AfterPublish: afterPublishFault()}, log)
	if err != nil {
		return err
	}

	hh := health.New(reg, 2*time.Second)
	hh.AddCheck("postgres", appPool.Ping)
	hh.AddCheck("kafka", producer.Ping)
	hh.AddCheck("rabbitmq", func(context.Context) error { return pub.Ping() })

	srv, grpcHealth := grpcx.NewServer(grpcx.ServerOptions{
		Logger: log, Reflection: cfg.Env == config.EnvLocal,
		Extra: []grpc.ServerOption{
			grpc.ChainUnaryInterceptor(tenant.UnaryServerInterceptor()),
			grpc.ChainStreamInterceptor(tenant.StreamServerInterceptor()),
		},
	})
	svc := api.New(appPool, systemPool, mgr, eng).WithDeadLetters(&dlq.Admin{URL: cfg.RabbitMQURL})
	if cfg.S3AccessKey != "" {
		store, err := blob.New(blob.Config{Endpoint: cfg.S3Endpoint, PublicEndpoint: cfg.S3PublicEndpoint, Region: cfg.S3Region, Bucket: cfg.S3Bucket,
			AccessKey: cfg.S3AccessKey, SecretKey: cfg.S3SecretKey, ForcePathStyle: cfg.S3ForcePath})
		if err != nil {
			return err
		}
		svc.WithPDFs(store)
	}
	billingv1.RegisterBillingServiceServer(srv, svc)
	billingv1.RegisterSandboxServiceServer(srv, svc)
	billingv1.RegisterHostedInvoiceServiceServer(srv, svc)
	billingv1.RegisterBillingAdminServiceServer(srv, svc)

	lis, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		return err
	}
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return health.Serve(gctx, cfg.MetricsAddr, hh) })
	g.Go(func() error { return relay.Run(gctx) })
	g.Go(func() error { return rc.Run(gctx) })
	g.Go(func() error { loop(gctx, cfg.SchedulerInterval, "scheduler", log, mgr.Tick); return nil })
	g.Go(func() error { loop(gctx, 5*time.Second, "tenant-events", log, mgr.FlushTenantEvents); return nil })
	g.Go(func() error {
		loop(gctx, cfg.SweepInterval, "sweeper", log, func(ctx context.Context) error {
			res, err := eng.Sweep(ctx, systemPool, 200)
			if err == nil && (res.Reissued+res.Exhausted+res.Dunned) > 0 {
				log.InfoContext(ctx, "saga sweep", "reissued", res.Reissued, "exhausted", res.Exhausted, "dunning", res.Dunned)
			}
			return err
		})
		return nil
	})
	g.Go(func() error {
		log.Info("grpc listening", "addr", cfg.GRPCAddr, "metrics", cfg.MetricsAddr)
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

// loop calls fn every interval until ctx is done; errors are logged, never fatal.
func loop(ctx context.Context, interval time.Duration, name string, log *slog.Logger, fn func(context.Context) error) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := fn(ctx); err != nil && ctx.Err() == nil {
				log.Warn(name+" pass failed", "error", err)
			}
		}
	}
}
