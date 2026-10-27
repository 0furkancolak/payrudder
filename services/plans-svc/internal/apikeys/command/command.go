// Package command issues, revokes, expires and touches API keys. The keys of every tenant live behind the system pool (role plans_system, BYPASSRLS):
// a key is found by its prefix before anyone knows whose tenant it is. The commands do no authorisation; the operator RPC and the identity module
// (a member of the tenant's account) each check theirs before calling.
package command

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/apikey"
	"github.com/0furkancolak/payrudder/pkg/env"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/idgen"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/convert"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// Evictor removes a key from the gateway's API-key cache (Redis gw:apikey:{prefix}).
type Evictor interface {
	EvictAPIKey(ctx context.Context, prefix string) error
}

// prefixAttempts is how many times a key is drawn again when its prefix is taken (62^8 values: a collision is rare).
const prefixAttempts = 3

// Commands changes API keys.
type Commands struct {
	pool  *pgxpool.Pool
	evict Evictor // nil without Redis: a revoked key then stays valid in the gateway cache until its TTL
	log   *slog.Logger
}

// New returns the API-key commands on the system pool.
func New(systemPool *pgxpool.Pool, evict Evictor, log *slog.Logger) *Commands {
	return &Commands{pool: systemPool, evict: evict, log: log}
}

func (c *Commands) q() *sqlc.Queries { return sqlc.New(c.pool) }

// Issue makes a key with the role and permissions (already checked by domain.KeyGrant) for the tenant and returns it with its plaintext, which is shown
// once. A live tenant that is not active has no keys (the go-live gate), and neither has a suspended one or an app that was deleted.
func (c *Commands) Issue(ctx context.Context, tenantID uuid.UUID, role string, permissions []string) (*plansv1.ApiKey, string, error) {
	t, err := c.q().GetTenant(ctx, tenantID)
	if err != nil {
		return nil, "", convert.Err(err, "tenant")
	}
	if t.Status != domain.StatusActive {
		return nil, "", errkey.New(codes.FailedPrecondition, "plans.api_key.tenant_not_active", "this environment is not active: finish the go-live checklist first")
	}
	if app, err := c.q().GetApp(ctx, t.AppID); err != nil {
		return nil, "", convert.Err(err, "app")
	} else if app.ArchivedAt != nil {
		return nil, "", errkey.New(codes.FailedPrecondition, "plans.api_key.app_archived", "this app was deleted: it has no keys")
	}
	// The key environment always follows the tenant: test <-> sandbox, live <-> live (ADR 0011).
	tenantEnv, err := env.Parse(t.Environment)
	if err != nil {
		return nil, "", status.Error(codes.Internal, "tenant has no environment")
	}
	keyEnv := tenantEnv.ForKey()
	for range prefixAttempts {
		key, err := apikey.Generate(keyEnv)
		if err != nil {
			return nil, "", status.Error(codes.Internal, "key generation failed")
		}
		row, err := c.q().InsertApiKey(ctx, sqlc.InsertApiKeyParams{
			ID: idgen.NewUUIDv7(), TenantID: t.ID, Prefix: key.Prefix, KeyHash: key.Hash(), Env: string(keyEnv), Role: role, Permissions: permissions,
		})
		if pgxx.ErrCode(err) == pgxx.CodeUniqueViolation {
			continue // prefix collision: draw again
		}
		if err != nil {
			return nil, "", convert.Err(err, "api key")
		}
		c.log.InfoContext(ctx, "api key created", "tenant_id", t.ID, "api_key_id", row.ID, "prefix", row.Prefix, "role", role)
		return convert.APIKey(row), key.String(), nil
	}
	return nil, "", errkey.New(codes.Aborted, "identity.api_key.prefix_unavailable", "could not allocate a unique key prefix; retry")
}

// Revoke revokes the key and evicts it from the gateway cache.
func (c *Commands) Revoke(ctx context.Context, id uuid.UUID) (*plansv1.ApiKey, error) {
	row, err := c.q().RevokeApiKey(ctx, id)
	if err != nil {
		return nil, convert.Err(err, "api key")
	}
	// Revocation takes effect immediately by evicting the gateway cache entry. If that fails, the 60 s cache TTL
	// bounds how long the key keeps working.
	c.evictPrefix(ctx, row.Prefix)
	return convert.APIKey(row), nil
}

// Expire makes an active key stop working at until without revoking it: the old key of a rotation. It never extends a key that already ends sooner.
// The gateway checks the time itself on every request, so the cache entry only has to be dropped to carry the new end time.
func (c *Commands) Expire(ctx context.Context, id uuid.UUID, until time.Time) (*plansv1.ApiKey, error) {
	row, err := c.q().ExpireApiKey(ctx, sqlc.ExpireApiKeyParams{ID: id, ExpiresAt: until})
	if err != nil {
		return nil, convert.Err(err, "api key")
	}
	c.evictPrefix(ctx, row.Prefix)
	return convert.APIKey(row), nil
}

// Touch records that a key was just used. A key that does not exist is not an error: it was revoked and deleted, or the call is late.
func (c *Commands) Touch(ctx context.Context, id uuid.UUID) error {
	if err := c.q().TouchApiKey(ctx, id); err != nil {
		return convert.Err(err, "api key")
	}
	return nil
}

// EvictTenant drops every key of the tenant from the gateway cache at once: a suspended tenant's keys stop working now, not at their cache TTL.
func (c *Commands) EvictTenant(ctx context.Context, tenantID uuid.UUID) error {
	if c.evict == nil {
		return nil
	}
	prefixes, err := c.q().ListTenantApiKeyPrefixes(ctx, tenantID)
	if err != nil {
		return convert.Err(err, "api key")
	}
	for _, p := range prefixes {
		c.evictPrefix(ctx, p)
	}
	return nil
}

func (c *Commands) evictPrefix(ctx context.Context, prefix string) {
	if c.evict == nil {
		return
	}
	if err := c.evict.EvictAPIKey(ctx, prefix); err != nil {
		c.log.WarnContext(ctx, "api key cache eviction failed; bounded by cache TTL", "prefix", prefix, "error", err)
	}
}
