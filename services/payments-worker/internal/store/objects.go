package store

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Objects remembers what Quotify created at a provider: the provider's id for each of Quotify's names.
type Objects struct{ DB *pgxpool.Pool }

// Object is one such mapping.
type Object struct {
	Kind string `json:"kind"`
	Key  string `json:"key"`
	Ref  string `json:"ref"`
}

// Get returns the provider's id for a key.
func (o Objects) Get(ctx context.Context, tenant uuid.UUID, provider, env, kind, key string) (string, bool, error) {
	var ref string
	err := o.DB.QueryRow(ctx, `SELECT ref FROM provider_objects WHERE tenant_id = $1 AND provider = $2 AND environment = $3 AND kind = $4 AND key = $5`,
		tenant, provider, env, kind, key).Scan(&ref)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	return ref, err == nil, err
}

// Put records a mapping. The first mapping of a key wins: a repeated put changes nothing, so two concurrent syncs agree on one id.
func (o Objects) Put(ctx context.Context, tenant uuid.UUID, provider, env, kind, key, ref string) error {
	_, err := o.DB.Exec(ctx, `INSERT INTO provider_objects (tenant_id, provider, environment, kind, key, ref) VALUES ($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING`,
		tenant, provider, env, kind, key, ref)
	return err
}

// ByRef returns Quotify's key for a provider id.
func (o Objects) ByRef(ctx context.Context, tenant uuid.UUID, provider, env, kind, ref string) (string, bool, error) {
	var key string
	err := o.DB.QueryRow(ctx, `SELECT key FROM provider_objects WHERE tenant_id = $1 AND provider = $2 AND environment = $3 AND kind = $4 AND ref = $5 LIMIT 1`,
		tenant, provider, env, kind, ref).Scan(&key)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	return key, err == nil, err
}

// List returns a provider's mappings of the given kinds, by kind and key.
func (o Objects) List(ctx context.Context, tenant uuid.UUID, provider, env string, kinds ...string) ([]Object, error) {
	rows, err := o.DB.Query(ctx, `SELECT kind, key, ref FROM provider_objects WHERE tenant_id = $1 AND provider = $2 AND environment = $3 AND kind = ANY($4) ORDER BY kind, key`,
		tenant, provider, env, kinds)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Object
	for rows.Next() {
		var x Object
		if err := rows.Scan(&x.Kind, &x.Key, &x.Ref); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}
