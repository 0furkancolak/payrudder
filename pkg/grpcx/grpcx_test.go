package grpcx_test

import (
	"context"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/0furkancolak/payrudder/pkg/grpcx"
	"github.com/0furkancolak/payrudder/pkg/tenant"
)

func TestNewTenantClientForwardsTenantMetadata(t *testing.T) {
	var got []string
	capture := grpc.UnaryInterceptor(func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, h grpc.UnaryHandler) (any, error) {
		md, _ := metadata.FromIncomingContext(ctx)
		got = md.Get(tenant.MDOperator)
		return h(ctx, req)
	})
	srv, _ := grpcx.NewServer(grpcx.ServerOptions{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Extra: []grpc.ServerOption{capture}})
	lis := bufconn.Listen(1 << 20)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	conn, err := grpcx.NewTenantClient("peer", "passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if _, err := healthpb.NewHealthClient(conn).Check(tenant.WithOperator(context.Background()), &healthpb.HealthCheckRequest{}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "true" {
		t.Fatalf("operator metadata = %v, want [true]", got)
	}
}

// panicHealth panics in Check to exercise the recovery interceptor.
type panicHealth struct {
	healthpb.UnimplementedHealthServer
}

func (panicHealth) Check(context.Context, *healthpb.HealthCheckRequest) (*healthpb.HealthCheckResponse, error) {
	panic("boom")
}

func dial(t *testing.T, srv *grpc.Server) *grpc.ClientConn {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	conn, err := grpcx.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func TestHealthServing(t *testing.T) {
	srv, _ := grpcx.NewServer(grpcx.ServerOptions{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	conn := dial(t, srv)
	resp, err := healthpb.NewHealthClient(conn).Check(context.Background(), &healthpb.HealthCheckRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetStatus() != healthpb.HealthCheckResponse_SERVING {
		t.Fatalf("status = %v", resp.GetStatus())
	}
}

func TestRecoveryReturnsInternal(t *testing.T) {
	// Register a panicking service under a different name than the built-in health service.
	s, _ := grpcx.NewServer(grpcx.ServerOptions{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	desc := healthpb.Health_ServiceDesc
	desc.ServiceName = "test.Panicky"
	s.RegisterService(&desc, panicHealth{})
	conn := dial(t, s)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := conn.Invoke(ctx, "/test.Panicky/Check", &healthpb.HealthCheckRequest{}, &healthpb.HealthCheckResponse{})
	if status.Code(err) != codes.Internal {
		t.Fatalf("code = %v (err %v), want Internal", status.Code(err), err)
	}
}
