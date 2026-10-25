package replay

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Run returns only after the jobs it started have: the caller closes the pool they use as soon as Run returns.
func TestRunWaitsForItsJobs(t *testing.T) {
	// A pool that never connects: the scan fails and is logged, which is all this test needs of it.
	pool, err := pgxpool.New(context.Background(), "postgres://nobody@127.0.0.1:1/none?connect_timeout=1")
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	m := New(Config{Pool: pool, Scan: time.Hour})
	var finished atomic.Bool
	started := make(chan struct{})
	m.run = func(ctx context.Context, _, _ uuid.UUID) error {
		close(started)
		<-ctx.Done()
		time.Sleep(100 * time.Millisecond) // winding the job down takes a moment
		finished.Store(true)
		return ctx.Err()
	}

	ctx, cancel := context.WithCancel(context.Background())
	m.launch(ctx, &Job{ID: uuid.New(), TenantID: uuid.New()})
	<-started
	done := make(chan struct{})
	go func() { _ = m.Run(ctx); close(done) }()
	cancel()
	<-done
	if !finished.Load() {
		t.Fatal("Run returned while a job was still running")
	}
}
