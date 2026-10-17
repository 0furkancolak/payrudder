package hello

import (
	"context"
	"errors"
	"testing"

	"github.com/twmb/franz-go/pkg/kgo"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	hellov1 "github.com/0furkancolak/quotify/gen/go/quotify/hello/v1"
	"github.com/0furkancolak/quotify/pkg/otelx"
)

type fakeProducer struct {
	got []*kgo.Record
	err error
}

func (f *fakeProducer) ProduceSync(_ context.Context, rs ...*kgo.Record) kgo.ProduceResults {
	out := make(kgo.ProduceResults, 0, len(rs))
	for _, r := range rs {
		f.got = append(f.got, r)
		r.Partition, r.Offset = 3, 42
		out = append(out, kgo.ProduceResult{Record: r, Err: f.err})
	}
	return out
}

func TestSayHelloInjectsTraceHeaders(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	otel.SetTracerProvider(tp)
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	p := &fakeProducer{}
	srv := NewServer(p, "hello.events.v1")
	resp, err := srv.SayHello(context.Background(), &hellov1.SayHelloRequest{Name: "ada"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetPartition() != 3 || resp.GetOffset() != 42 || resp.GetMessage() != "hello, ada" {
		t.Fatalf("resp = %v", resp)
	}
	if len(p.got) != 1 {
		t.Fatalf("produced %d records", len(p.got))
	}
	tp2 := otelx.KafkaHeadersCarrier{Record: p.got[0]}.Get("traceparent")
	spans := rec.Ended()
	if len(spans) != 1 || tp2 == "" {
		t.Fatalf("spans=%d traceparent=%q", len(spans), tp2)
	}
	want := "00-" + spans[0].SpanContext().TraceID().String() + "-" + spans[0].SpanContext().SpanID().String() + "-01"
	if tp2 != want {
		t.Fatalf("traceparent = %q, want %q", tp2, want)
	}
}

func TestSayHelloErrors(t *testing.T) {
	srv := NewServer(&fakeProducer{}, "t")
	if _, err := srv.SayHello(context.Background(), &hellov1.SayHelloRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("empty name: %v", err)
	}
	srv = NewServer(&fakeProducer{err: errors.New("broker down")}, "t")
	if _, err := srv.SayHello(context.Background(), &hellov1.SayHelloRequest{Name: "x"}); status.Code(err) != codes.Unavailable {
		t.Fatalf("produce error: %v", err)
	}
}
