package workerx

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"
	"google.golang.org/protobuf/proto"

	commandsv1 "github.com/0furkancolak/quotify/gen/go/quotify/commands/v1"
	"github.com/0furkancolak/quotify/pkg/rabbitx"
)

// Duplicates of one command that arrive while others wait must still run one at a time: the lock may not be dropped from the map while a
// waiter holds it, or a newcomer gets a second mutex.
func TestDuplicateCommandsRunOneAtATime(t *testing.T) {
	var running, peak atomic.Int32
	w := &Worker{
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Handler: HandlerFunc(func(context.Context, *commandsv1.CommandEnvelope) (*commandsv1.ReplyEnvelope, error) {
			n := running.Add(1)
			for {
				p := peak.Load()
				if n <= p || peak.CompareAndSwap(p, n) {
					break
				}
			}
			time.Sleep(4 * time.Millisecond)
			running.Add(-1)
			return nil, nil // no reply: acked without a broker
		}),
	}
	if err := w.init(); err != nil {
		t.Fatal(err)
	}
	body, err := proto.Marshal(&commandsv1.CommandEnvelope{
		CommandId: uuid.NewString(),
		Command:   &commandsv1.CommandEnvelope_RenderInvoicePdf{RenderInvoicePdf: &commandsv1.RenderInvoicePdf{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 60; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if out := w.handle(context.Background(), "q", amqp.Delivery{Body: body}); out != rabbitx.Ack {
				t.Errorf("outcome = %v", out)
			}
		}()
		time.Sleep(time.Millisecond)
	}
	wg.Wait()
	if peak.Load() != 1 {
		t.Fatalf("%d copies of one command ran at once", peak.Load())
	}
}

func TestKeyedMutexReleasesItsEntries(t *testing.T) {
	var k KeyedMutex
	unlock := k.Lock("a")
	unlock()
	k.mu.Lock()
	defer k.mu.Unlock()
	if len(k.m) != 0 {
		t.Fatalf("%d entries left after the last unlock", len(k.m))
	}
}
