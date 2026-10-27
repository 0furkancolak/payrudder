package kafkax

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/0furkancolak/payrudder/pkg/backoff"
)

// PartitionHandler applies the records of one partition, in order. Implementations persist the next offset in the same
// database transaction as their effects (docs/07-correctness.md §2): Kafka's own committed offset is advisory only.
type PartitionHandler interface {
	Apply(ctx context.Context, partition int32, recs []*kgo.Record) error
}

// OffsetLoader returns the next offset to read for each of the given partitions. A partition without an entry starts
// at the beginning of the log.
type OffsetLoader func(ctx context.Context, topic string, partitions []int32) (map[int32]int64, error)

// ConsumerConfig configures a Consumer.
type ConsumerConfig struct {
	Brokers []string
	// GroupID is the Kafka consumer group: it balances partitions between replicas and feeds lag dashboards.
	GroupID string
	Topic   string
	// LoadOffsets reads the authoritative offsets from the service database.
	LoadOffsets OffsetLoader
	Handler     PartitionHandler
	// MaxPollRecords bounds one poll (default 1000).
	MaxPollRecords int
	// Backoff is how long a partition is paused after a handler error (default 1s).
	Backoff time.Duration
	// SessionTimeout is how long a crashed member holds its partitions before the group reassigns them (default 10s;
	// the client's own default is 45s, which stalls a crash-looping consumer).
	SessionTimeout time.Duration
	Log            *slog.Logger
	// Extra client options (tests, TLS).
	Extra []kgo.Opt
	// RecordTime tells, for a record, the tenant it belongs to and the instant it entered the pipeline (ingested_at), so
	// the consumer can report consumer_lag_seconds per tenant. Optional.
	RecordTime func(rec *kgo.Record) (tenant string, at time.Time, ok bool)
}

// Consumer reads one topic with DB-stored offsets.
//
// On every assignment the offsets are loaded from the database and the client seeks there, whatever Kafka has
// committed. Partitions of one poll are applied concurrently, and a rebalance can't take a partition away while its
// batch is in flight (BlockRebalanceOnPoll). A handler error rewinds the partition to its database offset and pauses it
// for Backoff, so the batch is read again; that's also what a crashed process does on restart.
type Consumer struct {
	lag *lagState
	cfg ConsumerConfig
	cl  *kgo.Client
	log *slog.Logger
}

