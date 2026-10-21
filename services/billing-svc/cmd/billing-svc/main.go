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

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/twmb/franz-go/pkg/kadm"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	grpchealth "google.golang.org/grpc/health"
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
	creditcmd "github.com/0furkancolak/quotify/services/billing-svc/internal/credits/command"
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
	// TransferPoll is how often a pending bank transfer is looked for in the ledger (an operator's receipt does not wait for it).
	TransferPoll   time.Duration `env:"QUOTIFY_TRANSFER_POLL"`
	WatermarkDelay time.Duration `env:"QUOTIFY_WATERMARK_DELAY"`
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
	if c.TransferPoll < time.Minute {
		errs = append(errs, errors.New("QUOTIFY_TRANSFER_POLL must be at least 1m"))
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

// start wires billing-svc's dependencies and runs it until ctx is cancelled.
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

	be, err := dialBackends(cfg)
	if err != nil {
		return err
	}
	defer be.close()

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

	eng, err := saga.NewEngine(saga.Config{StepTimeout: cfg.StepTimeout, TransferPoll: cfg.TransferPoll, DunningLive: cfg.DunningLive, DunningSandbox: cfg.DunningSandbox, Log: log})
	if err != nil {
		return err
	}
	mgr, err := newRunManager(cfg, log, systemPool, eng, be, usageEndOffsets(kadm.NewClient(producer)))
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

	hh.AddCheck("postgres", appPool.Ping)
	hh.AddCheck("kafka", producer.Ping)
	hh.AddCheck("rabbitmq", func(context.Context) error { return pub.Ping() })

	srv, grpcHealth, err := newGRPCServer(cfg, log, appPool, systemPool, mgr, eng)
	if err != nil {
		return err
	}
	lis, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		return err
	}
	w := &workers{cfg: cfg, log: log, pool: systemPool, eng: eng, mgr: mgr, relay: relay, replies: rc,
		health: hh, srv: srv, grpcHealth: grpcHealth, lis: lis}
	return w.run(ctx)
}

// backends are the gRPC connections to the services billing-svc calls.
type backends struct {
	plans, ingest, aggregator *grpc.ClientConn
}

// dialBackends connects to every backend; on failure it closes the connections already made.
func dialBackends(cfg *Config) (b *backends, err error) {
	b = &backends{}
	defer func() {
		if err != nil {
			b.close()
		}
	}()
	unary := grpc.WithChainUnaryInterceptor(tenant.UnaryClientInterceptor())
	stream := grpc.WithChainStreamInterceptor(tenant.StreamClientInterceptor())
	targets := []struct {
		dst  **grpc.ClientConn
		name string
		addr string
		opts []grpc.DialOption
	}{
		{&b.plans, "plans-svc", cfg.PlansAddr, []grpc.DialOption{unary, stream}},
		{&b.ingest, "ingest-svc", cfg.IngestAddr, []grpc.DialOption{unary}},
		{&b.aggregator, "aggregator", cfg.AggregatorAddr, []grpc.DialOption{unary, stream}},
	}
	for _, t := range targets {
		conn, dialErr := grpcx.NewClient(t.addr, t.opts...)
		if dialErr != nil {
			return b, fmt.Errorf("%s client: %w", t.name, dialErr)
		}
		*t.dst = conn
	}
	return b, nil
}

// close closes the connections that were opened, in reverse dial order.
func (b *backends) close() {
	for _, c := range []*grpc.ClientConn{b.aggregator, b.ingest, b.plans} {
		if c != nil {
			_ = c.Close()
		}
	}
}

// newRunManager builds the billing-run manager from the backend clients and the usage topic's end offsets.
func newRunManager(cfg *Config, log *slog.Logger, systemPool *pgxpool.Pool, eng *saga.Engine, be *backends, endOffsets func(context.Context) (map[int32]int64, error)) (*runs.Manager, error) {
	return runs.NewManager(runs.Config{
		Pool: systemPool, Engine: eng,
		Plans: plansv1.NewPlansServiceClient(be.plans), Events: plansv1.NewWebhookServiceClient(be.plans), Admin: plansv1.NewAdminServiceClient(be.plans),
		Ingest: ingestv1.NewIngestServiceClient(be.ingest), Aggregator: aggregatorv1.NewUsageQueryServiceClient(be.aggregator),
		EndOffsets:     endOffsets,
		WatermarkDelay: cfg.WatermarkDelay, Log: log,
	})
}

