// Command aggregator consumes usage.events.v1 and maintains the authoritative usage aggregates.
package main

import (
	"context"
	_ "embed" // config.default.yaml
	"errors"
	"log/slog"
	"net"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	grpchealth "google.golang.org/grpc/health"

	adminv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/admin/v1"
	aggregatorv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/aggregator/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/app"
	"github.com/0furkancolak/payrudder/pkg/chaos"
	"github.com/0furkancolak/payrudder/pkg/config"
	"github.com/0furkancolak/payrudder/pkg/grpcx"
	"github.com/0furkancolak/payrudder/pkg/health"
	"github.com/0furkancolak/payrudder/pkg/kafkax"
	"github.com/0furkancolak/payrudder/pkg/meters"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/sandboxreset"
	"github.com/0furkancolak/payrudder/pkg/tenantdir"
	_ "github.com/0furkancolak/payrudder/pkg/tzdata" // IANA zones for time.LoadLocation in distroless images
	"github.com/0furkancolak/payrudder/services/aggregator/internal/apply"
	"github.com/0furkancolak/payrudder/services/aggregator/internal/grpcapi"
	"github.com/0furkancolak/payrudder/services/aggregator/internal/maintenance"
	"github.com/0furkancolak/payrudder/services/aggregator/internal/replay"
	aggregatorreset "github.com/0furkancolak/payrudder/services/aggregator/internal/sandboxreset"
	"github.com/0furkancolak/payrudder/services/aggregator/internal/settings"
	"github.com/0furkancolak/payrudder/services/aggregator/migrations"
)

//go:embed config.default.yaml
var defaults []byte

type Config struct {
	config.Base
	pgxx.Settings
	// CellSettings turns on cells mode (ADR 0018). Off unless PAYRUDDER_CELLS names at least one cell.
	pgxx.CellSettings
	GRPCAddr       string        `env:"PAYRUDDER_GRPC_ADDR"`
	PlansAddr      string        `env:"PAYRUDDER_PLANS_ADDR"`
	KafkaBrokers   []string      `env:"PAYRUDDER_KAFKA_BROKERS,required" envSeparator:","`
	DLQTopic       string        `env:"PAYRUDDER_DLQ_TOPIC"`
	PollRecords    int           `env:"PAYRUDDER_POLL_RECORDS"`
	Backoff        time.Duration `env:"PAYRUDDER_CONSUMER_BACKOFF"`
	SessionTimeout time.Duration `env:"PAYRUDDER_CONSUMER_SESSION_TIMEOUT"`
	GroupID        string        `env:"PAYRUDDER_CONSUMER_GROUP"`
}

func (c *Config) Validate() error {
	errs := []error{c.Base.Validate()}
	errs = append(errs, c.CheckMigration(), c.CheckCells())
	if c.PollRecords < 1 {
		errs = append(errs, errors.New("PAYRUDDER_POLL_RECORDS must be ≥ 1"))
	}
	return errors.Join(errs...)
}

func main() { app.Main("aggregator", defaults, start) }

