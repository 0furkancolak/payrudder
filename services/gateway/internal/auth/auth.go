// Package auth authenticates API keys at the gateway and resolves the tenant.
//
// Lookup order: Redis cache gw:apikey:{prefix} (60 s TTL) → plans-svc AdminService.ResolveApiKey on a miss.
// The secret is verified with a constant-time compare of sha256(secret) against the stored hash; the cache never
// holds anything that would authenticate on its own.
package auth

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/apikey"
	"github.com/0furkancolak/quotify/pkg/env"
	"github.com/0furkancolak/quotify/pkg/tenant"
)

// CacheTTL bounds how stale a cached key can be if the revocation eviction fails.
const CacheTTL = 60 * time.Second

var (
	// ErrUnauthenticated: missing, malformed, unknown, revoked or wrong key. Clients never learn which.
	ErrUnauthenticated = errors.New("auth: invalid api key")
	// ErrUnavailable: the key isn't cached and plans-svc can't be reached.
	ErrUnavailable = errors.New("auth: key resolution unavailable")
	// ErrSuspended: the key is genuine but its tenant is not active (suspended by an operator, or a live tenant that was never activated). Unlike a bad
	// key this tells the caller the truth: they hold a real key and have to talk to the operator.
	ErrSuspended = errors.New("auth: the tenant is not active")
)

// Key scopes (the plans-svc names).
const (
	ScopeFull   = "full"
	ScopeIngest = "ingest"
	ScopeRead   = "read"
)

// Principal is an authenticated caller.
type Principal struct {
	Tenant tenant.Info
	Tier   string
	// Scope is what the key may do: ScopeFull, ScopeIngest or ScopeRead. Empty means a caller that is not an API key (an operator, a signed-in person),
	// whose limits are decided elsewhere.
	Scope string
}

// Entry is the cached resolution of a key prefix.
type Entry struct {
	APIKeyID    string `json:"api_key_id"`
	TenantID    string `json:"tenant_id"`
	Environment string `json:"environment"` // sandbox | live
	Timezone    string `json:"timezone"`
	Tier        string `json:"tier"`
	Active      bool   `json:"active"`
	KeyHash     []byte `json:"key_hash"`
	// Status is the tenant's: "inactive", "active" or "suspended". Empty (an entry written by an older release) is active.
	Status string `json:"status,omitempty"`
	Scope  string `json:"scope,omitempty"`
	// ExpiresAt is the unix time the key stops working although it is not revoked (the old key of a rotation); 0 for a key without an end.
	ExpiresAt int64 `json:"expires_at,omitempty"`
}

// Cache stores entries by prefix.
type Cache interface {
	Get(ctx context.Context, prefix string) (Entry, bool, error)
	Set(ctx context.Context, prefix string, e Entry) error
}

// Resolver is the subset of AdminServiceClient the gateway uses.
type Resolver interface {
	ResolveApiKey(ctx context.Context, prefix string) (*plansv1.ResolveApiKeyResponse, error)
}

// Toucher records that a key was used (plans-svc TouchApiKey).
type Toucher interface {
	Touch(ctx context.Context, apiKeyID string) error
}

// touchEvery is how often one replica tells plans-svc that one key is in use; "last used" is only as exact as this.
const touchEvery = 5 * time.Minute

// maxTouched bounds the memory of the touch throttle (a key costs about 100 bytes).
const maxTouched = 20_000

// Authenticator verifies keys.
type Authenticator struct {
	cache       Cache
	resolver    Resolver
	allowedEnvs []string
	log         *slog.Logger
	failures    metric.Int64Counter
	now         func() time.Time
	toucher     Toucher
	touchMu     sync.Mutex
	touched     map[string]time.Time
}

// New returns an Authenticator.
func New(cache Cache, resolver Resolver, allowedEnvs []string, log *slog.Logger) (*Authenticator, error) {
	c, err := otel.Meter("github.com/0furkancolak/quotify/services/gateway/auth").
		Int64Counter("gateway_auth_failures", metric.WithDescription("Rejected API keys by reason"))
	if err != nil {
		return nil, err
	}
	return &Authenticator{cache: cache, resolver: resolver, allowedEnvs: allowedEnvs, log: log, failures: c, now: time.Now, touched: map[string]time.Time{}}, nil
}

