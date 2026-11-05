package reseal

import (
	"context"
	"log/slog"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/0furkancolak/payrudder/pkg/payments"
)

// Step 2 of the account_id migration (ADR 0026, docs/runbooks/account-id-migration-rehearsal.md): every account of payments-worker's old tables (provider_accounts, the
// payment accounts; einvoice_accounts, the e-invoice ones) gets one row of integration_accounts, owned by its tenant, and one of integration_bindings with the same
// label and default flag. The sealed settings are opened with the associated data of the old row and sealed again to the new account's id (accountAAD): never copied
// as bytes, because the old data names the tenant and would not open. The old rows are only read.
//
// It can be run again at any time. An account already there is found by what names it (owner, kind, environment, provider, label), not created twice; if the old row has
// changed since (new settings, another default) the new row is brought up to date, which is what keeps the two equal until the cut-over. A new account whose old row is
// gone is counted (Orphans) and left alone: removing it is a decision of the cut-over, not of a copy.

// AccountsReport is what the backfill found, over every place it visited.
type AccountsReport struct {
	// Old are the rows of the two old tables, Created the new accounts made, Refreshed the existing ones brought up to date (settings, mode), Unchanged those that already
	// matched. Created + Refreshed + Unchanged = Old - Unreadable.
	Old, Created, Refreshed, Unchanged int
	// Bindings are the bindings written or corrected.
	Bindings int
	// Unreadable old rows did not open with the keys given and were skipped; Orphans are new accounts with no old row.
	Unreadable, Orphans int
}

type oldAccount struct {
	tenant               uuid.UUID
	kind, env, key, mode string
	isDefault            bool
	box                  []byte
}

// BackfillAccounts runs the backfill in the pooled database and in every tenant schema of every cell of in (Pooled, Cells, Box, DryRun, Log).
func BackfillAccounts(ctx context.Context, in Input) (AccountsReport, error) {
	if in.Log == nil {
		in.Log = slog.Default()
	}
	var total AccountsReport
	err := eachPlace(ctx, in, func(q querier, _ bool) error {
		r, err := backfillPlace(ctx, q, in)
		total.Old += r.Old
		total.Created += r.Created
		total.Refreshed += r.Refreshed
		total.Unchanged += r.Unchanged
		total.Bindings += r.Bindings
		total.Unreadable += r.Unreadable
		total.Orphans += r.Orphans
		return err
	})
	return total, err
}

// oldAAD is the associated data of an old row, as places.go says (the writer's own form).
func oldAAD(kind string, tenant uuid.UUID, env, key string) []byte {
	if kind == "payment" {
		return []byte(tenant.String() + "|" + key + "|" + env)
	}
	if payments.AccountLabel(key) == "" {
		return []byte(tenant.String() + "|einvoice|" + env)
	}
	return []byte(tenant.String() + "|einvoice|" + env + "|" + key)
}

