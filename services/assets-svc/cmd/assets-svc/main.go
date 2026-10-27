// Command assets-svc stores tenant files in S3-compatible object storage: presigned multipart uploads, streamed
// uploads for small files, quotas and the housekeeping that keeps both honest.
package main

import (
	"context"
	_ "embed" // config.default.yaml
	"errors"
	"net"
	"time"

	assetsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/assets/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/app"
	"github.com/0furkancolak/payrudder/pkg/blob"
	"github.com/0furkancolak/payrudder/pkg/config"
	"github.com/0furkancolak/payrudder/pkg/grpcx"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/tenantdir"
	"github.com/0furkancolak/payrudder/services/assets-svc/internal/background"
	"github.com/0furkancolak/payrudder/services/assets-svc/internal/grpcapi"
	"github.com/0furkancolak/payrudder/services/assets-svc/internal/policy"
	"github.com/0furkancolak/payrudder/services/assets-svc/migrations"
)

//go:embed config.default.yaml
var defaults []byte

type Config struct {
	config.Base
	pgxx.Settings
	// CellSettings turns on cells mode (ADR 0018). Off unless PAYRUDDER_CELLS names at least one cell.
	pgxx.CellSettings
	GRPCAddr string `env:"PAYRUDDER_GRPC_ADDR"`
	// PlansAddr is plans-svc, where the tenant directory is; it is dialled only in cells mode.
	PlansAddr    string        `env:"PAYRUDDER_PLANS_ADDR"`
	S3Endpoint   string        `env:"S3_ENDPOINT"`
	S3Public     string        `env:"S3_PUBLIC_ENDPOINT"`
	S3Region     string        `env:"S3_REGION"`
	S3Bucket     string        `env:"S3_BUCKET"`
	S3AccessKey  string        `env:"S3_ACCESS_KEY,required"`
	S3SecretKey  string        `env:"S3_SECRET_KEY,required"`
	S3ForcePath  bool          `env:"S3_FORCE_PATH_STYLE"`
	GeneralTypes []string      `env:"ASSETS_GENERAL_TYPES" envSeparator:","`
	QuotaFreeMiB int64         `env:"ASSETS_QUOTA_FREE_MIB"`
	QuotaStdMiB  int64         `env:"ASSETS_QUOTA_STANDARD_MIB"`
	QuotaEntMiB  int64         `env:"ASSETS_QUOTA_ENTERPRISE_MIB"`
	SweepEvery   time.Duration `env:"ASSETS_SWEEP_INTERVAL"`
	VerifyEvery  time.Duration `env:"ASSETS_VERIFY_INTERVAL"`
	OrphansEvery time.Duration `env:"ASSETS_ORPHANS_INTERVAL"`
}

// Validate checks the settings at load time like the other services: migrating needs the migrator DSN (assets-svc used to find that out only after it had
// started), and a job interval of zero would make its ticker panic.
func (c *Config) Validate() error {
	errs := []error{c.Base.Validate(), c.CheckMigration(), c.CheckCells()}
	for name, d := range map[string]time.Duration{"ASSETS_SWEEP_INTERVAL": c.SweepEvery, "ASSETS_VERIFY_INTERVAL": c.VerifyEvery, "ASSETS_ORPHANS_INTERVAL": c.OrphansEvery} {
		if d <= 0 {
			errs = append(errs, errors.New(name+" must be a positive duration"))
		}
	}
	return errors.Join(errs...)
}

func main() { app.Main("assets-svc", defaults, start) }

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

	store, err := blob.New(blob.Config{Endpoint: cfg.S3Endpoint, PublicEndpoint: cfg.S3Public, Region: cfg.S3Region, Bucket: cfg.S3Bucket,
		AccessKey: cfg.S3AccessKey, SecretKey: cfg.S3SecretKey, ForcePathStyle: cfg.S3ForcePath})
	if err != nil {
		return err
	}
	// Cells mode (ADR 0018): a tenant's transactions go where the directory says its data is. Off, the pools are used directly, as ever.
	var appDB pgxx.Beginner = appPool
	var cells *pgxx.Router // the system-role router; nil when cells are off
	if cfg.Enabled() {
		plansConn, err := grpcx.NewTenantClient("plans-svc", cfg.PlansAddr)
		if err != nil {
			return err
		}
		defer func() { _ = plansConn.Close() }()
		routers, err := cfg.OpenRouters(ctx, appPool, systemPool, tenantdir.NewGRPC(plansv1.NewDirectoryServiceClient(plansConn), tenantdir.Options{}))
		if err != nil {
			return err
		}
		defer routers.Close()
		appDB, cells = routers.App, routers.System
		log.Info("cells mode", "cells", cells.CellNames())
	}
	svc := grpcapi.New(appDB, store, policy.New(cfg.GeneralTypes), grpcapi.Config{
		Quotas: map[string]int64{"free": cfg.QuotaFreeMiB * policy.MiB, "standard": cfg.QuotaStdMiB * policy.MiB, "enterprise": cfg.QuotaEntMiB * policy.MiB},
		Log:    log,
	})
	jobs := &background.Jobs{DB: systemPool, Cells: cells, Store: store, Log: log}

	hh := rt.Health()
	hh.AddCheck("postgres", appPool.Ping)
	srv, grpcHealth := app.NewGRPCServer(log, cfg.Common())
	assetsv1.RegisterAssetsServiceServer(srv, svc)
	lis, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		return err
	}
	g := app.NewGroup(ctx, log, cfg.MetricsAddr, hh)
	gctx := g.Context()
	g.Go(func() error { jobs.Run(gctx, cfg.SweepEvery, cfg.VerifyEvery, cfg.OrphansEvery); return nil })
	log.Info("grpc listening", "addr", cfg.GRPCAddr, "metrics", cfg.MetricsAddr, "bucket", cfg.S3Bucket)
	g.ServeGRPC(srv, grpcHealth, lis)
	return g.Wait()
}

// tenantSchemas is what a tenant's schema in a cell is built from (ADR 0018).
func tenantSchemas() pgxx.TenantSchemas {
	return pgxx.TenantSchemas{Migrations: migrations.Tenant(), Roles: []string{"assets_app", "assets_system"}}
}
