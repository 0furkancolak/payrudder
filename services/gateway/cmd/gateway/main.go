// Command gateway is Quotify's single public entry point: HTTP/JSON in, gRPC out.
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

	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"

	ingestv1 "github.com/0furkancolak/quotify/gen/go/quotify/ingest/v1"
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

type Config struct {
	config.Base
	HTTPAddr      string   `env:"QUOTIFY_HTTP_ADDR" envDefault:":8000"`
	PlansAddr     string   `env:"QUOTIFY_PLANS_ADDR" envDefault:"dns:///localhost:7001"`
	IngestAddr    string   `env:"QUOTIFY_INGEST_ADDR" envDefault:"dns:///localhost:7002"`
	RedisURL      string   `env:"QUOTIFY_REDIS_URL" envDefault:"redis://localhost:6379/0"`
	OperatorToken string   `env:"QUOTIFY_OPERATOR_TOKEN,required"`
	Replicas      int      `env:"QUOTIFY_GATEWAY_REPLICAS" envDefault:"1"`
	AllowedEnvs   []string `env:"QUOTIFY_ALLOWED_ENVIRONMENTS" envSeparator:"," envDefault:"sandbox,live"`
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

	opts, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		return fmt.Errorf("QUOTIFY_REDIS_URL: %w", err)
	}
	rdb := redis.NewClient(opts)
	defer func() { _ = rdb.Close() }()

	plansConn, err := grpcx.NewClient(cfg.PlansAddr,
		grpc.WithChainUnaryInterceptor(tenant.UnaryClientInterceptor()),
		grpc.WithChainStreamInterceptor(tenant.StreamClientInterceptor()))
	if err != nil {
		return fmt.Errorf("plans-svc client: %w", err)
	}
	defer func() { _ = plansConn.Close() }()
	adminClient := plansv1.NewAdminServiceClient(plansConn)

	ingestConn, err := grpcx.NewClient(cfg.IngestAddr,
		grpc.WithChainUnaryInterceptor(tenant.UnaryClientInterceptor()))
	if err != nil {
		return fmt.Errorf("ingest-svc client: %w", err)
	}
	defer func() { _ = ingestConn.Close() }()

	authn, err := auth.New(auth.NewRedisCache(rdb), auth.AdminResolver{Client: adminClient}, cfg.AllowedEnvs, log)
	if err != nil {
		return err
	}
	limiter, err := ratelimit.NewLimiter(rdb, cfg.Replicas, log)
	if err != nil {
		return err
	}
	api := httpapi.New(httpapi.Deps{
		Plans: plansv1.NewPlansServiceClient(plansConn), Admin: adminClient, Ingest: ingestv1.NewIngestServiceClient(ingestConn),
		Auth: authn, Limiter: limiter, OperatorToken: cfg.OperatorToken, Log: log,
	})

	hh := health.New(reg, 2*time.Second)
	// Readiness doesn't include Redis: the gateway serves (fail-open limits, plans-svc lookups) without it.
	hh.AddCheck("plans-svc", func(ctx context.Context) error {
		_, err := adminClient.ResolveApiKey(ctx, &plansv1.ResolveApiKeyRequest{Prefix: "00000000"})
		if err != nil && grpcCodeIsUp(err) {
			return nil
		}
		return err
	})

	srv := &http.Server{Addr: cfg.HTTPAddr, Handler: api, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second,
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
