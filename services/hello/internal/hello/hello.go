// Package hello is the throwaway Phase 00 service: SayHello produces a HelloEvent to Kafka with trace headers,
// and Consumer reads it back as a child span of the same trace. It is deleted at the end of Phase 01.
package hello

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	grpccodes "google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	hellov1 "github.com/0furkancolak/quotify/gen/go/quotify/hello/v1"
	"github.com/0furkancolak/quotify/pkg/idgen"
	"github.com/0furkancolak/quotify/pkg/otelx"
)

const tracerName = "github.com/0furkancolak/quotify/services/hello"

// Producer is the subset of *kgo.Client the server needs.
type Producer interface {
	ProduceSync(ctx context.Context, rs ...*kgo.Record) kgo.ProduceResults
}

// Server implements hellov1.HelloServiceServer.
type Server struct {
	hellov1.UnimplementedHelloServiceServer
	producer Producer
	topic    string
	tracer   trace.Tracer
}

// NewServer returns a Server producing to topic.
func NewServer(p Producer, topic string) *Server {
	return &Server{producer: p, topic: topic, tracer: otel.Tracer(tracerName)}
}

// SayHello produces one HelloEvent and reports its partition and offset.
func (s *Server) SayHello(ctx context.Context, req *hellov1.SayHelloRequest) (*hellov1.SayHelloResponse, error) {
	if req.GetName() == "" {
		return nil, status.Error(grpccodes.InvalidArgument, "name is required")
	}
	ev := &hellov1.HelloEvent{EventId: idgen.NewULID(), Name: req.GetName(), CreatedAt: timestamppb.Now()}
	value, err := proto.Marshal(ev)
	if err != nil {
		return nil, status.Errorf(grpccodes.Internal, "marshal: %v", err)
	}

	ctx, span := s.tracer.Start(ctx, s.topic+" publish", trace.WithSpanKind(trace.SpanKindProducer),
		trace.WithAttributes(
			attribute.String("messaging.system", "kafka"),
			attribute.String("messaging.destination.name", s.topic),
			attribute.String("messaging.message.id", ev.GetEventId()),
		))
	defer span.End()

	rec := &kgo.Record{Topic: s.topic, Key: []byte(req.GetName()), Value: value,
		Headers: []kgo.RecordHeader{{Key: "ce-type", Value: []byte("quotify.hello.v1.HelloEvent")}}}
	otelx.Propagator().Inject(ctx, otelx.KafkaHeadersCarrier{Record: rec})

	res := s.producer.ProduceSync(ctx, rec)
	if err := res.FirstErr(); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "produce failed")
		return nil, status.Errorf(grpccodes.Unavailable, "produce: %v", err)
	}
	out := res[0].Record
	span.SetAttributes(attribute.Int("messaging.kafka.destination.partition", int(out.Partition)),
		attribute.Int64("messaging.kafka.offset", out.Offset))
	return &hellov1.SayHelloResponse{
		Message:   fmt.Sprintf("hello, %s", req.GetName()),
		Partition: out.Partition,
		Offset:    out.Offset,
	}, nil
}

// Handler receives consumed events with the restored trace context.
type Handler func(ctx context.Context, ev *hellov1.HelloEvent) error

// Consumer reads HelloEvents and calls Handler inside a consumer span that continues the producer's trace.
type Consumer struct {
	client  *kgo.Client
	handle  Handler
	log     *slog.Logger
	tracer  trace.Tracer
	backoff time.Duration
}

// NewConsumer wraps a client that is already subscribed to the topic (kgo.ConsumeTopics).
func NewConsumer(client *kgo.Client, handle Handler, log *slog.Logger) *Consumer {
	return &Consumer{client: client, handle: handle, log: log, tracer: otel.Tracer(tracerName), backoff: time.Second}
}

// Run polls until ctx is cancelled. Records are committed by the client's autocommit, which is fine for hello.
func (c *Consumer) Run(ctx context.Context) error {
	for {
		fetches := c.client.PollFetches(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if fetches.IsClientClosed() {
			return errors.New("hello: kafka client closed")
		}
		fetches.EachError(func(topic string, partition int32, err error) {
			c.log.ErrorContext(ctx, "fetch error", "topic", topic, "partition", partition, "error", err)
		})
		fetches.EachRecord(func(rec *kgo.Record) { c.process(ctx, rec) })
	}
}

func (c *Consumer) process(ctx context.Context, rec *kgo.Record) {
	parent := otelx.Propagator().Extract(ctx, otelx.KafkaHeadersCarrier{Record: rec})
	ctx, span := c.tracer.Start(parent, rec.Topic+" process", trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithAttributes(
			attribute.String("messaging.system", "kafka"),
			attribute.String("messaging.destination.name", rec.Topic),
			attribute.Int("messaging.kafka.destination.partition", int(rec.Partition)),
			attribute.Int64("messaging.kafka.offset", rec.Offset),
		))
	defer span.End()

	var ev hellov1.HelloEvent
	if err := proto.Unmarshal(rec.Value, &ev); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "decode failed")
		c.log.ErrorContext(ctx, "undecodable hello event", "offset", rec.Offset, "error", err)
		return
	}
	if err := c.handle(ctx, &ev); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "handler failed")
		c.log.ErrorContext(ctx, "hello handler failed", "event_id", ev.GetEventId(), "error", err)
	}
}
