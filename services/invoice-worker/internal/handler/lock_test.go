package handler

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	commandsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/commands/v1"
	"github.com/0furkancolak/payrudder/pkg/blob"
)

type headStore struct {
	running, peak atomic.Int32
}

func (s *headStore) Head(context.Context, string) (blob.Info, error) {
	n := s.running.Add(1)
	for {
		p := s.peak.Load()
		if n <= p || s.peak.CompareAndSwap(p, n) {
			break
		}
	}
	time.Sleep(4 * time.Millisecond)
	s.running.Add(-1)
	return blob.Info{Size: 1, Metadata: map[string]string{"sha256": "abc"}}, nil
}
func (*headStore) Get(context.Context, string) ([]byte, error)                { return nil, blob.ErrNotFound }
func (*headStore) Put(context.Context, string, []byte, blob.PutOptions) error { return nil }

// Attempts at one object key must run one at a time even when more arrive while others wait.
func TestSameObjectKeyRunsOneAtATime(t *testing.T) {
	st := &headStore{}
	h := &Handler{Store: st}
	cmd := &commandsv1.CommandEnvelope{TenantId: "t", Command: &commandsv1.CommandEnvelope_RenderInvoicePdf{
		RenderInvoicePdf: &commandsv1.RenderInvoicePdf{Invoice: &commandsv1.InvoiceSnapshot{InvoiceId: "i", Version: 1}}}}
	var wg sync.WaitGroup
	for i := 0; i < 60; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := h.Handle(context.Background(), cmd); err != nil {
				t.Error(err)
			}
		}()
		time.Sleep(time.Millisecond)
	}
	wg.Wait()
	if st.peak.Load() != 1 {
		t.Fatalf("%d attempts at one key ran at once", st.peak.Load())
	}
}
