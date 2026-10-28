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
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/grpc"

	aggregatorv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/aggregator/v1"
	limitsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/limits/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/app"
	"github.com/0furkancolak/payrudder/pkg/chaos"
	"github.com/0furkancolak/payrudder/pkg/config"
	"github.com/0furkancolak/payrudder/pkg/grpcx"
	"github.com/0furkancolak/payrudder/pkg/health"
	"github.com/0furkancolak/payrudder/pkg/kafkax"
	"github.com/0furkancolak/payrudder/pkg/meters"
	"github.com/0furkancolak/payrudder/pkg/tenantinfo"
	_ "github.com/0furkancolak/payrudder/pkg/tzdata" // IANA zones for time.LoadLocation in distroless images
	"github.com/0furkancolak/payrudder/services/limits-svc/internal/engine"
	"github.com/0furkancolak/payrudder/services/limits-svc/internal/entitlements"
	"github.com/0furkancolak/payrudder/services/limits-svc/internal/grpcapi"
)

const (
	usageTopic        = "usage.events.v1"
	entitlementsTopic = "plans.entitlements.v1"
)

//go:embed config.default.yaml
var defaults []byte

type Config struct {
	config.Base
	GRPCAddr          string        `env:"PAYRUDDER_GRPC_ADDR"`
	RedisURL          string        `env:"PAYRUDDER_REDIS_URL"`
	PlansAddr         string        `env:"PAYRUDDER_PLANS_ADDR"`
	AggregatorAddr    string        `env:"PAYRUDDER_AGGREGATOR_ADDR"`
	KafkaBrokers      []string      `env:"PAYRUDDER_KAFKA_BROKERS,required" envSeparator:","`
	LimitTopic        string        `env:"PAYRUDDER_LIMIT_TOPIC"`
	GroupID           string        `env:"PAYRUDDER_CONSUMER_GROUP"`
	PodName           string        `env:"PAYRUDDER_POD_NAME"`
	PollRecords       int           `env:"PAYRUDDER_POLL_RECORDS"`
	Backoff           time.Duration `env:"PAYRUDDER_CONSUMER_BACKOFF"`
	SessionTimeout    time.Duration `env:"PAYRUDDER_CONSUMER_SESSION_TIMEOUT"`
	ReconcileInterval time.Duration `env:"PAYRUDDER_RECONCILE_INTERVAL"`
	// UniqueSetCap is how many distinct values a UNIQUE counter remembers before it leaves new ones to reconciliation.
	UniqueSetCap int `env:"PAYRUDDER_UNIQUE_SET_CAP"`
}

func (c *Config) Validate() error {
	errs := []error{c.Base.Validate()}
	if c.PollRecords < 1 {
		errs = append(errs, errors.New("PAYRUDDER_POLL_RECORDS must be ≥ 1"))
	}
	if c.ReconcileInterval < time.Second {
		errs = append(errs, errors.New("PAYRUDDER_RECONCILE_INTERVAL must be ≥ 1s"))
	}
	if c.UniqueSetCap < 1 {
		errs = append(errs, errors.New("PAYRUDDER_UNIQUE_SET_CAP must be ≥ 1"))
	}
	return errors.Join(errs...)
}

func main() { app.Main("limits-svc", defaults, start) }

