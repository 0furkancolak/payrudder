package kafkax

import (
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/protobuf/proto"

	eventsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/events/v1"
)

// UsageRecordTime is the ConsumerConfig.RecordTime of consumers of usage.events.v1: the tenant and ingested_at of an event.
func UsageRecordTime(rec *kgo.Record) (string, time.Time, bool) {
	var ev eventsv1.UsageEvent
	if err := proto.Unmarshal(rec.Value, &ev); err != nil || ev.GetIngestedAt() == nil {
		return "", time.Time{}, false
	}
	return ev.GetTenantId(), ev.GetIngestedAt().AsTime(), true
}
