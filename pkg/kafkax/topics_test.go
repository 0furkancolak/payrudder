package kafkax

import (
	"os"
	"regexp"
	"strconv"
	"testing"
)

// TestTopicsMatchTopicsSh keeps Topics and the compose stack's topics.sh the same: name, partitions, retention or compaction.
func TestTopicsMatchTopicsSh(t *testing.T) {
	b, err := os.ReadFile("../../deploy/compose/redpanda/topics.sh")
	if err != nil {
		t.Fatal(err)
	}
	var want []Topic
	for _, m := range regexp.MustCompile(`(?m)^create (\S+)\s+(\d+)\s+(\S+)\s*$`).FindAllStringSubmatch(string(b), -1) {
		p, _ := strconv.Atoi(m[2])
		days := 0
		if m[3] != "compact" {
			days, _ = strconv.Atoi(m[3])
		}
		want = append(want, Topic{m[1], int32(p), days})
	}
	if len(want) == 0 || len(want) != len(Topics) {
		t.Fatalf("topics.sh has %d topics, Topics %d", len(want), len(Topics))
	}
	for i := range want {
		if want[i] != Topics[i] {
			t.Errorf("topic %d: topics.sh %+v, Topics %+v", i, want[i], Topics[i])
		}
	}
	if c := (Topic{RetentionDays: 0}).configs(); c["cleanup.policy"] != "compact" {
		t.Errorf("compacted configs: %v", c)
	}
	if c := (Topic{RetentionDays: 30}).configs(); c["retention.ms"] != "2592000000" {
		t.Errorf("30 days: %v", c)
	}
}
