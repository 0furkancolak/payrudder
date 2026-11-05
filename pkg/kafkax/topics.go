package kafkax

import (
	"context"
	"fmt"
	"strconv"

	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
)

// Topic is one topic of docs/04-messaging.md. RetentionDays 0 means compacted.
type Topic struct {
	Name          string
	Partitions    int32
	RetentionDays int
}

// Topics are the topics deploy/compose/redpanda/topics.sh creates (a test keeps the two the same).
var Topics = []Topic{
	{"usage.events.v1", 12, 30},
	{"usage.events.dlq.v1", 3, 30},
	{"usage.limit-exceeded.v1", 6, 90},
	{"usage.limit-exceeded.dlq.v1", 1, 30},
	{"subscription.trial-will-end.v1", 6, 90},
	{"subscription.trial-will-end.dlq.v1", 1, 30},
	{"subscription.price-change.v1", 6, 90},
	{"subscription.price-change.dlq.v1", 1, 30},
	{"contract.accepted.v1", 6, 90},
	{"contract.accepted.dlq.v1", 1, 30},
	{"subscription.renewal-notice.v1", 6, 90},
	{"subscription.renewal-notice.dlq.v1", 1, 30},
	{"contract.withdrawal-received.v1", 6, 90},
	{"contract.withdrawal-received.dlq.v1", 1, 30},
	{"customer.block-notice.v1", 6, 90},
	{"customer.block-notice.dlq.v1", 1, 30},
	{"plans.entitlements.v1", 6, 0},
	{"billing.invoices.v1", 6, 400},
	{"billing.invoices.dlq.v1", 1, 30},
}

const day = 86_400_000 // ms

func (t Topic) configs() map[string]string {
	if t.RetentionDays == 0 {
		return map[string]string{"cleanup.policy": "compact", "min.compaction.lag.ms": "3600000", "delete.retention.ms": strconv.Itoa(day)}
	}
	return map[string]string{"cleanup.policy": "delete", "retention.ms": strconv.FormatInt(int64(t.RetentionDays)*day, 10)}
}

// EnsureTopics creates the Topics that do not exist yet, with replication factor rf, and leaves the others as they are: a
// deployment with no init job (Railway) runs it at start. It is what topics.sh does.
func EnsureTopics(ctx context.Context, cl *kgo.Client, rf int16) error {
	req := kmsg.NewPtrCreateTopicsRequest()
	req.TimeoutMillis = 30_000
	for _, t := range Topics {
		rt := kmsg.NewCreateTopicsRequestTopic()
		rt.Topic, rt.NumPartitions, rt.ReplicationFactor = t.Name, t.Partitions, rf
		for k, v := range t.configs() {
			c := kmsg.NewCreateTopicsRequestTopicConfig()
			c.Name, c.Value = k, kmsg.StringPtr(v)
			rt.Configs = append(rt.Configs, c)
		}
		req.Topics = append(req.Topics, rt)
	}
	resp, err := req.RequestWith(ctx, cl)
	if err != nil {
		return fmt.Errorf("kafkax: create topics: %w", err)
	}
	for _, t := range resp.Topics {
		if err := kerr.ErrorForCode(t.ErrorCode); err != nil && err != kerr.TopicAlreadyExists {
			return fmt.Errorf("kafkax: create topic %s: %w", t.Topic, err)
		}
	}
	return nil
}
