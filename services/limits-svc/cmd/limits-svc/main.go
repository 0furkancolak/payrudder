// Command limits-svc keeps near-real-time usage counters in Redis, answers entitlement checks and emits
// usage.limit-exceeded.v1 when a customer crosses an alert threshold.
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

	"github.com/redis/go-redis/v9"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	aggregatorv1 "github.com/0furkancolak/quotify/gen/go/quotify/aggregator/v1"
	limitsv1 "github.com/0furkancolak/quotify/gen/go/quotify/limits/v1"
	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/config"
	"github.com/0furkancolak/quotify/pkg/grpcx"
	"github.com/0furkancolak/quotify/pkg/health"
	"github.com/0furkancolak/quotify/pkg/kafkax"
	"github.com/0furkancolak/quotify/pkg/logx"
	"github.com/0furkancolak/quotify/pkg/otelx"
	"github.com/0furkancolak/quotify/pkg/tenant"
	"github.com/0furkancolak/quotify/services/limits-svc/internal/engine"
	"github.com/0furkancolak/quotify/services/limits-svc/internal/entitlements"
	"github.com/0furkancolak/quotify/services/limits-svc/internal/meters"
	"github.com/0furkancolak/quotify/services/limits-svc/internal/query"
	"github.com/0furkancolak/quotify/services/limits-svc/internal/tenants"
)

const (
	usageTopic        = "usage.events.v1"
	entitlementsTopic = "plans.entitlements.v1"
)

//go:embed config.default.yaml
var defaults []byte

type Config struct {
	config.Base
	GRPCAddr          string        `env:"QUOTIFY_GRPC_ADDR"`
	RedisURL          string        `env:"QUOTIFY_REDIS_URL"`
	PlansAddr         string        `env:"QUOTIFY_PLANS_ADDR"`
	AggregatorAddr    string        `env:"QUOTIFY_AGGREGATOR_ADDR"`
	KafkaBrokers      []string      `env:"QUOTIFY_KAFKA_BROKERS,required" envSeparator:","`
	LimitTopic        string        `env:"QUOTIFY_LIMIT_TOPIC"`
	GroupID           string        `env:"QUOTIFY_CONSUMER_GROUP"`
	PodName           string        `env:"QUOTIFY_POD_NAME"`
	PollRecords       int           `env:"QUOTIFY_POLL_RECORDS"`
	Backoff           time.Duration `env:"QUOTIFY_CONSUMER_BACKOFF"`
	SessionTimeout    time.Duration `env:"QUOTIFY_CONSUMER_SESSION_TIMEOUT"`
	ReconcileInterval time.Duration `env:"QUOTIFY_RECONCILE_INTERVAL"`
}