// NewConsumer connects (lazily) and joins the group on Run.
func NewConsumer(cfg ConsumerConfig) (*Consumer, error) {
	if cfg.LoadOffsets == nil || cfg.Handler == nil || cfg.Topic == "" || cfg.GroupID == "" {
		return nil, errors.New("kafkax: consumer needs GroupID, Topic, LoadOffsets and Handler")
	}
	if cfg.MaxPollRecords <= 0 {
		cfg.MaxPollRecords = 1000
	}
	if cfg.Backoff <= 0 {
		cfg.Backoff = time.Second
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if cfg.SessionTimeout <= 0 {
		cfg.SessionTimeout = 10 * time.Second
	}
	c := &Consumer{cfg: cfg, log: cfg.Log}
	if err := c.initMetrics(); err != nil {
		return nil, err
	}
	opts := []kgo.Opt{
		kgo.SeedBrokers(cfg.Brokers...),
		kgo.ConsumerGroup(cfg.GroupID),
		kgo.ConsumeTopics(cfg.Topic),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
		kgo.FetchIsolationLevel(kgo.ReadCommitted()),
		kgo.Balancers(kgo.CooperativeStickyBalancer()),
		kgo.AutoCommitMarks(),
		kgo.BlockRebalanceOnPoll(),
		kgo.SessionTimeout(cfg.SessionTimeout),
		kgo.HeartbeatInterval(cfg.SessionTimeout / 3),
		kgo.AdjustFetchOffsetsFn(c.adjustOffsets),
	}
	cl, err := kgo.NewClient(append(opts, cfg.Extra...)...)
	if err != nil {
		return nil, fmt.Errorf("kafkax: new consumer: %w", err)
	}
	c.cl = cl
	return c, nil
}

// Client exposes the underlying client (health checks, tests).
func (c *Consumer) Client() *kgo.Client { return c.cl }

// Close leaves the group.
func (c *Consumer) Close() { c.cl.Close() }

func (c *Consumer) adjustOffsets(ctx context.Context, assigned map[string]map[int32]kgo.Offset) (map[string]map[int32]kgo.Offset, error) {
	out := make(map[string]map[int32]kgo.Offset, len(assigned))
	for topic, parts := range assigned {
		ids := make([]int32, 0, len(parts))
		for p := range parts {
			ids = append(ids, p)
		}
		saved, err := c.cfg.LoadOffsets(ctx, topic, ids)
		if err != nil {
			return nil, fmt.Errorf("load offsets: %w", err)
		}
		m := make(map[int32]kgo.Offset, len(parts))
		for _, p := range ids {
			if next, ok := saved[p]; ok {
				m[p] = kgo.NewOffset().At(next)
			} else {
				m[p] = kgo.NewOffset().AtStart()
			}
		}
		out[topic] = m
	}
	return out, nil
}

// Run consumes until ctx is done.
func (c *Consumer) Run(ctx context.Context) error {
	// A broker that is down answers every poll with an error at once: wait longer after each, and say it once, not on every poll.
	retry := backoff.Retry{Base: 250 * time.Millisecond, Max: 15 * time.Second}
	for ctx.Err() == nil {
		fetches := c.cl.PollRecords(ctx, c.cfg.MaxPollRecords)
		if fetches.IsClientClosed() {
			return nil
		}
		var fetchErr error
		fetches.EachError(func(topic string, p int32, err error) {
			if ctx.Err() == nil && !errors.Is(err, context.Canceled) && fetchErr == nil {
				fetchErr = fmt.Errorf("%s/%d: %w", topic, p, err)
			}
		})
		switch {
		case fetchErr != nil && fetches.NumRecords() == 0:
			retry.Fail(ctx, c.log, "kafka fetch error", fetchErr)
			continue
		case fetchErr != nil:
			c.log.WarnContext(ctx, "kafka fetch error", "error", fetchErr)
		default:
			retry.OK(ctx, c.log, "kafka fetch is working again")
		}
		var wg sync.WaitGroup
		fetches.EachPartition(func(p kgo.FetchTopicPartition) {
			if len(p.Records) == 0 {
				return
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				c.apply(ctx, p)
			}()
		})
		wg.Wait()
		c.cl.AllowRebalance()
	}
	return nil
}

func (c *Consumer) apply(ctx context.Context, p kgo.FetchTopicPartition) {
	bctx, end := StartBatch(ctx, p.Topic, c.cfg.GroupID, p.Partition, p.Records)
	err := c.cfg.Handler.Apply(bctx, p.Partition, p.Records)
	end(err)
	if err == nil {
		c.observeLag(p)
	}
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		c.log.WarnContext(ctx, "batch failed, rewinding partition", "topic", p.Topic, "partition", p.Partition,
			"first_offset", p.Records[0].Offset, "records", len(p.Records), "error", err)
		c.rewind(ctx, p)
		return
	}
	c.cl.MarkCommitRecords(p.Records[len(p.Records)-1])
}

// rewind seeks the partition back to its database offset and pauses it for Backoff.
func (c *Consumer) rewind(ctx context.Context, p kgo.FetchTopicPartition) {
	next := p.Records[0].Offset
	if saved, err := c.cfg.LoadOffsets(ctx, p.Topic, []int32{p.Partition}); err == nil {
		if n, ok := saved[p.Partition]; ok {
			next = n
		}
	}
	c.cl.SetOffsets(map[string]map[int32]kgo.EpochOffset{p.Topic: {p.Partition: {Epoch: -1, Offset: next}}})
	c.cl.PauseFetchPartitions(map[string][]int32{p.Topic: {p.Partition}})
	time.AfterFunc(c.cfg.Backoff, func() {
		c.cl.ResumeFetchPartitions(map[string][]int32{p.Topic: {p.Partition}})
	})
}
