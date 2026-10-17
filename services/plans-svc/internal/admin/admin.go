// Package admin implements AdminService. Its operations are cross-tenant by nature (creating accounts, resolving keys),
// so it runs on the system pool (role plans_system, BYPASSRLS). It is the only request-handling package allowed to
// hold that pool; access is restricted to operator calls, except ResolveApiKey (gateway only).
package admin

import (
	"context"
	"errors"
	"log/slog"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/apikey"
	"github.com/0furkancolak/quotify/pkg/idgen"
	"github.com/0furkancolak/quotify/pkg/pgxx"
	"github.com/0furkancolak/quotify/pkg/tenant"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/convert"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/domain"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/store/sqlc"
)

// CacheEvictor removes a key from the gateway's API-key cache (Redis gw:apikey:{prefix}).
type CacheEvictor interface {
	EvictAPIKey(ctx context.Context, prefix string) error
}

// Server implements plansv1.AdminServiceServer.
type Server struct {
	plansv1.UnimplementedAdminServiceServer
	pool        *pgxpool.Pool
	evict       CacheEvictor
	log         *slog.Logger
	allowedEnvs []string
}

// New returns an AdminService on the system pool. allowedEnvs is QUOTIFY_ALLOWED_ENVIRONMENTS ("sandbox", "live").
func New(systemPool *pgxpool.Pool, evict CacheEvictor, allowedEnvs []string, log *slog.Logger) *Server {
	return &Server{pool: systemPool, evict: evict, log: log, allowedEnvs: allowedEnvs}
}

func (s *Server) q() *sqlc.Queries { return sqlc.New(s.pool) }

func (s *Server) CreateAccount(ctx context.Context, req *plansv1.CreateAccountRequest) (*plansv1.CreateAccountResponse, error) {
	if err := tenant.RequireOperator(ctx); err != nil {
		return nil, err
	}
	settings, err := domain.ValidateCreateAccount(req)
	if err != nil {
		return nil, err
	}
	envs := []string{"sandbox"}
	if slices.Contains(s.allowedEnvs, "live") {
		envs = append(envs, "live")
	}
	acct := &plansv1.Account{}
	err = pgxx.InTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		a, err := q.InsertAccount(ctx, sqlc.InsertAccountParams{ID: idgen.NewUUIDv7(), Name: settings.Name})
		if err != nil {
			return err
		}
		acct.Id, acct.Name, acct.CreateTime = a.ID.String(), a.Name, convertTS(a.CreatedAt)
		for _, env := range envs {
			name := settings.Name
			if env == "sandbox" {
				name += " (sandbox)"
			}
			t, err := q.InsertTenant(ctx, sqlc.InsertTenantParams{
				ID: idgen.NewUUIDv7(), AccountID: a.ID, Environment: env, Name: name, Tier: settings.Tier,
				Currency: settings.Currency, Timezone: settings.Timezone, GracePeriod: convert.Interval(settings.Grace),
			})
			if err != nil {
				return err
			}
			acct.Tenants = append(acct.Tenants, convert.Tenant(t))
		}
		return nil
	})
	if err != nil {
		return nil, convert.Err(err, "account")
	}
	s.log.InfoContext(ctx, "account created", "account_id", acct.GetId(), "environments", envs)
	return &plansv1.CreateAccountResponse{Account: acct}, nil
}

func parseID(field, raw string) (uuid.UUID, error) {
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, domain.Invalid(field, "must be a UUID")
	}
	return id, nil
}

func (s *Server) GetTenant(ctx context.Context, req *plansv1.GetTenantRequest) (*plansv1.GetTenantResponse, error) {
	if err := tenant.RequireOperator(ctx); err != nil {
		return nil, err
	}
	id, err := parseID("tenant_id", req.GetTenantId())
	if err != nil {
		return nil, err
	}
	t, err := s.q().GetTenant(ctx, id)
	if err != nil {
		return nil, convert.Err(err, "tenant")
	}
	return &plansv1.GetTenantResponse{Tenant: convert.Tenant(t)}, nil
}

