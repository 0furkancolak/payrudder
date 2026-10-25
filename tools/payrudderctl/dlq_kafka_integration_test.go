//go:build integration

package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/0furkancolak/payrudder/pkg/testkit"
)

// Dead letters go back to their source topic, once, without the DLQ headers; running the redrive again moves nothing.
func TestKafkaDLQRedrive(t *testing.T) {
	brokers := testkit.StartRedpanda(t)
	ctx := context.Background()
	cl, err := kgo.NewClient(kgo.SeedBrokers(brokers))
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	if _, err := kadm.NewClient(cl).CreateTopics(ctx, 1, 1, nil, "usage.events.dlq.v1", "usage.events.v1"); err != nil {
		t.Fatal(err)
	}
	for i, v := range []string{"a", "b", "c"} {
		rec := &kgo.Record{Topic: "usage.events.dlq.v1", Key: []byte("tenant-1"), Value: []byte(v), Headers: []kgo.RecordHeader{
			{Key: "traceparent", Value: []byte("00-abc-def-01")}, {Key: "x-dlq-reason", Value: []byte("UNKNOWN_METER")}, {Key: "x-dlq-at", Value: []byte(time.Now().Format(time.RFC3339))}}}
		if err := cl.ProduceSync(ctx, rec).FirstErr(); err != nil {
			t.Fatalf("produce %d: %v", i, err)
		}
	}

	if err := kafkaDLQRead(ctx, []string{brokers}, "usage.events.dlq.v1", 2, true); err != nil {
		t.Fatalf("first redrive: %v", err)
	}
	if err := kafkaDLQRead(ctx, []string{brokers}, "usage.events.dlq.v1", 10, true); err != nil {
		t.Fatalf("second redrive: %v", err)
	}
	if err := kafkaDLQRead(ctx, []string{brokers}, "usage.events.dlq.v1", 10, true); err != nil {
		t.Fatalf("third redrive: %v", err)
	}

	out, err := kgo.NewClient(kgo.SeedBrokers(brokers), kgo.ConsumeTopics("usage.events.v1"), kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()))
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	var got []string
	for deadline := time.Now().Add(20 * time.Second); len(got) < 3 && time.Now().Before(deadline); {
		pctx, cancel := context.WithTimeout(ctx, time.Second)
		out.PollFetches(pctx).EachRecord(func(r *kgo.Record) {
			got = append(got, string(r.Value))
			for _, h := range r.Headers {
				if strings.HasPrefix(h.Key, "x-dlq-") {
					t.Errorf("record %q kept header %s", r.Value, h.Key)
				}
			}
			var redriven, trace bool
			for _, h := range r.Headers {
				redriven = redriven || h.Key == "x-redriven-at"
				trace = trace || h.Key == "traceparent"
			}
			if !redriven || !trace {
				t.Errorf("record %q: x-redriven-at=%v traceparent=%v, want both", r.Value, redriven, trace)
			}
		})
		cancel()
	}
	if strings.Join(got, "") != "abc" {
		t.Fatalf("redriven records = %v, want a b c exactly once and in order", got)
	}
	if err := dlqKafka("purge", []string{brokers}, "", 0, true); err == nil {
		t.Error("purge on Kafka must be refused")
	}
}
