// Command ingest-svc accepts usage events and relays them to Kafka through a transactional outbox.
// The handler and the relay run in the same binary.
package main

import (
	"context"
	_ "embed" // config.default.yaml
	"errors"
	"net"
	"time"

	adminv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/admin/v1"
	ingestv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/ingest/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/app"
	"github.com/0furkancolak/payrudder/pkg/chaos"
	"github.com/0furkancolak/payrudder/pkg/config"
	"github.com/0furkancolak/payrudder/pkg/grpcx"
	"github.com/0furkancolak/payrudder/pkg/kafkax"
	"github.com/0furkancolak/payrudder/pkg/outbox"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/sandboxreset"
	"github.com/0furkancolak/payrudder/pkg/tenantdir"
	_ "github.com/0furkancolak/payrudder/pkg/tzdata" // IANA zones for time.LoadLocation in distroless images
	"github.com/0furkancolak/payrudder/services/ingest-svc/internal/grpcapi"
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
	// CellSettings turns on cells mode (ADR 0018). Off unless PAYRUDDER_CELLS names at least one cell.
	pgxx.CellSettings
	GRPCAddr        string        `env:"PAYRUDDER_GRPC_ADDR"`
	PlansAddr       string        `env:"PAYRUDDER_PLANS_ADDR"`
	KafkaBrokers    []string      `env:"PAYRUDDER_KAFKA_BROKERS,required" envSeparator:","`
	RelayWorkers    int           `env:"PAYRUDDER_RELAY_WORKERS"`
	RelayBatchSize  int           `env:"PAYRUDDER_RELAY_BATCH_SIZE"`
	MaxOutboxDepth  int64         `env:"PAYRUDDER_MAX_OUTBOX_DEPTH"`
	PartitionPeriod time.Duration `env:"PAYRUDDER_PARTITION_MAINTENANCE_INTERVAL"`
}

func (c *Config) Validate() error {
	errs := []error{c.Base.Validate()}
	errs = append(errs, c.CheckMigration(), c.CheckCells())
	if c.RelayWorkers < 1 || c.RelayBatchSize < 1 {
		errs = append(errs, errors.New("PAYRUDDER_RELAY_WORKERS and PAYRUDDER_RELAY_BATCH_SIZE must be ≥ 1"))
	}
	if c.PartitionPeriod <= 0 {
		errs = append(errs, errors.New("PAYRUDDER_PARTITION_MAINTENANCE_INTERVAL must be a positive duration"))
	}
	return errors.Join(errs...)
}

func main() { app.Main("ingest-svc", defaults, start) }

func start(ctx context.Context, cfg *Config, rt *app.Runtime) error {
	log := rt.Log
	if cfg.Enabled() && cfg.MigrateOnStart {
		if err := cfg.MigrateCells(ctx, tenantSchemas(), cfg.MigrateOnly, log); err != nil {
			return err
		}
	}
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

	plansConn, err := grpcx.NewTenantClient("plans-svc", cfg.PlansAddr)
	if err != nil {
		return err
	}
	defer func() { _ = plansConn.Close() }()

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

	svc, err := grpcapi.New(grpcapi.Options{
		App: appDB, System: systemPool, Cells: cells, Log: log, MaxOutboxDepth: cfg.MaxOutboxDepth,
		Lookup: lookup.NewPlans(plansv1.NewPlansServiceClient(plansConn), lookup.Options{}),
	})
	if err != nil {
		return err
	}
	relay, err := outbox.NewRelay(systemPool, outbox.KafkaSink{Client: producer}, outbox.Config{
		Workers: cfg.RelayWorkers, BatchSize: cfg.RelayBatchSize, AfterPublish: chaos.Fail("RELAY_CRASH_AFTER_PRODUCE", "chaos build: relay will crash after produce", "chaos: simulated relay crash after produce"),
	}, log)
	if err != nil {
		return err
	}
	relay.WithCells(cells)

	hh := rt.Health()
	hh.AddCheck("postgres", appPool.Ping)
	hh.AddCheck("kafka", producer.Ping)

	srv, grpcHealth := app.NewGRPCServer(log, cfg.Common())
	ingestv1.RegisterIngestServiceServer(srv, svc)
	adminv1.RegisterSandboxResetServiceServer(srv, sandboxreset.New(ingestreset.New(systemPool).WithCells(cells).Reset))

	lis, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		return err
	}
	g := app.NewGroup(ctx, log, cfg.MetricsAddr, hh)
	gctx := g.Context()
	g.Go(func() error { return svc.Run(gctx) })
	g.Go(func() error { return relay.Run(gctx) })
	g.Go(func() error { maintenance.Run(gctx, systemPool, cells, cfg.PartitionPeriod, log); return nil })
	log.Info("grpc listening", "addr", cfg.GRPCAddr, "metrics", cfg.MetricsAddr, "kafka", cfg.KafkaBrokers)
	g.ServeGRPC(srv, grpcHealth, lis)
	return g.Wait()
}

// tenantSchemas is what a tenant's schema in a cell is built from (ADR 0018).
func tenantSchemas() pgxx.TenantSchemas {
	return pgxx.TenantSchemas{Migrations: migrations.Tenant(), Roles: []string{"ingest_app", "ingest_system"}}
}
