// Command payments-worker executes charge, refund and status commands against the payment provider.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"

	billingv1 "github.com/0furkancolak/quotify/gen/go/quotify/billing/v1"
	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/config"
	"github.com/0furkancolak/quotify/pkg/grpcx"
	"github.com/0furkancolak/quotify/pkg/health"
	"github.com/0furkancolak/quotify/pkg/logx"
	"github.com/0furkancolak/quotify/pkg/otelx"
	"github.com/0furkancolak/quotify/pkg/payments"
	"github.com/0furkancolak/quotify/pkg/payments/iyzico"
	"github.com/0furkancolak/quotify/pkg/payments/paddle"
	"github.com/0furkancolak/quotify/pkg/pgxx"
	"github.com/0furkancolak/quotify/pkg/rabbitx"
	"github.com/0furkancolak/quotify/pkg/secretbox"
	"github.com/0furkancolak/quotify/pkg/tenant"
	"github.com/0furkancolak/quotify/pkg/workerx"
	"github.com/0furkancolak/quotify/services/payments-worker/internal/handler"
	"github.com/0furkancolak/quotify/services/payments-worker/internal/store"
	"github.com/0furkancolak/quotify/services/payments-worker/internal/webhook"
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
	// SecretsKey (base64, 32 bytes) seals the credentials of tenants' provider accounts. Without it provider accounts, and with them
	// provider webhooks, are disabled.
	SecretsKey string `env:"QUOTIFY_SECRETS_KEY"`
	// HTTPAddr serves provider webhooks and the operator endpoints for provider accounts and the ledger; the gateway forwards to it.
	HTTPAddr string `env:"PAYMENTS_HTTP_ADDR" envDefault:":8095"`
	// PlansAddr is plans-svc, which is told about subscriptions a provider runs. Empty skips that.
	PlansAddr string `env:"QUOTIFY_PLANS_ADDR"`
	// BillingAddr is billing-svc, whose invoices the reconciliation compares with the ledger. Empty disables the reconciliation.
	BillingAddr string `env:"QUOTIFY_BILLING_ADDR"`
	// InternalToken is the shared secret the gateway sends on /admin calls. Empty disables them.
	InternalToken string `env:"PAYMENTS_INTERNAL_TOKEN"`
}

func (c *Config) Validate() error {
	errs := []error{c.Base.Validate()}
	if c.MigrateOnStart && c.PGMigratorDSN == "" {
		errs = append(errs, errors.New("QUOTIFY_PG_MIGRATOR_DSN is required with QUOTIFY_MIGRATE_ON_START"))
	}
	if c.SecretsKey != "" {
		if _, err := secretbox.ParseKey(c.SecretsKey); err != nil {
			errs = append(errs, fmt.Errorf("QUOTIFY_SECRETS_KEY: %w", err))
		}
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
	if cfg.MigrateOnly {
		log.Info("migrations applied; exiting (QUOTIFY_MIGRATE_ONLY)")
		return nil
	}
	pool, err := pgxx.NewPool(ctx, cfg.PGSystemDSN)
	if err != nil {
		return err
	}
	defer pool.Close()

	mock := payments.NewMockPSP(cfg.MockPSPURL, cfg.PSPTimeout)
	h := &handler.Handler{Sandbox: mock, Attempts: store.Attempts{DB: pool}, Ledger: store.Ledger{DB: pool}, Log: log}
	if cfg.LiveProvider == "mockpsp" {
		h.Live = mock
	}
	registry := payments.NewRegistry()
	iyzico.Register(registry)
	paddle.Register(registry)
	var httpHandler http.Handler
	if cfg.SecretsKey != "" {
		key, _ := secretbox.ParseKey(cfg.SecretsKey) // validated
		box, err := secretbox.New(key)
		if err != nil {
			return err
		}
		accounts := store.Accounts{DB: pool, Box: box}
		h.Resolver = &handler.Resolver{Registry: registry, Accounts: accounts}
		var plansSync webhook.PlansAPI
		if cfg.PlansAddr != "" {
			plansConn, err := grpcx.NewClient(cfg.PlansAddr, grpc.WithChainUnaryInterceptor(tenant.UnaryClientInterceptor()))
			if err != nil {
				return fmt.Errorf("plans-svc client: %w", err)
			}
			defer func() { _ = plansConn.Close() }()
			plansSync = webhook.PlansClient{Client: plansv1.NewPlansServiceClient(plansConn), Admin: plansv1.NewAdminServiceClient(plansConn)}
		}
		var billing webhook.BillingAPI
		if cfg.BillingAddr != "" {
			billingConn, err := grpcx.NewClient(cfg.BillingAddr, grpc.WithChainUnaryInterceptor(tenant.UnaryClientInterceptor()))
			if err != nil {
				return fmt.Errorf("billing-svc client: %w", err)
			}
			defer func() { _ = billingConn.Close() }()
			billing = webhook.BillingClient{Client: billingv1.NewBillingServiceClient(billingConn)}
		}
		httpHandler = (&webhook.Server{Plans: plansSync, Billing: billing, Registry: registry, Resolver: h.Resolver, Accounts: accounts, Events: store.Events{DB: pool},
			Attempts: h.Attempts, Ledger: h.Ledger, Subscriptions: store.Subscriptions{DB: pool}, Objects: store.Objects{DB: pool}, Log: log, Token: cfg.InternalToken}).Handler()
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
	if httpHandler != nil {
		g.Go(func() error { return serveHTTP(gctx, cfg.HTTPAddr, httpHandler, log) })
	} else {
		log.Warn("QUOTIFY_SECRETS_KEY is not set: provider accounts and provider webhooks are disabled")
	}
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

// serveHTTP runs the webhook and operator server until ctx ends.
func serveHTTP(ctx context.Context, addr string, h http.Handler, log *slog.Logger) error {
	srv := &http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Info("payments-worker http listening", "addr", addr)
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(c)
	}
}