func backfillPlace(ctx context.Context, q querier, in Input) (AccountsReport, error) {
	var r AccountsReport
	// shortcut: every old row of the place is held in memory at once (a sealed box of a few hundred bytes each; accounts are few next to the ledger), upgrade to
	// keyset batches if a place ever holds millions.
	rows, err := q.Query(ctx, `SELECT 'payment', tenant_id, environment, provider, NULL::text, is_default, settings_enc FROM provider_accounts
		UNION ALL SELECT 'einvoice', tenant_id, environment, provider, mode, is_default, settings_enc FROM einvoice_accounts`)
	if err != nil {
		return r, err
	}
	olds, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (oldAccount, error) {
		var o oldAccount
		var mode *string
		err := row.Scan(&o.kind, &o.tenant, &o.env, &o.key, &mode, &o.isDefault, &o.box)
		if mode != nil {
			o.mode = *mode
		}
		return o, err
	})
	if err != nil {
		return r, err
	}
	r.Old = len(olds)
	// Non-defaults first: the default of a (tenant, environment, kind) moves by clearing the others before setting this one (a partial unique index allows one).
	slices.SortStableFunc(olds, func(a, b oldAccount) int {
		if a.isDefault == b.isDefault {
			return 0
		}
		if !a.isDefault {
			return -1
		}
		return 1
	})
	for _, o := range olds {
		settings, err := in.Box.Open(o.box, oldAAD(o.kind, o.tenant, o.env, o.key))
		if err != nil {
			r.Unreadable++
			in.Log.WarnContext(ctx, "an old account does not open: skipped", "kind", o.kind, "tenant", o.tenant, "environment", o.env, "account", o.key)
			continue
		}
		label := payments.AccountLabel(o.key)
		provider := payments.BaseProvider(o.key)
		var modeArg any
		if o.kind == "einvoice" {
			modeArg = o.mode
		}

		var id uuid.UUID
		var stored []byte
		var storedMode *string
		found := false
		rs, err := q.Query(ctx, `SELECT id, settings_enc, mode FROM integration_accounts WHERE owner_kind = 'tenant' AND owner_id = $1 AND kind = $2 AND environment = $3 AND provider = $4 AND label = $5`,
			o.tenant, o.kind, o.env, provider, label)
		if err != nil {
			return r, err
		}
		if err := func() error {
			defer rs.Close()
			for rs.Next() {
				found = true
				if err := rs.Scan(&id, &stored, &storedMode); err != nil {
					return err
				}
			}
			return rs.Err()
		}(); err != nil {
			return r, err
		}

		same := false
		if found {
			if cur, err := in.Box.Open(stored, []byte(accountAAD+id.String())); err == nil && string(cur) == string(settings) {
				same = (storedMode == nil && modeArg == nil) || (storedMode != nil && modeArg != nil && *storedMode == modeArg.(string))
			}
		}
		switch {
		case !found:
			r.Created++
		case same:
			r.Unchanged++
		default:
			r.Refreshed++
		}
		if in.DryRun {
			continue
		}
		if !found {
			id = uuid.New()
		}
		if !found || !same {
			sealed, err := in.Box.Seal(settings, []byte(accountAAD+id.String()))
			if err != nil {
				return r, err
			}
			if found {
				_, err = q.Exec(ctx, `UPDATE integration_accounts SET settings_enc = $2, mode = $3, updated_at = now() WHERE id = $1`, id, sealed, modeArg)
			} else {
				_, err = q.Exec(ctx, `INSERT INTO integration_accounts (id, owner_kind, owner_id, kind, environment, provider, label, mode, settings_enc)
					VALUES ($1, 'tenant', $2, $3, $4, $5, $6, $7, $8)`, id, o.tenant, o.kind, o.env, provider, label, modeArg, sealed)
			}
			if err != nil {
				return r, err
			}
		}
		tag, err := q.Exec(ctx, `INSERT INTO integration_bindings (tenant_id, environment, kind, account_id, provider, label, is_default) VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (tenant_id, environment, kind, account_id) DO UPDATE SET is_default = excluded.is_default, label = excluded.label, provider = excluded.provider, updated_at = now()
			WHERE (integration_bindings.is_default, integration_bindings.label, integration_bindings.provider) IS DISTINCT FROM (excluded.is_default, excluded.label, excluded.provider)`,
			o.tenant, o.env, o.kind, id, provider, label, o.isDefault)
		if err != nil {
			return r, err
		}
		r.Bindings += int(tag.RowsAffected())
	}

	// New accounts that no old row accounts for.
	rs, err := q.Query(ctx, `SELECT a.owner_id, a.kind, a.environment, a.provider, a.label FROM integration_accounts a WHERE a.owner_kind = 'tenant'`)
	if err != nil {
		return r, err
	}
	type name struct{ tenant, kind, env, key string }
	have := map[name]bool{}
	for _, o := range olds {
		have[name{o.tenant.String(), o.kind, o.env, o.key}] = true
	}
	news, err := pgx.CollectRows(rs, func(row pgx.CollectableRow) (name, error) {
		var tenant uuid.UUID
		var n name
		var provider, label string
		if err := row.Scan(&tenant, &n.kind, &n.env, &provider, &label); err != nil {
			return n, err
		}
		n.tenant, n.key = tenant.String(), payments.AccountKey(provider, label)
		return n, nil
	})
	if err != nil {
		return r, err
	}
	for _, n := range news {
		if !have[n] {
			r.Orphans++
		}
	}
	return r, nil
}
