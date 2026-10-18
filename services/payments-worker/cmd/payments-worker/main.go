// Command payments-worker executes charge, refund and status commands against the payment provider.
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

	"github.com/0furkancolak/quotify/pkg/config"
	"github.com/0furkancolak/quotify/pkg/health"
	"github.com/0furkancolak/quotify/pkg/logx"
	"github.com/0furkancolak/quotify/pkg/otelx"
	"github.com/0furkancolak/quotify/pkg/payments"
	"github.com/0furkancolak/quotify/pkg/pgxx"
	"github.com/0furkancolak/quotify/pkg/rabbitx"
	"github.com/0furkancolak/quotify/pkg/workerx"
	"github.com/0furkancolak/quotify/services/payments-worker/internal/handler"
	"github.com/0furkancolak/quotify/services/payments-worker/internal/store"
	"github.com/0furkancolak/quotify/services/payments-worker/migrations"
)

type Config struct {
	config.Base
	PGSystemDSN    string        `env:"QUOTIFY_PG_SYSTEM_DSN,required"`
	PGMigratorDSN  string        `env:"QUOTIFY_PG_MIGRATOR_DSN"`
	MigrateOnStart bool          `env:"QUOTIFY_MIGRATE_ON_START" envDefault:"false"`
	RabbitMQURL    string        `env:"QUOTIFY_RABBITMQ_URL,required"`
	MockPSPURL     string        `env:"MOCKPSP_URL" envDefault:"http://localhost:8090"`
	PSPTimeout     time.Duration `env:"PAYMENTS_PSP_TIMEOUT" envDefault:"5s"`
	// LiveProvider selects the provider for live tenants: "mockpsp" (the local demo) or empty (not configured: live
	// charges reply PROVIDER_NOT_CONFIGURED). Sandbox tenants always use mock-psp.
	LiveProvider string `env:"PAYMENTS_LIVE_PROVIDER"`
	Prefetch     int    `env:"QUOTIFY_PREFETCH" envDefault:"16"`
}

func (c *Config) Validate() error {
	errs := []error{c.Base.Validate()}
	if c.MigrateOnStart && c.PGMigratorDSN == "" {
		errs = append(errs, errors.New("QUOTIFY_PG_MIGRATOR_DSN is required with QUOTIFY_MIGRATE_ON_START"))
	}
	if c.LiveProvider != "" && c.LiveProvider != "mockpsp" {
		errs = append(errs, fmt.Errorf("PAYMENTS_LIVE_PROVIDER %q is not supported (v1 ships only mockpsp)", c.LiveProvider))
	}
	return errors.Join(errs...)
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "payments-worker:", err)
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
		if err := pgxx.Migrate(ctx, cfg.PGMigratorDSN, migrations.FS, log); err != nil {
			return err
		}
	}
	pool, err := pgxx.NewPool(ctx, cfg.PGSystemDSN)
	if err != nil {
		return err
	}
	defer pool.Close()

	mock := payments.NewMockPSP(cfg.MockPSPURL, cfg.PSPTimeout)
	h := &handler.Handler{Sandbox: mock, Attempts: store.Attempts{DB: pool}}
	if cfg.LiveProvider == "mockpsp" {
		h.Live = mock
	}
	w := &workerx.Worker{
		URL: cfg.RabbitMQURL, Prefetch: cfg.Prefetch, Handler: h, Inbox: h.Attempts, Log: log,
		Queues: []string{rabbitx.QueuePaymentCharge, rabbitx.QueuePaymentRefund, rabbitx.QueuePaymentStatus},
	}

	hh := health.New(reg, 2*time.Second)
	hh.AddCheck("postgres", pool.Ping)
	hh.AddCheck("rabbitmq", func(context.Context) error { return w.Ping() })
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return health.Serve(gctx, cfg.MetricsAddr, hh) })
	g.Go(func() error {
		log.Info("payments-worker consuming", "live_provider", cfg.LiveProvider)
		return w.Run(gctx)
	})
	if err := g.Wait(); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	log.Info("stopped")
	return nil
}
