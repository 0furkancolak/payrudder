package meters

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/usage"
)

type fakePlans struct {
	plansv1.PlansServiceClient
	calls atomic.Int32
	err   error
}

func (f *fakePlans) GetMeter(ctx context.Context, in *plansv1.GetMeterRequest, _ ...grpc.CallOption) (*plansv1.GetMeterResponse, error) {
	f.calls.Add(1)
	if f.err != nil {
		return nil, f.err
	}
	return &plansv1.GetMeterResponse{Meter: &plansv1.Meter{Code: in.GetCode(), Aggregation: plansv1.Aggregation_AGGREGATION_MAX, QuantityScale: 3}}, nil
}

func TestGetCachesAndMapsNotFound(t *testing.T) {
	notFound := errors.New("service not-found error")
	f := &fakePlans{}
	c := New(f, time.Minute, notFound)
	tn := uuid.New()
	for range 3 {
		m, err := c.Get(context.Background(), tn, "api")
		if err != nil || m.Code != "api" || m.Aggregation != usage.Max || m.Scale != 3 {
			t.Fatalf("Get = %+v %v", m, err)
		}
	}
	if f.calls.Load() != 1 {
		t.Fatalf("plans-svc called %d times, want 1", f.calls.Load())
	}

	f.err = status.Error(codes.NotFound, "no such meter")
	if _, err := c.Get(context.Background(), tn, "ghost"); !errors.Is(err, notFound) {
		t.Fatalf("err = %v, want the service's not-found error", err)
	}
	f.err = status.Error(codes.Unavailable, "down")
	if _, err := c.Get(context.Background(), tn, "other"); status.Code(err) != codes.Unavailable {
		t.Fatalf("err = %v, want the transport error", err)
	}
}
