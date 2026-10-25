// Command assets-svc stores tenant files in S3-compatible object storage: presigned multipart uploads, streamed
// uploads for small files, quotas and the housekeeping that keeps both honest.
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

	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	assetsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/assets/v1"
	"github.com/0furkancolak/payrudder/pkg/blob"
	"github.com/0furkancolak/payrudder/pkg/config"
	"github.com/0furkancolak/payrudder/pkg/grpcx"
	"github.com/0furkancolak/payrudder/pkg/health"
	"github.com/0furkancolak/payrudder/pkg/logx"
	"github.com/0furkancolak/payrudder/pkg/otelx"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/assets-svc/internal/background"
	"github.com/0furkancolak/payrudder/services/assets-svc/internal/policy"
	"github.com/0furkancolak/payrudder/services/assets-svc/internal/service"
	"github.com/0furkancolak/payrudder/services/assets-svc/migrations"
)

//go:embed config.default.yaml
var defaults []byte

type Config struct {
	config.Base
	GRPCAddr       string        `env:"PAYRUDDER_GRPC_ADDR"`
	PGAppDSN       string        `env:"PAYRUDDER_PG_APP_DSN,required"`
	PGSystemDSN    string        `env:"PAYRUDDER_PG_SYSTEM_DSN,required"`
	PGMigratorDSN  string        `env:"PAYRUDDER_PG_MIGRATOR_DSN"`
	MigrateOnStart bool          `env:"PAYRUDDER_MIGRATE_ON_START"`
	S3Endpoint     string        `env:"S3_ENDPOINT"`
	S3Public       string        `env:"S3_PUBLIC_ENDPOINT"`
	S3Region       string        `env:"S3_REGION"`
	S3Bucket       string        `env:"S3_BUCKET"`
	S3AccessKey    string        `env:"S3_ACCESS_KEY,required"`
	S3SecretKey    string        `env:"S3_SECRET_KEY,required"`
	S3ForcePath    bool          `env:"S3_FORCE_PATH_STYLE"`
	GeneralTypes   []string      `env:"ASSETS_GENERAL_TYPES" envSeparator:","`
	QuotaFreeMiB   int64         `env:"ASSETS_QUOTA_FREE_MIB"`
	QuotaStdMiB    int64         `env:"ASSETS_QUOTA_STANDARD_MIB"`
	QuotaEntMiB    int64         `env:"ASSETS_QUOTA_ENTERPRISE_MIB"`
	SweepEvery     time.Duration `env:"ASSETS_SWEEP_INTERVAL"`
	VerifyEvery    time.Duration `env:"ASSETS_VERIFY_INTERVAL"`
	OrphansEvery   time.Duration `env:"ASSETS_ORPHANS_INTERVAL"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "assets-svc:", err)
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
		if cfg.PGMigratorDSN == "" {
			return errors.New("PAYRUDDER_PG_MIGRATOR_DSN is required with PAYRUDDER_MIGRATE_ON_START")
		}
		if err := pgxx.Migrate(ctx, cfg.PGMigratorDSN, migrations.FS, log); err != nil {
			return err
		}
	}
	if cfg.MigrateOnly {
		log.Info("migrations applied; exiting (PAYRUDDER_MIGRATE_ONLY)")
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
