// Command payments-worker executes charge, refund and status commands against the payment provider.
package main

import (
	"context"
	_ "embed" // config.default.yaml
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel"
	"google.golang.org/grpc"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/app"
	"github.com/0furkancolak/payrudder/pkg/audit"
	"github.com/0furkancolak/payrudder/pkg/blob"
	"github.com/0furkancolak/payrudder/pkg/config"
	"github.com/0furkancolak/payrudder/pkg/einvoice"
	"github.com/0furkancolak/payrudder/pkg/einvoice/mock"
	"github.com/0furkancolak/payrudder/pkg/einvoice/nilvera"
	einvoicewebhook "github.com/0furkancolak/payrudder/pkg/einvoice/webhook"
	"github.com/0furkancolak/payrudder/pkg/env"
	"github.com/0furkancolak/payrudder/pkg/grpcx"
	"github.com/0furkancolak/payrudder/pkg/health"
	"github.com/0furkancolak/payrudder/pkg/payments"
	"github.com/0furkancolak/payrudder/pkg/payments/banktransfer"
	"github.com/0furkancolak/payrudder/pkg/payments/iyzico"
	"github.com/0furkancolak/payrudder/pkg/payments/paddle"
	"github.com/0furkancolak/payrudder/pkg/payments/stripe"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/rabbitx"
	"github.com/0furkancolak/payrudder/pkg/secretbox"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/pkg/tenantdir"
	"github.com/0furkancolak/payrudder/pkg/workerx"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/auditlog"
	einvoiceworker "github.com/0furkancolak/payrudder/services/payments-worker/internal/einvoice"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/handler"
	accounthealth "github.com/0furkancolak/payrudder/services/payments-worker/internal/health"
	reconcilecmd "github.com/0furkancolak/payrudder/services/payments-worker/internal/reconciliation/command"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/reconciliation/schedule"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/sandboxreset"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/transfers"
	transfercommand "github.com/0furkancolak/payrudder/services/payments-worker/internal/transfers/command"
	transferquery "github.com/0furkancolak/payrudder/services/payments-worker/internal/transfers/query"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/webhook"
	"github.com/0furkancolak/payrudder/services/payments-worker/migrations"
)

//go:embed config.default.yaml
var defaults []byte

