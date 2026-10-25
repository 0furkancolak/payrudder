// Command invoice-worker renders invoice PDFs and stores them in S3-compatible object storage.
package main

import (
	"context"
	_ "embed" // config.default.yaml

	"github.com/0furkancolak/payrudder/pkg/app"
	"github.com/0furkancolak/payrudder/pkg/blob"
	"github.com/0furkancolak/payrudder/pkg/config"
	"github.com/0furkancolak/payrudder/pkg/rabbitx"
	"github.com/0furkancolak/payrudder/pkg/workerx"
	"github.com/0furkancolak/payrudder/services/invoice-worker/internal/handler"
)

//go:embed config.default.yaml
var defaults []byte

type Config struct {
	config.Base
	RabbitMQURL    string `env:"PAYRUDDER_RABBITMQ_URL,required"`
	S3Endpoint     string `env:"S3_ENDPOINT"`
	S3Region       string `env:"S3_REGION"`
	S3Bucket       string `env:"S3_BUCKET"`
	S3AssetsBucket string `env:"S3_ASSETS_BUCKET"`
	S3AccessKey    string `env:"S3_ACCESS_KEY,required"`
	S3SecretKey    string `env:"S3_SECRET_KEY,required"`
	S3ForcePath    bool   `env:"S3_FORCE_PATH_STYLE"`
	Prefetch       int    `env:"PAYRUDDER_PREFETCH"`
	EnsureBucket   bool   `env:"S3_ENSURE_BUCKET"`
}

func main() { app.Main("invoice-worker", defaults, start) }

func start(ctx context.Context, cfg *Config, rt *app.Runtime) error {
	log := rt.Log
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
	logos, err := blob.New(blob.Config{Endpoint: cfg.S3Endpoint, Region: cfg.S3Region, Bucket: cfg.S3AssetsBucket,
		AccessKey: cfg.S3AccessKey, SecretKey: cfg.S3SecretKey, ForcePathStyle: cfg.S3ForcePath})
	if err != nil {
		return err
	}
	w := &workerx.Worker{URL: cfg.RabbitMQURL, Prefetch: cfg.Prefetch, Handler: &handler.Handler{Store: store, Logos: logos}, Log: log,
		Queues: []string{rabbitx.QueueInvoiceRender}}

	hh := rt.Health()
	hh.AddCheck("rabbitmq", func(context.Context) error { return w.Ping() })
	g := app.NewGroup(ctx, log, cfg.MetricsAddr, hh)
	gctx := g.Context()
	g.Go(func() error { log.Info("invoice-worker consuming", "bucket", cfg.S3Bucket); return w.Run(gctx) })
	return g.Wait()
}