// start wires aggregator's dependencies and runs it until ctx is cancelled.
func start(ctx context.Context, cfg *Config, rt *app.Runtime) error {
	log, hh := rt.Log, rt.Health()
	if cfg.Enabled() && cfg.MigrateOnStart {
		if err := cfg.MigrateCells(ctx, tenantSchemas(), cfg.MigrateOnly, log); err != nil {
			return err
		}
	}
	if done, err := cfg.MigrateIfAsked(ctx, migrations.FS, cfg.MigrateOnly, log); err != nil || done {
		return err
	}
	appPool, systemPool, err := cfg.OpenPools(ctx)
	if err != nil {
		return err
	}
	defer appPool.Close()
	defer systemPool.Close()

	plansConn, err := grpcx.NewTenantClient("plans-svc", cfg.PlansAddr)
	if err != nil {
		return err
	}
	defer func() { _ = plansConn.Close() }()
	src := settings.NewSource(plansv1.NewAdminServiceClient(plansConn), time.Minute)

	// Cells mode (ADR 0018): a tenant's transactions go where the directory says its data is. Off, the pools are used directly, as ever.
	var appDB pgxx.Beginner = appPool
	var cells *pgxx.Router // the system-role router; nil when cells are off
	if cfg.Enabled() {
		dir := tenantdir.NewGRPC(plansv1.NewDirectoryServiceClient(plansConn), tenantdir.Options{})
		routers, err := cfg.OpenRouters(ctx, appPool, systemPool, dir)
		if err != nil {
			return err
		}
		defer routers.Close()
		appDB, cells = routers.App, routers.System
		log.Info("cells mode", "cells", cells.CellNames())
	}

	producer, err := kafkax.NewProducer(cfg.KafkaBrokers)
	if err != nil {
		return err
	}
	defer producer.Close()

	handler := &apply.Handler{
		Pool: systemPool, Cells: cells, Meters: meters.New(plansv1.NewPlansServiceClient(plansConn), time.Minute, apply.ErrMeterNotFound), Settings: src,
		DLQ: kafkax.DLQ{Client: producer, Topic: cfg.DLQTopic, Consumer: cfg.GroupID}, Log: log,
		ConsumerGroup: cfg.GroupID, AfterCommit: chaos.Crash("AGG_CRASH_AFTER_COMMIT", "chaos build: aggregator will crash after commit", "chaos: crashing after commit"),
	}
	consumer, err := newConsumer(cfg, log, handler)
	if err != nil {
		return err
	}
	defer consumer.Close()

	hh.AddCheck("postgres", appPool.Ping)
	hh.AddCheck("kafka", producer.Ping)

	srv, grpcHealth, replays := newGRPCServer(cfg, log, appDB, systemPool, cells, handler, src)
	lis, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		return err
	}
	w := &workers{cfg: cfg, log: log, pool: systemPool, cells: cells, consumer: consumer, replays: replays, health: hh, srv: srv, grpcHealth: grpcHealth, lis: lis}
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
func newGRPCServer(cfg *Config, log *slog.Logger, appDB pgxx.Beginner, systemPool *pgxpool.Pool, cells *pgxx.Router, handler *apply.Handler, src *settings.Source) (*grpc.Server, *grpchealth.Server, *replay.Manager) {
	srv, grpcHealth := app.NewGRPCServer(log, cfg.Common())
	aggregatorv1.RegisterUsageQueryServiceServer(srv, grpcapi.New(appDB, src, cfg.GroupID, apply.Topic))
	replays := replay.New(replay.Config{
		Pool: systemPool, Cells: cells, Brokers: cfg.KafkaBrokers, Topic: apply.Topic, LiveGroup: cfg.GroupID,
		Meters: handler.Meters, Settings: src, Log: log,
	})
	aggregatorv1.RegisterReplayServiceServer(srv, replay.NewService(replays))
	adminv1.RegisterSandboxResetServiceServer(srv, sandboxreset.New(aggregatorreset.New(systemPool).WithCells(cells).Reset))
	return srv, grpcHealth, replays
}

// workers are the long-running parts of aggregator: the usage consumer, maintenance, replay jobs and the gRPC server.
type workers struct {
	cfg        *Config
	log        *slog.Logger
	pool       *pgxpool.Pool
	cells      *pgxx.Router
	consumer   *kafkax.Consumer
	replays    *replay.Manager
	health     *health.Handler
	srv        *grpc.Server
	grpcHealth *grpchealth.Server
	lis        net.Listener
}

// run starts everything and blocks until ctx is cancelled or one of them fails, then drains the gRPC server.
func (w *workers) run(ctx context.Context) error {
	g := app.NewGroup(ctx, w.log, w.cfg.MetricsAddr, w.health)
	gctx := g.Context()
	g.Go(func() error { return w.consumer.Run(gctx) })
	g.Go(func() error { maintenance.Run(gctx, w.pool, w.cells, time.Hour, w.log); return nil })
	g.Go(func() error { return w.replays.Run(gctx) })
	w.log.Info("grpc listening", "addr", w.cfg.GRPCAddr, "metrics", w.cfg.MetricsAddr, "kafka", w.cfg.KafkaBrokers)
	g.ServeGRPC(w.srv, w.grpcHealth, w.lis)
	return g.Wait()
}

// tenantSchemas is what a tenant's schema in a cell is built from (ADR 0018).
func tenantSchemas() pgxx.TenantSchemas {
	return pgxx.TenantSchemas{Migrations: migrations.Tenant(), Roles: []string{"aggregator_app", "aggregator_system"}}
}