type Config struct {
	config.Base
	pgxx.SystemSettings
	// CellSettings turns on cells mode (ADR 0018). Off unless PAYRUDDER_CELLS names at least one cell; it needs PAYRUDDER_PLANS_ADDR, where the directory is.
	pgxx.CellSettings
	RabbitMQURL string        `env:"PAYRUDDER_RABBITMQ_URL,required"`
	MockPSPURL  string        `env:"MOCKPSP_URL"`
	PSPTimeout  time.Duration `env:"PAYMENTS_PSP_TIMEOUT"`
	// LiveProvider selects the provider for live tenants: "mockpsp" (the local demo) or empty (not configured: live
	// charges reply PROVIDER_NOT_CONFIGURED). Sandbox tenants always use mock-psp.
	LiveProvider string `env:"PAYMENTS_LIVE_PROVIDER"`
	// AllowMockLive must be true to run with PAYMENTS_LIVE_PROVIDER=mockpsp: live tenants then pay through the mock. It is for a local stack and CI, where no real
	// provider exists. Without it the worker refuses to start with the mock as the live provider, and refuses at run time any live command routed to the mock.
	AllowMockLive bool `env:"PAYMENTS_ALLOW_MOCK_LIVE"`
	Prefetch      int  `env:"PAYRUDDER_PREFETCH"`
	// Secrets holds the key (base64, 32 bytes) that seals the credentials of tenants' provider accounts and e-invoice accounts, and the previous key while one is
	// replaced. Without a key provider accounts, and with them provider webhooks, are disabled.
	Secrets secretbox.Settings
	// HTTPAddr serves provider webhooks and the operator endpoints for provider accounts and the ledger; the gateway forwards to it.
	HTTPAddr string `env:"PAYMENTS_HTTP_ADDR"`
	// PlansAddr is plans-svc, which is told about subscriptions a provider runs. Empty skips that.
	PlansAddr string `env:"PAYRUDDER_PLANS_ADDR"`
	// BillingAddr is billing-svc, whose invoices the reconciliation compares with the ledger. Empty disables the reconciliation.
	BillingAddr string `env:"PAYRUDDER_BILLING_ADDR"`
	// InternalToken is the shared secret the gateway sends on /admin calls. Empty disables them.
	InternalToken string `env:"PAYMENTS_INTERNAL_TOKEN"`
	// The object storage e-invoices (UBL, PDF) are kept in: the invoices bucket invoice-worker writes to. Without S3_ACCESS_KEY documents are issued but have no files.
	S3Endpoint  string `env:"S3_ENDPOINT"`
	S3Region    string `env:"S3_REGION"`
	S3Bucket    string `env:"S3_BUCKET"`
	S3AccessKey string `env:"S3_ACCESS_KEY"`
	S3SecretKey string `env:"S3_SECRET_KEY"`
	S3ForcePath bool   `env:"S3_FORCE_PATH_STYLE"`
	// EInvoiceAllowPrivateURLs lets the nilvera base_url and the webhook adapter's URL be http:// or on private addresses. For development and tests; never in production.
	EInvoiceAllowPrivateURLs bool `env:"EINVOICE_ALLOW_PRIVATE_URLS"`
	// TransferWindowDays is how long a bank transfer waits for its money (1 to 90 days) when the tenant's account does not say. After that the payment counts
	// as declined and the tenant's dunning applies.
	TransferWindowDays int `env:"PAYMENTS_TRANSFER_WINDOW_DAYS"`
	// ReconcileInterval is the pause between the scheduled reconciliations (docs/plans/22 G7); 0 turns the schedule off. It runs only with BillingAddr and the secrets key,
	// like the operator endpoints it shares. Default (not agreed): 6 hours.
	ReconcileInterval time.Duration `env:"PAYMENTS_RECONCILE_INTERVAL"`
	// SettlementLag is how long a payment may go unreported by its provider before the reconciliation calls that a discrepancy. Default (not agreed): 7 days.
	SettlementLag time.Duration `env:"PAYMENTS_SETTLEMENT_LAG"`
	// WebhookReclaimAfter is how long a provider notification may stay received or failed in the inbox before the provider's retry takes it over and processes it again (the
	// delivery that recorded it is taken to have died). Default (not agreed): 10 minutes.
	WebhookReclaimAfter time.Duration `env:"PAYMENTS_WEBHOOK_RECLAIM_AFTER"`
	// The circuit breaker of every provider account (docs/provider-health.md), all default (not agreed): this many outages in a row open it (5), it stays open this long
	// (30s) and then lets one charge through to see whether the provider is back.
	BreakerFailures int           `env:"PAYMENTS_BREAKER_FAILURES"`
	BreakerOpenFor  time.Duration `env:"PAYMENTS_BREAKER_OPEN_FOR"`
	// RedisURL, when set, makes the circuit breaker of an account one for all replicas (docs/provider-health.md); without it, or when Redis cannot be reached, each replica
	// keeps its own.
	RedisURL string `env:"PAYRUDDER_REDIS_URL"`
	// HealthInterval is the pause between the checks of every account's health (5m; 0 turns the checks off) and HealthTimeout the limit of one (10s).
	HealthInterval time.Duration `env:"PAYMENTS_HEALTH_INTERVAL"`
	HealthTimeout  time.Duration `env:"PAYMENTS_HEALTH_TIMEOUT"`
	// RetryCeiling bounds how long a provider's Retry-After holds a command's retry back (docs/04-messaging.md): the retry waits the longer of its tier's delay and the
	// Retry-After, at most this. Default (not agreed): 10 minutes, which is also the longest delay queue, so more changes nothing.
	RetryCeiling time.Duration `env:"PAYMENTS_RETRY_CEILING"`
}

