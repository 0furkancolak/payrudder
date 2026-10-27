package backoff

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestDelayDoublesUpToMax(t *testing.T) {
	r := Retry{Base: time.Second, Max: 8 * time.Second}
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 8 * time.Second}
	for i, w := range want {
		r.failures = i + 1
		if got := r.delay(); got != w {
			t.Fatalf("failure %d: delay %v, want %v", i+1, got, w)
		}
	}
}

func TestLogsOnceThenThrottled(t *testing.T) {
	h := &countHandler{}
	log := newLog(h)
	r := Retry{Base: time.Millisecond, Max: time.Millisecond, Every: time.Hour}
	for range 50 {
		r.Fail(context.Background(), log, "down", errors.New("refused"))
	}
	if h.warns != 1 {
		t.Fatalf("warnings = %d, want 1", h.warns)
	}
	r.OK(context.Background(), log, "back")
	if h.infos != 1 {
		t.Fatalf("recovery lines = %d, want 1", h.infos)
	}
	r.OK(context.Background(), log, "back")
	if h.infos != 1 {
		t.Fatal("OK without a streak must be silent")
	}
}
