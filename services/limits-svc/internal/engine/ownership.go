package engine

import (
	"context"
	"sort"
	"sync"

	"github.com/twmb/franz-go/pkg/kgo"
)

// Ownership tracks which partitions of a topic this pod's consumer currently owns.
type Ownership struct {
	mu    sync.Mutex
	owned map[int32]struct{}
}

// NewOwnership returns an empty set.
func NewOwnership() *Ownership { return &Ownership{owned: map[int32]struct{}{}} }

// Opts are the kgo options that keep the set up to date.
func (o *Ownership) Opts(topic string) []kgo.Opt {
	return []kgo.Opt{
		kgo.OnPartitionsAssigned(func(_ context.Context, _ *kgo.Client, m map[string][]int32) {
			o.mu.Lock()
			defer o.mu.Unlock()
			for _, p := range m[topic] {
				o.owned[p] = struct{}{}
			}
		}),
		kgo.OnPartitionsRevoked(func(_ context.Context, _ *kgo.Client, m map[string][]int32) { o.drop(topic, m) }),
		kgo.OnPartitionsLost(func(_ context.Context, _ *kgo.Client, m map[string][]int32) { o.drop(topic, m) }),
	}
}

func (o *Ownership) drop(topic string, m map[string][]int32) {
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, p := range m[topic] {
		delete(o.owned, p)
	}
}

// Partitions returns the owned partitions, ascending.
func (o *Ownership) Partitions() []int32 {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := make([]int32, 0, len(o.owned))
	for p := range o.owned {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