func (c *Config) Validate() error {
	errs := []error{c.Base.Validate(), c.CheckSecret("PAYRUDDER_SECRETS_KEY", c.Secrets.SecretsKey), c.CheckSecret("PAYMENTS_INTERNAL_TOKEN", c.InternalToken)}
	errs = append(errs, c.CheckMigration(), c.CheckCells())
	if c.Enabled() && c.PlansAddr == "" {
		errs = append(errs, errors.New("PAYRUDDER_CELLS needs PAYRUDDER_PLANS_ADDR: the tenant directory is plans-svc's"))
	}
	if _, err := c.Secrets.SecretBox(); err != nil {
		errs = append(errs, err)
	}
	if c.TransferWindowDays < 1 || c.TransferWindowDays > banktransfer.MaxPaymentWindowDays {
		errs = append(errs, fmt.Errorf("PAYMENTS_TRANSFER_WINDOW_DAYS must be 1 to %d", banktransfer.MaxPaymentWindowDays))
	}
	if c.ReconcileInterval != 0 && c.ReconcileInterval < time.Minute {
		errs = append(errs, errors.New("PAYMENTS_RECONCILE_INTERVAL must be 0 (off) or at least 1m"))
	}
	if c.BreakerFailures < 1 || c.BreakerOpenFor < time.Second {
		errs = append(errs, errors.New("PAYMENTS_BREAKER_FAILURES must be at least 1 and PAYMENTS_BREAKER_OPEN_FOR at least 1s"))
	}
	if c.HealthInterval != 0 && c.HealthInterval < 30*time.Second || c.HealthTimeout < time.Second {
		errs = append(errs, errors.New("PAYMENTS_HEALTH_INTERVAL must be 0 (off) or at least 30s and PAYMENTS_HEALTH_TIMEOUT at least 1s"))
	}
	if c.RetryCeiling < rabbitx.Tiers[len(rabbitx.Tiers)-1].Delay {
		errs = append(errs, fmt.Errorf("PAYMENTS_RETRY_CEILING must be at least %s, the longest ordinary retry delay", rabbitx.Tiers[len(rabbitx.Tiers)-1].Delay))
	}
	if c.WebhookReclaimAfter < time.Minute {
		errs = append(errs, errors.New("PAYMENTS_WEBHOOK_RECLAIM_AFTER must be at least 1m"))
	}
	if c.SettlementLag < 24*time.Hour {
		errs = append(errs, errors.New("PAYMENTS_SETTLEMENT_LAG must be at least 24h"))
	}
	errs = append(errs, checkLiveProvider(c.LiveProvider, c.AllowMockLive, c.Demo))
	return errors.Join(errs...)
}

// checkLiveProvider is the startup rule for PAYMENTS_LIVE_PROVIDER. Live never reaches the mock (ADR 0023): the mock as the live provider needs an explicit
// "this is not production" (PAYMENTS_ALLOW_MOCK_LIVE), or the demo, which is the mock everywhere. Any other value is not a provider this release ships.
func checkLiveProvider(provider string, allowMock, demo bool) error {
	switch provider {
	case "":
		return nil
	case "mockpsp":
		if !allowMock && !demo {
			return errors.New("PAYMENTS_LIVE_PROVIDER=mockpsp would charge live tenants through the mock: set PAYMENTS_ALLOW_MOCK_LIVE=true on a local or CI stack only, or leave it empty so live tenants use their own provider accounts")
		}
		return nil
	}
	return fmt.Errorf("PAYMENTS_LIVE_PROVIDER %q is not supported (v1 ships only mockpsp, and live tenants use their own provider accounts)", provider)
}

func main() { app.Main("payments-worker", defaults, start) }

