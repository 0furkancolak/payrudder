package reseal

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// Step 4 of the account_id migration (ADR 0026, docs/runbooks/account-id-migration-rehearsal.md): the ledger and its neighbours carry the id of the account their key names.
// New rows get it from a trigger (migration 00018); this fills the rows that were there before, in batches, so no statement holds a lock for long. Nothing here is sealed, but it
// lives with the backfill of step 2 because it visits the same places (the pooled database and every tenant schema of every cell).
//
// It walks each table by physical page ranges (ctid), so a batch is a bounded slice of the table whatever its size, a row is visited once per run, and a run that is stopped
// and started again simply finds the rows done: the cursor is the data (account_id IS NULL), not a file.

// IDTables are the tables that carry the key (tenant_id, environment, provider) and get an account_id. Registry, never input: the statements are built from it.
var IDTables = []string{"payment_ledger", "provider_objects", "payment_attempts", "provider_subscriptions", "reconciliation_discrepancies", "settlement_lines", "fee_rules", "provider_account_health"}

// IDOptions are the knobs of a run. Batch is the number of pages per statement (0 is 64 pages, about 500 KB); Pause is the wait between statements (0 is none); DryRun counts
// what would be filled and changes nothing.
type IDOptions struct {
	Batch  int
	Pause  time.Duration
	DryRun bool
}

// IDReport is what one table held over every place visited. Filled are the rows this run set; Pending the rows still without an id that have an account (a dry run, or rows
// inserted after the run passed them); NoAccount the rows whose key has no account (step 2 not run for it, or the account is gone): they stay NULL and are not an error of this job;
// Keyless the rows with no provider at all (a discrepancy that names none), which never get one. Conflicts are keys that map to two ids and Mismatched rows whose id is not the
// account the key names: both must be 0 before step 5.
type IDReport struct {
	Table                                                            string
	Rows, Filled, Pending, NoAccount, Keyless, Conflicts, Mismatched int
}

// BackfillAccountIDs fills account_id in every table of IDTables in every place of in (Pooled, Cells, DryRun, Log), batch by batch.
func BackfillAccountIDs(ctx context.Context, in Input, o IDOptions) ([]IDReport, error) {
	return accountIDs(ctx, in, o, true)
}

// VerifyAccountIDs counts, changes nothing, and is the "check" of step 4 in the runbook.
func VerifyAccountIDs(ctx context.Context, in Input) ([]IDReport, error) {
	return accountIDs(ctx, in, IDOptions{DryRun: true}, false)
}

func accountIDs(ctx context.Context, in Input, o IDOptions, fill bool) ([]IDReport, error) {
	if in.Log == nil {
		in.Log = slog.Default()
	}
	if o.Batch <= 0 {
		o.Batch = 64
	}
	total := make([]IDReport, len(IDTables))
	for i, t := range IDTables {
		total[i].Table = t
	}
	err := eachPlace(ctx, in, func(q querier, _ bool) error {
		for i, t := range IDTables {
			r, err := idTable(ctx, q, in, o, t, fill)
			total[i].Rows += r.Rows
			total[i].Filled += r.Filled
			total[i].Pending += r.Pending
			total[i].NoAccount += r.NoAccount
			total[i].Keyless += r.Keyless
			total[i].Conflicts += r.Conflicts
			total[i].Mismatched += r.Mismatched
			if err != nil {
				return fmt.Errorf("%s: %w", t, err)
			}
		}
		return nil
	})
	return total, err
}

// keyMatch is the account a row's key names (the trigger of migration 00018 does the same lookup; written as a join here so it can use integration_accounts' unique index).
const keyMatch = `a.owner_kind = 'tenant' AND a.owner_id = t.tenant_id AND a.kind = 'payment' AND a.environment = t.environment
	AND a.provider = split_part(t.provider, '~', 1) AND a.label = substr(t.provider, length(split_part(t.provider, '~', 1)) + 2)`

func idTable(ctx context.Context, q querier, in Input, o IDOptions, table string, fill bool) (IDReport, error) {
	var r IDReport
	// table comes from IDTables, never from input.
	var pages int64
	if err := scanOne(ctx, q, &pages, `SELECT (pg_relation_size($1::regclass) / current_setting('block_size')::bigint)`, table); err != nil {
		return r, err
	}
	if fill {
		// The page count is taken once: a row written after this point has the trigger's id, and the new versions this run writes go past it.
		for from := int64(0); from < pages; from += int64(o.Batch) {
			if err := ctx.Err(); err != nil {
				return r, err
			}
			where := fmt.Sprintf(`t.ctid >= '(%d,0)'::tid AND t.ctid < '(%d,0)'::tid AND t.account_id IS NULL AND t.provider <> ''`, from, from+int64(o.Batch))
			if o.DryRun {
				var n int
				if err := scanOne(ctx, q, &n, `SELECT count(*) FROM `+table+` t JOIN integration_accounts a ON `+keyMatch+` WHERE `+where); err != nil {
					return r, err
				}
				r.Filled += n
			} else {
				tag, err := q.Exec(ctx, `UPDATE `+table+` t SET account_id = a.id FROM integration_accounts a WHERE `+keyMatch+` AND `+where)
				if err != nil {
					return r, err
				}
				r.Filled += int(tag.RowsAffected())
				if o.Pause > 0 {
					select {
					case <-ctx.Done():
						return r, ctx.Err()
					case <-time.After(o.Pause):
					}
				}
			}
		}
		in.Log.InfoContext(ctx, "account ids", "table", table, "pages", pages, "filled", r.Filled, "dry_run", o.DryRun)
	}
	var withID int
	err := scanOne(ctx, q, &r.Rows, `SELECT count(*) FROM `+table)
	if err == nil {
		err = scanOne(ctx, q, &r.Keyless, `SELECT count(*) FROM `+table+` WHERE provider = ''`)
	}
	if err == nil {
		err = scanOne(ctx, q, &withID, `SELECT count(*) FROM `+table+` WHERE account_id IS NOT NULL`)
	}
	if err == nil {
		err = scanOne(ctx, q, &r.NoAccount, `SELECT count(*) FROM `+table+` t WHERE t.account_id IS NULL AND t.provider <> '' AND NOT EXISTS (SELECT 1 FROM integration_accounts a WHERE `+keyMatch+`)`)
	}
	if err == nil {
		r.Pending = r.Rows - r.Keyless - withID - r.NoAccount
		err = scanOne(ctx, q, &r.Conflicts, `SELECT count(*) FROM (SELECT 1 FROM `+table+` WHERE account_id IS NOT NULL GROUP BY tenant_id, environment, provider HAVING count(DISTINCT account_id) > 1) c`)
	}
	if err == nil {
		err = scanOne(ctx, q, &r.Mismatched, `SELECT count(*) FROM `+table+` t WHERE t.account_id IS NOT NULL AND NOT EXISTS (SELECT 1 FROM integration_accounts a WHERE a.id = t.account_id AND `+keyMatch+`)`)
	}
	return r, err
}

func scanOne[T any](ctx context.Context, q querier, dst *T, sql string, args ...any) error {
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	if rows.Next() {
		if err := rows.Scan(dst); err != nil {
			return err
		}
	}
	return rows.Err()
}
