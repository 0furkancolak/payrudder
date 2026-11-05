package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/0furkancolak/payrudder/pkg/payments"
	"github.com/0furkancolak/payrudder/pkg/secretbox"
)

// Mirrored writes, the part of step 3 of the account_id migration (ADR 0026) that the shadow comparison waited for: when Mirror is on, every write to provider_accounts or
// einvoice_accounts also writes the account and its binding in the new tables, in the same transaction, so the two never differ for longer than a statement. The old row is
// still the one that is used. Off by default (PAYMENTS_ACCOUNT_MIRROR); a tenant's accounts that were written while it was off are brought level by `payrudderctl secrets
// accounts-backfill`, which is also what makes the shadow comparison read "equal" again.

// mirrorUpsert writes the account the key names (sealed to its own id, as the backfill does) and a binding to it; the binding's default flag is left to mirrorDefaults.
func mirrorUpsert(ctx context.Context, tx pgx.Tx, box *secretbox.Box, tenant uuid.UUID, kind, env, key string, mode *string, settings []byte) error {
	provider, label := payments.BaseProvider(key), payments.AccountLabel(key)
	var id uuid.UUID
	err := tx.QueryRow(ctx, `SELECT id FROM integration_accounts WHERE owner_kind = 'tenant' AND owner_id = $1 AND kind = $2 AND environment = $3 AND provider = $4 AND label = $5 FOR UPDATE`,
		tenant, kind, env, provider, label).Scan(&id)
	found := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if !found {
		id = uuid.New()
	}
	sealed, err := box.Seal(settings, boundAAD(id))
	if err != nil {
		return err
	}
	if found {
		_, err = tx.Exec(ctx, `UPDATE integration_accounts SET settings_enc = $2, mode = $3, updated_at = now() WHERE id = $1`, id, sealed, mode)
	} else {
		_, err = tx.Exec(ctx, `INSERT INTO integration_accounts (id, owner_kind, owner_id, kind, environment, provider, label, mode, settings_enc) VALUES ($1, 'tenant', $2, $3, $4, $5, $6, $7, $8)`,
			id, tenant, kind, env, provider, label, mode, sealed)
	}
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO integration_bindings (tenant_id, environment, kind, account_id, provider, label) VALUES ($1, $2, $3, $4, $5, $6) ON CONFLICT DO NOTHING`,
		tenant, env, kind, id, provider, label)
	return err
}

// mirrorDelete removes the binding and the account (and with it the sealed credentials, as removing the old row removes them). Rows of the ledger keep the id they carry.
func mirrorDelete(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, kind, env, key string) error {
	provider, label := payments.BaseProvider(key), payments.AccountLabel(key)
	if _, err := tx.Exec(ctx, `DELETE FROM integration_bindings WHERE tenant_id = $1 AND environment = $2 AND kind = $3 AND provider = $4 AND label = $5`, tenant, env, kind, provider, label); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `DELETE FROM integration_accounts a WHERE a.owner_kind = 'tenant' AND a.owner_id = $1 AND a.kind = $2 AND a.environment = $3 AND a.provider = $4 AND a.label = $5
		AND NOT EXISTS (SELECT 1 FROM integration_bindings b WHERE b.account_id = a.id)`, tenant, kind, env, provider, label)
	return err
}

// mirrorDefaults makes the bindings' default flags those of the old table (oldTable is "provider_accounts" or "einvoice_accounts", never input), whatever the old write did:
// the flags that become false first, then the one that becomes true, because a partial unique index allows one default and checks it row by row.
func mirrorDefaults(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, kind, env, oldTable string) error {
	const join = ` FROM %s o WHERE b.tenant_id = $1 AND b.environment = $2 AND b.kind = $3 AND o.tenant_id = b.tenant_id AND o.environment = b.environment
		AND o.provider = CASE WHEN b.label = '' THEN b.provider ELSE b.provider || '~' || b.label END`
	for _, to := range []bool{false, true} {
		sql := `UPDATE integration_bindings b SET is_default = ` + map[bool]string{false: "false", true: "true"}[to] + `, updated_at = now()` + fmt.Sprintf(join, oldTable) +
			` AND o.is_default = ` + map[bool]string{false: "false", true: "true"}[to] + ` AND b.is_default <> o.is_default`
		if _, err := tx.Exec(ctx, sql, tenant, env, kind); err != nil { //nolint:gosec // the table is one of two constants of this package
			return err
		}
	}
	return nil
}
