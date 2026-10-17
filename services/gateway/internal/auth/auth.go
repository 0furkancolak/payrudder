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
	"slices"
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
	"github.com/0furkancolak/quotify/pkg/tenant"
)

// CacheTTL bounds how stale a cached key can be if the revocation eviction fails.
const CacheTTL = 60 * time.Second

var (
	// ErrUnauthenticated: missing, malformed, unknown, revoked or wrong key. Clients never learn which.
	ErrUnauthenticated = errors.New("auth: invalid api key")
	// ErrUnavailable: the key isn't cached and plans-svc can't be reached.
	ErrUnavailable = errors.New("auth: key resolution unavailable")
)

// Principal is an authenticated caller.
type Principal struct {
	Tenant tenant.Info
	Tier   string
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

// Authenticator verifies keys.
type Authenticator struct {
	cache       Cache
	resolver    Resolver
	allowedEnvs []string
	log         *slog.Logger
	failures    metric.Int64Counter
}

// New returns an Authenticator.
func New(cache Cache, resolver Resolver, allowedEnvs []string, log *slog.Logger) (*Authenticator, error) {
	c, err := otel.Meter("github.com/0furkancolak/quotify/services/gateway/auth").
		Int64Counter("gateway_auth_failures", metric.WithDescription("Rejected API keys by reason"))
	if err != nil {
		return nil, err
	}
	return &Authenticator{cache: cache, resolver: resolver, allowedEnvs: allowedEnvs, log: log, failures: c}, nil
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
	wantEnv := tenant.EnvLive
	if key.Env == apikey.EnvTest {
		wantEnv = tenant.EnvSandbox
	}
	if e.Environment != wantEnv || !slices.Contains(a.allowedEnvs, e.Environment) {
		return Principal{}, a.fail(ctx, "environment")
	}
	id, err := uuid.Parse(e.TenantID)
	if err != nil {
		return Principal{}, a.fail(ctx, "tenant")
	}
	return Principal{
		Tenant: tenant.Info{ID: id, Environment: e.Environment, Timezone: e.Timezone, APIKeyID: e.APIKeyID},
		Tier:   e.Tier,
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

// AdminResolver adapts the generated client.
type AdminResolver struct {
	Client plansv1.AdminServiceClient
}

func (r AdminResolver) ResolveApiKey(ctx context.Context, prefix string) (*plansv1.ResolveApiKeyResponse, error) {
	return r.Client.ResolveApiKey(ctx, &plansv1.ResolveApiKeyRequest{Prefix: prefix})
}
