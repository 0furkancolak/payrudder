// Command invoice-worker renders invoice PDFs and stores them in S3-compatible object storage.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/0furkancolak/quotify/pkg/blob"
	"github.com/0furkancolak/quotify/pkg/config"
	"github.com/0furkancolak/quotify/pkg/health"
	"github.com/0furkancolak/quotify/pkg/logx"
	"github.com/0furkancolak/quotify/pkg/otelx"
	"github.com/0furkancolak/quotify/pkg/rabbitx"
	"github.com/0furkancolak/quotify/pkg/workerx"
	"github.com/0furkancolak/quotify/services/invoice-worker/internal/handler"
)

type Config struct {
	config.Base
	RabbitMQURL  string `env:"QUOTIFY_RABBITMQ_URL,required"`
	S3Endpoint   string `env:"S3_ENDPOINT"`
	S3Region     string `env:"S3_REGION" envDefault:"us-east-1"`
	S3Bucket     string `env:"S3_BUCKET" envDefault:"invoices"`
	S3AccessKey  string `env:"S3_ACCESS_KEY,required"`
	S3SecretKey  string `env:"S3_SECRET_KEY,required"`
	S3ForcePath  bool   `env:"S3_FORCE_PATH_STYLE" envDefault:"false"`
	Prefetch     int    `env:"QUOTIFY_PREFETCH" envDefault:"8"`
	EnsureBucket bool   `env:"S3_ENSURE_BUCKET" envDefault:"false"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "invoice-worker:", err)
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

	store, err := blob.New(blob.Config{Endpoint: cfg.S3Endpoint, Region: cfg.S3Region, Bucket: cfg.S3Bucket,
		AccessKey: cfg.S3AccessKey, SecretKey: cfg.S3SecretKey, ForcePathStyle: cfg.S3ForcePath})
	if err != nil {
		return err
	}
	if cfg.EnsureBucket {
		if err := store.EnsureBucket(ctx); err != nil {
			return err
		}
	}
	w := &workerx.Worker{URL: cfg.RabbitMQURL, Prefetch: cfg.Prefetch, Handler: &handler.Handler{Store: store}, Log: log,
		Queues: []string{rabbitx.QueueInvoiceRender}}

	hh := health.New(reg, 2*time.Second)
	hh.AddCheck("rabbitmq", func(context.Context) error { return w.Ping() })
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return health.Serve(gctx, cfg.MetricsAddr, hh) })
	g.Go(func() error { log.Info("invoice-worker consuming", "bucket", cfg.S3Bucket); return w.Run(gctx) })
	if err := g.Wait(); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	log.Info("stopped")
	return nil
}
