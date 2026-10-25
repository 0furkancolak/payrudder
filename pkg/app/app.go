// Package app is the start-up and shutdown every Payrudder process shares: load the configuration, build the logger, listen
// for SIGINT and SIGTERM, set up OpenTelemetry and the health registry, run the service, and on the way out flush the
// telemetry. A service's main.go keeps only what is its own.
//
//	func main() { app.Main("aggregator", defaults, start) }
//
//	func start(ctx context.Context, cfg *Config, rt *app.Runtime) error { ... }
//
// The service wires its dependencies, then runs its workers in a Group, which also serves the health endpoint and, for a
// gRPC service, drains the server in the right order on shutdown.
package app

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

	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	grpchealth "google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	"github.com/0furkancolak/payrudder/pkg/config"
	"github.com/0furkancolak/payrudder/pkg/grpcx"
	"github.com/0furkancolak/payrudder/pkg/health"
	"github.com/0furkancolak/payrudder/pkg/logx"
	"github.com/0furkancolak/payrudder/pkg/otelx"
	"github.com/0furkancolak/payrudder/pkg/tenant"
)

const (
	// healthCheckTimeout bounds one readiness check.
	healthCheckTimeout = 2 * time.Second
	// telemetryFlush bounds the flush of traces and metrics at exit.
	telemetryFlush = 5 * time.Second
)

// Runtime is what a service gets once the common start-up is done.
type Runtime struct {
	// Log is the service's logger; it is also slog's default.
	Log *slog.Logger
	// Registry is the Prometheus registry served on the metrics address.
	Registry *prometheus.Registry

	hh *health.Handler
}

// Health is the handler of /healthz, /readyz and /metrics; the service adds its readiness checks to it. The same handler
// is returned on every call.
func (rt *Runtime) Health() *health.Handler {
	if rt.hh == nil {
		rt.hh = health.New(rt.Registry, healthCheckTimeout)
	}
	return rt.hh
}

// Configured is implemented by the config structs that embed config.Base.
type Configured[C any] interface {
	*C
	Common() *config.Base
}

// Main runs a service and exits the process with status 1 and "<service>: <error>" on stderr when it fails.
func Main[C any, P Configured[C]](service string, defaults []byte, run func(ctx context.Context, cfg P, rt *Runtime) error) {
	if err := Run[C, P](defaults, run); err != nil {
		fmt.Fprintln(os.Stderr, service+":", err)
		os.Exit(1)
	}
}

// Run is Main without the exit: it loads the configuration (the service's embedded defaults over the shared ones, then the
// environment), builds the logger, installs the signal handler and OpenTelemetry, calls run, and flushes telemetry on return.
func Run[C any, P Configured[C]](defaults []byte, run func(ctx context.Context, cfg P, rt *Runtime) error) error {
	loaded, err := config.Load[C](defaults)
	if err != nil {
		return err
	}
	cfg := P(&loaded)
	base := cfg.Common()
	log, err := logx.New(logx.Options{Service: base.ServiceName, Version: base.Version, Level: base.LogLevel, Writer: os.Stdout})
	if err != nil {
		return err
	}
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	reg := health.NewRegistry()
	providers, err := otelx.Setup(ctx, otelx.Options{
		ServiceName: base.ServiceName, Version: base.Version, Environment: string(base.Env), OTLPEndpoint: base.OTLPEndpoint, Registry: reg,
	})
	if err != nil {
		return err
	}
	defer func() {
		c, cancel := context.WithTimeout(context.Background(), telemetryFlush)
		defer cancel()
		_ = providers.Shutdown(c)
	}()

	return run(ctx, cfg, &Runtime{Log: log, Registry: reg})
}

// NewGRPCServer builds a service's gRPC server: tracing, panic recovery, the health service, the tenant interceptors that
// put the caller's tenant on the context, and reflection in the local environment. The returned health server starts
// SERVING; Group.ServeGRPC flips it on shutdown.
func NewGRPCServer(log *slog.Logger, cfg *config.Base) (*grpc.Server, *grpchealth.Server) {
	return grpcx.NewServer(grpcx.ServerOptions{
		Logger: log, Reflection: cfg.Env == config.EnvLocal,
		Extra: []grpc.ServerOption{
			grpc.ChainUnaryInterceptor(tenant.UnaryServerInterceptor()),
			grpc.ChainStreamInterceptor(tenant.StreamServerInterceptor()),
		},
	})
}

// Group runs a service's workers together with its health endpoint: the first to fail, or the end of the parent context,
// stops them all.
type Group struct {
	*errgroup.Group
	ctx context.Context
	log *slog.Logger
}

// NewGroup returns a Group whose first member serves the health endpoint on metricsAddr.
func NewGroup(ctx context.Context, log *slog.Logger, metricsAddr string, hh *health.Handler) *Group {
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return health.Serve(gctx, metricsAddr, hh) })
	return &Group{Group: g, ctx: gctx, log: log}
}

// Context is cancelled when a member fails or the parent is cancelled. Workers run on it.
func (g *Group) Context() context.Context { return g.ctx }

// ServeGRPC serves srv on lis and, when the group stops, marks the service NOT_SERVING before draining the server, so a
// load balancer stops sending first and in-flight calls finish.
func (g *Group) ServeGRPC(srv *grpc.Server, hs *grpchealth.Server, lis net.Listener) {
	g.Go(func() error { return srv.Serve(lis) })
	g.Go(func() error {
		<-g.ctx.Done()
		hs.SetServingStatus("", healthpb.HealthCheckResponse_NOT_SERVING)
		srv.GracefulStop()
		return nil
	})
}

// Wait blocks until every member has returned. A cancelled context is a normal stop; any other error is the first one a
// member returned.
func (g *Group) Wait() error {
	if err := g.Group.Wait(); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	g.log.Info("stopped")
	return nil
}
