package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	"github.com/0furkancolak/payrudder/pkg/config"
	"github.com/0furkancolak/payrudder/pkg/health"
)

type testConfig struct {
	config.Base
	Extra string `env:"APPTEST_EXTRA"`
}

func TestRunLoadsTheConfigAndHandsOverTheRuntime(t *testing.T) {
	t.Setenv("PAYRUDDER_SERVICE_NAME", "apptest")
	t.Setenv("APPTEST_EXTRA", "x")
	t.Setenv("PAYRUDDER_OTLP_ENDPOINT", "")
	boom := errors.New("boom")
	var gotHealth *health.Handler
	err := Run[testConfig](nil, func(ctx context.Context, cfg *testConfig, rt *Runtime) error {
		if cfg.ServiceName != "apptest" || cfg.Extra != "x" || rt.Log == nil || rt.Registry == nil || ctx.Err() != nil {
			t.Errorf("run got cfg %+v, runtime %+v", cfg, rt)
		}
		gotHealth = rt.Health()
		if rt.Health() != gotHealth {
			t.Error("Health must return the same handler every time")
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the run function's error", err)
	}
}

func TestRunRefusesAnInvalidConfig(t *testing.T) {
	t.Setenv("PAYRUDDER_SERVICE_NAME", "")
	called := false
	err := Run[testConfig](nil, func(context.Context, *testConfig, *Runtime) error { called = true; return nil })
	if err == nil || called {
		t.Fatalf("err = %v, called = %v: a config without a service name must not start", err, called)
	}
}

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().String()
}

func TestGroupStopsEveryoneWhenAMemberFails(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	hh := health.New(health.NewRegistry(), time.Second)
	g := NewGroup(context.Background(), log, freeAddr(t), hh)
	stopped := make(chan struct{})
	g.Go(func() error { <-g.Context().Done(); close(stopped); return g.Context().Err() })
	boom := errors.New("worker failed")
	g.Go(func() error { return boom })
	if err := g.Wait(); !errors.Is(err, boom) {
		t.Fatalf("Wait = %v, want the first failure", err)
	}
	select {
	case <-stopped:
	default:
		t.Fatal("the other worker was not stopped")
	}
}

func TestGroupDrainsGRPCOnCancel(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv, hs := NewGRPCServer(log, &config.Base{Env: config.EnvCI})
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	g := NewGroup(ctx, log, freeAddr(t), health.New(health.NewRegistry(), time.Second))
	g.ServeGRPC(srv, hs, lis)

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecureCreds()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	hc := healthpb.NewHealthClient(conn)
	deadline := time.Now().Add(5 * time.Second)
	for {
		resp, err := hc.Check(context.Background(), &healthpb.HealthCheckRequest{})
		if err == nil && resp.GetStatus() == healthpb.HealthCheckResponse_SERVING {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the server never became SERVING: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	done := make(chan error, 1)
	go func() { done <- g.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Wait after cancel = %v, want a clean stop", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the group did not stop after the context was cancelled")
	}
}
