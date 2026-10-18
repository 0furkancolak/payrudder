package kafkax

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
)

// DLQ header names (docs/04-messaging.md#kafka).
const (
	HeaderDLQReason     = "x-dlq-reason"
	HeaderDLQError      = "x-dlq-error"
	HeaderDLQConsumer   = "x-dlq-consumer"
	HeaderDLQAt         = "x-dlq-at"
	HeaderOrigTopic     = "x-original-topic"
	HeaderOrigPartition = "x-original-partition"
	HeaderOrigOffset    = "x-original-offset"
)

// DLQ publishes records a consumer can't process to a dead-letter topic, keeping the original bytes.
type DLQ struct {
	Client   *kgo.Client
	Topic    string
	Consumer string // consumer group name recorded in x-dlq-consumer
	Now      func() time.Time
}

// Send produces the original key and value with the DLQ headers (and the original headers) and waits for the ack.
// reason is a short code (DECODE_ERROR, UNKNOWN_METER, ...); err is the detail.
func (d DLQ) Send(ctx context.Context, orig *kgo.Record, reason string, err error) error {
	now := time.Now
	if d.Now != nil {
		now = d.Now
	}
	detail := ""
	if err != nil {
		detail = err.Error()
	}
	if len(detail) > 1024 {
		detail = detail[:1024]
	}
	rec := &kgo.Record{
		Topic: d.Topic,
		Key:   orig.Key,
		Value: orig.Value,
		Headers: append(append([]kgo.RecordHeader(nil), orig.Headers...),
			Headers(map[string]string{
				HeaderDLQReason:     reason,
				HeaderDLQError:      detail,
				HeaderDLQConsumer:   d.Consumer,
				HeaderDLQAt:         now().UTC().Format(time.RFC3339Nano),
				HeaderOrigTopic:     orig.Topic,
				HeaderOrigPartition: strconv.Itoa(int(orig.Partition)),
				HeaderOrigOffset:    strconv.FormatInt(orig.Offset, 10),
			})...),
	}
	if e := d.Client.ProduceSync(ctx, rec).FirstErr(); e != nil {
		return fmt.Errorf("kafkax: dlq produce: %w", e)
	}
	return nil
}
