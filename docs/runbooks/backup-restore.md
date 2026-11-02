# Backup, restore and alerting

What is backed up, how often, how to prove it restores, how to restore (to a chosen minute), and where alerts go. Plan: [16](../plans/16-backup-alerting.md).

## Objectives

**Default (not agreed): RPO 15 minutes, RTO 1 hour**, for the Postgres databases (everything the money depends on). The owner may overturn them; the numbers below follow from them.

| | Value | Where it comes from |
|---|---|---|
| RPO (data you can lose) | 15 min | WAL is archived to S3-compatible storage: `archive_timeout` 5 min + shipping every 30 s + the upload. `WalArchiveStale` pages after 15 min without a shipped segment. |
| RTO (time to be back) | 1 h | Measured by `make backup-drill`: on a laptop the restore of a small database took 28 s to 112 s over several runs depending on the machine's load (install of the aws CLI, download, unpack, replay, promote, checks). A big database is dominated by the base backup download and the WAL replay: re-measure with production volumes. |
| Dumps (second, portable copy) | 24 h | `pg_dump` nightly; what you fall back on when the WAL archive itself is lost or unusable. |

The WAL archive protects against the loss of the Postgres host or volume, a bad deploy and a deleted table. It does **not** protect against the loss of the bucket (keep it in another account or region in production) or of the encryption/secret keys (below).

## What runs

- **Dumps** (`deploy/backup/backup.sh`): `pg_dump` (custom format, compressed) of every `*_db` database, each to `<db>-<UTC time>.dump`. The newest `BACKUP_KEEP` (7) dumps of each stay; a dump that `pg_restore --list` cannot read is removed and counts as a failure. With `BACKUP_S3_URL` the files are also copied to S3-compatible storage (a volume is not a second copy of the data: set it in production).
- **Dump restore drill** (`deploy/backup/verify.sh`): restores the newest dump of each database into a scratch database, checks it has tables **and that its data is consistent** (`deploy/backup/invariants.sh`, below), drops it. A backup nobody has restored is a hope, not a backup.
- **Point-in-time recovery** (compose: `make up-pitr`; scripts in `deploy/backup`):
  - Postgres runs with `archive_mode=on`, `archive_timeout=300` and `archive_command = sh wal-archive.sh %p %f`, which keeps each finished WAL segment (compressed, synced) in a spool.
  - The sidecar `pitr-agent` (`pitr-agent.sh`) ships the spool to the bucket every 30 s (`wal-ship.sh`; a file leaves the spool only after its upload succeeded), writes a row into `payrudder_pitr_markers` (database `postgres`) every 5 min so there is always activity to archive and a recovery point to look for, and takes a base backup when the newest is older than a day (`pitr-basebackup.sh`).
  - Storage layout: `s3://backups/pitr/base/<UTC time>/{base.tar.gz,pg_wal.tar.gz,start_wal}` and `s3://backups/pitr/wal/<segment>.gz`; the dumps go to `s3://backups/dumps/`. Retention is the bucket's lifecycle rule, made by `minio-init`: base backups 8 days, WAL 9 days (the WAL since the oldest base backup must outlive it), dumps 30 days. A production bucket needs the same three rules.
  - All of it reports to the Pushgateway: `payrudder_backup_*`, `payrudder_restore_*`, `payrudder_wal_*`, `payrudder_pitr_basebackup_*` (what the alerts read).
- **Point-in-time restore** (`pitr-restore.sh`): below.
- **Local stack**: `docker compose --profile ops up -d backup` for the dumps (every `BACKUP_INTERVAL_SECONDS`, a day; the dump drill every `VERIFY_EVERY_RUNS`, 7 runs; dumps in the `backup-data` volume), `make up-pitr` for PITR (recreates the postgres container with archiving; the data stays; `pitr-agent` needs `minio-init` to have made the bucket, `make up-pitr` does). To stop it: `docker compose -f deploy/compose/compose.yaml -f deploy/compose/compose.pitr.yaml --profile ops stop pitr-agent`, and take `compose.pitr.yaml` off the next `up` so archiving stops too: **an archive nobody ships fills the disk**.
- **Kubernetes**: `payrudder-infra` chart, `backup.enabled=true`: CronJobs `postgres-backup` (02:17 daily) and `postgres-verify` (Sundays 03:47, with the invariants) on a volume of their own. Set `backup.s3Url`, `backup.s3SecretName` and `backup.pushgatewayUrl`. **The chart has no point-in-time recovery yet**: a cluster either uses the managed database's own PITR or a pgBackRest/WAL-G setup (an infrastructure decision, plan 16), or runs the scripts above in a sidecar of its own.

## The data check (`invariants.sh`)

