package main

import "testing"

// `replay start --wait` read the job id with an unchecked type assertion, so an answer without one (a proxy's error page, a changed API) made the tool panic.
func TestJobIDOfAStartAnswer(t *testing.T) {
	if id, err := jobID(map[string]any{"id": "job-1"}); err != nil || id != "job-1" {
		t.Fatalf("id = %q, err = %v", id, err)
	}
	for name, out := range map[string]map[string]any{
		"no body":      nil,
		"no id":        {"status": "REPLAY_STATUS_PENDING"},
		"empty id":     {"id": ""},
		"id not text":  {"id": 7},
		"id is a null": {"id": nil},
	} {
		if id, err := jobID(out); err == nil {
			t.Errorf("%s: got id %q, want an error", name, id)
		}
	}
}