// start wires payments-worker's dependencies and runs it until ctx is cancelled.
func start(ctx context.Context, cfg *Config, rt *app.Runtime) error {
	log, hh := rt.Log, rt.Health()
	if cfg.Enabled() && cfg.MigrateOnStart {
		if err := cfg.MigrateCells(ctx, tenantSchemas(), cfg.MigrateOnly, log); err != nil {
			return err
		}
	}
	if done, err := cfg.MigrateIfAsked(ctx, migrations.FS, cfg.MigrateOnly, log); err != nil || done {
		return err
	}
	pool, err := pgxx.NewPool(ctx, cfg.PGSystemDSN)
	if err != nil {
		return err
	}
	defer pool.Close()
	// A table with a tenant_id and no forced row-level security, that is not listed as exempt, is a deployment that must not serve (docs/plans/22 G11).
	if err := pgxx.VerifyForcedRLS(ctx, pool, migrations.RLSExempt); err != nil {
		return fmt.Errorf("row-level security: %w", err)
	}
	// Cells mode (ADR 0018): a tenant's statements go where the directory says its data is. Off, the pool is used directly, as ever. payments-worker has the
	// system role only, so only the system-role router is used.
	var db store.DB = pool
	if cfg.Enabled() {
		plansConn, err := grpcx.NewTenantClient("plans-svc", cfg.PlansAddr)
		if err != nil {
			return err
		}
		defer closeConn(plansConn)
		routers, err := cfg.OpenRouters(ctx, pool, pool, tenantdir.NewGRPC(plansv1.NewDirectoryServiceClient(plansConn), tenantdir.Options{}))
		if err != nil {
			return err
		}
		defer routers.Close()
		db = routers.System
		log.Info("cells mode", "cells", routers.System.CellNames())
	}

	rabbitx.RetryCeiling = cfg.RetryCeiling
	h := newHandler(cfg, db, log)
	dir, closeDir, err := tenantDirectory(cfg)
	if err != nil {
		return err
	}
	defer closeDir()
	h.Tenants = dir
	registry := payments.NewRegistry()
	iyzico.Register(registry)
	paddle.Register(registry)
	stripe.Register(registry)
	einvoices := newEInvoiceRegistry(cfg)
	box, err := cfg.Secrets.SecretBox()
	if err != nil {
		return err
	}
	if err := wireEInvoice(cfg, db, box, h, einvoices, log); err != nil {
		return err
	}
	banktransfer.Register(registry)
	var httpHandler http.Handler
	var reconcile *schedule.Job
	var monitor *accounthealth.Monitor
	if box != nil {
		srv, closeConns, err := webhookServer(*cfg, db, box, h, registry, einvoices, log)
		if err != nil {
			return err
		}
		defer closeConns()
		// audit.Middleware reads who the gateway says is acting (pkg/audit); the gateway sets those headers itself and removes the caller's own.
		httpHandler = audit.Middleware(srv.Handler())
		// The circuit breakers and the failover are of the tenants' own provider accounts, which need the secrets key like everything else that calls a provider for them.
		providerHealth := store.ProviderHealth{DB: db}
		h.Breakers = &accounthealth.Breakers{Settings: payments.BreakerSettings{Failures: cfg.BreakerFailures, OpenFor: cfg.BreakerOpenFor}, Store: providerHealth, Log: log}
		if cfg.RedisURL != "" {
			opts, err := redis.ParseURL(cfg.RedisURL)
			if err != nil {
				return fmt.Errorf("PAYRUDDER_REDIS_URL: %w", err)
			}
			rdb := redis.NewClient(opts)
			defer func() { _ = rdb.Close() }()
			h.Breakers.Shared = &accounthealth.RedisBreakers{Client: rdb, Log: log}
		}
		h.Failovers = providerHealth
		if cfg.HealthInterval > 0 {
			monitor = &accounthealth.Monitor{Store: providerHealth, Resolver: h.Resolver, Breakers: h.Breakers, Interval: cfg.HealthInterval, Timeout: cfg.HealthTimeout, Log: log}
		}
		if cfg.BillingAddr != "" && cfg.ReconcileInterval > 0 {
			reconcile = &schedule.Job{Deps: srv.Reconcile(), Interval: cfg.ReconcileInterval, Log: log}
		}
	}
	w := &workerx.Worker{
		URL: cfg.RabbitMQURL, Prefetch: cfg.Prefetch, Handler: h, Inbox: h.Attempts, Log: log,
		Queues: []string{rabbitx.QueuePaymentCharge, rabbitx.QueuePaymentRefund, rabbitx.QueuePaymentStatus, rabbitx.QueueEInvoiceIssue},
	}

	// The number of live discrepancies waiting for an operator is read on every scrape, whether or not this process runs the schedule (an operator's run, or another
	// replica's, keeps them): plan 22 G6 alerts on it.
	if err := reconcilecmd.RegisterOpenGauge(otel.Meter("github.com/0furkancolak/payrudder/services/payments-worker/reconciliation"), store.Discrepancies{DB: db}.OpenCounts, log); err != nil {
		return err
	}
	hh.AddCheck("postgres", pool.Ping)
	hh.AddCheck("rabbitmq", func(context.Context) error { return w.Ping() })
	return serve(ctx, cfg, log, hh, w, httpHandler, reconcile, monitor)
}