Run by `verify.sh` on every restored dump and by the drill on the restored point-in-time copy. It needs a role that sees every row (a superuser: with row level security forced, any other role sees nothing and every check would pass on nothing). Per schema (`public` and the `t_*` schemas of cell databases):

- `invoices.total_minor = max(0, sum of its lines)` and `subtotal_minor = sum of its lines but tax`;
- `credit_lots.remaining = sum of that lot's ledger rows`;
- `manual_receipts.amount_minor = the amount of its payment_ledger row`.

It prints `counts <db> table=rows ...` for the key tables; the drill compares them with the live database at the target time. The checks were run against the local development database (77 invoices, 4 credit lots) and found no violation, so they are not false alarms on real data; a violation is a torn or corrupted copy, or a bug.

## Restore

### To a point in time (the normal restore)

```sh
# A new, empty data directory (a volume) for the same Postgres major version; WAL_S3_URL and the AWS_* variables as the sidecar has them.
sh deploy/backup/pitr-restore.sh '2026-11-01 12:30:00' /var/lib/postgresql/data     # a UTC time, or: latest
# then start Postgres on that directory WITHOUT archive_mode; it replays to the target and opens read-write (recovery_target_action = promote)
```

1. **Pick the time in UTC** (the script refuses other zones: the likeliest mistake of a night with an incident is the wrong hour). The newest base backup that ended at or before it is used; none that early is an error.
2. The target must be **before the end of the archive**: if you restore to "just now", run `SELECT pg_switch_wal()` on the primary first and let `pitr-agent` ship it (`payrudder_wal_spool_files` 0), or Postgres stops with an error instead of guessing.
3. Start Postgres on the directory with the same major version. `max_connections` and the other settings a standby must not undercut are written from the base backup's control file. The server must not archive into the production prefix: start it without `archive_mode`.
4. Wait for `SELECT pg_is_in_recovery()` to return `f`, then run `sh deploy/backup/invariants.sh <db>...` and look at what you expected to see (the last marker: `SELECT max(written_at) FROM payrudder_pitr_markers`).
5. Make it the primary: the recovered server is on a **new timeline**. Take a new base backup before you rely on it (`pitr-basebackup.sh`), and switch archiving on for it; do not reuse the old prefix for the old timeline's files unless you know why.
6. The directory holds `pitr_wal/` (the downloaded archive); remove it once recovered. The roles and grants come with the cluster, since it is a physical restore.

The restore downloads the WAL since the base backup whole, which is fine for a day of a small database. For a large archive use a `restore_command` that fetches on demand.

### From a dump (one database, no point in time)

```sh
# One database from a dump. Creates it; --force replaces one that exists (close its connections first).
PGHOST=... PGUSER=postgres PGPASSWORD=... sh deploy/backup/restore.sh /backups/plans_db-20261028T021700Z.dump plans_db --force
```

The dump has no owners (`--no-owner`), so the objects belong to the user who restored them. Give them back to the service's roles the way `deploy/compose/postgres/01-databases.sh` does (migrator owns the schema, app is subject to RLS, system bypasses it) — the same three statements per service — and restart the service. To check without touching production, restore into another name and look (`restore.sh <dump> plans_check`).

Order for a full loss: Postgres first (all `*_db`), then Redpanda topics (`deploy/*/redpanda/topics.sh`; events not yet consumed are lost, the outbox tables are in the database and are republished), then the services. ClickHouse (analytics) is rebuilt from the event stream.

## The drill (`make backup-drill`)

`deploy/backup/drill.sh` is the acceptance of plan 16: it proves on a throwaway stack (own compose project `bkdrill-<pid>`, no host port published, the stack you work in is untouched) that the database comes back to a chosen minute from what the stack really archives. About five minutes with warm images; Docker and Compose v2.24+. In CI as the `backup` job.

1. brings up Postgres (archiving), MinIO and the Pushgateway, builds the billing and payments schemas from their migrations, seeds 20 invoices with lines, credit lots and payments;
2. starts `pitr-agent` (marker, base backup, shipping); runs the nightly dump to MinIO and the dump drill (`verify.sh` with the invariants);
3. adds 10 invoices and the marker `drill-before-T`, records the row counts, picks the target time T;
4. the incident after T: every invoice deleted, more payments, the marker `drill-after-T`; the WAL is switched, archived and shipped;
5. refuses a target from before the first base backup; restores T into a new volume; starts a scratch Postgres on it; waits for recovery to end;
6. checks the restored copy: invariants hold, the row counts are those of T (not those before, not those after the incident), `drill-before-T` is there and `drill-after-T` is not, and the metrics the alerts read are at the Pushgateway;
7. prints the measured RTO against the objective and fails if it is longer.

