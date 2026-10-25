// Command analytics-svc streams usage, limit and invoice facts into ClickHouse and serves usage series and rankings.
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

	"github.com/ClickHouse/clickhouse-go/v2"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	analyticsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/analytics/v1"
	"github.com/0furkancolak/payrudder/pkg/config"
	"github.com/0furkancolak/payrudder/pkg/grpcx"
	"github.com/0furkancolak/payrudder/pkg/health"
	"github.com/0furkancolak/payrudder/pkg/kafkax"
	"github.com/0furkancolak/payrudder/pkg/logx"
	"github.com/0furkancolak/payrudder/pkg/otelx"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/analytics-svc/internal/api"
	"github.com/0furkancolak/payrudder/services/analytics-svc/internal/chmigrate"
	"github.com/0furkancolak/payrudder/services/analytics-svc/internal/consumer"
	"github.com/0furkancolak/payrudder/services/analytics-svc/migrations"
)

//go:embed config.default.yaml
var defaults []byte

type Config struct {
	config.Base
	GRPCAddr       string        `env:"PAYRUDDER_GRPC_ADDR"`
	ClickHouseAddr string        `env:"CLICKHOUSE_ADDR"`
	ClickHouseDB   string        `env:"CLICKHOUSE_DB"`
	ClickHouseUser string        `env:"CLICKHOUSE_USER"`
	ClickHousePass string        `env:"CLICKHOUSE_PASSWORD,required"`
	KafkaBrokers   []string      `env:"PAYRUDDER_KAFKA_BROKERS,required" envSeparator:","`
	Group          string        `env:"PAYRUDDER_CONSUMER_GROUP"`
	BatchRows      int           `env:"ANALYTICS_BATCH_ROWS"`
	BatchWait      time.Duration `env:"ANALYTICS_BATCH_WAIT"`
	MigrateOnStart bool          `env:"PAYRUDDER_MIGRATE_ON_START"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "analytics-svc:", err)
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

	conn, err := clickhouse.Open(chmigrate.Options(cfg.ClickHouseAddr, cfg.ClickHouseDB, cfg.ClickHouseUser, cfg.ClickHousePass))
	if err != nil {
		return fmt.Errorf("clickhouse: %w", err)
	}
	defer conn.Close()
	if cfg.MigrateOnStart {
		// ClickHouse may still be starting: retry for a while.
		var merr error
		for i := 0; i < 30 && ctx.Err() == nil; i++ {
			if merr = chmigrate.Run(ctx, conn, migrations.FS); merr == nil {
				break
			}
			log.Warn("clickhouse migration not ready, retrying", "error", merr)
			time.Sleep(2 * time.Second)
		}
		if merr != nil {
			return merr
		}
	}
	if cfg.MigrateOnly {
		log.Info("migrations applied; exiting (PAYRUDDER_MIGRATE_ONLY)")
		return nil
	}
	producer, err := kafkax.NewProducer(cfg.KafkaBrokers)
	if err != nil {
		return err
	}
	defer producer.Close()
	runner, err := consumer.New(consumer.Config{Brokers: cfg.KafkaBrokers, Group: cfg.Group, Conn: conn, Producer: producer,
		BatchRows: cfg.BatchRows, BatchWait: cfg.BatchWait, Log: log})
	if err != nil {
		return err
	}

	hh := health.New(reg, 2*time.Second)
	hh.AddCheck("clickhouse", func(ctx context.Context) error { return conn.Ping(ctx) })
	hh.AddCheck("kafka", producer.Ping)
	srv, grpcHealth := grpcx.NewServer(grpcx.ServerOptions{
		Logger: log, Reflection: cfg.Env == config.EnvLocal,
		Extra: []grpc.ServerOption{
			grpc.ChainUnaryInterceptor(tenant.UnaryServerInterceptor()),
			grpc.ChainStreamInterceptor(tenant.StreamServerInterceptor()),
		},
	})
	analyticsv1.RegisterAnalyticsServiceServer(srv, api.New(conn))
	lis, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		return err
	}
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return health.Serve(gctx, cfg.MetricsAddr, hh) })
	g.Go(func() error { return runner.Run(gctx) })
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
