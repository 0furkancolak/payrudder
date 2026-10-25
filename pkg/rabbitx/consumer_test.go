package rabbitx

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

type fakeAck struct {
	mu     sync.Mutex
	at     time.Time
	method string
	req    bool
}

func (f *fakeAck) record(m string, requeue bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.at, f.method, f.req = time.Now(), m, requeue
	return nil
}
func (f *fakeAck) Ack(uint64, bool) error          { return f.record("ack", false) }
func (f *fakeAck) Nack(_ uint64, _, rq bool) error { return f.record("nack", rq) }
func (f *fakeAck) Reject(_ uint64, rq bool) error  { return f.record("reject", rq) }
func (f *fakeAck) got() (string, bool, time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.method, f.req, f.at
}

// A Requeue goes back to the queue after a pause that grows with the redeliveries: an immediate requeue burns the quorum queue's delivery limit
// in milliseconds and dead-letters a message over a one-second outage.
func TestRequeueWaitsBeforeGoingBack(t *testing.T) {
	old := RequeueBaseDelay
	RequeueBaseDelay = 20 * time.Millisecond
	defer func() { RequeueBaseDelay = old }()

	for _, count := range []int64{0, 2} {
		ack := &fakeAck{}
		d := amqp.Delivery{Acknowledger: ack, Headers: amqp.Table{"x-delivery-count": count}}
		start := time.Now()
		settle(context.Background(), context.Background(), d, "q", func(context.Context, amqp.Delivery) Outcome { return Requeue }, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
		method, requeue, at := ack.got()
		if method != "nack" || !requeue {
			t.Fatalf("count %d: settled with %s requeue=%v", count, method, requeue)
		}
		if want := RequeueBaseDelay << count; at.Sub(start) < want {
			t.Errorf("count %d: requeued after %v, want at least %v", count, at.Sub(start), want)
		}
	}
}

// A shutdown does not hold a delivery back: it goes straight back to the queue.
func TestRequeueDelayEndsWithTheContext(t *testing.T) {
	old := RequeueBaseDelay
	RequeueBaseDelay = time.Minute
	defer func() { RequeueBaseDelay = old }()

	ack := &fakeAck{}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(10 * time.Millisecond); cancel() }()
	start := time.Now()
	settle(context.Background(), ctx, amqp.Delivery{Acknowledger: ack}, "q", func(context.Context, amqp.Delivery) Outcome { return Requeue }, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if method, requeue, _ := ack.got(); method != "nack" || !requeue || time.Since(start) > 5*time.Second {
		t.Fatalf("settled with %s requeue=%v after %v", method, requeue, time.Since(start))
	}
}
