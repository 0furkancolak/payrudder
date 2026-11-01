# 16 — Backup and alerting, completed

## The request
List 2 (13): we must have **backups** and **Alertmanager**.

## Where we are
#254: `deploy/backup` (`backup.sh` pg_dump of every `*_db` with retention and a readable-dump check and optional S3 copy; `restore.sh`; `verify.sh` restore drill), compose `ops` profile and Helm CronJobs (off by default), Pushgateway + Alertmanager in the `obs` profile with page/warn routing and inhibition, eight alert rules with unit tests (`BackupStale`, `BackupFailed`, `RestoreDrillFailed`, `PostgresDown`…), a runbook. The alert rules are unit-tested with promtool in CI; the backup scripts had no committed test and the S3 path was never exercised.

**Branch `feat/backup-pitr-alerts` (2026-11-01), PR open, not merged.** Done: point-in-time recovery for the compose Postgres (WAL archiving to MinIO, a sidecar that ships it, daily base backups, `pitr-restore.sh` to a UTC minute, `make up-pitr`); `invariants.sh` (invoice totals vs lines, credit lots vs ledger, receipts vs ledger, row counts) run by `verify.sh` and by the drill; `make test-backup-scripts` (real Postgres container, CI job `backup`) and `make backup-drill` (compose stack, restore to a chosen minute, checks, measured RTO; CI job `backup`); 12 new alert rules with promtool tests (payment discrepancy, command errors, charge failure rate, refund spike, provider webhooks, dunning queue, DLQ growth, `Watchdog`, WAL/base-backup/spool); Alertmanager mails Mailpit locally, a webhook config with a dead man's switch route, an example with PagerDuty/Slack/SMTP validated by `amtool`; the runbook with objectives, drill, first response per alert and what is NOT verified. **Defaults (not agreed): RPO 15 minutes, RTO 1 hour**, local receiver Mailpit, dead man's switch alert `Watchdog`.

**Still open / NOT verified:** real S3 (only MinIO), a cluster (the Helm chart has the dump jobs and the invariants, no PITR), real receivers (PagerDuty, Slack, SMTP, a heartbeat monitor), a crash instead of a clean stop, a multi-day run (lifecycle expiry, `archive_timeout` driving the shipping), tuning the alert thresholds on real traffic, the other stores (work item 3), the game day (below).

## Gaps
- ~~**No point-in-time recovery.** A nightly dump loses up to a day.~~ Done for the compose Postgres (WAL archiving + base backups to S3-compatible storage, restore to a minute, drilled). Open: Kubernetes, and the production tool choice (pgBackRest / WAL-G / managed PITR instead of these scripts).
- The dump is a **logical dump of a running database**; for the multi-tenant cells (ADR 0018) each cell database must be covered (the script lists `*_db` on one host).
- Not backed up: Redpanda topics and consumer offsets, RabbitMQ definitions, ClickHouse (analytics), MinIO (assets, invoice PDFs), Redis, secrets (`PAYRUDDER_SECRETS_KEY`: losing it makes sealed provider credentials unreadable). Nothing encrypts dumps at rest; the S3 upload path (`aws` CLI) was not exercised.
- ~~The restore drill checks "has tables", not that the data is consistent~~ It now checks invariants and row counts (`invariants.sh`). Open: the migrations version, and the roles/grants of a *dump* restore (a point-in-time restore is physical and brings them).
- Alert coverage: ~~payment failures spike, webhook delivery failures, queue depth~~ now alerted (money group). Still none for disk, memory, certificate expiry, replication lag, Kafka/RabbitMQ health beyond the existing ones, latency SLOs; no on-call schedule or escalation; the cluster side (Prometheus Operator / `kube-prometheus-stack`, Alertmanager HA, silences, templates) is not provided; real receivers are an example validated by `amtool`, not exercised (Mailpit and a webhook sink were).
- ~~`BackupNeverReported` and staleness alerts depend on Prometheus being up: nothing watches the watcher.~~ `Watchdog` always fires and has its own route; the outside heartbeat monitor that pages on its silence is the deployment's to provide.
- ~~The compose backup service was not run through a full cycle.~~ The drill ran the dumps to MinIO and the dump drill with invariants in a throwaway stack (the nightly *loop* with its 24 h sleep was not run for a day).

## Research first
1. Decide the **recovery objectives** with the owner: RPO and RTO per data store.
2. Inventory every stateful component and its backup method (table in this plan).
3. Choose the PITR tool for the Postgres in production (self-run vs managed).

## Decisions needed
RPO/RTO, backup storage target and encryption keys, retention, alert channels and who is on call, Postgres hosting model.

## Work
1. Recovery objectives and the inventory table. **Objectives written (defaults, not agreed) in the runbook; the inventory table of every stateful component is still to do.**
2. PITR (WAL archiving) + base backups; restore-to-time drill in CI/nightly on a scratch cluster. **Done for compose (CI runs the drill on every PR, not nightly); not for Kubernetes.**
3. Back up the other stores (MinIO bucket replication, Redpanda topics for outbox-less streams or document rebuild, ClickHouse backup, secrets escrow). **Open.**
4. Stronger drill: restore into a scratch Postgres, run the migrations check and a set of invariants (e.g. `invoices` totals vs lines), measure RTO, report it. **Done except the migrations-version check.**
5. Alert rules for the missing areas with unit tests; dead-man's switch; receivers (mail via Nafru, Slack/PagerDuty as chosen); the Helm/Kubernetes install of Prometheus + Alertmanager with these rules. **Rules, tests, dead man's switch and receiver configs done; real receivers and the Kubernetes install open.**
6. Runbooks: restore, failover, each alert's first response. **Restore (point in time and dump) and the first response of every alert are in [the runbook](../runbooks/backup-restore.md); failover is not written.**

## Done means
A scripted **game day**: destroy a scratch environment, restore from backup to a chosen time, services healthy, RTO measured and written down; each alert has a runbook entry and has been fired once on purpose. (`make backup-drill` does the restore, the invariants and the RTO for the databases; "services healthy" on top of the restored database and firing every alert for real are not done.)

## Not verifiable here
Production storage, real paging.

## Risks
A backup that was never restored is worthless: the game day is the acceptance, not the cron job.


## Addendum (2026-11-01)
The audit in [22](22-platform-audit-and-roadmap.md) adds to this plan: point-in-time recovery with a stated RPO/RTO (G5), a restore drill that checks data and invariants, financial anomaly alerts (G6) and a real alert receiver. Do them here.

## Review addendum (2026-11-01)
- **Owner of the overlap:** this plan owns backup/PITR/alerting; plan 22 G5/G6 are implemented here. The earlier "do them here" and 22's "absorbed" mean the same thing.
- **Defaults (not agreed):** RPO 15 minutes via WAL archiving to S3-compatible storage (MinIO in compose), RTO 1 hour; local alert receiver = webhook/Mailpit; a dead-man's-switch alert; `verify.sh` also checks invariants (invoice totals vs lines, ledger sums), not only table counts.
