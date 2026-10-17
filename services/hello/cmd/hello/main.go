// Command hello is the temporary Phase 00 service that proves the skeleton end to end.
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

	"github.com/twmb/franz-go/pkg/kgo"
	"golang.org/x/sync/errgroup"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	hellov1 "github.com/0furkancolak/quotify/gen/go/quotify/hello/v1"
	"github.com/0furkancolak/quotify/pkg/config"
	"github.com/0furkancolak/quotify/pkg/grpcx"
	"github.com/0furkancolak/quotify/pkg/health"
	"github.com/0furkancolak/quotify/pkg/logx"
	"github.com/0furkancolak/quotify/pkg/otelx"
	"github.com/0furkancolak/quotify/services/hello/internal/hello"
)

type Config struct {
	config.Base
	GRPCAddr     string   `env:"QUOTIFY_GRPC_ADDR" envDefault:":7099"`
	KafkaBrokers []string `env:"QUOTIFY_KAFKA_BROKERS" envSeparator:"," envDefault:"localhost:19092"`
	Topic        string   `env:"HELLO_TOPIC" envDefault:"hello.events.v1"`
}

func (c *Config) Validate() error {
	if len(c.KafkaBrokers) == 0 {
		return errors.Join(c.Base.Validate(), errors.New("QUOTIFY_KAFKA_BROKERS is empty"))
	}
	return c.Base.Validate()
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "hello:", err)
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
		ServiceName: cfg.ServiceName, Version: cfg.Version, Environment: string(cfg.Env),
		OTLPEndpoint: cfg.OTLPEndpoint, Registry: reg,
	})
	if err != nil {
		return err
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = providers.Shutdown(shutdownCtx)
	}()

	producer, err := kgo.NewClient(kgo.SeedBrokers(cfg.KafkaBrokers...), kgo.AllowAutoTopicCreation())
	if err != nil {
		return fmt.Errorf("kafka producer: %w", err)
	}
	defer producer.Close()
	consumerClient, err := kgo.NewClient(kgo.SeedBrokers(cfg.KafkaBrokers...), kgo.ConsumerGroup(cfg.ServiceName),
		kgo.ConsumeTopics(cfg.Topic), kgo.AllowAutoTopicCreation())
	if err != nil {
		return fmt.Errorf("kafka consumer: %w", err)
	}
	defer consumerClient.Close()

	hh := health.New(reg, 2*time.Second)
	hh.AddCheck("kafka", producer.Ping)

	srv, grpcHealth := grpcx.NewServer(grpcx.ServerOptions{Logger: log, Reflection: cfg.Env == config.EnvLocal})
	hellov1.RegisterHelloServiceServer(srv, hello.NewServer(producer, cfg.Topic))
	lis, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		return err
	}

	consumer := hello.NewConsumer(consumerClient, func(ctx context.Context, ev *hellov1.HelloEvent) error {
		log.InfoContext(ctx, "hello event consumed", "event_id", ev.GetEventId(), "name", ev.GetName())
		return nil
	}, log)

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return health.Serve(gctx, cfg.MetricsAddr, hh) })
	g.Go(func() error { return consumer.Run(gctx) })
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
