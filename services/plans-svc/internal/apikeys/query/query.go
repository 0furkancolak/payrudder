// Package query finds the API key behind a prefix, for the gateway.
package query

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/apikey"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/convert"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// Queries reads API keys on the system pool.
type Queries struct{ pool *pgxpool.Pool }

// New returns the API-key queries on the system pool.
func New(systemPool *pgxpool.Pool) *Queries { return &Queries{pool: systemPool} }

// Resolve returns what the gateway needs to check a key presented with the given prefix: its hash, tenant, scope and state.
func (q *Queries) Resolve(ctx context.Context, prefix string) (*plansv1.ResolveApiKeyResponse, error) {
	if len(prefix) != apikey.PrefixLen {
		return nil, domain.Invalid("prefix", "must be 8 characters")
	}
	r, err := sqlc.New(q.pool).ResolveApiKey(ctx, prefix)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errkey.New(codes.NotFound, "identity.api_key.not_found", "api key not found")
	}
	if err != nil {
		return nil, convert.Err(err, "api key")
	}
	return &plansv1.ResolveApiKeyResponse{
		ApiKeyId: r.ID.String(), TenantId: r.TenantID.String(), Status: convert.APIKeyStatus(r.Status), KeyHash: r.KeyHash,
		Environment: domain.EnvironmentFromDB(r.Environment), Tier: domain.TierFromDB(r.Tier), Timezone: r.Timezone,
		TenantStatus: domain.StatusFromDB(r.TenantStatus), Scope: domain.ScopeFromDB(r.Scope), ExpireTime: expireTime(r.ExpiresAt),
	}, nil
}

func expireTime(t *time.Time) *timestamppb.Timestamp {
	if t == nil {
		return nil
	}
	return timestamppb.New(*t)
}