// WithToucher makes the authenticator record the use of keys, at most once per key and replica every few minutes, in the background.
func (a *Authenticator) WithToucher(t Toucher) *Authenticator {
	a.toucher = t
	return a
}

// touch tells plans-svc a key was just used, unless this replica did so lately. It never delays or fails the request.
func (a *Authenticator) touch(keyID string) {
	if a.toucher == nil {
		return
	}
	now := a.now()
	a.touchMu.Lock()
	if last, ok := a.touched[keyID]; ok && now.Sub(last) < touchEvery {
		a.touchMu.Unlock()
		return
	}
	if len(a.touched) >= maxTouched {
		clear(a.touched) // a flood of distinct keys: start again rather than grow
	}
	a.touched[keyID] = now
	a.touchMu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := a.toucher.Touch(ctx, keyID); err != nil {
			a.log.WarnContext(ctx, "api key use not recorded", "api_key_id", keyID, "error", err)
		}
	}()
}

func (a *Authenticator) fail(ctx context.Context, reason string) error {
	a.failures.Add(ctx, 1, metric.WithAttributes(attribute.String("reason", reason)))
	return ErrUnauthenticated
}

// Authenticate verifies a plaintext key (the Bearer token).
func (a *Authenticator) Authenticate(ctx context.Context, plaintext string) (Principal, error) {
	key, err := apikey.Parse(plaintext)
	if err != nil {
		return Principal{}, a.fail(ctx, "malformed")
	}
	e, err := a.lookup(ctx, key.Prefix)
	if err != nil {
		if errors.Is(err, ErrUnauthenticated) {
			return Principal{}, a.fail(ctx, "unknown")
		}
		return Principal{}, err
	}
	if subtle.ConstantTimeCompare(key.Hash(), e.KeyHash) != 1 {
		return Principal{}, a.fail(ctx, "secret")
	}
	if !e.Active {
		return Principal{}, a.fail(ctx, "revoked")
	}
	// The prefix names an environment and the tenant has one: they must be the same, and the deployment must serve it. A qk_test_ key on a live tenant (or the
	// reverse) is a hard 401, not a lookup miss, whatever else is true of it.
	wantEnv, err := env.KeyEnv(key.Env)
	if err != nil {
		return Principal{}, a.fail(ctx, "environment")
	}
	gotEnv, err := env.Parse(e.Environment)
	if err != nil || gotEnv != wantEnv || !env.Allowed(a.allowedEnvs, gotEnv) {
		return Principal{}, a.fail(ctx, "environment")
	}
	now := a.now()
	if e.ExpiresAt != 0 && !now.Before(time.Unix(e.ExpiresAt, 0)) {
		return Principal{}, a.fail(ctx, "expired")
	}
	if e.Status != "" && e.Status != "active" {
		a.failures.Add(ctx, 1, metric.WithAttributes(attribute.String("reason", "tenant_"+e.Status)))
		return Principal{}, ErrSuspended
	}
	id, err := uuid.Parse(e.TenantID)
	if err != nil {
		return Principal{}, a.fail(ctx, "tenant")
	}
	scope := e.Scope
	if scope == "" {
		scope = ScopeFull
	}
	a.touch(e.APIKeyID)
	return Principal{
		Tenant: tenant.Info{ID: id, Environment: e.Environment, Timezone: e.Timezone, APIKeyID: e.APIKeyID},
		Tier:   e.Tier,
		Scope:  scope,
	}, nil
}

