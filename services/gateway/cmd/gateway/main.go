// Command gateway is Quotify's single public entry point: HTTP/JSON in, gRPC out.
package main

import (
	"context"
	_ "embed" // config.default.yaml
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"

	aggregatorv1 "github.com/0furkancolak/quotify/gen/go/quotify/aggregator/v1"
	analyticsv1 "github.com/0furkancolak/quotify/gen/go/quotify/analytics/v1"
	assetsv1 "github.com/0furkancolak/quotify/gen/go/quotify/assets/v1"
	billingv1 "github.com/0furkancolak/quotify/gen/go/quotify/billing/v1"
	ingestv1 "github.com/0furkancolak/quotify/gen/go/quotify/ingest/v1"
	limitsv1 "github.com/0furkancolak/quotify/gen/go/quotify/limits/v1"
	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/config"
	"github.com/0furkancolak/quotify/pkg/grpcx"
	"github.com/0furkancolak/quotify/pkg/health"
	"github.com/0furkancolak/quotify/pkg/logx"
	"github.com/0furkancolak/quotify/pkg/otelx"
	"github.com/0furkancolak/quotify/pkg/tenant"
	"github.com/0furkancolak/quotify/services/gateway/internal/auth"
	"github.com/0furkancolak/quotify/services/gateway/internal/httpapi"
	"github.com/0furkancolak/quotify/services/gateway/internal/ratelimit"
)

//go:embed config.default.yaml
var defaults []byte

type Config struct {
	config.Base
	HTTPAddr       string `env:"QUOTIFY_HTTP_ADDR"`
	PlansAddr      string `env:"QUOTIFY_PLANS_ADDR"`
	IngestAddr     string `env:"QUOTIFY_INGEST_ADDR"`
	AggregatorAddr string `env:"QUOTIFY_AGGREGATOR_ADDR"`
	LimitsAddr     string `env:"QUOTIFY_LIMITS_ADDR"`
	BillingAddr    string `env:"QUOTIFY_BILLING_ADDR"`
	AnalyticsAddr  string `env:"QUOTIFY_ANALYTICS_ADDR"`
	AssetsAddr     string `env:"QUOTIFY_ASSETS_ADDR"`
	RedisURL       string `env:"QUOTIFY_REDIS_URL"`
	OperatorToken  string `env:"QUOTIFY_OPERATOR_TOKEN,required"`
	// PaymentsURL is payments-worker's HTTP address; empty disables provider webhooks and provider accounts.
	PaymentsURL   string   `env:"QUOTIFY_PAYMENTS_URL"`
	PaymentsToken string   `env:"QUOTIFY_PAYMENTS_INTERNAL_TOKEN"`
	Replicas      int      `env:"QUOTIFY_GATEWAY_REPLICAS"`
	AllowedEnvs   []string `env:"QUOTIFY_ALLOWED_ENVIRONMENTS" envSeparator:","`
	// PublicURL is the address customers reach the gateway at, for the invoice links it hands out (https://api.example.com). Empty: relative links.
	PublicURL string `env:"QUOTIFY_PUBLIC_URL"`
	// TrustForwardedFor counts the public sign-in limits by X-Forwarded-For, for requests that come from a private address (the console's nginx).
	TrustForwardedFor bool `env:"QUOTIFY_TRUST_FORWARDED_FOR"`
}

func (c *Config) Validate() error {
	errs := []error{c.Base.Validate()}
	if len(c.OperatorToken) < 32 {
		errs = append(errs, errors.New("QUOTIFY_OPERATOR_TOKEN must be at least 32 characters"))
	}
	if c.Replicas < 1 {
		errs = append(errs, errors.New("QUOTIFY_GATEWAY_REPLICAS must be ≥ 1"))
	}
	return errors.Join(errs...)
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "gateway:", err)
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

	opts, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		return fmt.Errorf("QUOTIFY_REDIS_URL: %w", err)
	}
	rdb := redis.NewClient(opts)
	defer func() { _ = rdb.Close() }()

	up, err := dialUpstreams(&cfg)
	if err != nil {
		return err
	}
	defer up.close()

	api, err := newAPI(&cfg, log, rdb, up)
	if err != nil {
		return err
	}
	hh := health.New(reg, 2*time.Second)
	addReadinessChecks(hh, up)
	return serve(ctx, &cfg, log, api, hh)
}

// upstreams are the gateway's gRPC connections, one per backend service.
type upstreams struct {
	plans, ingest, aggregator, limits, billing, analytics, assets *grpc.ClientConn
}

