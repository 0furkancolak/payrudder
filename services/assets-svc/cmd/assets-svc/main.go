// Command assets-svc stores tenant files in S3-compatible object storage: presigned multipart uploads, streamed
// uploads for small files, quotas and the housekeeping that keeps both honest.
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

	assetsv1 "github.com/0furkancolak/quotify/gen/go/quotify/assets/v1"
	"github.com/0furkancolak/quotify/pkg/blob"
	"github.com/0furkancolak/quotify/pkg/config"
	"github.com/0furkancolak/quotify/pkg/grpcx"
	"github.com/0furkancolak/quotify/pkg/health"
	"github.com/0furkancolak/quotify/pkg/logx"
	"github.com/0furkancolak/quotify/pkg/otelx"
	"github.com/0furkancolak/quotify/pkg/pgxx"
	"github.com/0furkancolak/quotify/pkg/tenant"
	"github.com/0furkancolak/quotify/services/assets-svc/internal/background"
	"github.com/0furkancolak/quotify/services/assets-svc/internal/policy"
	"github.com/0furkancolak/quotify/services/assets-svc/internal/service"
	"github.com/0furkancolak/quotify/services/assets-svc/migrations"
)

type Config struct {
	config.Base
	GRPCAddr       string        `env:"QUOTIFY_GRPC_ADDR" envDefault:":7007"`
	PGAppDSN       string        `env:"QUOTIFY_PG_APP_DSN,required"`
	PGSystemDSN    string        `env:"QUOTIFY_PG_SYSTEM_DSN,required"`
	PGMigratorDSN  string        `env:"QUOTIFY_PG_MIGRATOR_DSN"`
	MigrateOnStart bool          `env:"QUOTIFY_MIGRATE_ON_START" envDefault:"false"`
	S3Endpoint     string        `env:"S3_ENDPOINT"`
	S3Public       string        `env:"S3_PUBLIC_ENDPOINT"`
	S3Region       string        `env:"S3_REGION" envDefault:"us-east-1"`
	S3Bucket       string        `env:"S3_BUCKET" envDefault:"assets"`
	S3AccessKey    string        `env:"S3_ACCESS_KEY,required"`
	S3SecretKey    string        `env:"S3_SECRET_KEY,required"`
	S3ForcePath    bool          `env:"S3_FORCE_PATH_STYLE" envDefault:"false"`
	GeneralTypes   []string      `env:"ASSETS_GENERAL_TYPES" envSeparator:"," envDefault:"application/pdf,image/png,image/jpeg,text/csv,application/json,application/zip"`
	QuotaFreeMiB   int64         `env:"ASSETS_QUOTA_FREE_MIB" envDefault:"100"`
	QuotaStdMiB    int64         `env:"ASSETS_QUOTA_STANDARD_MIB" envDefault:"10240"`
	QuotaEntMiB    int64         `env:"ASSETS_QUOTA_ENTERPRISE_MIB" envDefault:"1048576"`
	SweepEvery     time.Duration `env:"ASSETS_SWEEP_INTERVAL" envDefault:"1m"`
	VerifyEvery    time.Duration `env:"ASSETS_VERIFY_INTERVAL" envDefault:"30s"`
	OrphansEvery   time.Duration `env:"ASSETS_ORPHANS_INTERVAL" envDefault:"24h"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "assets-svc:", err)
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
		if cfg.PGMigratorDSN == "" {
			return errors.New("QUOTIFY_PG_MIGRATOR_DSN is required with QUOTIFY_MIGRATE_ON_START")
		}
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

	store, err := blob.New(blob.Config{Endpoint: cfg.S3Endpoint, PublicEndpoint: cfg.S3Public, Region: cfg.S3Region, Bucket: cfg.S3Bucket,
		AccessKey: cfg.S3AccessKey, SecretKey: cfg.S3SecretKey, ForcePathStyle: cfg.S3ForcePath})
	if err != nil {
		return err
	}
	svc := service.New(appPool, store, policy.New(cfg.GeneralTypes), service.Config{
		Quotas: map[string]int64{"free": cfg.QuotaFreeMiB * policy.MiB, "standard": cfg.QuotaStdMiB * policy.MiB, "enterprise": cfg.QuotaEntMiB * policy.MiB},
		Log:    log,
	})
	jobs := &background.Jobs{DB: systemPool, Store: store, Log: log}

	hh := health.New(reg, 2*time.Second)
	hh.AddCheck("postgres", appPool.Ping)
	srv, grpcHealth := grpcx.NewServer(grpcx.ServerOptions{
		Logger: log, Reflection: cfg.Env == config.EnvLocal,
		Extra: []grpc.ServerOption{
			grpc.ChainUnaryInterceptor(tenant.UnaryServerInterceptor()),
			grpc.ChainStreamInterceptor(tenant.StreamServerInterceptor()),
		},
	})
	assetsv1.RegisterAssetsServiceServer(srv, svc)
	lis, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		return err
	}
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return health.Serve(gctx, cfg.MetricsAddr, hh) })
	g.Go(func() error { jobs.Run(gctx, cfg.SweepEvery, cfg.VerifyEvery, cfg.OrphansEvery); return nil })
	g.Go(func() error {
		log.Info("grpc listening", "addr", cfg.GRPCAddr, "metrics", cfg.MetricsAddr, "bucket", cfg.S3Bucket)
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
