package auth

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/apikey"
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

// A frozen tenant's key still opens the door, read-only; a banned tenant's key stops like a suspended one.
func TestFrozenTenantKeyIsReadOnlyAndBannedStops(t *testing.T) {
	k, _, res, a := setup(t, apikey.EnvLive, plansv1.Environment_ENVIRONMENT_LIVE, plansv1.ApiKey_STATUS_ACTIVE)
	res.keys[k.Prefix].TenantStatus = plansv1.TenantStatus_TENANT_STATUS_FROZEN
	p, err := a.Authenticate(context.Background(), k.String())
	if err != nil || !p.ReadOnly {
		t.Fatalf("frozen: %+v %v, want an admitted read-only principal", p, err)
	}
	k, _, res, a = setup(t, apikey.EnvLive, plansv1.Environment_ENVIRONMENT_LIVE, plansv1.ApiKey_STATUS_ACTIVE)
	res.keys[k.Prefix].TenantStatus = plansv1.TenantStatus_TENANT_STATUS_BANNED
	if _, err := a.Authenticate(context.Background(), k.String()); !errors.Is(err, ErrSuspended) {
		t.Errorf("banned: err = %v, want ErrSuspended", err)
	}
	k, _, res, a = setup(t, apikey.EnvLive, plansv1.Environment_ENVIRONMENT_LIVE, plansv1.ApiKey_STATUS_ACTIVE)
	res.keys[k.Prefix].TenantStatus = plansv1.TenantStatus_TENANT_STATUS_ACTIVE
	if p, err := a.Authenticate(context.Background(), k.String()); err != nil || p.ReadOnly {
		t.Errorf("active: %+v %v, want a principal that can write", p, err)
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

func TestGrantTravelsWithThePrincipal(t *testing.T) {
	custom := []string{apikey.PermCustomersRead, apikey.PermInvoicesRead}
	for _, tc := range []struct {
		name      string
		role      plansRole
		scope     string
		perms     []string
		wantRole  string
		wantCount int
	}{
		{"a key resolved with a role", plansRole{"custom", custom}, "", nil, "custom", 2},
		{"an admin holds everything that exists", plansRole{"admin", nil}, "", nil, "admin", len(apikey.Permissions)},
		{"a cached entry from before roles: full", plansRole{}, "full", nil, "admin", len(apikey.Permissions)},
		{"a cached entry from before roles: ingest", plansRole{}, "ingest", nil, "ingest_only", 1},
		{"a cached entry with a scope nobody knows holds nothing", plansRole{}, "mystery", nil, "custom", 0},
	} {
		k, cache, res, a := setup(t, apikey.EnvTest, plansv1.Environment_ENVIRONMENT_SANDBOX, plansv1.ApiKey_STATUS_ACTIVE)
		if tc.role.role != "" {
			res.keys[k.Prefix].Role, res.keys[k.Prefix].Permissions = tc.role.role, tc.role.perms
		}
		if tc.scope != "" {
			cache.m[k.Prefix] = Entry{APIKeyID: "k", TenantID: "0192f0b4-6f6e-7c3a-9a3e-6a1f2b3c4d5e", Environment: "sandbox", Active: true, KeyHash: k.Hash(), Scope: tc.scope}
		}
		p, err := a.Authenticate(context.Background(), k.String())
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if p.Role != tc.wantRole || len(p.Permissions) != tc.wantCount {
			t.Errorf("%s: principal %q with %d permissions, want %q with %d", tc.name, p.Role, len(p.Permissions), tc.wantRole, tc.wantCount)
		}
	}
}

type plansRole struct {
	role  string
	perms []string
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

// A key that comes back with no role, no permissions and no scope holds nothing. It used to be read as the scope "" of a key from before roles, which means full: any
// half-filled answer (an older plans-svc, a bug) made an admin.
func TestAKeyWithNeitherRoleNorScopeHoldsNothing(t *testing.T) {
	k, shared, res, a := setup(t, apikey.EnvTest, plansv1.Environment_ENVIRONMENT_SANDBOX, plansv1.ApiKey_STATUS_ACTIVE)
	p, err := a.Authenticate(context.Background(), k.String())
	if err != nil {
		t.Fatal(err)
	}
	if p.Role == apikey.RoleAdmin || p.Role == "" || len(p.Permissions) != 0 {
		t.Fatalf("a key with no grant became %q with %d permissions, want a role that holds nothing", p.Role, len(p.Permissions))
	}
	// The same through a cache entry (Redis) that has neither.
	cache := &memCache{m: map[string]Entry{k.Prefix: {APIKeyID: "k", TenantID: "0192f0b4-6f6e-7c3a-9a3e-6a1f2b3c4d5e", Environment: "sandbox", Active: true, KeyHash: k.Hash()}}}
	a2, _ := New(cache, res, []string{"sandbox", "live"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if p, err := a2.Authenticate(context.Background(), k.String()); err != nil || p.Role == apikey.RoleAdmin || len(p.Permissions) != 0 {
		t.Errorf("cache entry with no grant: %+v %v", p, err)
	}
	// Permissions without a role are a custom key with those permissions.
	res.keys[k.Prefix].Permissions = []string{apikey.PermCustomersRead}
	delete(shared.m, k.Prefix)
	if p, err := a.Authenticate(context.Background(), k.String()); err != nil || p.Role != apikey.RoleCustom || len(p.Permissions) != 1 {
		t.Errorf("permissions without a role: %+v %v", p, err)
	}
}

// Mixed versions: plans-svc from before roles answers with the scope only. The gateway has to carry it: it used to drop it and read the empty scope as full, so an
// ingest-only key became an admin.
func TestTheScopeOfAnOlderPlansSvcIsCarried(t *testing.T) {
	for name, tc := range map[string]struct {
		scope     plansv1.ApiKey_Scope
		wantRole  string
		wantCount int
	}{
		"full":   {plansv1.ApiKey_SCOPE_FULL, apikey.RoleAdmin, len(apikey.Permissions)},
		"ingest": {plansv1.ApiKey_SCOPE_INGEST, apikey.RoleIngestOnly, 1},
		"read":   {plansv1.ApiKey_SCOPE_READ, apikey.RoleReadOnly, -1},
	} {
		k, _, res, a := setup(t, apikey.EnvTest, plansv1.Environment_ENVIRONMENT_SANDBOX, plansv1.ApiKey_STATUS_ACTIVE)
		res.keys[k.Prefix].Scope = tc.scope
		p, err := a.Authenticate(context.Background(), k.String())
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if p.Role != tc.wantRole || (tc.wantCount >= 0 && len(p.Permissions) != tc.wantCount) || (tc.wantCount < 0 && (len(p.Permissions) == 0 || apikey.Allows(p.Permissions, apikey.PermEventsWrite))) {
			t.Errorf("%s: principal %q with %d permissions, want %q", name, p.Role, len(p.Permissions), tc.wantRole)
		}
	}
}

// Random well-formed keys miss every cache. A prefix plans-svc does not know, or a key it revoked, is remembered for a short while so the same guess does not cost
// plans-svc a call each time; the memory is bounded.
func TestAnUnknownOrRevokedKeyIsAskedOnceAWhile(t *testing.T) {
	k, _, res, a := setup(t, apikey.EnvLive, plansv1.Environment_ENVIRONMENT_LIVE, plansv1.ApiKey_STATUS_ACTIVE)
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	a.now = func() time.Time { return now }
	a.neg.SetClock(func() time.Time { return now })
	ghost := apikey.Key{Env: apikey.EnvLive, Prefix: "ZZZZZZZZ", Secret: k.Secret}
	for range 5 {
		if _, err := a.Authenticate(context.Background(), ghost.String()); !errors.Is(err, ErrUnauthenticated) {
			t.Fatal(err)
		}
	}
	if res.calls != 1 {
		t.Fatalf("plans-svc was asked %d times about one unknown prefix, want 1", res.calls)
	}
	// Another secret behind the same prefix is the same question.
	other := ghost
	other.Secret = k.Secret[1:] + "x"
	_, _ = a.Authenticate(context.Background(), other.String())
	if res.calls != 1 {
		t.Errorf("another secret behind the same prefix asked again: %d calls", res.calls)
	}
	// It is forgotten after a while (a key made meanwhile must be able to work).
	now = now.Add(NegativeTTL + time.Second)
	_, _ = a.Authenticate(context.Background(), ghost.String())
	if res.calls != 2 {
		t.Errorf("after the time is up plans-svc was asked %d times in all, want 2", res.calls)
	}
	// The real key is not affected by the unknown ones.
	if _, err := a.Authenticate(context.Background(), k.String()); err != nil {
		t.Errorf("the genuine key: %v", err)
	}

	// A revoked key: found once, then refused without asking, even if the shared cache is down.
	rk, cache, rres, ra := setup(t, apikey.EnvLive, plansv1.Environment_ENVIRONMENT_LIVE, plansv1.ApiKey_STATUS_REVOKED)
	cache.err = errors.New("redis down")
	for range 4 {
		if _, err := ra.Authenticate(context.Background(), rk.String()); !errors.Is(err, ErrUnauthenticated) {
			t.Fatal(err)
		}
	}
	if rres.calls != 1 {
		t.Errorf("a revoked key cost %d calls with the cache down, want 1", rres.calls)
	}
}

func TestTheUnknownKeyMemoryIsBounded(t *testing.T) {
	k, _, res, a := setup(t, apikey.EnvLive, plansv1.Environment_ENVIRONMENT_LIVE, plansv1.ApiKey_STATUS_ACTIVE)
	for i := range NegativeMax + 500 {
		ghost := apikey.Key{Env: apikey.EnvLive, Prefix: fmt.Sprintf("%08d", i), Secret: k.Secret}
		_, _ = a.Authenticate(context.Background(), ghost.String())
	}
	if n := a.neg.Len(); n > NegativeMax {
		t.Errorf("the memory holds %d guesses, want at most %d", n, NegativeMax)
	}
	if res.calls != NegativeMax+500 {
		t.Errorf("calls = %d", res.calls)
	}
}
