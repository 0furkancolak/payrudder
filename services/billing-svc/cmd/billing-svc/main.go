// Command billing-svc closes billing periods, rates usage into invoices and drives each invoice through its saga.
package main

import (
	"context"
	_ "embed" // config.default.yaml
	"errors"
	"fmt"
	"log/slog"
	"net"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/twmb/franz-go/pkg/kadm"
	"google.golang.org/grpc"
	grpchealth "google.golang.org/grpc/health"

	adminv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/admin/v1"
	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
	ingestv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/ingest/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/app"
	"github.com/0furkancolak/payrudder/pkg/blob"
	"github.com/0furkancolak/payrudder/pkg/config"
	"github.com/0furkancolak/payrudder/pkg/env"
	"github.com/0furkancolak/payrudder/pkg/grpcx"
	"github.com/0furkancolak/payrudder/pkg/health"
	"github.com/0furkancolak/payrudder/pkg/kafkax"
	"github.com/0furkancolak/payrudder/pkg/outbox"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/rabbitx"
	"github.com/0furkancolak/payrudder/pkg/sandboxreset"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	_ "github.com/0furkancolak/payrudder/pkg/tzdata" // IANA zones for time.LoadLocation in distroless images
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/api"
	runcmd "github.com/0furkancolak/payrudder/services/billing-svc/internal/billingruns/command"
	creditcmd "github.com/0furkancolak/payrudder/services/billing-svc/internal/credits/command"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/dlq"
	einvoicecmd "github.com/0furkancolak/payrudder/services/billing-svc/internal/einvoices/command"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/replies"
	sagacommand "github.com/0furkancolak/payrudder/services/billing-svc/internal/sagas/command"
	billingreset "github.com/0furkancolak/payrudder/services/billing-svc/internal/sandboxreset"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/tenantevents"
	"github.com/0furkancolak/payrudder/services/billing-svc/migrations"
)

const usageTopic = "usage.events.v1"

//go:embed config.default.yaml
var defaults []byte

type Config struct {
	config.Base
	pgxx.Settings
	GRPCAddr          string        `env:"PAYRUDDER_GRPC_ADDR"`
	PlansAddr         string        `env:"PAYRUDDER_PLANS_ADDR"`
	IngestAddr        string        `env:"PAYRUDDER_INGEST_ADDR"`
	AggregatorAddr    string        `env:"PAYRUDDER_AGGREGATOR_ADDR"`
	KafkaBrokers      []string      `env:"PAYRUDDER_KAFKA_BROKERS,required" envSeparator:","`
	RabbitMQURL       string        `env:"PAYRUDDER_RABBITMQ_URL,required"`
	SchedulerInterval time.Duration `env:"PAYRUDDER_SCHEDULER_INTERVAL"`
	SweepInterval     time.Duration `env:"PAYRUDDER_SWEEP_INTERVAL"`
	StepTimeout       time.Duration `env:"PAYRUDDER_STEP_TIMEOUT"`
	// TransferPoll is how often a pending bank transfer is looked for in the ledger (an operator's receipt does not wait for it).
	TransferPoll   time.Duration `env:"PAYRUDDER_TRANSFER_POLL"`
	WatermarkDelay time.Duration `env:"PAYRUDDER_WATERMARK_DELAY"`
	// Dunning delays before payment retry 1..3 (ADR 0011: sandbox tenants run on minutes instead of days).
	DunningLive    []time.Duration `env:"PAYRUDDER_DUNNING_LIVE" envSeparator:","`
	DunningSandbox []time.Duration `env:"PAYRUDDER_DUNNING_SANDBOX" envSeparator:","`
	RelayWorkers   int             `env:"PAYRUDDER_RELAY_WORKERS"`
	RelayBatchSize int             `env:"PAYRUDDER_RELAY_BATCH_SIZE"`
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
	errs = append(errs, c.CheckMigration())
	if len(c.DunningLive) != sagacommand.DefaultDunningAttempts || len(c.DunningSandbox) != sagacommand.DefaultDunningAttempts {
		errs = append(errs, fmt.Errorf("PAYRUDDER_DUNNING_LIVE and PAYRUDDER_DUNNING_SANDBOX need exactly %d delays", sagacommand.DefaultDunningAttempts))
	}
	if c.TransferPoll < time.Minute {
		errs = append(errs, errors.New("PAYRUDDER_TRANSFER_POLL must be at least 1m"))
	}
	if c.SchedulerInterval < time.Second || c.SweepInterval < time.Second {
		errs = append(errs, errors.New("PAYRUDDER_SCHEDULER_INTERVAL and PAYRUDDER_SWEEP_INTERVAL must be ≥ 1s"))
	}
	errs = append(errs, c.checkObjectStorage())
	return errors.Join(errs...)
}