func (s *Server) UpdateTenantSettings(ctx context.Context, req *plansv1.UpdateTenantSettingsRequest) (*plansv1.UpdateTenantSettingsResponse, error) {
	if err := tenant.RequireOperator(ctx); err != nil {
		return nil, err
	}
	id, err := parseID("tenant_id", req.GetTenantId())
	if err != nil {
		return nil, err
	}
	params := sqlc.UpdateTenantSettingsParams{ID: id}
	if req.GetGracePeriod() != nil {
		g, err := domain.ValidateGrace(req.GetGracePeriod().AsDuration())
		if err != nil {
			return nil, domain.Invalid("grace_period", err.Error())
		}
		params.GracePeriod = convert.Interval(g)
	} else {
		params.GracePeriod = pgtype.Interval{}
	}
	if req.GetTier() != plansv1.TenantTier_TENANT_TIER_UNSPECIFIED {
		name, ok := domain.TierToDB(req.GetTier())
		if !ok {
			return nil, domain.Invalid("tier", "unknown tier")
		}
		params.Tier = &name
	}
	t, err := s.q().UpdateTenantSettings(ctx, params)
	if err != nil {
		return nil, convert.Err(err, "tenant")
	}
	return &plansv1.UpdateTenantSettingsResponse{Tenant: convert.Tenant(t)}, nil
}

func (s *Server) CreateApiKey(ctx context.Context, req *plansv1.CreateApiKeyRequest) (*plansv1.CreateApiKeyResponse, error) {
	if err := tenant.RequireOperator(ctx); err != nil {
		return nil, err
	}
	id, err := parseID("tenant_id", req.GetTenantId())
	if err != nil {
		return nil, err
	}
	t, err := s.q().GetTenant(ctx, id)
	if err != nil {
		return nil, convert.Err(err, "tenant")
	}
	// The key environment always follows the tenant: test ↔ sandbox, live ↔ live (ADR 0011).
	env := apikey.EnvLive
	if t.Environment == "sandbox" {
		env = apikey.EnvTest
	}
	for attempt := 0; attempt < 3; attempt++ {
		key, err := apikey.Generate(env)
		if err != nil {
			return nil, status.Error(codes.Internal, "key generation failed")
		}
		row, err := s.q().InsertApiKey(ctx, sqlc.InsertApiKeyParams{
			ID: idgen.NewUUIDv7(), TenantID: t.ID, Prefix: key.Prefix, KeyHash: key.Hash(), Env: string(env),
		})
		if pgxx.ErrCode(err) == pgxx.CodeUniqueViolation {
			continue // prefix collision (62^8 space): draw again
		}
		if err != nil {
			return nil, convert.Err(err, "api key")
		}
		s.log.InfoContext(ctx, "api key created", "tenant_id", t.ID, "api_key_id", row.ID, "prefix", row.Prefix)
		return &plansv1.CreateApiKeyResponse{ApiKey: convert.APIKey(row), Plaintext: key.String()}, nil
	}
	return nil, status.Error(codes.Aborted, "could not allocate a unique key prefix; retry")
}

func (s *Server) RevokeApiKey(ctx context.Context, req *plansv1.RevokeApiKeyRequest) (*plansv1.RevokeApiKeyResponse, error) {
	if err := tenant.RequireOperator(ctx); err != nil {
		return nil, err
	}
	id, err := parseID("api_key_id", req.GetApiKeyId())
	if err != nil {
		return nil, err
	}
	row, err := s.q().RevokeApiKey(ctx, id)
	if err != nil {
		return nil, convert.Err(err, "api key")
	}
	// Revocation takes effect immediately by evicting the gateway cache entry. If that fails, the 60 s cache TTL
	// bounds how long the key keeps working.
	if s.evict != nil {
		if err := s.evict.EvictAPIKey(ctx, row.Prefix); err != nil {
			s.log.WarnContext(ctx, "api key cache eviction failed; bounded by cache TTL", "prefix", row.Prefix, "error", err)
		}
	}
	return &plansv1.RevokeApiKeyResponse{ApiKey: convert.APIKey(row)}, nil
}

func (s *Server) ResolveApiKey(ctx context.Context, req *plansv1.ResolveApiKeyRequest) (*plansv1.ResolveApiKeyResponse, error) {
	if len(req.GetPrefix()) != apikey.PrefixLen {
		return nil, domain.Invalid("prefix", "must be 8 characters")
	}
	r, err := s.q().ResolveApiKey(ctx, req.GetPrefix())
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, status.Error(codes.NotFound, "api key not found")
	}
	if err != nil {
		return nil, convert.Err(err, "api key")
	}
	return &plansv1.ResolveApiKeyResponse{
		ApiKeyId: r.ID.String(), TenantId: r.TenantID.String(), Status: convert.APIKeyStatus(r.Status), KeyHash: r.KeyHash,
		Environment: domain.EnvironmentFromDB(r.Environment), Tier: domain.TierFromDB(r.Tier), Timezone: r.Timezone,
	}, nil
}
