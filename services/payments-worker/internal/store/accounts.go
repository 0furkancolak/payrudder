package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/0furkancolak/payrudder/pkg/secretbox"

	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store/sqlc"
)

// ErrNoAccount: the tenant has no account at that provider in that environment.
var ErrNoAccount = errors.New("store: no such provider account")

// Accounts is the table of a tenant's provider credentials. Settings are sealed; the box is bound to tenant, provider and
// environment, so a row copied elsewhere does not open.
type Accounts struct {
	DB  DB
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

// Audit says who changed an account and what was established when they did; it is written in the same transaction as the change. No secret goes in it.
type Audit struct {
	// Actor is "user:<id>" for a person in the console, "operator" for support, or empty when nobody was named.
	Actor string
	// VerifiedRemote: the provider itself accepted the credentials before they were stored.
	VerifiedRemote bool
}

// Put stores (or replaces) an account. makeDefault makes it the account live charges go to; the previous default stops being one.
func (a Accounts) Put(ctx context.Context, tenant uuid.UUID, provider, env string, settings []byte, makeDefault bool) error {
	return a.PutAudited(ctx, tenant, provider, env, settings, makeDefault, Audit{})
}

// PutAudited is Put with the entry of the audit trail.
func (a Accounts) PutAudited(ctx context.Context, tenant uuid.UUID, provider, env string, settings []byte, makeDefault bool, audit Audit) error {
	sealed, err := a.Box.Seal(settings, aad(tenant, provider, env))
	if err != nil {
		return err
	}
	return tx(ctx, a.DB, tenant, func(_ pgx.Tx, q *sqlc.Queries) error {
		if makeDefault {
			if err := q.ClearDefaultProviderAccounts(ctx, sqlc.ClearDefaultProviderAccountsParams{TenantID: tenant, Environment: env, Provider: provider}); err != nil {
				return err
			}
		}
		_, err := q.GetProviderAccountSettings(ctx, sqlc.GetProviderAccountSettingsParams{TenantID: tenant, Provider: provider, Environment: env})
		action := "connected"
		switch {
		case err == nil:
			action = "replaced"
		case !errors.Is(err, pgx.ErrNoRows):
			return err
		}
		if err := q.UpsertProviderAccount(ctx, sqlc.UpsertProviderAccountParams{
			TenantID: tenant, Provider: provider, Environment: env, IsDefault: makeDefault, SettingsEnc: sealed,
		}); err != nil {
			return err
		}
		return q.InsertProviderAccountEvent(ctx, sqlc.InsertProviderAccountEventParams{TenantID: tenant, Provider: provider, Environment: env, Action: action,
			Actor: audit.Actor, IsDefault: makeDefault, VerifiedRemote: audit.VerifiedRemote})
	})
}

// Delete removes an account. It reports whether there was one.
func (a Accounts) Delete(ctx context.Context, tenant uuid.UUID, provider, env string) (bool, error) {
	return a.DeleteAudited(ctx, tenant, provider, env, Audit{})
}

// DeleteAudited is Delete with the entry of the audit trail (written only when there was an account to remove).
func (a Accounts) DeleteAudited(ctx context.Context, tenant uuid.UUID, provider, env string, audit Audit) (bool, error) {
	var deleted bool
	err := tx(ctx, a.DB, tenant, func(_ pgx.Tx, q *sqlc.Queries) error {
		n, err := q.DeleteProviderAccount(ctx, sqlc.DeleteProviderAccountParams{TenantID: tenant, Provider: provider, Environment: env})
		if err != nil || n != 1 {
			return err
		}
		deleted = true
		return q.InsertProviderAccountEvent(ctx, sqlc.InsertProviderAccountEventParams{TenantID: tenant, Provider: provider, Environment: env, Action: "removed", Actor: audit.Actor})
	})
	return deleted, err
}

// List returns a tenant's accounts without secrets.
func (a Accounts) List(ctx context.Context, tenant uuid.UUID) ([]AccountInfo, error) {
	rows, err := get(ctx, a.DB, tenant, func(q *sqlc.Queries) ([]sqlc.ListProviderAccountsRow, error) {
		return q.ListProviderAccounts(ctx, tenant)
	})
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
	row, ok, err := lookup(ctx, a.DB, tenant, func(q *sqlc.Queries) (sqlc.GetProviderAccountSettingsRow, error) {
		return q.GetProviderAccountSettings(ctx, sqlc.GetProviderAccountSettingsParams{TenantID: tenant, Provider: provider, Environment: env})
	})
	if err != nil {
		return nil, time.Time{}, err
	}
	if !ok {
		return nil, time.Time{}, ErrNoAccount
	}
	settings, err = a.Box.Open(row.SettingsEnc, aad(tenant, provider, env))
	return settings, row.UpdatedAt, err
}

// Default returns the provider live charges of the tenant go to.
func (a Accounts) Default(ctx context.Context, tenant uuid.UUID, env string) (string, bool, error) {
	return lookup(ctx, a.DB, tenant, func(q *sqlc.Queries) (string, error) {
		return q.GetDefaultProvider(ctx, sqlc.GetDefaultProviderParams{TenantID: tenant, Environment: env})
	})
}

// SetDefault makes an existing account the one live charges go to, without touching its settings; the previous default stops being one. It reports whether
// there was such an account.
func (a Accounts) SetDefault(ctx context.Context, tenant uuid.UUID, provider, env string) (bool, error) {
	var found bool
	err := tx(ctx, a.DB, tenant, func(_ pgx.Tx, q *sqlc.Queries) error {
		if err := q.ClearDefaultProviderAccounts(ctx, sqlc.ClearDefaultProviderAccountsParams{TenantID: tenant, Environment: env, Provider: provider}); err != nil {
			return err
		}
		n, err := q.MarkProviderAccountDefault(ctx, sqlc.MarkProviderAccountDefaultParams{TenantID: tenant, Provider: provider, Environment: env})
		if err != nil {
			return err
		}
		if n != 1 {
			// Nothing to promote: leave the tenant's default where it was (returning the error rolls the clear back).
			return ErrNoAccount
		}
		found = true
		return nil
	})
	if errors.Is(err, ErrNoAccount) {
		return false, nil
	}
	return found, err
}