// dialUpstreams connects to every backend; on failure it closes the connections already made.
func dialUpstreams(cfg *Config) (u *upstreams, err error) {
	u = &upstreams{}
	defer func() {
		if err != nil {
			u.close()
		}
	}()
	unary := grpc.WithChainUnaryInterceptor(tenant.UnaryClientInterceptor())
	stream := grpc.WithChainStreamInterceptor(tenant.StreamClientInterceptor())
	targets := []struct {
		dst  **grpc.ClientConn
		name string
		addr string
		opts []grpc.DialOption
	}{
		{&u.plans, "plans-svc", cfg.PlansAddr, []grpc.DialOption{unary, stream}},
		{&u.ingest, "ingest-svc", cfg.IngestAddr, []grpc.DialOption{unary}},
		{&u.aggregator, "aggregator", cfg.AggregatorAddr, []grpc.DialOption{unary}},
		{&u.limits, "limits-svc", cfg.LimitsAddr, []grpc.DialOption{unary}},
		{&u.billing, "billing-svc", cfg.BillingAddr, []grpc.DialOption{unary}},
		{&u.analytics, "analytics-svc", cfg.AnalyticsAddr, []grpc.DialOption{unary}},
		{&u.assets, "assets-svc", cfg.AssetsAddr, []grpc.DialOption{unary, stream}},
	}
	for _, t := range targets {
		conn, dialErr := grpcx.NewClient(t.addr, t.opts...)
		if dialErr != nil {
			return u, fmt.Errorf("%s client: %w", t.name, dialErr)
		}
		*t.dst = conn
	}
	return u, nil
}

// close closes the connections that were opened, in reverse dial order.
func (u *upstreams) close() {
	for _, c := range []*grpc.ClientConn{u.assets, u.analytics, u.billing, u.limits, u.aggregator, u.ingest, u.plans} {
		if c != nil {
			_ = c.Close()
		}
	}
}

// newAPI wires auth, the rate limiter and the backend clients into the HTTP handler.
func newAPI(cfg *Config, log *slog.Logger, rdb *redis.Client, up *upstreams) (*httpapi.Server, error) {
	adminClient := plansv1.NewAdminServiceClient(up.plans)
	authn, err := auth.New(auth.NewRedisCache(rdb), auth.AdminResolver{Client: adminClient}, cfg.AllowedEnvs, log)
	if err != nil {
		return nil, err
	}
	limiter, err := ratelimit.NewLimiter(rdb, cfg.Replicas, log)
	if err != nil {
		return nil, err
	}
	var paymentsURL *url.URL
	if cfg.PaymentsURL != "" {
		if paymentsURL, err = url.Parse(cfg.PaymentsURL); err != nil {
			return nil, fmt.Errorf("QUOTIFY_PAYMENTS_URL: %w", err)
		}
	}
	return httpapi.New(httpapi.Deps{
		Plans: plansv1.NewPlansServiceClient(up.plans), Admin: adminClient, Webhooks: plansv1.NewWebhookServiceClient(up.plans), Ingest: ingestv1.NewIngestServiceClient(up.ingest),
		Usage: aggregatorv1.NewUsageQueryServiceClient(up.aggregator), Replay: aggregatorv1.NewReplayServiceClient(up.aggregator), Limits: limitsv1.NewLimitsServiceClient(up.limits),
		Billing: billingv1.NewBillingServiceClient(up.billing), Sandbox: billingv1.NewSandboxServiceClient(up.billing),
		BillingAdmin: billingv1.NewBillingAdminServiceClient(up.billing), Hosted: billingv1.NewHostedInvoiceServiceClient(up.billing), PublicURL: strings.TrimRight(cfg.PublicURL, "/"), Analytics: analyticsv1.NewAnalyticsServiceClient(up.analytics),
		Assets: assetsv1.NewAssetsServiceClient(up.assets),
		Auth:   authn, Limiter: limiter, OperatorToken: cfg.OperatorToken, Log: log,
		PaymentsURL: paymentsURL, PaymentsToken: cfg.PaymentsToken, Demo: cfg.Demo,
		Identity: plansv1.NewIdentityServiceClient(up.plans), TrustForwardedFor: cfg.TrustForwardedFor,
	}), nil
}

// addReadinessChecks registers what readiness depends on.
// Readiness doesn't include Redis: the gateway serves (fail-open limits, plans-svc lookups) without it.
func addReadinessChecks(hh *health.Handler, up *upstreams) {
	adminClient := plansv1.NewAdminServiceClient(up.plans)
	hh.AddCheck("plans-svc", func(ctx context.Context) error {
		_, err := adminClient.ResolveApiKey(ctx, &plansv1.ResolveApiKeyRequest{Prefix: "00000000"})
		if err != nil && grpcCodeIsUp(err) {
			return nil
		}
		return err
	})
}

// serve runs the HTTP and metrics servers until ctx is cancelled or one of them fails, then shuts down gracefully.
func serve(ctx context.Context, cfg *Config, log *slog.Logger, api http.Handler, hh *health.Handler) error {
	srv := &http.Server{Addr: cfg.HTTPAddr, Handler: withLiveness(api, hh), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second,
		WriteTimeout: 30 * time.Second, IdleTimeout: 120 * time.Second}
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return health.Serve(gctx, cfg.MetricsAddr, hh) })
	g.Go(func() error {
		log.Info("http listening", "addr", cfg.HTTPAddr, "plans", cfg.PlansAddr, "metrics", cfg.MetricsAddr)
		if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	})
	g.Go(func() error {
		<-gctx.Done()
		c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(c)
	})
	if err := g.Wait(); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	log.Info("stopped")
	return nil
}
