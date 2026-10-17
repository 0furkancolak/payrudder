package auth

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/apikey"
)

type memCache struct {
	mu   sync.Mutex
	m    map[string]Entry
	err  error
	sets int
}

func (c *memCache) Get(_ context.Context, p string) (Entry, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return Entry{}, false, c.err
	}
	e, ok := c.m[p]
	return e, ok, nil
}

func (c *memCache) Set(_ context.Context, p string, e Entry) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[p] = e
	c.sets++
	return nil
}

type fakeResolver struct {
	keys  map[string]*plansv1.ResolveApiKeyResponse
	err   error
	calls int
}

func (f *fakeResolver) ResolveApiKey(_ context.Context, prefix string) (*plansv1.ResolveApiKeyResponse, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	r, ok := f.keys[prefix]
	if !ok {
		return nil, status.Error(codes.NotFound, "nope")
	}
	return r, nil
}

func setup(t *testing.T, env apikey.Env, tenantEnv plansv1.Environment, st plansv1.ApiKey_Status) (apikey.Key, *memCache, *fakeResolver, *Authenticator) {
	t.Helper()
	k, err := apikey.Generate(env)
	if err != nil {
		t.Fatal(err)
	}
	res := &fakeResolver{keys: map[string]*plansv1.ResolveApiKeyResponse{k.Prefix: {
		ApiKeyId: "key-1", TenantId: "0192f0b4-6f6e-7c3a-9a3e-6a1f2b3c4d5e", Status: st, KeyHash: k.Hash(),
		Environment: tenantEnv, Tier: plansv1.TenantTier_TENANT_TIER_STANDARD, Timezone: "Europe/Istanbul",
	}}}
	cache := &memCache{m: map[string]Entry{}}
	a, err := New(cache, res, []string{"sandbox", "live"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return k, cache, res, a
}

func TestAuthenticateCachesResolution(t *testing.T) {
	k, cache, res, a := setup(t, apikey.EnvTest, plansv1.Environment_ENVIRONMENT_SANDBOX, plansv1.ApiKey_STATUS_ACTIVE)
	for range 3 {
		p, err := a.Authenticate(context.Background(), k.String())
		if err != nil {
			t.Fatal(err)
		}
		if p.Tenant.Environment != "sandbox" || p.Tier != "standard" || p.Tenant.Timezone != "Europe/Istanbul" || p.Tenant.APIKeyID != "key-1" {
			t.Fatalf("principal = %+v", p)
		}
	}
	if res.calls != 1 || cache.sets != 1 {
		t.Fatalf("resolver calls = %d, cache sets = %d; want 1 and 1", res.calls, cache.sets)
	}
}

func TestAuthenticateRejects(t *testing.T) {
	k, _, _, a := setup(t, apikey.EnvLive, plansv1.Environment_ENVIRONMENT_LIVE, plansv1.ApiKey_STATUS_ACTIVE)
	wrong := k
	wrong.Secret = "A" + k.Secret[1:]
	if wrong.Secret == k.Secret {
		wrong.Secret = "B" + k.Secret[1:]
	}
	for name, plaintext := range map[string]string{
		"malformed":    "Bearer nonsense",
		"wrong secret": wrong.String(),
		"unknown":      "qk_live_ZZZZZZZZ_" + k.Secret,
		"env mismatch": apikey.Key{Env: apikey.EnvTest, Prefix: k.Prefix, Secret: k.Secret}.String(),
	} {
		if _, err := a.Authenticate(context.Background(), plaintext); !errors.Is(err, ErrUnauthenticated) {
			t.Errorf("%s: err = %v", name, err)
		}
	}

	rk, _, _, ra := setup(t, apikey.EnvLive, plansv1.Environment_ENVIRONMENT_LIVE, plansv1.ApiKey_STATUS_REVOKED)
	if _, err := ra.Authenticate(context.Background(), rk.String()); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("revoked: err = %v", err)
	}
}

func TestCacheDownFallsBackToPlans(t *testing.T) {
	k, cache, res, a := setup(t, apikey.EnvLive, plansv1.Environment_ENVIRONMENT_LIVE, plansv1.ApiKey_STATUS_ACTIVE)
	cache.err = errors.New("redis down")
	if _, err := a.Authenticate(context.Background(), k.String()); err != nil {
		t.Fatal(err)
	}
	res.err = status.Error(codes.Unavailable, "plans down")
	if _, err := a.Authenticate(context.Background(), k.String()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("both down: %v", err)
	}
}

func TestSandboxOnlyDeploymentRejectsLiveKeys(t *testing.T) {
	k, cache, res, _ := setup(t, apikey.EnvLive, plansv1.Environment_ENVIRONMENT_LIVE, plansv1.ApiKey_STATUS_ACTIVE)
	a, _ := New(cache, res, []string{"sandbox"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if _, err := a.Authenticate(context.Background(), k.String()); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("live key in sandbox-only deployment: %v", err)
	}
}
