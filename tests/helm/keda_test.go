package helm

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// KEDA is off in every profile that ships but its own, and the default install autoscales nothing: every Deployment states its replicas.
func TestAutoscalingIsOffByDefault(t *testing.T) {
	for _, v := range []string{"values.yaml", "values-small.yaml", "values-kind.yaml", "values-sandbox.yaml"} {
		// values-small is a production profile: it renders only with the pepper and the public URL that its secrets file would hold.
		for _, d := range render(t, "payrudder", "-f", values(t, "payrudder", v), "--set", "auth.otpPepper=0123456789abcdef0123456789abcdef", "--set", "global.publicURL="+publicURL) {
			if d.kind() == "ScaledObject" {
				t.Errorf("%s renders the ScaledObject %s by default", v, d.name())
			}
		}
	}
	for _, d := range render(t, "payrudder") {
		switch d.kind() {
		case "HorizontalPodAutoscaler":
			t.Errorf("the default install renders the HorizontalPodAutoscaler %s", d.name())
		case "Deployment":
			if d.get("spec", "replicas") == nil {
				t.Errorf("deployment %s has no replicas", d.name())
			}
		}
	}
}

// The KEDA profile scales the consumers on their backlog. What it names is checked against what the services and the broker really have, so a renamed group, topic or
// queue breaks this test and not a cluster: the group is the service's own default, the topic exists with at least as many partitions as the ScaledObject allows
// replicas (a consumer group cannot use more), and the queue is declared in the broker definitions.
func TestKedaScalesTheConsumersOnTheirBacklog(t *testing.T) {
	docs := render(t, "payrudder", "-f", values(t, "payrudder", "values-keda.yaml"))
	root := repoRoot(t)

	partitions := map[string]int{}
	topics, err := os.ReadFile(filepath.Join(root, "deploy", "compose", "redpanda", "topics.sh"))
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range regexp.MustCompile(`(?m)^create\s+(\S+)\s+(\d+)\s`).FindAllStringSubmatch(string(topics), -1) {
		partitions[m[1]], _ = strconv.Atoi(m[2])
	}
	var defs struct {
		Queues []struct {
			Name string `json:"name"`
		} `json:"queues"`
	}
	raw, err := os.ReadFile(filepath.Join(root, "deploy", "rabbitmq", "definitions.json"))
	if err != nil || json.Unmarshal(raw, &defs) != nil {
		t.Fatalf("broker definitions: %v", err)
	}
	queues := map[string]bool{}
	for _, q := range defs.Queues {
		queues[q.Name] = true
	}
	groupOf := func(configDir string) string {
		b, err := os.ReadFile(filepath.Join(root, "services", configDir, "config.default.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]string
		if err := yaml.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		return m["PAYRUDDER_CONSUMER_GROUP"]
	}

	want := map[string]string{"aggregator": "kafka", "limits-svc": "kafka", "invoice-worker": "rabbitmq", "payments-worker": "rabbitmq"}
	seen := 0
	for _, d := range docs {
		if d.kind() != "ScaledObject" {
			continue
		}
		seen++
		name := d.name()
		kind, ok := want[name]
		if !ok {
			t.Errorf("unexpected ScaledObject %s", name)
			continue
		}
		dep := find(docs, "Deployment", name)
		if dep == nil || d.str("spec", "scaleTargetRef", "name") != name || d.str("spec", "scaleTargetRef", "kind") != "Deployment" {
			t.Errorf("%s: the ScaledObject does not point at its Deployment", name)
			continue
		}
		if dep.get("spec", "replicas") != nil {
			t.Errorf("%s: the Deployment sets replicas next to a ScaledObject, which would reset the scale at every upgrade", name)
		}
		min, _ := d.get("spec", "minReplicaCount").(int)
		max, _ := d.get("spec", "maxReplicaCount").(int)
		if min < 1 || max < min {
			t.Errorf("%s: replicas %d..%d", name, min, max)
		}
		trigs, _ := d.get("spec", "triggers").([]any)
		if len(trigs) == 0 {
			t.Errorf("%s: no triggers", name)
		}
		for _, tr := range trigs {
			tm := obj(tr.(map[string]any))
			md := obj(tm["metadata"].(map[string]any))
			if tm.str("type") != kind {
				t.Errorf("%s: trigger type %q, want %s", name, tm.str("type"), kind)
			}
			switch kind {
			case "kafka":
				if got := md.str("bootstrapServers"); got != "redpanda:9092" {
					t.Errorf("%s: bootstrapServers %q is not the chart's kafkaBrokers", name, got)
				}
				if got, want := md.str("consumerGroup"), groupOf(map[string]string{"aggregator": "aggregator/cmd/aggregator", "limits-svc": "limits-svc/cmd/limits-svc"}[name]); got != want {
					t.Errorf("%s: consumer group %q, the service's default is %q", name, got, want)
				}
				if p := partitions[md.str("topic")]; p == 0 || max > p {
					t.Errorf("%s: topic %q has %d partitions, the ScaledObject allows %d replicas", name, md.str("topic"), p, max)
				}
				if _, err := strconv.Atoi(md.str("lagThreshold")); err != nil {
					t.Errorf("%s: lagThreshold %q is not a number", name, md.str("lagThreshold"))
				}
			case "rabbitmq":
				if !queues[md.str("queueName")] {
					t.Errorf("%s: queue %q is not declared in deploy/rabbitmq/definitions.json", name, md.str("queueName"))
				}
				if md.str("hostFromEnv") != "PAYRUDDER_RABBITMQ_URL" || md.str("mode") != "QueueLength" {
					t.Errorf("%s: rabbitmq trigger %v", name, md)
				}
			}
		}
	}
	if seen != len(want) {
		t.Errorf("%d ScaledObjects, want %d", seen, len(want))
	}
}

// KEDA owns a HorizontalPodAutoscaler of its own: a service that asks for both is refused at render time.
func TestKedaAndHpaTogetherAreRefused(t *testing.T) {
	root := repoRoot(t)
	helm := filepath.Join(root, "bin", "helm")
	if _, err := os.Stat(helm); err != nil {
		t.Skipf("%s not built (make tools)", helm)
	}
	cmd := exec.Command(helm, "template", "x", filepath.Join(root, "deploy", "helm", "payrudder"), "-f", values(t, "payrudder", "values-keda.yaml"),
		"--set", "services.aggregator.hpa.enabled=true", "--set", "services.aggregator.hpa.max=4", "--set", "services.aggregator.hpa.cpu=70")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err == nil || !strings.Contains(stderr.String(), "hpa and keda are both enabled") {
		t.Fatalf("helm accepted both: %v %s", err, stderr.String())
	}
}
