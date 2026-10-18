package lookup

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
)

type fakePlans struct {
	plansv1.PlansServiceClient
	meterCalls, customerCalls atomic.Int32
	failMeter                 atomic.Bool
	release                   chan struct{} // when set, GetMeter blocks until it is closed
}

func (f *fakePlans) GetMeter(_ context.Context, in *plansv1.GetMeterRequest, _ ...grpc.CallOption) (*plansv1.GetMeterResponse, error) {
	f.meterCalls.Add(1)
	if f.release != nil {
		<-f.release
	}
	switch {
	case f.failMeter.Load():
		return nil, status.Error(codes.Unavailable, "down")
	case in.GetCode() == "gb":
		return &plansv1.GetMeterResponse{Meter: &plansv1.Meter{Code: "gb", Version: 3, QuantityScale: 2}}, nil
	}
	return nil, status.Error(codes.NotFound, "no meter")
}

func (f *fakePlans) ResolveCustomer(_ context.Context, in *plansv1.ResolveCustomerRequest, _ ...grpc.CallOption) (*plansv1.ResolveCustomerResponse, error) {
	f.customerCalls.Add(1)
	if in.GetExternalId() == "cus_1" {
		return &plansv1.ResolveCustomerResponse{Customer: &plansv1.Customer{Id: "uuid-1", ExternalId: "cus_1"}}, nil
	}
	return nil, status.Error(codes.NotFound, "no customer")
}

func TestHitsAreCachedPerTenant(t *testing.T) {
	f := &fakePlans{}
	p := NewPlans(f, Options{})
	ctx := context.Background()
	for range 5 {
		m, ok, err := p.Meter(ctx, "t1", "gb")
		if err != nil || !ok || m.Version != 3 || m.Scale != 2 {
			t.Fatalf("meter = %+v %v %v", m, ok, err)
		}
	}
	if f.meterCalls.Load() != 1 {
		t.Fatalf("plans-svc called %d times for one tenant, want 1", f.meterCalls.Load())
	}
	if _, ok, _ := p.Meter(ctx, "t2", "gb"); !ok || f.meterCalls.Load() != 2 {
		t.Fatalf("another tenant must not share the entry (calls = %d)", f.meterCalls.Load())
	}
	c, ok, err := p.Customer(ctx, "t1", "cus_1")
	if err != nil || !ok || c.ID != "uuid-1" {
		t.Fatalf("customer = %+v %v %v", c, ok, err)
	}
}

func TestMissesAreCachedBrieflyAndErrorsNever(t *testing.T) {
	f := &fakePlans{}
	p := NewPlans(f, Options{MissTTL: 50 * time.Millisecond})
	ctx := context.Background()
	for range 3 {
		if _, ok, err := p.Meter(ctx, "t1", "nope"); ok || err != nil {
			t.Fatalf("unknown meter: ok=%v err=%v", ok, err)
		}
	}
	if f.meterCalls.Load() != 1 {
		t.Fatalf("miss not cached: %d calls", f.meterCalls.Load())
	}
	time.Sleep(80 * time.Millisecond)
	_, _, _ = p.Meter(ctx, "t1", "nope")
	if f.meterCalls.Load() != 2 {
		t.Fatalf("miss did not expire: %d calls", f.meterCalls.Load())
	}

	f.failMeter.Store(true)
	for range 2 {
		if _, _, err := p.Meter(ctx, "t1", "other"); status.Code(err) != codes.Unavailable {
			t.Fatalf("err = %v, want Unavailable", err)
		}
	}
	if f.meterCalls.Load() != 4 {
		t.Fatalf("an error must not be cached: %d calls", f.meterCalls.Load())
	}
}

func TestConcurrentMissesShareOneCall(t *testing.T) {
	f := &fakePlans{release: make(chan struct{})}
	p := NewPlans(f, Options{})
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, ok, err := p.Meter(context.Background(), "t1", "gb"); !ok || err != nil {
				t.Errorf("ok=%v err=%v", ok, err)
			}
		}()
	}
	time.Sleep(50 * time.Millisecond)
	close(f.release)
	wg.Wait()
	if n := f.meterCalls.Load(); n != 1 {
		t.Fatalf("plans-svc called %d times for 20 concurrent callers, want 1", n)
	}
}