`make test-backup-scripts` is the quick one (about 30 s, one Postgres container): `backup.sh` retention and the unreadable-dump rule, `restore.sh`, `verify.sh` and `invariants.sh` on good and on torn data (and the failure reaching the Pushgateway), `wal-archive.sh`, and the argument checks of `pitr-restore.sh`; it also checks that the Helm chart's copies of the scripts equal `deploy/backup`.

**Run the drill (or a restore of your own) before you trust a production setup, and on a schedule.** The compose drill uses a local MinIO on the same host; it proves the scripts and the settings, not the durability of a real bucket.

## Alerts

Prometheus (`deploy/prometheus/rules/payrudder.yaml`, tested by `make test-rules`) sends to Alertmanager (`deploy/alertmanager`). Severity decides the route: **page** (wakes someone) and **warn** (working hours); a page for a service silences that service's warnings. Thresholds are defaults (not agreed): tune them with real traffic.

| Alert | Meaning | First response |
|---|---|---|
| `BackupStale` | no dump succeeded for 26 h | look at the `backup` job's log; run `backup.sh` by hand; the dumps are the fallback, fix before the next night |
| `BackupNeverReported` | the dump job never reported | is the `backup` service / CronJob running and given `PUSHGATEWAY_URL`? |
| `BackupFailed` | the last dump run failed for a database | the job log names the database; a failed `pg_dump` or an unreadable dump |
| `RestoreDrillFailed` / `RestoreNotVerified` | the newest dump could not be restored or its data is inconsistent / no drill passed for a week | `verify.sh` output names the database and the invariant; **treat an inconsistent dump as a real finding** (the live database may hold the same damage) and compare with the live database |
| `WalArchiveStale` | no WAL shipped for 15 min: the RPO is exceeded | `pitr-agent` log; is storage reachable and the credentials valid; is Postgres archiving (`SELECT * FROM pg_stat_archiver`: `last_failed_wal`); is the spool growing (`WalSpoolBacklog`) |
| `WalSpoolBacklog` | archived WAL waits over 10 min to be shipped | storage unreachable; free the disk before it fills (a full WAL disk stops Postgres): fix the upload, never delete spool files |
| `BaseBackupStale` | no base backup for 26 h | `pitr-basebackup.sh` by hand; needs REPLICATION and the socket or a pg_hba entry; without a recent base backup a restore has to replay more WAL |
| `PostgresDown`, `PostgresConnectionsHigh` | from the postgres exporter | |
| `PaymentDiscrepancy` | a charge's outcomes disagree, or a customer paid twice | `GET /admin/tenants/{tenant}/reconciliation` (payments-worker, operator token) for the tenant, find the payment, refund the second one; the differences a reconciliation keeps are in the review queue of the tenant's Payments page ([`docs/reconciliation.md`](../reconciliation.md)); see plan 22 G1/G7 |
| `PaymentCommandsErroring` | over 10% of payment commands end in an error (not a decline) | provider status page, expired or wrong credentials (`provider_accounts`), a deploy; the DLQ may be filling |
| `ChargeFailureRateHigh` | over half of the charges of 30 min failed | usually one provider or one card network; compare declines and errors by `result`; dunning will retry |
| `RefundSpike` | refunds over a quarter of successful charges in an hour | who issued them (operator, a script, a customer wave); is it a bug refunding twice |
| `ProviderWebhookFailing` | over 20% of a provider's webhooks are not accepted | `retry` = we answered 503 and the provider redelivers (lag builds); `unverified` = wrong signing secret or someone else; `malformed` = a format change |
| `DunningQueueGrowing` | invoices waiting for a dunning retry doubled in 6 h | a provider outage or a decline wave; check `ChargeFailureRateHigh`; nothing to do for the queue itself |
| `LedgerWriteFailed` | a ledger row could not be written after the provider was called (`provider`, `kind` say which): the money moved and the books are short | the command was answered, so nothing is retried by the saga; the provider's webhook or the next reconciliation (every 6 h, or run it from the Payments page) writes the missing row; if the database was the cause, fix it first (`PostgresDown`, connections); for a charge, a `paid_without_charge` discrepancy in the queue for that invoice is this |
| `RefundOutcomeUnknown` | a refund was started before and nothing records how it went; it is **not** sent again (iyzico and Paddle do not dedupe by key, plan 22 G1) | find the credit note (failed, `REFUND_OUTCOME_UNKNOWN`) and look at the refund at the provider, then use **Resolve refund** on the note (`POST /admin/v1/credit-notes/{id}/resolve-refund`): *paid back* (give the provider's reference; it is written to the payment ledger and the note becomes refunded) or *not paid back* (the refund is asked again under a new key; only after you checked, because the first one may have gone through). Never press Retry refund to settle it: the same key is refused again by iyzico and Paddle |
| `ProviderCircuitOpened` | the circuit breaker of a provider account opened after repeated outages (plan 22 G8) | the provider's status page; `GET /admin/v1/tenants/{id}/account-health` shows which account; charges to it are refused without a call and retried later, so nothing is lost; it closes by itself after a probe succeeds |
| `ProviderChargesRefused` | charges have been refused by an open breaker for ten minutes: payments are being delayed | the provider is down or unreachable from here (network, credentials revoked); if a failover account is set (`docs/provider-health.md`) check it took over; otherwise wait for the provider, and watch `ChargeFailureRateHigh` |
| `ReconciliationBacklog` | more than 10 live reconciliation discrepancies have waited an hour (the number is a default, not agreed) | the review queue on the Payments page ([`docs/reconciliation.md`](../reconciliation.md)): explain, assign or resolve them; a queue that only grows means a cause nobody is fixing (look at the kinds in `reconciliation_discrepancies_open`) |
| `SettlementOverdue` | a paid invoice is older than the settlement lag (7 days) and absent from the provider's settlement report | is the payout late at the provider; for a provider without an API (Paddle, iyzico) was the report imported (CSV import on the Payments page); only Stripe's settlement is read by Payrudder itself (UNVERIFIED against a real account) |
| `DLQGrowing` | 10 or more dead letters in 15 min | `payrudderctl dlq` to see them; fix the cause before redriving |
| `Watchdog` | **always firing** on purpose: the dead man's switch | see below |
| `AlertmanagerNotifyFailing` | an alert could not be delivered | the receiver URL / credentials |

Webhook lag is not measured directly: a provider event has no common timestamp across providers (UNVERIFIED), so a growing backlog shows as `ProviderWebhookFailing` (retries) rather than an age.

### Where alerts go

- **Default**: Alertmanager (<http://localhost:42993>) keeps them and mails Mailpit (<http://localhost:42825>): pages to `oncall@payrudder.local`, warnings to `team@payrudder.local`. Nothing leaves the laptop. `make alert-test` fires a test page and warning at the running Alertmanager so you can see the mail arrive.
- **A webhook** (a chat bridge, an incident tool): put the URL in a file and start the stack with

  ```sh
  ALERTMANAGER_CONFIG=../alertmanager/alertmanager.webhook.yml ALERT_WEBHOOK_URL_FILE=/path/to/url-file ALERT_WATCHDOG_URL_FILE=/path/to/heartbeat-url-file docker compose --profile obs up -d alertmanager
  ```

  The URLs are read from files and never written into the config.
- **Real receivers** (PagerDuty, Slack, SMTP): `deploy/alertmanager/alertmanager.receivers.example.yml` is the same route with those receivers; every secret is a `*_file` key. Copy it, replace the receivers' targets, mount the secret files, select it with `ALERTMANAGER_CONFIG`. `make test-rules` parses it with `amtool`, so the keys exist in the pinned Alertmanager; delivery to the real services was **not** tested. On Kubernetes the install of Prometheus and Alertmanager (kube-prometheus-stack with these rules and this route) is the deployment's to add.
- **The dead man's switch**: `Watchdog` always fires; Alertmanager sends it to its own receiver every minute. Point `ALERT_WATCHDOG_URL_FILE` (or the `watchdog` receiver) at a heartbeat monitor that pages when the heartbeat stops: it is the only thing that notices Prometheus, Alertmanager or the network to the receiver dying, which no alert inside the stack can report. Locally the heartbeat goes nowhere (it is visible in Alertmanager), so nothing here proves the monitor.

## What is NOT verified

- **Real S3**: everything ran against MinIO. The lifecycle rules, the `aws` CLI flags against AWS itself, bucket policies, encryption at rest (nothing encrypts the dumps or the archive; use bucket encryption and keep the keys outside), cross-region copies.
- **A cluster**: the Helm chart has the dump CronJobs and the invariants, no PITR; nothing here ran in Kubernetes.
- **Real receivers**: only Mailpit and a test webhook sink were exercised; PagerDuty, Slack, SMTP and a heartbeat monitor were not.
- **A crash**: the drill stops cleanly and restores from the archive; it does not kill Postgres mid-write, lose the volume, or run for days (retention, `archive_timeout` driving the shipping, lifecycle expiry).
- **Other stores**: Redpanda, RabbitMQ definitions, ClickHouse, MinIO (assets, invoice PDFs), Redis and the secrets key `PAYRUDDER_SECRETS_KEY` are not backed up here (plan 16, work item 3). Losing the secrets key makes the sealed provider credentials unreadable.
- **Outbox replay after a broker loss** (plan 22 G5) is described above (the outbox tables are in the database and are republished) but was not drilled.
- The alert thresholds were not tuned on real traffic and the alerts were fired by unit tests, `make alert-test` and the drill's metrics, not by real incidents.
