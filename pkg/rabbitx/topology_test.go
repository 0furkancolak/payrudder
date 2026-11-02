package rabbitx

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

const definitionsPath = "../../deploy/rabbitmq/definitions.json"

// deploy/rabbitmq/definitions.json is generated from Declare's single definition; regenerate with
// UPDATE_DEFINITIONS=1 go test ./rabbitx.
func TestDefinitionsFileIsInSync(t *testing.T) {
	want, err := Definitions()
	if err != nil {
		t.Fatal(err)
	}
	want = append(want, '\n')
	if os.Getenv("UPDATE_DEFINITIONS") != "" {
		if err := os.WriteFile(definitionsPath, want, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(definitionsPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatal("deploy/rabbitmq/definitions.json is stale; run UPDATE_DEFINITIONS=1 go test ./rabbitx")
	}
}

func TestTopologyShape(t *testing.T) {
	names := map[string]bool{}
	for _, q := range queues() {
		names[q.Name] = true
		if strings.HasSuffix(q.Name, ".dlq") || strings.Contains(q.Name, ".retry.") {
			continue
		}
		if q.Arguments["x-queue-type"] != "quorum" || q.Arguments["x-delivery-limit"] != int64(DeliveryLimit) {
			t.Errorf("queue %s must be a quorum queue with a delivery limit: %v", q.Name, q.Arguments)
		}
	}
	for _, q := range CommandQueues {
		for _, want := range []string{q, q + ".dlq", q + ".retry.5s", q + ".retry.30s", q + ".retry.2m", q + ".retry.5m", q + ".retry.10m"} {
			if !names[want] {
				t.Errorf("queue %s is not declared", want)
			}
		}
	}
	if !names[ReplyQueue] || !names[ReplyQueue+".dlq"] {
		t.Error("reply queues are not declared")
	}
	if RetryRoutingKey("payment.charge", 1) != "payment.charge.retry.5s" || RetryRoutingKey("payment.charge", 3) != "payment.charge.retry.2m" ||
		RetryRoutingKey("payment.charge", 9) != "payment.charge.retry.2m" {
		t.Error("retry routing keys are wrong")
	}
}

// A provider's Retry-After lengthens the wait of a retry: the longer of the attempt's tier and the request, at most RetryCeiling, in the shortest queue that holds it
// that long. Without one, or with a shorter one, the tier of the attempt stays.
func TestRetryRoutingKeyAfter(t *testing.T) {
	for _, c := range []struct {
		retry int
		wait  time.Duration
		want  string
	}{
		{1, 0, "5s"},
		{1, 3 * time.Second, "5s"},
		{1, 20 * time.Second, "30s"},
		{1, 30 * time.Second, "30s"},
		{1, 31 * time.Second, "2m"},
		{3, 90 * time.Second, "2m"}, // the tier of the attempt is already longer
		{1, 3 * time.Minute, "5m"},
		{2, 6 * time.Minute, "10m"},
		{1, time.Hour, "10m"}, // the ceiling
	} {
		if got, want := RetryRoutingKeyAfter("payment.charge", c.retry, c.wait), "payment.charge.retry."+c.want; got != want {
			t.Errorf("retry %d after %v: %s, want %s", c.retry, c.wait, got, want)
		}
	}
	old := RetryCeiling
	RetryCeiling = time.Minute
	defer func() { RetryCeiling = old }()
	if got := RetryRoutingKeyAfter("payment.charge", 1, time.Hour); got != "payment.charge.retry.2m" {
		t.Errorf("with a ceiling of a minute: %s, want the 2m queue, the shortest that holds it", got)
	}
}

// AskRetryAfter only reaches a handler that a consumer called; elsewhere it is a no-op.
func TestAskRetryAfterWithoutAConsumerDoesNothing(t *testing.T) {
	AskRetryAfter(context.Background(), time.Minute)
	h := &retryHint{}
	ctx := context.WithValue(context.Background(), hintKey{}, h)
	AskRetryAfter(ctx, 0)
	AskRetryAfter(ctx, -time.Second)
	if h.after.Load() != 0 {
		t.Fatalf("a zero or negative wait was kept: %d", h.after.Load())
	}
	AskRetryAfter(ctx, 3*time.Minute)
	if time.Duration(h.after.Load()) != 3*time.Minute {
		t.Fatalf("the wait was not kept: %d", h.after.Load())
	}
}
