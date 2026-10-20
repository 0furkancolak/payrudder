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

	"github.com/0furkancolak/quotify/services/payments-worker/internal/store/sqlc"
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
		q := sqlc.New(tx)
		if makeDefault {
			if err := q.ClearDefaultProviderAccounts(ctx, sqlc.ClearDefaultProviderAccountsParams{TenantID: tenant, Environment: env, Provider: provider}); err != nil {
				return err
			}
		}
		return q.UpsertProviderAccount(ctx, sqlc.UpsertProviderAccountParams{
			TenantID: tenant, Provider: provider, Environment: env, IsDefault: makeDefault, SettingsEnc: sealed,
		})
	})
}

// Delete removes an account. It reports whether there was one.
func (a Accounts) Delete(ctx context.Context, tenant uuid.UUID, provider, env string) (bool, error) {
	n, err := sqlc.New(a.DB).DeleteProviderAccount(ctx, sqlc.DeleteProviderAccountParams{TenantID: tenant, Provider: provider, Environment: env})
	return n == 1, err
}

// List returns a tenant's accounts without secrets.
func (a Accounts) List(ctx context.Context, tenant uuid.UUID) ([]AccountInfo, error) {
	rows, err := sqlc.New(a.DB).ListProviderAccounts(ctx, tenant)
	if err != nil {
		return nil, err
	}
	out := make([]AccountInfo, len(rows))
	for i, r := range rows {
		out[i] = AccountInfo{Provider: r.Provider, Environment: r.Environment, IsDefault: r.IsDefault, UpdatedAt: r.UpdatedAt}
	}
	return out, nil
}

// Settings opens an account's settings. updated lets callers cache what they built from it.
func (a Accounts) Settings(ctx context.Context, tenant uuid.UUID, provider, env string) (settings []byte, updated time.Time, err error) {
	row, err := sqlc.New(a.DB).GetProviderAccountSettings(ctx, sqlc.GetProviderAccountSettingsParams{TenantID: tenant, Provider: provider, Environment: env})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, time.Time{}, ErrNoAccount
	}
	if err != nil {
		return nil, time.Time{}, err
	}
	settings, err = a.Box.Open(row.SettingsEnc, aad(tenant, provider, env))
	return settings, row.UpdatedAt, err
}

// Default returns the provider live charges of the tenant go to.
func (a Accounts) Default(ctx context.Context, tenant uuid.UUID, env string) (string, bool, error) {
	p, err := sqlc.New(a.DB).GetDefaultProvider(ctx, sqlc.GetDefaultProviderParams{TenantID: tenant, Environment: env})
	return found(p, err)
}
