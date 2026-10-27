package tenantdir

import (
	"strings"
	"testing"
)

func TestJobsInventoryIsWellFormed(t *testing.T) {
	seen := map[string]bool{}
	schema := map[string]bool{}
	for _, j := range Jobs {
		if j.Service == "" || j.Name == "" || j.Where == "" {
			t.Errorf("incomplete entry %+v", j)
		}
		if seen[j.String()] {
			t.Errorf("%s listed twice", j)
		}
		seen[j.String()] = true
		if j.Name == "tenant schema" {
			schema[j.Service] = true
		}
	}
	for _, j := range Jobs {
		if !schema[j.Service] {
			t.Errorf("%s has jobs but no tenant schema entry", j.Service)
		}
	}
}

func TestACellIsRefusedWhileAnyJobIsUnconverted(t *testing.T) {
	jobs := []Job{{"a", "tenant schema", true, "x"}, {"a", "relay", false, "x"}, {"b", "sweep", false, "x"}, {"b", "tick", false, "x"}}
	err := ReadyOf(jobs)
	if err == nil {
		t.Fatal("ready with three jobs left")
	}
	if want := "3 of 4 jobs"; !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), "a (1), b (2)") {
		t.Fatalf("err = %v", err)
	}
	for i := range jobs {
		jobs[i].Converted = true
	}
	if err := ReadyOf(jobs); err != nil {
		t.Fatalf("all converted but %v", err)
	}
	if len(UnconvertedOf(nil)) != 0 || ReadyOf(nil) != nil {
		t.Fatal("an empty inventory is ready")
	}
}
