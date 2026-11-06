//go:build integration

package kafkax_test

import (
	"testing"

	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/0furkancolak/payrudder/pkg/kafkax"
	"github.com/0furkancolak/payrudder/pkg/testkit"
)

func TestEnsureTopics(t *testing.T) {
	ctx := testkit.Ctx(t)
	cl, err := kgo.NewClient(kgo.SeedBrokers(testkit.StartRedpanda(t)))
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	for range 2 { // the second run finds them all and changes nothing
		if err := kafkax.EnsureTopics(ctx, cl, 1); err != nil {
			t.Fatal(err)
		}
	}
	details, err := kadm.NewClient(cl).ListTopics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range kafkax.Topics {
		d, ok := details[want.Name]
		if !ok || int32(len(d.Partitions)) != want.Partitions {
			t.Errorf("%s: present %v, partitions %d, want %d", want.Name, ok, len(d.Partitions), want.Partitions)
		}
	}
}
