package kafkax

import (
	"context"
	"strconv"
	"sync"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// lagTTL is how long a per-tenant lag reading is reported after its last update.
const lagTTL = 10 * time.Minute

type lagKey struct {
	partition int32
	tenant    string
}

type lagReading struct {
	seconds float64
	at      time.Time
}

type lagState struct {
	mu      sync.Mutex
	records map[int32]int64
	seconds map[lagKey]lagReading
}

// initMetrics registers consumer_lag_records{group,partition} and consumer_lag_seconds{group,partition,tenant}
// (docs/08-observability.md#metrics): records behind the log end, and how old the newest applied record of each tenant
// was when it was applied (a per-tenant freshness figure, so one slow tenant is visible).
func (c *Consumer) initMetrics() error {
	c.lag = &lagState{records: map[int32]int64{}, seconds: map[lagKey]lagReading{}}
	m := otel.Meter("github.com/0furkancolak/payrudder/pkg/kafkax")
	group := attribute.String("group", c.cfg.GroupID)
	rec, err := m.Int64ObservableGauge("consumer_lag_records", metric.WithDescription("Records between the applied offset and the log end, per partition"))
	if err != nil {
		return err
	}
	sec, err := m.Float64ObservableGauge("consumer_lag_seconds", metric.WithUnit("s"),
		metric.WithDescription("Age (now - ingested_at) of the newest applied record, per partition and tenant"))
	if err != nil {
		return err
	}
	_, err = m.RegisterCallback(func(_ context.Context, o metric.Observer) error {
		c.lag.mu.Lock()
		defer c.lag.mu.Unlock()
		for p, n := range c.lag.records {
			o.ObserveInt64(rec, n, metric.WithAttributes(group, attribute.String("partition", strconv.Itoa(int(p)))))
		}
		now := time.Now()
		for k, r := range c.lag.seconds {
			if now.Sub(r.at) > lagTTL {
				delete(c.lag.seconds, k)
				continue
			}
			o.ObserveFloat64(sec, r.seconds, metric.WithAttributes(group, attribute.String("partition", strconv.Itoa(int(k.partition))), attribute.String("tenant", k.tenant)))
		}
		return nil
	}, rec, sec)
	return err
}

// observeLag updates the readings after a batch was applied.
func (c *Consumer) observeLag(p kgo.FetchTopicPartition) {
	last := p.Records[len(p.Records)-1]
	c.lag.mu.Lock()
	defer c.lag.mu.Unlock()
	if p.HighWatermark >= 0 {
		c.lag.records[p.Partition] = max(p.HighWatermark-(last.Offset+1), 0)
	}
	if c.cfg.RecordTime == nil {
		return
	}
	seen := map[string]bool{}
	now := time.Now()
	for i := len(p.Records) - 1; i >= 0; i-- {
		r := p.Records[i]
		if seen[string(r.Key)] {
			continue
		}
		seen[string(r.Key)] = true
		if tenant, at, ok := c.cfg.RecordTime(r); ok {
			c.lag.seconds[lagKey{p.Partition, tenant}] = lagReading{seconds: max(now.Sub(at).Seconds(), 0), at: now}
		}
	}
}
