package main

import (
	"strings"
	"testing"
)

func TestCellsJobsNeedsNoConfig(t *testing.T) {
	if err := cellsCmd([]string{"jobs"}); err != nil {
		t.Fatal(err)
	}
}

func TestCellsCommandsRefuseWithoutAConfigOrAKnownVerb(t *testing.T) {
	t.Setenv("PAYRUDDER_CELLS_CONFIG", "")
	for _, args := range [][]string{{"doctor"}, {"move", "--tenant", "x", "--to", "c1"}, {"migrate"}} {
		if err := cellsCmd(args); err == nil || !strings.Contains(err.Error(), "--config") {
			t.Errorf("cells %v: %v, want a request for --config", args, err)
		}
	}
	if err := cellsCmd(nil); err == nil {
		t.Error("cells without a verb was accepted")
	}
}

func TestPlaceName(t *testing.T) {
	if placeName("") != "the pooled database" || placeName("c1") != "cell c1" {
		t.Fatalf("placeName: %q %q", placeName(""), placeName("c1"))
	}
}
