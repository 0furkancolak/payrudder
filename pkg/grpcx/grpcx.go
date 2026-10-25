// Package grpcx builds gRPC servers and clients with Payrudder's defaults: OpenTelemetry stats handlers,
// panic recovery, a default client deadline, the standard health service and (locally) reflection.
package grpcx

import (
	"context"
	"log/slog"
	"runtime/debug"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/resolver/dns"
	"google.golang.org/grpc/status"
)

// DefaultClientTimeout applies to unary calls whose context has no deadline.
const DefaultClientTimeout = 5 * time.Second

// ServerOptions configures NewServer.
type ServerOptions struct {
	Logger     *slog.Logger
	Reflection bool // enable only in local environments
	Extra      []grpc.ServerOption
}

// NewServer returns a server with tracing, recovery and the health service registered.
// The returned health server starts as SERVING for the empty service name. Services flip it on shutdown.
func NewServer(o ServerOptions) (*grpc.Server, *health.Server) {
	log := o.Logger
	if log == nil {
		log = slog.Default()
	}
	opts := append([]grpc.ServerOption{
		grpc.StatsHandler(otelgrpc.NewServerHandler()),
		grpc.ChainUnaryInterceptor(unaryRecovery(log)),
		grpc.ChainStreamInterceptor(streamRecovery(log)),
	}, o.Extra...)
	srv := grpc.NewServer(opts...)

	hs := health.NewServer()
	healthpb.RegisterHealthServer(srv, hs)
	if o.Reflection {
		reflection.Register(srv)
	}
	return srv, hs
}

func recovered(ctx context.Context, log *slog.Logger, method string, p any) error {
	log.ErrorContext(ctx, "grpc handler panic", "method", method, "panic", p, "stack", string(debug.Stack()))
	return status.Error(codes.Internal, "internal error")
}

func unaryRecovery(log *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		defer func() {
			if p := recover(); p != nil {
				err = recovered(ctx, log, info.FullMethod, p)
			}
		}()
		return handler(ctx, req)
	}
}

func streamRecovery(log *slog.Logger) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) (err error) {
		defer func() {
			if p := recover(); p != nil {
				err = recovered(ss.Context(), log, info.FullMethod, p)
			}
		}()
		return handler(srv, ss)
	}
}

func init() {
	// grpc-go's DNS resolver re-resolves at most every 30 s after a connection fails. Behind a headless Kubernetes Service that
	// leaves a client talking to pods a rollout has replaced for half a minute; two seconds bounds it by the DNS cache instead.
	dns.SetMinResolutionInterval(2 * time.Second)
}

// NewClient creates a client connection with tracing, round-robin balancing and DefaultClientTimeout.
// Transport is plaintext: in-cluster mTLS is out of scope (NetworkPolicies restrict callers).
func NewClient(target string, extra ...grpc.DialOption) (*grpc.ClientConn, error) {
	opts := append([]grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
		grpc.WithChainUnaryInterceptor(defaultDeadline(DefaultClientTimeout)),
		grpc.WithDefaultServiceConfig(`{"loadBalancingConfig":[{"round_robin":{}}]}`),
	}, extra...)
	return grpc.NewClient(target, opts...)
}

func defaultDeadline(d time.Duration) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		if _, ok := ctx.Deadline(); !ok {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, d)
			defer cancel()
		}
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}
