# Backup, restore and alerting

What is backed up, how often, how to prove it restores, how to restore, and where alerts go.

## What runs

- **Backup** (`deploy/backup/backup.sh`): `pg_dump` (custom format, compressed) of every `*_db` database, each to `<db>-<UTC time>.dump`. The newest `BACKUP_KEEP` (7) dumps of each stay; a dump that `pg_restore --list` cannot read is removed and counts as a failure. With `BACKUP_S3_URL` the files are also copied to S3-compatible storage (a volume is not a second copy of the data: set it in production).
- **Restore drill** (`deploy/backup/verify.sh`): restores the newest dump of each database into a scratch database, checks it has tables, drops it. A backup nobody has restored is a hope, not a backup.
- Both report to the Pushgateway when it is given (`PUSHGATEWAY_URL`): `payrudder_backup_last_success_timestamp_seconds`, `payrudder_backup_failed`, `payrudder_restore_verified_timestamp_seconds`, `payrudder_restore_verify_failed`.
- **Local stack**: `docker compose --profile ops up -d backup` (with `--profile obs` for the alerts). It backs up every `BACKUP_INTERVAL_SECONDS` (a day) and runs the drill every `VERIFY_EVERY_RUNS` (7) runs. Dumps are in the `backup-data` volume.
- **Kubernetes**: `payrudder-infra` chart, `backup.enabled=true`: CronJobs `postgres-backup` (02:17 daily) and `postgres-verify` (Sundays 03:47) on a volume of their own. Set `backup.s3Url`, `backup.s3SecretName` and `backup.pushgatewayUrl`.

What this does **not** give you: point-in-time recovery. A dump is a snapshot, so you lose up to a day (the interval). For less, run Postgres with WAL archiving (pgBackRest or the managed database's own PITR) and keep these dumps as the portable second copy. That is an infrastructure decision (storage target, retention) the deployment owner makes.

## Restore

```sh
# One database from a dump. Creates it; --force replaces one that exists (close its connections first).
PGHOST=... PGUSER=postgres PGPASSWORD=... sh deploy/backup/restore.sh /backups/plans_db-20261028T021700Z.dump plans_db --force
```

The dump has no owners (`--no-owner`), so the objects belong to the user who restored them. Give them back to the service's roles the way `deploy/compose/postgres/01-databases.sh` does (migrator owns the schema, app is subject to RLS, system bypasses it) — the same three statements per service — and restart the service. To check without touching production, restore into another name and look (`restore.sh <dump> plans_check`).

Order for a full loss: Postgres first (all `*_db`), then Redpanda topics (`deploy/*/redpanda/topics.sh`; events not yet consumed are lost, the outbox tables are in the dumps and are republished), then the services. ClickHouse (analytics) is rebuilt from the event stream.

## Alerts

Prometheus (`deploy/prometheus/rules/payrudder.yaml`, tested by `make test-rules`) sends to Alertmanager (`deploy/alertmanager`). Severity decides the route: **page** (wakes someone) and **warn** (working hours). A page for a service silences that service's warnings.

| Alert | Meaning |
|---|---|
| `BackupStale` | no backup succeeded for 26 h |
| `BackupNeverReported` | the backup job never reported (is it running?) |
| `BackupFailed` | the last run failed for a database |
| `RestoreDrillFailed` / `RestoreNotVerified` | the newest dump could not be restored / no drill passed for a week |
| `PostgresDown`, `PostgresConnectionsHigh` | from the postgres exporter |
| `AlertmanagerNotifyFailing` | an alert could not be delivered: check the webhook |

By default alerts only stay in Alertmanager (<http://localhost:42993>): nothing leaves a laptop. To deliver them put the webhook URL (a chat bridge, an incident tool, an on-call service) in a file and start the stack with

```sh
ALERTMANAGER_CONFIG=../alertmanager/alertmanager.webhook.yml ALERT_WEBHOOK_URL_FILE=/path/to/url-file docker compose --profile obs up -d alertmanager
```

The URL is read from a file and never written into the config. Mail receivers and the Kubernetes Prometheus/Alertmanager install (kube-prometheus-stack with these rules) are the deployment's to add; the rule file and the routing are the parts that are Payrudder's.