// tenantDirectory is how the worker learns the environment of a command's tenant, to refuse a command that disagrees (ADR 0023). It asks plans-svc and keeps
// the answers (a tenant's environment never changes). Without QUOTIFY_PLANS_ADDR it is nil and commands are checked for having an environment only.
func tenantDirectory(cfg *Config) (*env.Directory, func(), error) {
	if cfg.PlansAddr == "" {
		return nil, func() {}, nil
	}
	conn, err := grpcx.NewTenantClient("plans-svc", cfg.PlansAddr)
	if err != nil {
		return nil, nil, err
	}
	admin := plansv1.NewAdminServiceClient(conn)
	return env.NewDirectory(func(ctx context.Context, id uuid.UUID) (env.Env, error) {
		r, err := admin.GetTenant(tenant.OutgoingContext(tenant.WithOperator(ctx)), &plansv1.GetTenantRequest{TenantId: id.String()})
		if err != nil {
			return "", err
		}
		return env.FromProto(r.GetTenant().GetEnvironment())
	}, 0), func() { closeConn(conn) }, nil
}

// closeConn closes a gRPC client connection at shutdown; there is nothing to do about a failure but to say so.
func closeConn(c *grpc.ClientConn) {
	if err := c.Close(); err != nil {
		slog.Warn("closing a gRPC connection", "err", err)
	}
}

// newHandler builds the command handler: sandbox tenants always use mock-psp, live tenants only when configured.
func newHandler(cfg *Config, db store.DB, log *slog.Logger) *handler.Handler {
	mock := payments.NewMockPSP(cfg.MockPSPURL, cfg.PSPTimeout)
	h := &handler.Handler{Sandbox: mock, Attempts: store.Attempts{DB: db}, Ledger: store.Ledger{DB: db}, Log: log, Demo: cfg.Demo, AllowMockLive: cfg.AllowMockLive,
		Transfers: store.Transfers{DB: db}, TransferWindow: time.Duration(cfg.TransferWindowDays) * 24 * time.Hour}
	if cfg.LiveProvider == "mockpsp" {
		h.Live = mock
	}
	return h
}

// serve runs the metrics server, the optional webhook/operator HTTP server, the scheduled reconciliation and the queue worker until ctx is cancelled or one of them fails.
func serve(ctx context.Context, cfg *Config, log *slog.Logger, hh *health.Handler, w *workerx.Worker, httpHandler http.Handler, reconcile *schedule.Job, monitor *accounthealth.Monitor) error {
	g := app.NewGroup(ctx, log, cfg.MetricsAddr, hh)
	gctx := g.Context()
	if httpHandler != nil {
		g.Go(func() error { return serveHTTP(gctx, cfg.HTTPAddr, httpHandler, log) })
	} else {
		log.Warn("PAYRUDDER_SECRETS_KEY is not set: provider accounts and provider webhooks are disabled")
	}
	if reconcile != nil {
		g.Go(func() error { return reconcile.Run(gctx) })
	}
	if monitor != nil {
		g.Go(func() error { return monitor.Run(gctx) })
	}
	g.Go(func() error {
		log.Info("payments-worker consuming", "live_provider", cfg.LiveProvider)
		return w.Run(gctx)
	})
	return g.Wait()
}

