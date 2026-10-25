// Command analytics-svc streams usage, limit and invoice facts into ClickHouse and serves usage series and rankings.
package main

import (
	"context"
	_ "embed" // config.default.yaml
	"fmt"
	"net"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"

	adminv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/admin/v1"
	analyticsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/analytics/v1"
	"github.com/0furkancolak/payrudder/pkg/app"
	"github.com/0furkancolak/payrudder/pkg/config"
	"github.com/0furkancolak/payrudder/pkg/kafkax"
	"github.com/0furkancolak/payrudder/pkg/sandboxreset"
	"github.com/0furkancolak/payrudder/services/analytics-svc/internal/chmigrate"
	"github.com/0furkancolak/payrudder/services/analytics-svc/internal/consumer"
	"github.com/0furkancolak/payrudder/services/analytics-svc/internal/grpcapi"
	analyticsreset "github.com/0furkancolak/payrudder/services/analytics-svc/internal/sandboxreset"
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

func main() { app.Main("analytics-svc", defaults, start) }

func start(ctx context.Context, cfg *Config, rt *app.Runtime) error {
	log := rt.Log
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

	hh := rt.Health()
	hh.AddCheck("clickhouse", func(ctx context.Context) error { return conn.Ping(ctx) })
	hh.AddCheck("kafka", producer.Ping)
	srv, grpcHealth := app.NewGRPCServer(log, cfg.Common())
	analyticsv1.RegisterAnalyticsServiceServer(srv, grpcapi.New(conn))
	adminv1.RegisterSandboxResetServiceServer(srv, sandboxreset.New(analyticsreset.New(conn).Reset))
	lis, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		return err
	}
	g := app.NewGroup(ctx, log, cfg.MetricsAddr, hh)
	gctx := g.Context()
	g.Go(func() error { return runner.Run(gctx) })
	log.Info("grpc listening", "addr", cfg.GRPCAddr, "metrics", cfg.MetricsAddr)
	g.ServeGRPC(srv, grpcHealth, lis)
	return g.Wait()
}
