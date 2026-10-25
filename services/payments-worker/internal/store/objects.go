package store

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store/sqlc"
)

// Objects remembers what Payrudder created at a provider: the provider's id for each of Payrudder's names.
type Objects struct{ DB *pgxpool.Pool }

// Object is one such mapping.
type Object struct {
	Kind string `json:"kind"`
	Key  string `json:"key"`
	Ref  string `json:"ref"`
}

// Get returns the provider's id for a key.
func (o Objects) Get(ctx context.Context, tenant uuid.UUID, provider, env, kind, key string) (string, bool, error) {
	return found(sqlc.New(o.DB).GetProviderObjectRef(ctx, sqlc.GetProviderObjectRefParams{
		TenantID: tenant, Provider: provider, Environment: env, Kind: kind, Key: key,
	}))
}

// Put records a mapping. The first mapping of a key wins: a repeated put changes nothing, so two concurrent syncs agree on one id.
func (o Objects) Put(ctx context.Context, tenant uuid.UUID, provider, env, kind, key, ref string) error {
	return sqlc.New(o.DB).PutProviderObject(ctx, sqlc.PutProviderObjectParams{
		TenantID: tenant, Provider: provider, Environment: env, Kind: kind, Key: key, Ref: ref,
	})
}

// ByRef returns Payrudder's key for a provider id.
func (o Objects) ByRef(ctx context.Context, tenant uuid.UUID, provider, env, kind, ref string) (string, bool, error) {
	return found(sqlc.New(o.DB).GetProviderObjectKey(ctx, sqlc.GetProviderObjectKeyParams{
		TenantID: tenant, Provider: provider, Environment: env, Kind: kind, Ref: ref,
	}))
}

// List returns a provider's mappings of the given kinds, by kind and key.
func (o Objects) List(ctx context.Context, tenant uuid.UUID, provider, env string, kinds ...string) ([]Object, error) {
	rows, err := sqlc.New(o.DB).ListProviderObjects(ctx, sqlc.ListProviderObjectsParams{TenantID: tenant, Provider: provider, Environment: env, Kinds: kinds})
	if err != nil {
		return nil, err
	}
	var out []Object
	for _, r := range rows {
		out = append(out, Object{Kind: r.Kind, Key: r.Key, Ref: r.Ref})
	}
	return out, nil
}
