package main

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"
)

// kafkaDLQs maps each Kafka dead-letter topic to the topic its records are redriven to (docs/04-messaging.md).
var kafkaDLQs = map[string]string{
	"usage.events.dlq.v1":                "usage.events.v1",
	"usage.limit-exceeded.dlq.v1":        "usage.limit-exceeded.v1",
	"billing.invoices.dlq.v1":            "billing.invoices.v1",
	"subscription.trial-will-end.dlq.v1": "subscription.trial-will-end.v1",
}

// redriveGroup records how far a DLQ topic has been redriven. Kafka cannot delete a record, so "pending" is
// end offset - committed offset of this group, and `dlq ls` reports exactly that.
const redriveGroup = "payrudderctl-redrive"

// dlqKafka implements `payrudderctl dlq ls|show|redrive --broker kafka`.
//
//	payrudderctl dlq ls --broker kafka
//	payrudderctl dlq show --broker kafka --topic usage.events.dlq.v1 [--limit 5]
//	payrudderctl dlq redrive --broker kafka --topic usage.events.dlq.v1 [--limit 100]
//
// A redrive produces each record back to its source topic with the x-dlq-* headers dropped and x-redriven-at added, waits
// for every produce to be acknowledged, and only then commits the group offset. Crashing in between redrives a record
// twice at worst, which the consumers' idempotency (event ids, command ids) absorbs.
func dlqKafka(sub string, brokers []string, topic string, limit int) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	switch sub {
	case "ls":
		return kafkaDLQList(ctx, brokers)
	case "show", "redrive":
		if _, ok := kafkaDLQs[topic]; !ok {
			return fmt.Errorf("--topic must be one of %s", strings.Join(kafkaDLQTopics(), ", "))
		}
		return kafkaDLQRead(ctx, brokers, topic, limit, sub == "redrive")
	case "purge":
		return errors.New("records of a Kafka topic cannot be deleted: use `redrive` to move them, or set the topic's retention to drop them")
	}
	return fmt.Errorf("unknown dlq command %q", sub)
}

func kafkaDLQTopics() []string {
	return slices.Sorted(maps.Keys(kafkaDLQs))
}

func kafkaDLQList(ctx context.Context, brokers []string) error {
	cl, err := kgo.NewClient(kgo.SeedBrokers(brokers...))
	if err != nil {
		return err
	}
	defer cl.Close()
	adm := kadm.NewClient(cl)
	ends, err := adm.ListEndOffsets(ctx, kafkaDLQTopics()...)
	if err != nil {
		return err
	}
	committed, err := adm.FetchOffsets(ctx, redriveGroup)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "%-30s %s\n", "DEAD-LETTER TOPIC", "PENDING")
	for _, t := range kafkaDLQTopics() {
		var pending int64
		ends.Each(func(o kadm.ListedOffset) {
			if o.Topic != t {
				return
			}
			done, _ := committed.Lookup(t, o.Partition)
			pending += o.Offset - max(done.At, 0)
		})
		fmt.Fprintf(os.Stdout, "%-30s %d\n", t, pending)
	}
	return nil
}

func kafkaDLQRead(ctx context.Context, brokers []string, topic string, limit int, redrive bool) error {
	opts := []kgo.Opt{kgo.SeedBrokers(brokers...), kgo.ConsumerGroup(redriveGroup), kgo.ConsumeTopics(topic),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()), kgo.DisableAutoCommit()}
	cl, err := kgo.NewClient(opts...)
	if err != nil {
		return err
	}
	defer cl.Close()

	var recs []*kgo.Record
	for len(recs) < limit {
		pctx, pcancel := context.WithTimeout(ctx, 5*time.Second)
		fetches := cl.PollRecords(pctx, limit-len(recs))
		pcancel()
		if err := firstErr(fetches); err != nil {
			return err
		}
		n := len(recs)
		fetches.EachRecord(func(r *kgo.Record) { recs = append(recs, r) })
		if len(recs) == n {
			break // nothing more within the timeout: the topic is drained
		}
	}
	target := kafkaDLQs[topic]
	for i, r := range recs {
		if !redrive {
			fmt.Fprintf(os.Stdout, "--- record %d (partition %d, offset %d, key %q, %d bytes)\n", i+1, r.Partition, r.Offset, r.Key, len(r.Value))
			for _, h := range r.Headers {
				if strings.HasPrefix(h.Key, "x-dlq-") {
					fmt.Fprintf(os.Stdout, "    %s: %s\n", h.Key, h.Value)
				}
			}
			continue
		}
		out := &kgo.Record{Topic: target, Key: r.Key, Value: r.Value}
		for _, h := range r.Headers {
			if !strings.HasPrefix(h.Key, "x-dlq-") {
				out.Headers = append(out.Headers, h)
			}
		}
		out.Headers = append(out.Headers, kgo.RecordHeader{Key: "x-redriven-at", Value: []byte(time.Now().UTC().Format(time.RFC3339))})
		if err := cl.ProduceSync(ctx, out).FirstErr(); err != nil {
			return fmt.Errorf("produce to %s (record %d of %d; the group offset is committed only after every record is produced, so none of this run is committed): %w", target, i+1, len(recs), err)
		}
	}
	if redrive && len(recs) > 0 {
		if err := cl.CommitRecords(ctx, recs...); err != nil {
			return fmt.Errorf("commit: %w (the %d records are redriven and will be redriven again; consumers deduplicate)", err, len(recs))
		}
		fmt.Fprintf(os.Stdout, "redriven %d record(s) from %s to %s\n", len(recs), topic, target)
	} else if redrive {
		fmt.Fprintf(os.Stdout, "nothing to redrive in %s\n", topic)
	} else {
		fmt.Fprintf(os.Stdout, "%d record(s) shown (not committed: show does not move the redrive offset)\n", len(recs))
	}
	return nil
}

func firstErr(f kgo.Fetches) error {
	var err error
	f.EachError(func(topic string, p int32, e error) {
		if err == nil && !errors.Is(e, context.DeadlineExceeded) && !errors.Is(e, context.Canceled) {
			err = fmt.Errorf("fetch %s/%d: %w", topic, p, e)
		}
	})
	return err
}