// webhookServer builds the provider-account, webhook and operator server. The returned func closes its gRPC connections.
func webhookServer(cfg Config, db store.DB, box *secretbox.Box, h *handler.Handler, registry *payments.Registry, einvoices *einvoice.Registry, log *slog.Logger) (*webhook.Server, func(), error) {
	accounts := store.Accounts{DB: db, Box: box}
	h.Resolver = &handler.Resolver{Registry: registry, Accounts: accounts}
	var conns []*grpc.ClientConn
	closeConns := func() {
		for _, c := range conns {
			closeConn(c)
		}
	}
	dial := func(addr, what string) (*grpc.ClientConn, error) {
		conn, err := grpcx.NewTenantClient(what, addr)
		if err != nil {
			closeConns()
			return nil, err
		}
		conns = append(conns, conn)
		return conn, nil
	}
	var plansSync webhook.PlansAPI
	if cfg.PlansAddr != "" {
		conn, err := dial(cfg.PlansAddr, "plans-svc")
		if err != nil {
			return nil, nil, err
		}
		plansSync = webhook.PlansClient{Client: plansv1.NewPlansServiceClient(conn), Admin: plansv1.NewAdminServiceClient(conn)}
		h.Customers = plansSync // a charge to a provider that vets the payer carries the customer's own details
	}
	var billing webhook.BillingAPI
	var disputes webhook.DisputesAPI
	var transferBilling transfercommand.Billing
	if cfg.BillingAddr != "" {
		conn, err := dial(cfg.BillingAddr, "billing-svc")
		if err != nil {
			return nil, nil, err
		}
		billingClient := webhook.BillingClient{Client: billingv1.NewBillingServiceClient(conn)}
		billing, disputes = billingClient, billingClient
		transferBilling = transfers.BillingClient{Invoices: billingv1.NewBillingServiceClient(conn), Admin: billingv1.NewBillingAdminServiceClient(conn)}
	}
	transferHandler := &transfers.Handler{Log: log, Deps: transfercommand.Deps{Billing: transferBilling, Deps: transferquery.Deps{
		Transfers: h.Transfers, Accounts: h.Resolver, DefaultWindow: h.TransferWindow}}}
	return &webhook.Server{Plans: plansSync, Billing: billing, Disputes: disputes, DisputeCases: store.Disputes{DB: db}, SettlementLag: cfg.SettlementLag, TransferWindow: h.TransferWindow, Registry: registry, Resolver: h.Resolver, Accounts: accounts, Events: store.Events{DB: db, ReclaimAfter: cfg.WebhookReclaimAfter},
		Attempts: h.Attempts, Ledger: h.Ledger, Subscriptions: store.Subscriptions{DB: db}, Objects: store.Objects{DB: db}, Methods: store.PaymentMethods{DB: db}, Log: log, Token: cfg.InternalToken,
		EInvoiceAccounts: store.EInvoiceAccounts{DB: db, Box: box}, EInvoiceRegistry: einvoices, AllowMockLive: cfg.Demo,
		Reset:   sandboxreset.New(db).Reset,
		Modules: []webhook.RouteRegistrar{transferHandler, &auditlog.Handler{DB: db, Log: log}}}, closeConns, nil
}

// serveHTTP runs the webhook and operator server until ctx ends.
func serveHTTP(ctx context.Context, addr string, h http.Handler, log *slog.Logger) error {
	log.Info("payments-worker http listening", "addr", addr)
	return health.ServeServer(ctx, &http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second})
}

// newEInvoiceRegistry registers the e-invoice adapters: the mock, Nilvera and the generic webhook.
func newEInvoiceRegistry(cfg *Config) *einvoice.Registry {
	r := einvoice.NewRegistry()
	mock.Register(r)
	nilvera.RegisterWithOptions(r, nilvera.Options{AllowPrivate: cfg.EInvoiceAllowPrivateURLs})
	einvoicewebhook.RegisterWithOptions(r, einvoicewebhook.Options{AllowPrivate: cfg.EInvoiceAllowPrivateURLs})
	return r
}

// wireEInvoice gives the command handler its e-invoice side. Sandbox documents need only the database (the mock); live ones also need the sealed accounts
// (PAYRUDDER_SECRETS_KEY); files need object storage.
func wireEInvoice(cfg *Config, db store.DB, box *secretbox.Box, h *handler.Handler, registry *einvoice.Registry, log *slog.Logger) error {
	ew := &einvoiceworker.Handler{Registry: registry, Issuances: store.EInvoiceIssuances{DB: db}, Log: log, Demo: cfg.Demo}
	if box != nil {
		ew.Accounts = store.EInvoiceAccounts{DB: db, Box: box}
	}
	if cfg.S3AccessKey != "" {
		bs, err := blob.New(blob.Config{Endpoint: cfg.S3Endpoint, Region: cfg.S3Region, Bucket: cfg.S3Bucket, AccessKey: cfg.S3AccessKey, SecretKey: cfg.S3SecretKey, ForcePathStyle: cfg.S3ForcePath})
		if err != nil {
			return err
		}
		ew.Blobs = bs
	} else {
		log.Warn("S3_ACCESS_KEY is not set: e-invoices are issued without stored UBL/PDF files")
	}
	ew.Tenants = h.Tenants
	h.EInvoice = ew
	return nil
}

// tenantSchemas is what a tenant's schema in a cell is built from (ADR 0018).
func tenantSchemas() pgxx.TenantSchemas {
	return pgxx.TenantSchemas{Migrations: migrations.Tenant(), Roles: []string{"payments_app", "payments_system"}}
}
