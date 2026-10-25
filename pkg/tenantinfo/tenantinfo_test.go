package tenantinfo

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestTenantIsCachedAndConcurrentMissesShareOneLoad(t *testing.T) {
	var calls atomic.Int32
	release := make(chan struct{})
	c := NewFunc(func(_ context.Context, id uuid.UUID) (Settings, error) {
		calls.Add(1)
		<-release
		return Settings{Timezone: "Europe/Istanbul", Grace: time.Hour}, nil
	}, time.Minute)
	id := uuid.New()
	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if s, err := c.Tenant(context.Background(), id); err != nil || s.Timezone != "Europe/Istanbul" {
				t.Errorf("Tenant = %+v %v", s, err)
			}
		}()
	}
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()
	if _, err := c.Tenant(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("loads = %d, want 1", calls.Load())
	}
}

func TestLoadErrorIsReturnedAndNotCached(t *testing.T) {
	var calls atomic.Int32
	boom := errors.New("plans-svc down")
	c := NewFunc(func(context.Context, uuid.UUID) (Settings, error) {
		if calls.Add(1) == 1 {
			return Settings{}, boom
		}
		return Settings{Timezone: "UTC"}, nil
	}, time.Minute)
	id := uuid.New()
	if _, err := c.Tenant(context.Background(), id); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if s, err := c.Tenant(context.Background(), id); err != nil || s.Timezone != "UTC" {
		t.Fatalf("retry = %+v %v", s, err)
	}
}
