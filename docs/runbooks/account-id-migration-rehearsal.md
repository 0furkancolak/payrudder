# Rehearsal of the `account_id` migration (ADR 0026, plan 10 and plan 22 G7/G8)

Written 2026-11-04; steps 1 to 4 rehearsed on synthetic data 2026-11-05 ("What was run" below). **Step 5 has not been built or run, and nothing here has been run on a copy of production.** It is the rehearsal the ADR says must pass before any step that touches the payment path ships, with the stop conditions written down first. Everything marked *default (not agreed)* is a choice for the owner to confirm.

The steps are those of [ADR 0026](../adr/0026-integration-accounts.md#organisation-level-accounts-why-not-on-the-key-and-the-migration-it-needs-written-2026-11-02-nothing-built). Steps 1 to 3 are additive; step 4 (ids on the ledger and its neighbours) and step 5 (cut over) are the dangerous ones. The e-mail slice of organisation accounts is already built and is not part of this.

## What you need

- A copy of production `payments_db` and `plans_db` taken with the [backup runbook](backup-restore.md) (point-in-time restore into a scratch cluster, never the live one), or, when there is no production yet, a synthetic copy: at least 1 million `payment_ledger` rows over 20 tenants, three accounts per tenant for two providers, including `~label` keys and a bank-transfer account.
- The release being tested, built from the branch, and the **previous** release, to prove rollback.
- Same Postgres major version and the same extensions as production. A copy on a laptop's Docker VM (2.9 GB here) is **not** a measurement of step 4's duration, only of its correctness.

## Before the first step: record the truth

Run on the copy and keep the output next to the rehearsal notes:

1. Row counts of `payment_ledger`, `provider_objects`, `payment_attempts`, `provider_subscriptions`, `reconciliation_discrepancies`, `settlement_lines`, `fee_rules`, `provider_account_health`, `provider_accounts`, `einvoice_accounts`.
2. For each, `SELECT tenant_id, environment, provider, count(*) ... GROUP BY 1,2,3` as a checksum file (sorted, hashed).
3. The ledger total per tenant and currency (`sum(amount)` by kind), and the number of open discrepancies from the last reconciliation run.
4. `pg_stat_user_tables` size of each table above, to size step 4.

## Per step: do, check, stop

| Step | Do | Check (all must hold) | Stop and roll back if |
|---|---|---|---|
| 1 new tables beside the old | apply the migrations | old release still starts and passes its smoke test; no lock held longer than 1 s (`log_lock_waits`) | any statement waits for a lock on a payment table |
| 2 backfill, owner = tenant | run the backfill and the reseal job | every old account has exactly one new account and one binding with the same label and default flag; every sealed box opens under the new AAD (the reseal job's verify mode); the old rows are unchanged (checksum from "record the truth") | one box fails to open, or one row differs |
| 3 dual read | deploy the resolver reading bindings with fallback; send traffic (replay the day's webhooks, run a renewal cycle and a dunning charge against the provider stubs) | results equal to the old release's on the same input; the old webhook URL still accepted; a notification on `/webhooks/a/{account_id}` is accepted | any result differs, or an unknown account is accepted |
| 4 ids on the ledger and neighbours | add the nullable columns, then backfill **in batches** (default: 5 000 rows, 100 ms pause, one table at a time, resumable by primary key) while the stub traffic runs | no batch holds a lock longer than 1 s; replication lag stays under 10 s (*default, not agreed*); at the end `count(*) WHERE account_id IS NULL` is 0 and every `(tenant, environment, provider)` maps to one id; the ledger totals equal the "record the truth" totals; the "one fact once" unique index still refuses a duplicate | totals differ by one unit, a batch exceeds the lock limit, or lag exceeds the limit |
| 5 cut over, then drop | switch routing and subscriptions to ids; keep the key columns one release | a full reconciliation run on the copy ends with the same discrepancies as before; rolling the **code** back to the previous release works with the id columns present and the key columns still written | the reconciliation differs; the rollback does not start |

The old rows are deleted only in a later release, after a clean reconciliation run in production, never in the same deployment as step 5.

## Duration and window

Record the wall-clock time of step 4 per table on production-sized hardware and multiply nothing: the number measured is the number written in the release notes. The default (*not agreed*) is to run step 4 outside the nightly reconciliation window and to pause it while a reconciliation runs.

## Roll back

- Steps 1 to 3: drop the new tables and columns; the old release never read them.
- Step 4: the columns are nullable; leave them, deploy the previous release.
- Step 5: deploy the previous release (it reads keys, which are still written).
- Never restore the ledger from a backup to roll back: money moved after the backup. A restore is for loss of the database only.

## Not covered here

The iyzico webhook that names neither tenant nor account (ADR 0026, "the key as a name"): with several iyzico accounts the signing secret cannot be chosen. It needs its own decision before step 3 is enabled for iyzico, and until then more than one iyzico account per tenant stays refused.

## Sign-off

A rehearsal counts only with: the recorded "before" files, the per-step checks above with their output, the step 4 durations, and the name of the person who read them. Put them in the pull request that enables the step.

## What was run (2026-11-05, steps 1 to 3 only; SYNTHETIC laptop data, NOT a measurement of production)

Harness: `TestAccountIDRehearsal` in `services/payments-worker/internal/itest/accountid_rehearsal_integration_test.go` (testcontainers, Postgres 17.11 in an OrbStack VM with 2.9 GB, Apple laptop). Run it with `go test -tags integration -run TestAccountIDRehearsal -v ./internal/itest` from `services/payments-worker`; `ACCOUNTID_REHEARSAL_TENANTS` (default 20) and `ACCOUNTID_REHEARSAL_LEDGER` (default 100000) set the scale. It builds the database as the previous release left it (every migration but `00017`), fills it, records the truth (row count and a checksum of every row of the ten tables, the columns and indexes of those tables, the ledger totals per tenant, currency and kind), applies the new migrations, runs the backfill and the verify mode and asserts the checks of the table above. Everything below is *default (not agreed)* unless the owner confirms it.

Data: 20 tenants; per tenant three live payment accounts at two providers (`stripe`, `stripe~eu`, `banktransfer`), a sandbox `stripe` account for every second tenant, two e-invoice accounts (`nilvera`, `nilvera~tr`): 70 `provider_accounts` rows and 40 `einvoice_accounts` rows, 110 in all; 100 000 `payment_ledger` rows. The other eight tables of "record the truth" were **empty** (0 rows): the harness checks that they do not change, which proves little for them. The runbook's own size (1 million ledger rows) was not run.

| Step | Result on the synthetic data |
|---|---|
| 1 | Migration `00017` (tenant twin `tenant/00018`) applied in 11 to 13 ms with 100 000 ledger rows present. No old table changed (row count and checksum of every row, columns and indexes, ledger totals equal the recorded truth); the previous release's reads work on the new schema; the new tables are empty; the row-level-security exemption test and the tenant-schema-equals-public test pass. **Not checked:** `log_lock_waits` (no statement of the migration touches a payment table, so none waits on one; nothing measured it), the previous *binary* started against the new schema (its store reads were exercised instead). |
| 2 | Backfill: 110 old accounts, 110 new accounts and 110 bindings in 30 to 37 ms; every old account has exactly one account (owner = its tenant, same kind, environment, provider, label, mode) and one binding with the same label and default flag; verify mode opened every box of `provider_accounts` (70), `einvoice_accounts` (40) and `integration_accounts` (110) under the new AAD, 0 that do not open, in 1 ms; the old tables are unchanged (checksums equal); a second run finds nothing (0 created, 0 refreshed, 110 unchanged); a box copied into another account is found by verify and mended by a run of the backfill. |
| 3 | Shadow comparison (not a dual read, see below): the resolver with the shadow on returns, for every stripe account and every default, the same adapter and the same account as with it off; the e-invoice accounts returned are equal; 100 comparisons (20 tenants times 5 reads) all `equal`, none `differs`, `missing` or `error`; a bank-transfer account is bound like the others. An account replaced on the old side after the backfill is counted `differs`, one added is counted `missing`, the old row is returned in both, and one backfill run makes them `equal` again. A Stripe notification signed with the account's secret on the old URL `/webhooks/stripe/{tenant}/live` is accepted, one with a wrong secret and one for a tenant with no account are refused. |
| cells | The same backfill and verify over the pooled database and a cell (3 tenants, 12 old accounts): 12 created, 12 bindings, 12 boxes open, shadow `equal`. |

Not built, so not run: a notification on `/webhooks/a/{account_id}` (the check in step 3 of the table); a replay of a day's webhooks or a renewal and dunning cycle against stubs (the harness sent three webhooks and resolved accounts, it did not charge); writes to both tables (the account is replaced on the old side only, so the new tables fall behind until the backfill is run again, which is why step 3 is a shadow and not a read that prefers the binding); the e-invoice, mail and payment routing tables' `account_id` (left for step 4; `einvoice_routing` is not touched in steps 1 to 3); a new account whose old row is gone is counted (`Orphans`) and left. Steps 4 and 5 and their stop conditions: not built.

Operating it: `payrudderctl secrets accounts-backfill [--dry-run]` (step 2; payments-worker's environment, as `secrets reseal`) and `payrudderctl secrets reseal --service payments --verify` (opens every box, changes nothing). `PAYMENTS_ACCOUNT_SHADOW=true` turns the comparison on (off by default): it costs one more read per account read and exports `account_shadow_reads{kind,result}`; a result other than `equal` is also logged without any setting in it. The reseal job (rotation) now has `integration_accounts` as a column of its own.

## Step 4 (2026-11-05; SYNTHETIC laptop data, NOT a measurement of production)

Built: migration `00018` (tenant twin `tenant/00019`) adds a nullable `account_id` to the eight tables, a column-only `UPDATE (account_id)` grant (the ledger stays immutable: an amount update is still refused, and the test proves it), and a `BEFORE INSERT` trigger that fills a NULL id from the key (`provider` or `provider~label`), so no writer changed and a code rollback leaves nothing behind. `payrudderctl secrets account-ids [--dry-run] [--verify] [--batch 64] [--pause 100ms]` fills the existing rows by page range (ctid), resumable because the cursor is the data (`account_id IS NULL`); verify exits non-zero while a key maps to two ids or an id is not its key's account. Defaults (*not agreed*): 64 pages and 100 ms pause per statement, no foreign key and no index on `account_id` until step 5 needs one.

Run (`ACCOUNTID_REHEARSAL_LEDGER=1000000`, 20 tenants, 110 accounts, Postgres 17.11 in a 2.9 GB VM, batch 16 pages, **no pause**): migration applied in 29 ms; backfill of all eight tables 21.0 s (100 000 rows: 2.0 s); 1 000 000 ledger rows filled, 0 pending, 0 keys with two ids, 0 wrong ids; a key with no account (`ghost`) and a discrepancy with no provider stay NULL and are counted, not an error; every other column of every table and the ledger totals unchanged; a second run fills 0; a wrong id is found by verify and mended by clearing it and running again; the one-fact-once index still refuses a duplicate; a row written by the previous release's code gets its id from the trigger. Also run over a cell's tenant schemas (3 facts filled).

Not checked: lock waits (`log_lock_waits` was not read), replication lag (no replica), the 100 ms pause under real traffic, table bloat (each filled row is a new version; run `VACUUM` after a large run), and anything on production hardware. Rows whose key has no account (`NoAccount`) need step 2 run for that account before step 5. Step 5 (cut over) and the mirrored writes it needs are not built.

## Mirrored writes (2026-11-05; SYNTHETIC)

`PAYMENTS_ACCOUNT_MIRROR` (off; default, not agreed) makes `Accounts` and `EInvoiceAccounts` write every Put, Delete and SetDefault also to `integration_accounts`/`integration_bindings` in the same transaction (store/mirror.go): the account is sealed to its own id and updated in place (its id does not change when its settings are replaced, so the ids the ledger carries stay valid), the default flags of the bindings are made those of the old table after every write, and deleting an account deletes its binding and its sealed credentials. The old row is still the one that is used. Turn it on after `accounts-backfill` so the two start equal; the shadow comparison reads `equal` for a mirrored write with no backfill in between (the `step 3 mirrored writes` subtest: new, replaced, new default, default moved back, refused default, delete, and the same for e-invoice, with a whole-database level check after each). Not checked: the extra write's cost on the save path under load, a mirrored write in a cell's tenant schema (the code is the same, the subtest runs on the pooled database), and what happens to tenants that save accounts while the flag is flipped on (they are level again after one `accounts-backfill`).

## Dual read (2026-11-05; SYNTHETIC)

`PAYMENTS_ACCOUNT_READ_BOUND` (off; default, not agreed) makes the account resolver and the e-invoice accounts use the account of the new tables when the tenant has a binding for it, and the old row when not or when the binding cannot be read (the comparison still counts `equal`, `differs`, `missing`, `error`). Turn it on only together with `PAYMENTS_ACCOUNT_MIRROR`, after `accounts-backfill` and with `account_shadow_reads{result!="equal"}` at zero for a full renewal and dunning cycle; a writer that reaches only the old row would otherwise be shadowed by a stale binding (the `step 3 read through the binding` subtest shows exactly that, and one `accounts-backfill` mending it). Rollback: turn the flag off; the old rows were never stopped being written. Not checked: the extra read per account read on the payment path under load (the resolver caches adapters, the binding is read each time to compare), and a cell's tenant schema.

**Not built, with the reason (default, not agreed):** `/webhooks/a/{account_id}` and the dropping of the key columns. The route names no tenant, and a tenant's data can live in a cell, so resolving an account id needs an account-to-cell directory that does not exist; a tenant-owned account is reachable on the old URL, which carries the tenant, so the route earns its keep only with organisation-owned accounts (ADR 0026 step 6). The key columns stay one release after any cut over and are dropped only after a clean reconciliation run in production.
