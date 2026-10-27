package tenantdir

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func TestSchemaNameRoundTrips(t *testing.T) {
	id := uuid.MustParse("0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b")
	if got, want := Schema(id), "t_0190a1b2c3d47e5f8a9b0c1d2e3f4a5b"; got != want {
		t.Fatalf("Schema = %q, want %q", got, want)
	}
	if back, ok := TenantOfSchema(Schema(id)); !ok || back != id {
		t.Fatalf("TenantOfSchema = %v %v", back, ok)
	}
	for _, name := range []string{"public", "shared", "t_", "t_zz", "t_0190A1B2C3D47E5F8A9B0C1D2E3F4A5B", "t_0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b", "x_0190a1b2c3d47e5f8a9b0c1d2e3f4a5b"} {
		if _, ok := TenantOfSchema(name); ok {
			t.Errorf("TenantOfSchema(%q) accepted", name)
		}
	}
}

func TestPlacementIsCachedAndConcurrentMissesShareOneLoad(t *testing.T) {
	var calls atomic.Int32
	release := make(chan struct{})
	d := New(func(_ context.Context, id uuid.UUID) (Placement, error) {
		calls.Add(1)
		<-release
		return Placement{Cell: "c1", Region: "eu", Status: StatusReady}, nil
	}, nil, Options{})
	id := uuid.New()
	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if p, err := d.Placement(context.Background(), id); err != nil || p.Cell != "c1" || p.TenantID != id {
				t.Errorf("Placement = %+v %v", p, err)
			}
		}()
	}
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()
	if _, err := d.Placement(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("loads = %d, want 1", calls.Load())
	}
}

func TestStaleAnswerIsServedWhenTheOwnerIsDownButOnlyForMaxStale(t *testing.T) {
	clk := &clock{t: time.Now()}
	var down atomic.Bool
	d := New(func(context.Context, uuid.UUID) (Placement, error) {
		if down.Load() {
			return Placement{}, errors.New("plans-svc down")
		}
		return Placement{Cell: "c1", Status: StatusReady}, nil
	}, nil, Options{TTL: time.Second, MaxStale: time.Minute, Now: clk.now})
	id := uuid.New()
	if _, err := d.Placement(context.Background(), id); err != nil {
		t.Fatal(err)
	}

	down.Store(true)
	clk.add(10 * time.Second) // past the TTL: it asks, fails, and serves the old answer
	p, err := d.Placement(context.Background(), id)
	if err != nil || p.Cell != "c1" {
		t.Fatalf("stale = %+v %v, want the old answer", p, err)
	}
	clk.add(2 * time.Minute) // past MaxStale: no answer is better than a wrong one
	if _, err := d.Placement(context.Background(), id); err == nil {
		t.Fatal("served an answer older than MaxStale")
	}
	if _, err := d.Placement(context.Background(), uuid.New()); err == nil {
		t.Fatal("a tenant never seen got an answer while the owner is down")
	}

	down.Store(false)
	if p, err := d.Placement(context.Background(), id); err != nil || p.Cell != "c1" {
		t.Fatalf("after recovery = %+v %v", p, err)
	}
}

func TestFreshAnswerWinsAfterTheTTL(t *testing.T) {
	clk := &clock{t: time.Now()}
	cell := "c1"
	d := New(func(context.Context, uuid.UUID) (Placement, error) {
		return Placement{Cell: cell, Status: StatusReady}, nil
	}, nil, Options{TTL: time.Second, Now: clk.now})
	id := uuid.New()
	if p, _ := d.Placement(context.Background(), id); p.Cell != "c1" {
		t.Fatalf("first = %+v", p)
	}
	cell = "c2"
	if p, _ := d.Placement(context.Background(), id); p.Cell != "c1" {
		t.Fatalf("within the TTL the cached answer is served, got %+v", p)
	}
	clk.add(2 * time.Second)
	if p, _ := d.Placement(context.Background(), id); p.Cell != "c2" {
		t.Fatalf("after the TTL = %+v, want c2", p)
	}
	cell = "c3"
	d.Invalidate(id)
	if p, _ := d.Placement(context.Background(), id); p.Cell != "c3" {
		t.Fatalf("after Invalidate = %+v, want c3", p)
	}
}

func TestUnknownTenantIsNotFoundAndNeverServedStale(t *testing.T) {
	clk := &clock{t: time.Now()}
	gone := false
	d := New(func(_ context.Context, id uuid.UUID) (Placement, error) {
		if gone {
			return Placement{}, ErrNotFound
		}
		return Placement{Cell: "c1", Status: StatusReady}, nil
	}, nil, Options{TTL: time.Second, Now: clk.now})
	id := uuid.New()
	if _, err := d.Placement(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	gone = true
	clk.add(5 * time.Second)
	if _, err := d.Placement(context.Background(), id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestNotServingMatchesTheSentinel(t *testing.T) {
	p := Placement{TenantID: uuid.New(), Cell: "c1", Status: StatusMoving}
	if p.Serving() || p.Pooled() {
		t.Fatalf("%+v: serving or pooled", p)
	}
	if err := NotServing(p); !errors.Is(err, ErrNotServing) {
		t.Fatalf("err = %v", err)
	}
}

func TestFixedPlacesUnknownTenantsInThePool(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	f := Fixed{a: {TenantID: a, Cell: "c1", Status: StatusReady}}
	if p, _ := f.Placement(context.Background(), a); p.Cell != "c1" {
		t.Fatalf("a = %+v", p)
	}
	if p, _ := f.Placement(context.Background(), b); !p.Pooled() || !p.Serving() {
		t.Fatalf("b = %+v, want pooled and serving", p)
	}
}
