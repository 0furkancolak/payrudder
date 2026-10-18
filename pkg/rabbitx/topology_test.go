package rabbitx

import (
	"os"
	"strings"
	"testing"
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
		for _, want := range []string{q, q + ".dlq", q + ".retry.5s", q + ".retry.30s", q + ".retry.2m"} {
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
