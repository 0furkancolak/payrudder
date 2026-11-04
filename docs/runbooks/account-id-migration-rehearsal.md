# Rehearsal of the `account_id` migration (ADR 0026, plan 10 and plan 22 G7/G8)

Written 2026-11-04. **Nothing in this runbook has been run.** It is the rehearsal the ADR says must pass before any step that touches the payment path ships, with the stop conditions written down first. Everything marked *default (not agreed)* is a choice for the owner to confirm.

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