// usageEndOffsets returns a func that reads the current end offset of every usage topic partition.
func usageEndOffsets(adm *kadm.Client) func(context.Context) (map[int32]int64, error) {
	return func(ctx context.Context) (map[int32]int64, error) {
		ends, err := adm.ListEndOffsets(ctx, usageTopic)
		if err != nil {
			return nil, err
		}
		if err := ends.Error(); err != nil {
			return nil, err
		}
		out := map[int32]int64{}
		ends.Each(func(o kadm.ListedOffset) { out[o.Partition] = o.Offset })
		return out, nil
	}
}

// newGRPCServer builds the gRPC server with every billing service registered; the PDF store is optional.
func newGRPCServer(cfg *Config, log *slog.Logger, appPool, systemPool *pgxpool.Pool, mgr *runs.Manager, eng *saga.Engine) (*grpc.Server, *grpchealth.Server, error) {
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
			return nil, nil, err
		}
		svc.WithPDFs(store)
	}
	billingv1.RegisterBillingServiceServer(srv, svc)
	billingv1.RegisterSandboxServiceServer(srv, svc)
	billingv1.RegisterHostedInvoiceServiceServer(srv, svc)
	billingv1.RegisterBillingAdminServiceServer(srv, svc)
	return srv, grpcHealth, nil
}

// workers are the long-running parts of billing-svc: the outbox relay, the reply consumer, the periodic loops and the gRPC server.
type workers struct {
	cfg        *Config
	log        *slog.Logger
	pool       *pgxpool.Pool
	eng        *saga.Engine
	mgr        *runs.Manager
	relay      *outbox.Relay
	replies    *replies.Consumer
	health     *health.Handler
	srv        *grpc.Server
	grpcHealth *grpchealth.Server
	lis        net.Listener
}

// run starts everything and blocks until ctx is cancelled or one of them fails, then drains the gRPC server.
func (w *workers) run(ctx context.Context) error {
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return health.Serve(gctx, w.cfg.MetricsAddr, w.health) })
	g.Go(func() error { return w.relay.Run(gctx) })
	g.Go(func() error { return w.replies.Run(gctx) })
	w.startLoops(gctx, g)
	g.Go(func() error {
		w.log.Info("grpc listening", "addr", w.cfg.GRPCAddr, "metrics", w.cfg.MetricsAddr)
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

// startLoops starts the scheduler, the tenant-event flusher and the saga sweeper.
func (w *workers) startLoops(ctx context.Context, g *errgroup.Group) {
	g.Go(func() error { loop(ctx, w.cfg.SchedulerInterval, "scheduler", w.log, w.mgr.Tick); return nil })
	g.Go(func() error { loop(ctx, 5*time.Second, "tenant-events", w.log, w.mgr.FlushTenantEvents); return nil })
	g.Go(func() error { loop(ctx, w.cfg.SweepInterval, "sweeper", w.log, w.sweep); return nil })
}

// sweep re-issues stuck saga steps, advances dunning and writes off credits whose time has come.
func (w *workers) sweep(ctx context.Context) error {
	res, err := w.eng.Sweep(ctx, w.pool, 200)
	if err == nil && (res.Reissued+res.Exhausted+res.Dunned+res.Polled) > 0 {
		w.log.InfoContext(ctx, "saga sweep", "reissued", res.Reissued, "exhausted", res.Exhausted, "dunning", res.Dunned, "polled", res.Polled)
	}
	if err != nil {
		return err
	}
	expired, err := creditcmd.ExpireDue(ctx, w.pool, 200)
	if err == nil && expired > 0 {
		w.log.InfoContext(ctx, "credits expired", "lots", expired)
	}
	return err
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
