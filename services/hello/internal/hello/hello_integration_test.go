//go:build integration

package hello

import (
	"context"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/twmb/franz-go/pkg/kgo"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/test/bufconn"

	hellov1 "github.com/0furkancolak/quotify/gen/go/quotify/hello/v1"
	"github.com/0furkancolak/quotify/pkg/grpcx"
	"github.com/0furkancolak/quotify/pkg/otelx"
	"github.com/0furkancolak/quotify/pkg/testkit"
)

// TestRoundTrip proves the skeleton: testkit containers, gRPC server with tracing, Kafka produce with trace
// headers, and a consumer span in the same trace as the caller.
func TestRoundTrip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	pg := testkit.StartPostgres(t)
	conn, err := pgx.Connect(ctx, pg.DSN)
	if err != nil {
		t.Fatalf("postgres: %v", err)
	}
	if err := conn.Ping(ctx); err != nil {
		t.Fatalf("postgres ping: %v", err)
	}
	_ = conn.Close(ctx)

	brokers := testkit.StartRedpanda(t)

	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(otelx.Propagator())
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	const topic = "hello.events.v1"
	producer, err := kgo.NewClient(kgo.SeedBrokers(brokers), kgo.AllowAutoTopicCreation())
	if err != nil {
		t.Fatal(err)
	}
	defer producer.Close()
	consumerClient, err := kgo.NewClient(kgo.SeedBrokers(brokers), kgo.ConsumerGroup("hello-it"),
		kgo.ConsumeTopics(topic), kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()), kgo.AllowAutoTopicCreation())
	if err != nil {
		t.Fatal(err)
	}
	defer consumerClient.Close()

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	type consumed struct {
		ev *hellov1.HelloEvent
		sc trace.SpanContext
	}
	got := make(chan consumed, 1)
	cons := NewConsumer(consumerClient, func(ctx context.Context, ev *hellov1.HelloEvent) error {
		got <- consumed{ev: ev, sc: trace.SpanContextFromContext(ctx)}
		return nil
	}, log)
	consCtx, stopCons := context.WithCancel(ctx)
	defer stopCons()
	go func() { _ = cons.Run(consCtx) }()

	srv, _ := grpcx.NewServer(grpcx.ServerOptions{Logger: log})
	hellov1.RegisterHelloServiceServer(srv, NewServer(producer, topic))
	lis := bufconn.Listen(1 << 20)
	go func() { _ = srv.Serve(lis) }()
	defer srv.Stop()
	cc, err := grpcx.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cc.Close() }()

	callCtx, root := tp.Tracer("test").Start(ctx, "test root")
	resp, err := hellov1.NewHelloServiceClient(cc).SayHello(callCtx, &hellov1.SayHelloRequest{Name: "ada"})
	root.End()
	if err != nil {
		t.Fatalf("SayHello: %v", err)
	}

	select {
	case c := <-got:
		if c.ev.GetName() != "ada" {
			t.Fatalf("consumed name = %q", c.ev.GetName())
		}
		if c.sc.TraceID() != root.SpanContext().TraceID() {
			t.Fatalf("consumer trace %s != caller trace %s", c.sc.TraceID(), root.SpanContext().TraceID())
		}
	case <-ctx.Done():
		t.Fatalf("event at offset %d never consumed", resp.GetOffset())
	}

	// The trace must contain the gRPC server span, the publish span and the process span.
	stopCons()
	names := map[string]bool{}
	for _, s := range recorder.Ended() {
		if s.SpanContext().TraceID() == root.SpanContext().TraceID() {
			names[s.Name()] = true
		}
	}
	for _, want := range []string{"quotify.hello.v1.HelloService/SayHello", topic + " publish"} {
		if !names[want] {
			t.Errorf("span %q missing from trace, have %v", want, names)
		}
	}
}
