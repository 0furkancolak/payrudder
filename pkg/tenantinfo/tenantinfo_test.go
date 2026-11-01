package tenantinfo

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
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

func TestSettingsKnowWhenTheTenantIsOnHold(t *testing.T) {
	for st, want := range map[plansv1.TenantStatus]bool{
		plansv1.TenantStatus_TENANT_STATUS_UNSPECIFIED: false, plansv1.TenantStatus_TENANT_STATUS_ACTIVE: false, plansv1.TenantStatus_TENANT_STATUS_INACTIVE: false,
		plansv1.TenantStatus_TENANT_STATUS_SUSPENDED: true, plansv1.TenantStatus_TENANT_STATUS_FROZEN: true, plansv1.TenantStatus_TENANT_STATUS_BANNED: true,
	} {
		if got := (Settings{Status: st}).OnHold(); got != want {
			t.Errorf("OnHold(%v) = %v, want %v", st, got, want)
		}
	}
}

type fakeAdmin struct {
	plansv1.AdminServiceClient
	tenant *plansv1.Tenant
}

func (f fakeAdmin) GetTenant(context.Context, *plansv1.GetTenantRequest, ...grpc.CallOption) (*plansv1.GetTenantResponse, error) {
	return &plansv1.GetTenantResponse{Tenant: f.tenant}, nil
}

func TestNewCarriesTheStatusOfTheTenant(t *testing.T) {
	c := New(fakeAdmin{tenant: &plansv1.Tenant{Timezone: "UTC", Status: plansv1.TenantStatus_TENANT_STATUS_FROZEN}}, time.Minute)
	s, err := c.Tenant(context.Background(), uuid.New())
	if err != nil || s.Status != plansv1.TenantStatus_TENANT_STATUS_FROZEN || !s.OnHold() {
		t.Fatalf("Tenant = %+v %v", s, err)
	}
}