// checkObjectStorage refuses half a storage setup: an endpoint without credentials, or one key without the other, would otherwise leave the PDF links switched off
// without a word.
func (c *Config) checkObjectStorage() error {
	switch {
	case c.S3AccessKey != "" && c.S3SecretKey == "":
		return errors.New("S3_ACCESS_KEY needs S3_SECRET_KEY")
	case c.S3AccessKey == "" && c.S3SecretKey != "":
		return errors.New("S3_SECRET_KEY needs S3_ACCESS_KEY")
	case c.S3Endpoint != "" && c.S3AccessKey == "":
		return errors.New("S3_ENDPOINT needs S3_ACCESS_KEY and S3_SECRET_KEY")
	}
	return nil
}

func main() { app.Main("billing-svc", defaults, start) }

// start wires billing-svc's dependencies and runs it until ctx is cancelled.
func start(ctx context.Context, cfg *Config, rt *app.Runtime) error {
	log, hh := rt.Log, rt.Health()
	if done, err := cfg.MigrateIfAsked(ctx, migrations.FS, cfg.MigrateOnly, log); err != nil || done {
		return err
	}
	appPool, systemPool, err := cfg.OpenPools(ctx)
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

	// Every command the sagas publish is stamped with the environment of the invoice's snapshot; this asks plans-svc that it is also the tenant's own.
	adminClient := plansv1.NewAdminServiceClient(be.plans)
	tenantEnvs := env.NewDirectory(func(ctx context.Context, id uuid.UUID) (env.Env, error) {
		r, err := adminClient.GetTenant(tenant.OutgoingContext(tenant.WithOperator(ctx)), &plansv1.GetTenantRequest{TenantId: id.String()})
		if err != nil {
			return "", err
		}
		return env.FromProto(r.GetTenant().GetEnvironment())
	}, 0)
	eng, err := sagacommand.NewEngine(sagacommand.Config{StepTimeout: cfg.StepTimeout, TransferPoll: cfg.TransferPoll, DunningLive: cfg.DunningLive, DunningSandbox: cfg.DunningSandbox, Log: log, Tenants: tenantEnvs})
	if err != nil {
		return err
	}
	mods, err := newModules(cfg, log, systemPool, eng, be)
	if err != nil {
		return err
	}
	mgr, err := newRunManager(cfg, log, systemPool, eng, be, mods, usageEndOffsets(kadm.NewClient(producer)))
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

	srv, grpcHealth, err := newGRPCServer(cfg, log, api.Deps{App: appPool, System: systemPool, Runs: mgr, Engine: eng, CreditNotes: mods.creditNotes, Rerating: mods.rerating, Inputs: mods.inputs, Hosted: mods.hosted})
	if err != nil {
		return err
	}
	lis, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		return err
	}
	w := &workers{cfg: cfg, log: log, pool: systemPool, eng: eng, mgr: mgr, events: mods.tenantEvents, relay: relay, replies: rc,
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
	stream := grpc.WithChainStreamInterceptor(tenant.StreamClientInterceptor())
	targets := []struct {
		dst  **grpc.ClientConn
		name string
		addr string
		opts []grpc.DialOption
	}{
		{&b.plans, "plans-svc", cfg.PlansAddr, []grpc.DialOption{stream}},
		{&b.ingest, "ingest-svc", cfg.IngestAddr, nil},
		{&b.aggregator, "aggregator", cfg.AggregatorAddr, []grpc.DialOption{stream}},
	}
	for _, t := range targets {
		conn, dialErr := grpcx.NewTenantClient(t.name, t.addr, t.opts...)
		if dialErr != nil {
			return b, dialErr
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
func newRunManager(cfg *Config, log *slog.Logger, systemPool *pgxpool.Pool, eng *sagacommand.Engine, be *backends, mods *modules, endOffsets func(context.Context) (map[int32]int64, error)) (*runcmd.Manager, error) {
	return runcmd.NewManager(runcmd.Config{
		Pool: systemPool, Engine: eng,
		Inputs: mods.inputs, Invoices: mods.invoices,
		Ingest:         ingestv1.NewIngestServiceClient(be.ingest),
		EndOffsets:     endOffsets,
		WatermarkDelay: cfg.WatermarkDelay, Log: log,
		Chores: []runcmd.Chore{
			{Name: "fee billing failed", Run: mods.fees.Bill},
			{Name: "credit note refunds not swept", Run: mods.creditNotes.SweepRefunds},
			{Name: "payment links not expired", Run: mods.hosted.ExpireLinks},
			{Name: "promotion reversals not delivered", Run: mods.hosted.ReversePromotions},
			{Name: "re-rating resume failed", Run: mods.rerating.Resume},
		},
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
func newGRPCServer(cfg *Config, log *slog.Logger, deps api.Deps) (*grpc.Server, *grpchealth.Server, error) {
	srv, grpcHealth := app.NewGRPCServer(log, cfg.Common())
	svc := api.New(deps).WithDeadLetters(&dlq.Admin{URL: cfg.RabbitMQURL}).WithDemo(cfg.Demo)
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
	adminv1.RegisterSandboxResetServiceServer(srv, sandboxreset.New(billingreset.New(deps.System).Reset))
	return srv, grpcHealth, nil
}

// workers are the long-running parts of billing-svc: the outbox relay, the reply consumer, the periodic loops and the gRPC server.
type workers struct {
	cfg        *Config
	log        *slog.Logger
	pool       *pgxpool.Pool
	eng        *sagacommand.Engine
	mgr        *runcmd.Manager
	events     *tenantevents.Relay
	relay      *outbox.Relay
	replies    *replies.Consumer
	health     *health.Handler
	srv        *grpc.Server
	grpcHealth *grpchealth.Server
	lis        net.Listener
}

// run starts everything and blocks until ctx is cancelled or one of them fails, then drains the gRPC server.
func (w *workers) run(ctx context.Context) error {
	g := app.NewGroup(ctx, w.log, w.cfg.MetricsAddr, w.health)
	gctx := g.Context()
	g.Go(func() error { return w.relay.Run(gctx) })
	g.Go(func() error { return w.replies.Run(gctx) })
	w.startLoops(gctx, g)
	w.log.Info("grpc listening", "addr", w.cfg.GRPCAddr, "metrics", w.cfg.MetricsAddr)
	g.ServeGRPC(w.srv, w.grpcHealth, w.lis)
	return g.Wait()
}

// startLoops starts the scheduler, the tenant-event flusher and the saga sweeper.
func (w *workers) startLoops(ctx context.Context, g *app.Group) {
	g.Go(func() error { loop(ctx, w.cfg.SchedulerInterval, "scheduler", w.log, w.mgr.Tick); return nil })
	g.Go(func() error { loop(ctx, 5*time.Second, "tenant-events", w.log, w.events.Flush); return nil })
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
	if err != nil {
		return err
	}
	// E-documents whose retry is due or whose command went unanswered.
	n, err := (&einvoicecmd.Commands{Pool: w.pool}).Sweep(ctx, 100)
	if err == nil && n > 0 {
		w.log.InfoContext(ctx, "e-invoice sweep", "documents", n)
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
