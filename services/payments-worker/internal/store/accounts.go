package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/0furkancolak/quotify/pkg/pgxx"
	"github.com/0furkancolak/quotify/pkg/secretbox"
)

// ErrNoAccount: the tenant has no account at that provider in that environment.
var ErrNoAccount = errors.New("store: no such provider account")

// Accounts is the table of a tenant's provider credentials. Settings are sealed; the box is bound to tenant, provider and
// environment, so a row copied elsewhere does not open.
type Accounts struct {
	DB  *pgxpool.Pool
	Box *secretbox.Box
}

// AccountInfo is an account without its secrets.
type AccountInfo struct {
	Provider    string    `json:"provider"`
	Environment string    `json:"environment"`
	IsDefault   bool      `json:"is_default"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func aad(tenant uuid.UUID, provider, env string) []byte {
	return []byte(fmt.Sprintf("%s|%s|%s", tenant, provider, env))
}

// Put stores (or replaces) an account. makeDefault makes it the account live charges go to; the previous default stops being one.
func (a Accounts) Put(ctx context.Context, tenant uuid.UUID, provider, env string, settings []byte, makeDefault bool) error {
	sealed, err := a.Box.Seal(settings, aad(tenant, provider, env))
	if err != nil {
		return err
	}
	return pgxx.InTx(ctx, a.DB, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if makeDefault {
			if _, err := tx.Exec(ctx, `UPDATE provider_accounts SET is_default = false WHERE tenant_id = $1 AND environment = $2 AND is_default AND provider <> $3`,
				tenant, env, provider); err != nil {
				return err
			}
		}
		_, err := tx.Exec(ctx, `INSERT INTO provider_accounts (tenant_id, provider, environment, is_default, settings_enc)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (tenant_id, provider, environment) DO UPDATE
			SET settings_enc = EXCLUDED.settings_enc, is_default = provider_accounts.is_default OR EXCLUDED.is_default, updated_at = now()`,
			tenant, provider, env, makeDefault, sealed)
		return err
	})
}

// Delete removes an account. It reports whether there was one.
func (a Accounts) Delete(ctx context.Context, tenant uuid.UUID, provider, env string) (bool, error) {
	tag, err := a.DB.Exec(ctx, `DELETE FROM provider_accounts WHERE tenant_id = $1 AND provider = $2 AND environment = $3`, tenant, provider, env)
	return tag.RowsAffected() == 1, err
}

// List returns a tenant's accounts without secrets.
func (a Accounts) List(ctx context.Context, tenant uuid.UUID) ([]AccountInfo, error) {
	rows, err := a.DB.Query(ctx, `SELECT provider, environment, is_default, updated_at FROM provider_accounts WHERE tenant_id = $1 ORDER BY environment, provider`, tenant)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AccountInfo{}
	for rows.Next() {
		var i AccountInfo
		if err := rows.Scan(&i.Provider, &i.Environment, &i.IsDefault, &i.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// Settings opens an account's settings. updated lets callers cache what they built from it.
func (a Accounts) Settings(ctx context.Context, tenant uuid.UUID, provider, env string) (settings []byte, updated time.Time, err error) {
	var sealed []byte
	err = a.DB.QueryRow(ctx, `SELECT settings_enc, updated_at FROM provider_accounts WHERE tenant_id = $1 AND provider = $2 AND environment = $3`,
		tenant, provider, env).Scan(&sealed, &updated)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, time.Time{}, ErrNoAccount
	}
	if err != nil {
		return nil, time.Time{}, err
	}
	settings, err = a.Box.Open(sealed, aad(tenant, provider, env))
	return settings, updated, err
}

// Default returns the provider live charges of the tenant go to.
func (a Accounts) Default(ctx context.Context, tenant uuid.UUID, env string) (string, bool, error) {
	var p string
	err := a.DB.QueryRow(ctx, `SELECT provider FROM provider_accounts WHERE tenant_id = $1 AND environment = $2 AND is_default`, tenant, env).Scan(&p)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	return p, err == nil, err
}
