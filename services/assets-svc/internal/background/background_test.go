package background

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"
)

// Run used to return as soon as its last loop ended, while the other two could still be in the middle of a job: the service closed the database and the
// object store under them. It now returns only after every loop has finished the job it was in.
func TestRunWaitsForEveryLoopToFinish(t *testing.T) {
	j := &Jobs{Log: slog.New(slog.DiscardHandler)}
	var finished atomic.Int32
	started := make(chan struct{}, 3)
	job := func(ctx context.Context) error {
		started <- struct{}{}
		<-ctx.Done()
		time.Sleep(50 * time.Millisecond) // winding down after the cancellation
		finished.Add(1)
		return nil
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		j.run(ctx, []loop{{"a", time.Millisecond, job}, {"b", time.Millisecond, job}, {"c", time.Millisecond, job}})
		close(done)
	}()
	for range 3 {
		<-started
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("run did not return")
	}
	if got := finished.Load(); got != 3 {
		t.Fatalf("%d of 3 jobs had finished when run returned", got)
	}
}