func (c *Config) Validate() error {
	errs := []error{c.Base.Validate()}
	if c.PollRecords < 1 {
		errs = append(errs, errors.New("QUOTIFY_POLL_RECORDS must be ≥ 1"))
	}
	if c.ReconcileInterval < time.Second {
		errs = append(errs, errors.New("QUOTIFY_RECONCILE_INTERVAL must be ≥ 1s"))
	}
	return errors.Join(errs...)
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "limits-svc:", err)
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
	if cfg.PodName == "" {
		if cfg.PodName, err = os.Hostname(); err != nil || cfg.PodName == "" {
			cfg.PodName = "limits-0"
		}
	}

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

	opts, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		return fmt.Errorf("QUOTIFY_REDIS_URL: %w", err)
	}
	rdb := redis.NewClient(opts)
	defer func() { _ = rdb.Close() }()

	plansConn, err := grpcx.NewClient(cfg.PlansAddr, grpc.WithChainUnaryInterceptor(tenant.UnaryClientInterceptor()))
	if err != nil {
		return fmt.Errorf("plans-svc client: %w", err)
	}
	defer func() { _ = plansConn.Close() }()
	aggConn, err := grpcx.NewClient(cfg.AggregatorAddr, grpc.WithChainUnaryInterceptor(tenant.UnaryClientInterceptor()))
	if err != nil {
		return fmt.Errorf("aggregator client: %w", err)
	}
	defer func() { _ = aggConn.Close() }()

	producer, err := kafkax.NewProducer(cfg.KafkaBrokers)
	if err != nil {
		return err
	}
	defer producer.Close()

	ms := meters.New(plansv1.NewPlansServiceClient(plansConn), time.Minute)
	tn := tenants.New(plansv1.NewAdminServiceClient(plansConn), time.Minute)
	ents := entitlements.New(rdb, log)
	applier, err := engine.NewApplier(engine.Config{
		Redis: rdb, Meters: ms, Tenants: tn, Ents: ents, Producer: producer, LimitTopic: cfg.LimitTopic, Log: log,
		AfterLua: afterLuaFault(),
	})
	if err != nil {
		return err
	}
	if err := applier.Load(ctx); err != nil {
		return fmt.Errorf("load apply_batch.lua: %w", err)
	}

	owned := engine.NewOwnership()
	usage, err := kafkax.NewConsumer(kafkax.ConsumerConfig{
		Brokers: cfg.KafkaBrokers, GroupID: cfg.GroupID, Topic: usageTopic, LoadOffsets: applier.LoadOffsets,
		Handler: applier, MaxPollRecords: cfg.PollRecords, Backoff: cfg.Backoff, SessionTimeout: cfg.SessionTimeout, Log: log,
		Extra: owned.Opts(usageTopic), RecordTime: kafkax.UsageRecordTime,
	})
	if err != nil {
		return err
	}
	defer usage.Close()
	// Every pod reads the whole entitlement topic from the beginning: one group per pod.
	entConsumer, err := kafkax.NewConsumer(kafkax.ConsumerConfig{
		Brokers: cfg.KafkaBrokers, GroupID: "limits-entitlements-" + cfg.PodName, Topic: entitlementsTopic,
		LoadOffsets: entitlements.OffsetLoader, Handler: ents, MaxPollRecords: cfg.PollRecords, Backoff: cfg.Backoff,
		SessionTimeout: cfg.SessionTimeout, Log: log,
	})
	if err != nil {
		return err
	}
	defer entConsumer.Close()

	rec := engine.NewReconciler(engine.ReconcilerConfig{
		Applier: applier, Redis: rdb, Aggregator: aggregatorv1.NewUsageQueryServiceClient(aggConn), Meters: ms,
		Owned: owned.Partitions, Interval: cfg.ReconcileInterval, Log: log,
	})

	hh := health.New(reg, 2*time.Second)
	hh.AddCheck("redis", func(ctx context.Context) error { return rdb.Ping(ctx).Err() })
	hh.AddCheck("kafka", producer.Ping)
	hh.AddCheck("entitlements", func(context.Context) error {
		if !ents.Ready() {
			return errors.New("entitlement map is still catching up")
		}
		return nil
	})

	srv, grpcHealth := grpcx.NewServer(grpcx.ServerOptions{
		Logger: log, Reflection: cfg.Env == config.EnvLocal,
		Extra: []grpc.ServerOption{
			grpc.ChainUnaryInterceptor(tenant.UnaryServerInterceptor()),
			grpc.ChainStreamInterceptor(tenant.StreamServerInterceptor()),
		},
	})
	limitsv1.RegisterLimitsServiceServer(srv, query.New(rdb, ents, ms, rec))

	lis, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		return err
	}
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return health.Serve(gctx, cfg.MetricsAddr, hh) })
	g.Go(func() error { return entConsumer.Run(gctx) })
	g.Go(func() error {
		// Usage is applied only once the entitlement map has caught up, so thresholds see the limits in force.
		recordTargets(gctx, entConsumer.Client(), ents, log)
		return usage.Run(gctx)
	})
	g.Go(func() error { rec.Run(gctx); return nil })
	g.Go(func() error {
		log.Info("grpc listening", "addr", cfg.GRPCAddr, "metrics", cfg.MetricsAddr, "kafka", cfg.KafkaBrokers, "pod", cfg.PodName)
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

// recordTargets reads the end offsets of the entitlement topic once, so that readiness knows what "caught up" means,
// then waits (bounded) for the map to get there.
func recordTargets(ctx context.Context, cl *kgo.Client, ents *entitlements.Store, log *slog.Logger) {
	adm := kadm.NewClient(cl)
	for ctx.Err() == nil {
		ends, err := adm.ListEndOffsets(ctx, entitlementsTopic)
		if err == nil && ends.Error() == nil {
			targets := map[int32]int64{}
			ends.Each(func(o kadm.ListedOffset) { targets[o.Partition] = o.Offset })
			ents.SetTargets(targets)
			break
		}
		log.Warn("cannot read entitlement end offsets, retrying", "error", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
	deadline := time.After(2 * time.Minute)
	for !ents.Ready() {
		select {
		case <-ctx.Done():
			return
		case <-deadline:
			log.Warn("entitlement map did not catch up in time, applying usage anyway")
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
}
