package auth

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/apikey"
)

// A genuine key whose prefix names one environment and whose tenant is in the other is a hard 401 whatever else is true of it: right secret, active, even
// an active tenant. It is not a lookup miss and not a mapping the code could be talked into.
func TestKeyPrefixThatDisagreesWithTheTenantIsRefused(t *testing.T) {
	for name, tc := range map[string]struct {
		key    apikey.Env
		tenant plansv1.Environment
	}{
		"qk_test_ on a live tenant":    {apikey.EnvTest, plansv1.Environment_ENVIRONMENT_LIVE},
		"qk_live_ on a sandbox tenant": {apikey.EnvLive, plansv1.Environment_ENVIRONMENT_SANDBOX},
		"qk_live_ on no environment":   {apikey.EnvLive, plansv1.Environment_ENVIRONMENT_UNSPECIFIED},
	} {
		k, _, res, a := setup(t, tc.key, tc.tenant, plansv1.ApiKey_STATUS_ACTIVE)
		res.keys[k.Prefix].TenantStatus = plansv1.TenantStatus_TENANT_STATUS_ACTIVE
		if _, err := a.Authenticate(context.Background(), k.String()); !errors.Is(err, ErrUnauthenticated) {
			t.Errorf("%s: err = %v, want ErrUnauthenticated", name, err)
		}
	}
}

func TestInactiveAndSuspendedTenantsAreToldApartFromBadKeys(t *testing.T) {
	for _, st := range []plansv1.TenantStatus{plansv1.TenantStatus_TENANT_STATUS_INACTIVE, plansv1.TenantStatus_TENANT_STATUS_SUSPENDED} {
		k, _, res, a := setup(t, apikey.EnvLive, plansv1.Environment_ENVIRONMENT_LIVE, plansv1.ApiKey_STATUS_ACTIVE)
		res.keys[k.Prefix].TenantStatus = st
		if _, err := a.Authenticate(context.Background(), k.String()); !errors.Is(err, ErrSuspended) {
			t.Errorf("status %v: err = %v, want ErrSuspended", st, err)
		}
	}
	// A wrong secret on a suspended tenant is still just a bad key: the status is told only to the holder of the real one.
	k, _, res, a := setup(t, apikey.EnvLive, plansv1.Environment_ENVIRONMENT_LIVE, plansv1.ApiKey_STATUS_ACTIVE)
	res.keys[k.Prefix].TenantStatus = plansv1.TenantStatus_TENANT_STATUS_SUSPENDED
	wrong := k
	wrong.Secret = "A" + k.Secret[1:]
	if wrong.Secret == k.Secret {
		wrong.Secret = "B" + k.Secret[1:]
	}
	if _, err := a.Authenticate(context.Background(), wrong.String()); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("wrong secret on a suspended tenant: %v", err)
	}
}

func TestRotatedKeyEndsAtItsExpiry(t *testing.T) {
	k, _, res, a := setup(t, apikey.EnvTest, plansv1.Environment_ENVIRONMENT_SANDBOX, plansv1.ApiKey_STATUS_ACTIVE)
	end := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	res.keys[k.Prefix].ExpireTime = timestamppb.New(end)
	now := end.Add(-time.Minute)
	a.now = func() time.Time { return now }
	if _, err := a.Authenticate(context.Background(), k.String()); err != nil {
		t.Fatalf("a minute before the end: %v", err)
	}
	now = end
	if _, err := a.Authenticate(context.Background(), k.String()); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("at the end: %v, want ErrUnauthenticated (the cached entry carries the end time, no eviction needed)", err)
	}
}

func TestScopeTravelsWithThePrincipal(t *testing.T) {
	for _, tc := range []struct {
		stored plansv1.ApiKey_Scope
		want   string
	}{
		{plansv1.ApiKey_SCOPE_UNSPECIFIED, ScopeFull}, // a key from before scopes
		{plansv1.ApiKey_SCOPE_FULL, ScopeFull},
		{plansv1.ApiKey_SCOPE_INGEST, ScopeIngest},
		{plansv1.ApiKey_SCOPE_READ, ScopeRead},
	} {
		k, _, res, a := setup(t, apikey.EnvTest, plansv1.Environment_ENVIRONMENT_SANDBOX, plansv1.ApiKey_STATUS_ACTIVE)
		res.keys[k.Prefix].Scope = tc.stored
		p, err := a.Authenticate(context.Background(), k.String())
		if err != nil {
			t.Fatal(err)
		}
		if p.Scope != tc.want {
			t.Errorf("scope %v: principal scope = %q, want %q", tc.stored, p.Scope, tc.want)
		}
	}
}

type recordingToucher struct {
	mu   sync.Mutex
	ids  []string
	done chan struct{}
}

func (r *recordingToucher) Touch(_ context.Context, id string) error {
	r.mu.Lock()
	r.ids = append(r.ids, id)
	r.mu.Unlock()
	r.done <- struct{}{}
	return nil
}

// A busy key does not turn into a stream of writes: one replica tells plans-svc at most once per window, and a new window tells it again.
func TestLastUsedIsReportedAtMostOncePerWindow(t *testing.T) {
	k, _, _, a := setup(t, apikey.EnvTest, plansv1.Environment_ENVIRONMENT_SANDBOX, plansv1.ApiKey_STATUS_ACTIVE)
	tc := &recordingToucher{done: make(chan struct{}, 10)}
	a.WithToucher(tc)
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	a.now = func() time.Time { return now }
	for range 50 {
		if _, err := a.Authenticate(context.Background(), k.String()); err != nil {
			t.Fatal(err)
		}
	}
	<-tc.done
	now = now.Add(touchEvery + time.Second)
	if _, err := a.Authenticate(context.Background(), k.String()); err != nil {
		t.Fatal(err)
	}
	<-tc.done
	tc.mu.Lock()
	defer tc.mu.Unlock()
	if len(tc.ids) != 2 || tc.ids[0] != "key-1" {
		t.Errorf("touched %v, want key-1 twice (once per window)", tc.ids)
	}
}

// A rejected key does not count as a use.
func TestRejectedKeysAreNotUses(t *testing.T) {
	k, _, res, a := setup(t, apikey.EnvLive, plansv1.Environment_ENVIRONMENT_LIVE, plansv1.ApiKey_STATUS_ACTIVE)
	res.keys[k.Prefix].TenantStatus = plansv1.TenantStatus_TENANT_STATUS_SUSPENDED
	tc := &recordingToucher{done: make(chan struct{}, 1)}
	a.WithToucher(tc)
	_, _ = a.Authenticate(context.Background(), k.String())
	select {
	case <-tc.done:
		t.Error("a suspended tenant's key was recorded as used")
	case <-time.After(50 * time.Millisecond):
	}
}