func start(ctx context.Context, cfg *Config, rt *app.Runtime) error {
	log := rt.Log
	if cfg.PodName == "" {
		if name, err := os.Hostname(); err == nil && name != "" {
			cfg.PodName = name
		} else {
			cfg.PodName = "limits-0"
		}
	}

	opts, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		return fmt.Errorf("PAYRUDDER_REDIS_URL: %w", err)
	}
	rdb := redis.NewClient(opts)
	defer func() { _ = rdb.Close() }()

	plansConn, err := dialPeer("plans-svc", cfg.PlansAddr)
	if err != nil {
		return err
	}
	defer func() { _ = plansConn.Close() }()
	aggConn, err := dialPeer("aggregator", cfg.AggregatorAddr)
	if err != nil {
		return err
	}
	defer func() { _ = aggConn.Close() }()

	producer, err := kafkax.NewProducer(cfg.KafkaBrokers)
	if err != nil {
		return err
	}
	defer producer.Close()

	ms := meters.New(plansv1.NewPlansServiceClient(plansConn), time.Minute, engine.ErrMeterNotFound)
	tn := tenantinfo.New(plansv1.NewAdminServiceClient(plansConn), time.Minute)
	ents := entitlements.New(rdb, log)
	applier, err := engine.NewApplier(engine.Config{
		Redis: rdb, Meters: ms, Tenants: tn, Ents: ents, Producer: producer, LimitTopic: cfg.LimitTopic, Log: log, UniqueSetCap: cfg.UniqueSetCap,
		AfterLua: chaos.Crash("LIMITS_CRASH_AFTER_LUA", "chaos build: limits-svc will crash after the script", "chaos: crashing after the script"),
	})
	if err != nil {
		return err
	}
	if err := applier.Load(ctx); err != nil {
		return fmt.Errorf("load apply_batch.lua: %w", err)
	}

	owned := engine.NewOwnership()
	usage, entConsumer, closeConsumers, err := newConsumers(cfg, applier, ents, owned, log)
	if err != nil {
		return err
	}
	defer closeConsumers()

	rec := engine.NewReconciler(engine.ReconcilerConfig{
		Applier: applier, Redis: rdb, Aggregator: aggregatorv1.NewUsageQueryServiceClient(aggConn), Meters: ms,
		Owned: owned.Partitions, Interval: cfg.ReconcileInterval, Log: log, Replay: usage.Rewind,
	})

	hh := rt.Health()
	addHealthChecks(hh, rdb, producer, ents)
	srv, grpcHealth := app.NewGRPCServer(log, cfg.Common())
	limitsv1.RegisterLimitsServiceServer(srv, grpcapi.New(rdb, ents, ms, rec))

	lis, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		return err
	}
	g := app.NewGroup(ctx, log, cfg.MetricsAddr, hh)
	gctx := g.Context()
	g.Go(func() error { return entConsumer.Run(gctx) })
	g.Go(func() error {
		// Usage is applied only once the entitlement map has caught up, so thresholds see the limits in force.
		recordTargets(gctx, entConsumer.Client(), ents, log)
		return usage.Run(gctx)
	})
	g.Go(func() error { rec.Run(gctx); return nil })
	log.Info("grpc listening", "addr", cfg.GRPCAddr, "metrics", cfg.MetricsAddr, "kafka", cfg.KafkaBrokers, "pod", cfg.PodName)
	g.ServeGRPC(srv, grpcHealth, lis)
	return g.Wait()
}

// dialPeer connects to another service, forwarding the tenant on every call.
func dialPeer(name, addr string) (*grpc.ClientConn, error) {
	return grpcx.NewTenantClient(name, addr)
}

// newConsumers builds the usage consumer and the entitlement consumer; closeAll closes both.
func newConsumers(cfg *Config, applier *engine.Applier, ents *entitlements.Store, owned *engine.Ownership, log *slog.Logger) (usage, entConsumer *kafkax.Consumer, closeAll func(), err error) {
	usage, err = kafkax.NewConsumer(kafkax.ConsumerConfig{
		Brokers: cfg.KafkaBrokers, GroupID: cfg.GroupID, Topic: usageTopic, LoadOffsets: applier.LoadOffsets,
		Handler: applier, MaxPollRecords: cfg.PollRecords, Backoff: cfg.Backoff, SessionTimeout: cfg.SessionTimeout, Log: log,
		Extra: owned.Opts(usageTopic), RecordTime: kafkax.UsageRecordTime,
	})
	if err != nil {
		return nil, nil, nil, err
	}
	// Every pod reads the whole entitlement topic from the beginning: one group per pod.
	entConsumer, err = kafkax.NewConsumer(kafkax.ConsumerConfig{
		Brokers: cfg.KafkaBrokers, GroupID: "limits-entitlements-" + cfg.PodName, Topic: entitlementsTopic,
		LoadOffsets: entitlements.OffsetLoader, Handler: ents, MaxPollRecords: cfg.PollRecords, Backoff: cfg.Backoff,
		SessionTimeout: cfg.SessionTimeout, Log: log,
	})
	if err != nil {
		usage.Close()
		return nil, nil, nil, err
	}
	return usage, entConsumer, func() { entConsumer.Close(); usage.Close() }, nil
}

// addHealthChecks registers the readiness checks of the dependencies.
func addHealthChecks(hh *health.Handler, rdb *redis.Client, producer *kgo.Client, ents *entitlements.Store) {
	hh.AddCheck("redis", func(ctx context.Context) error { return rdb.Ping(ctx).Err() })
	hh.AddCheck("kafka", producer.Ping)
	hh.AddCheck("entitlements", func(context.Context) error {
		if !ents.Ready() {
			return errors.New("entitlement map is still catching up")
		}
		return nil
	})
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
