# 16 — Backup and alerting, completed

## The request
List 2 (13): we must have **backups** and **Alertmanager**.

## Where we are
#254: `deploy/backup` (`backup.sh` pg_dump of every `*_db` with retention and a readable-dump check and optional S3 copy; `restore.sh`; `verify.sh` restore drill), compose `ops` profile and Helm CronJobs (off by default), Pushgateway + Alertmanager in the `obs` profile with page/warn routing and inhibition, eight alert rules with unit tests (`BackupStale`, `BackupFailed`, `RestoreDrillFailed`, `PostgresDown`…), a runbook. Tested against a real Postgres (retention, restore, no overwrite, corrupt dump), promtool, amtool, and a local webhook receiver.

## Gaps
- **No point-in-time recovery.** A nightly dump loses up to a day. WAL archiving (pgBackRest / wal-g / managed PITR) is not done.
- The dump is a **logical dump of a running database**; for the multi-tenant cells (ADR 0018) each cell database must be covered (the script lists `*_db` on one host).
- Not backed up: Redpanda topics and consumer offsets, RabbitMQ definitions, ClickHouse (analytics), MinIO (assets, invoice PDFs), Redis, secrets (`PAYRUDDER_SECRETS_KEY`: losing it makes sealed provider credentials unreadable). Nothing encrypts dumps at rest; the S3 upload path (`aws` CLI) was not exercised.
- The restore drill checks "has tables", not that the data is **consistent** (row counts, invariants, migrations version) and doesn't restore the roles/grants.
- Alert coverage: no alerts for disk, memory, certificate expiry, replication lag, Kafka/RabbitMQ health beyond the existing ones, error rates, latency SLOs, payment failures spike, webhook delivery failures, queue depth; no on-call schedule or escalation; the cluster side (Prometheus Operator / `kube-prometheus-stack`, Alertmanager HA, silences, templates) is not provided; no mail/Slack/PagerDuty receivers (only a generic webhook via a file).
- `BackupNeverReported` and staleness alerts depend on Prometheus being up: nothing watches the watcher (a dead-man's switch).
- The compose backup service was not run through a full cycle.

## Research first
1. Decide the **recovery objectives** with the owner: RPO and RTO per data store.
2. Inventory every stateful component and its backup method (table in this plan).
3. Choose the PITR tool for the Postgres in production (self-run vs managed).

## Decisions needed
RPO/RTO, backup storage target and encryption keys, retention, alert channels and who is on call, Postgres hosting model.

## Work
1. Recovery objectives and the inventory table.
2. PITR (WAL archiving) + base backups; restore-to-time drill in CI/nightly on a scratch cluster.
3. Back up the other stores (MinIO bucket replication, Redpanda topics for outbox-less streams or document rebuild, ClickHouse backup, secrets escrow).
4. Stronger drill: restore into a scratch Postgres, run the migrations check and a set of invariants (e.g. `invoices` totals vs lines), measure RTO, report it.
5. Alert rules for the missing areas with unit tests; dead-man's switch; receivers (mail via Nafru, Slack/PagerDuty as chosen); the Helm/Kubernetes install of Prometheus + Alertmanager with these rules.
6. Runbooks: restore, failover, each alert's first response.

## Done means
A scripted **game day**: destroy a scratch environment, restore from backup to a chosen time, services healthy, RTO measured and written down; each alert has a runbook entry and has been fired once on purpose.

## Not verifiable here
Production storage, real paging.

## Risks
A backup that was never restored is worthless: the game day is the acceptance, not the cron job.
