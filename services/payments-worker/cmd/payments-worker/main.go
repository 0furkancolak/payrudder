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
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/app"
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
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/rabbitx"
	"github.com/0furkancolak/payrudder/pkg/secretbox"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/pkg/workerx"
	einvoiceworker "github.com/0furkancolak/payrudder/services/payments-worker/internal/einvoice"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/handler"
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
	// SecretsKey (base64, 32 bytes) seals the credentials of tenants' provider accounts. Without it provider accounts, and with them
	// provider webhooks, are disabled.
	SecretsKey string `env:"PAYRUDDER_SECRETS_KEY"`
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
}

func (c *Config) Validate() error {
	errs := []error{c.Base.Validate()}
	errs = append(errs, c.CheckMigration())
	if c.SecretsKey != "" {
		if _, err := secretbox.ParseKey(c.SecretsKey); err != nil {
			errs = append(errs, fmt.Errorf("PAYRUDDER_SECRETS_KEY: %w", err))
		}
	}
	if c.TransferWindowDays < 1 || c.TransferWindowDays > banktransfer.MaxPaymentWindowDays {
		errs = append(errs, fmt.Errorf("PAYMENTS_TRANSFER_WINDOW_DAYS must be 1 to %d", banktransfer.MaxPaymentWindowDays))
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
	if done, err := cfg.MigrateIfAsked(ctx, migrations.FS, cfg.MigrateOnly, log); err != nil || done {
		return err
	}
	pool, err := pgxx.NewPool(ctx, cfg.PGSystemDSN)
	if err != nil {
		return err
	}
	defer pool.Close()

	h := newHandler(cfg, pool, log)
	dir, closeDir, err := tenantDirectory(cfg)
	if err != nil {
		return err
	}
	defer closeDir()
	h.Tenants = dir
	registry := payments.NewRegistry()
	iyzico.Register(registry)
	paddle.Register(registry)
	einvoices := newEInvoiceRegistry(cfg)
	box, err := newBox(cfg.SecretsKey)
	if err != nil {
		return err
	}
	if err := wireEInvoice(cfg, pool, box, h, einvoices, log); err != nil {
		return err
	}
	banktransfer.Register(registry)
	var httpHandler http.Handler
	if box != nil {
		var closeConns func()
		if httpHandler, closeConns, err = webhookHandler(*cfg, pool, box, h, registry, einvoices, log); err != nil {
			return err
		}
		defer closeConns()
	}
	w := &workerx.Worker{
		URL: cfg.RabbitMQURL, Prefetch: cfg.Prefetch, Handler: h, Inbox: h.Attempts, Log: log,
		Queues: []string{rabbitx.QueuePaymentCharge, rabbitx.QueuePaymentRefund, rabbitx.QueuePaymentStatus, rabbitx.QueueEInvoiceIssue},
	}

	hh.AddCheck("postgres", pool.Ping)
	hh.AddCheck("rabbitmq", func(context.Context) error { return w.Ping() })
	return serve(ctx, cfg, log, hh, w, httpHandler)
}

// newBox opens the box that seals the stored provider and e-invoice credentials. Without a key (PAYRUDDER_SECRETS_KEY) the features that need it are off,
// and the box is nil.
func newBox(secretsKey string) (*secretbox.Box, error) {
	if secretsKey == "" {
		return nil, nil
	}
	key, err := secretbox.ParseKey(secretsKey)
	if err != nil {
		return nil, fmt.Errorf("PAYRUDDER_SECRETS_KEY: %w", err)
	}
	return secretbox.New(key)
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
func newHandler(cfg *Config, pool *pgxpool.Pool, log *slog.Logger) *handler.Handler {
	mock := payments.NewMockPSP(cfg.MockPSPURL, cfg.PSPTimeout)
	h := &handler.Handler{Sandbox: mock, Attempts: store.Attempts{DB: pool}, Ledger: store.Ledger{DB: pool}, Log: log, Demo: cfg.Demo, AllowMockLive: cfg.AllowMockLive,
		Transfers: store.Transfers{DB: pool}, TransferWindow: time.Duration(cfg.TransferWindowDays) * 24 * time.Hour}
	if cfg.LiveProvider == "mockpsp" {
		h.Live = mock
	}
	return h
}

// serve runs the metrics server, the optional webhook/operator HTTP server and the queue worker until ctx is cancelled or one of them fails.
func serve(ctx context.Context, cfg *Config, log *slog.Logger, hh *health.Handler, w *workerx.Worker, httpHandler http.Handler) error {
	g := app.NewGroup(ctx, log, cfg.MetricsAddr, hh)
	gctx := g.Context()
	if httpHandler != nil {
		g.Go(func() error { return serveHTTP(gctx, cfg.HTTPAddr, httpHandler, log) })
	} else {
		log.Warn("PAYRUDDER_SECRETS_KEY is not set: provider accounts and provider webhooks are disabled")
	}
	g.Go(func() error {
		log.Info("payments-worker consuming", "live_provider", cfg.LiveProvider)
		return w.Run(gctx)
	})
	return g.Wait()
}

// webhookHandler builds the provider-account, webhook and operator server. The returned func closes its gRPC connections.
func webhookHandler(cfg Config, pool *pgxpool.Pool, box *secretbox.Box, h *handler.Handler, registry *payments.Registry, einvoices *einvoice.Registry, log *slog.Logger) (http.Handler, func(), error) {
	accounts := store.Accounts{DB: pool, Box: box}
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
	}
	var billing webhook.BillingAPI
	var transferBilling transfercommand.Billing
	if cfg.BillingAddr != "" {
		conn, err := dial(cfg.BillingAddr, "billing-svc")
		if err != nil {
			return nil, nil, err
		}
		billing = webhook.BillingClient{Client: billingv1.NewBillingServiceClient(conn)}
		transferBilling = transfers.BillingClient{Invoices: billingv1.NewBillingServiceClient(conn), Admin: billingv1.NewBillingAdminServiceClient(conn)}
	}
	transferHandler := &transfers.Handler{Log: log, Deps: transfercommand.Deps{Billing: transferBilling, Deps: transferquery.Deps{
		Transfers: h.Transfers, Accounts: h.Resolver, DefaultWindow: h.TransferWindow}}}
	return (&webhook.Server{Plans: plansSync, Billing: billing, Registry: registry, Resolver: h.Resolver, Accounts: accounts, Events: store.Events{DB: pool},
		Attempts: h.Attempts, Ledger: h.Ledger, Subscriptions: store.Subscriptions{DB: pool}, Objects: store.Objects{DB: pool}, Log: log, Token: cfg.InternalToken,
		EInvoiceAccounts: store.EInvoiceAccounts{DB: pool, Box: box}, EInvoiceRegistry: einvoices, AllowMockLive: cfg.Demo,
		Reset:   sandboxreset.New(pool).Reset,
		Modules: []webhook.RouteRegistrar{transferHandler}}).Handler(), closeConns, nil
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
func wireEInvoice(cfg *Config, pool *pgxpool.Pool, box *secretbox.Box, h *handler.Handler, registry *einvoice.Registry, log *slog.Logger) error {
	ew := &einvoiceworker.Handler{Registry: registry, Issuances: store.EInvoiceIssuances{DB: pool}, Log: log, Demo: cfg.Demo}
	if box != nil {
		ew.Accounts = store.EInvoiceAccounts{DB: pool, Box: box}
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