func (a *Authenticator) lookup(ctx context.Context, prefix string) (Entry, error) {
	if a.cache != nil {
		e, ok, err := a.cache.Get(ctx, prefix)
		switch {
		case err != nil:
			// Redis down: fall through to plans-svc.
			a.log.WarnContext(ctx, "api key cache unavailable", "error", err)
		case ok:
			return e, nil
		}
	}
	res, err := a.resolver.ResolveApiKey(ctx, prefix)
	if err != nil {
		switch status.Code(err) {
		case codes.NotFound, codes.InvalidArgument:
			return Entry{}, ErrUnauthenticated
		default:
			a.log.ErrorContext(ctx, "ResolveApiKey failed", "error", err)
			return Entry{}, ErrUnavailable
		}
	}
	e := Entry{
		APIKeyID: res.GetApiKeyId(), TenantID: res.GetTenantId(), Timezone: res.GetTimezone(),
		Environment: envName(res.GetEnvironment()), Tier: tierName(res.GetTier()),
		Active: res.GetStatus() == plansv1.ApiKey_STATUS_ACTIVE, KeyHash: res.GetKeyHash(),
		Status: statusName(res.GetTenantStatus()), Scope: scopeName(res.GetScope()),
	}
	if res.GetExpireTime() != nil {
		e.ExpiresAt = res.GetExpireTime().AsTime().Unix()
	}
	if a.cache != nil {
		if err := a.cache.Set(ctx, prefix, e); err != nil {
			a.log.WarnContext(ctx, "api key cache write failed", "error", err)
		}
	}
	return e, nil
}

func envName(e plansv1.Environment) string {
	switch e {
	case plansv1.Environment_ENVIRONMENT_SANDBOX:
		return tenant.EnvSandbox
	case plansv1.Environment_ENVIRONMENT_LIVE:
		return tenant.EnvLive
	default:
		return ""
	}
}

// statusName is "" for a status plans-svc did not send (an older release), which counts as active.
func statusName(s plansv1.TenantStatus) string {
	switch s {
	case plansv1.TenantStatus_TENANT_STATUS_INACTIVE:
		return "inactive"
	case plansv1.TenantStatus_TENANT_STATUS_ACTIVE:
		return "active"
	case plansv1.TenantStatus_TENANT_STATUS_SUSPENDED:
		return "suspended"
	case plansv1.TenantStatus_TENANT_STATUS_UNSPECIFIED:
	}
	return ""
}

func scopeName(s plansv1.ApiKey_Scope) string {
	switch s {
	case plansv1.ApiKey_SCOPE_INGEST:
		return ScopeIngest
	case plansv1.ApiKey_SCOPE_READ:
		return ScopeRead
	case plansv1.ApiKey_SCOPE_FULL, plansv1.ApiKey_SCOPE_UNSPECIFIED:
	}
	return ScopeFull
}

func tierName(t plansv1.TenantTier) string {
	switch t {
	case plansv1.TenantTier_TENANT_TIER_STANDARD:
		return "standard"
	case plansv1.TenantTier_TENANT_TIER_ENTERPRISE:
		return "enterprise"
	default:
		return "free"
	}
}

// RedisCache is the shared cache. plans-svc deletes gw:apikey:{prefix} on revocation.
type RedisCache struct {
	rdb redis.Cmdable
}

// NewRedisCache wraps a Redis client.
func NewRedisCache(rdb redis.Cmdable) *RedisCache { return &RedisCache{rdb: rdb} }

func (c *RedisCache) Get(ctx context.Context, prefix string) (Entry, bool, error) {
	raw, err := c.rdb.Get(ctx, "gw:apikey:"+prefix).Bytes()
	if errors.Is(err, redis.Nil) {
		return Entry{}, false, nil
	}
	if err != nil {
		return Entry{}, false, err
	}
	var e Entry
	if err := json.Unmarshal(raw, &e); err != nil {
		return Entry{}, false, nil // treat a corrupt entry as a miss
	}
	return e, true, nil
}

func (c *RedisCache) Set(ctx context.Context, prefix string, e Entry) error {
	raw, err := json.Marshal(e)
	if err != nil {
		return err
	}
	return c.rdb.Set(ctx, "gw:apikey:"+prefix, raw, CacheTTL).Err()
}

// AdminToucher adapts the generated client.
type AdminToucher struct {
	Client plansv1.AdminServiceClient
}

func (t AdminToucher) Touch(ctx context.Context, apiKeyID string) error {
	_, err := t.Client.TouchApiKey(ctx, &plansv1.TouchApiKeyRequest{ApiKeyId: apiKeyID})
	return err
}

// AdminResolver adapts the generated client.
type AdminResolver struct {
	Client plansv1.AdminServiceClient
}

func (r AdminResolver) ResolveApiKey(ctx context.Context, prefix string) (*plansv1.ResolveApiKeyResponse, error) {
	return r.Client.ResolveApiKey(ctx, &plansv1.ResolveApiKeyRequest{Prefix: prefix})
}
