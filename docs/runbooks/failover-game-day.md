# Failover and the game day

Plan: [16](../plans/16-backup-alerting.md). What to do when a stateful part is gone, and how to prove on a scratch environment that it works. The components, what is backed up and the objectives are in [stateful-components](stateful-components.md); restore commands are in [backup-restore](backup-restore.md); the alerts are in its [alert table](backup-restore.md#alerts). **There is no automatic failover for the data stores in this repository:** Postgres is one primary (no streaming replica, no promotion), and "failover" for it is a restore. What fails over by itself is the stateless side: Kubernetes reschedules a service, a replica set keeps serving while one pod dies (the rollout test and the chaos suite exercise that), the gateway answers `503` when plans-svc cannot be reached after its 60-second cache (`docs/02-architecture.md`).

## When something is gone

Do the first block of each scenario in order; stop when the symptom has gone. Every scenario ends with [what to read after a restore](stateful-components.md#what-to-read-after-a-restore).

### The Postgres host or volume is lost
1. Silence nothing: `PostgresDown` and the services' readiness page; that is correct.
2. Pick the target: `latest` (the end of the archive) unless a bad write is the cause, then the minute before it, **in UTC**.
3. `pitr-restore.sh '<UTC time>' <empty data dir>` on a host of the same major version; start Postgres on it **without archiving** to the production prefix; wait for `pg_is_in_recovery()` to be `f`.
4. Check: `invariants.sh`, `migrations-version.sh`, `testdata/roles.sh`; `SELECT max(written_at) FROM payrudder_pitr_markers` is the last marker, i.e. how far the copy reaches.
5. Point the services at it (connection strings; a new address means a rollout), start them, read `/readyz` of each, watch `OutboxStalled`, `ConsumerLagHigh`, `SagaStuck`.
6. New timeline: take a base backup, switch archiving on for the new server under a new prefix, and only then call it done. **If only dumps exist (Kubernetes today):** `restore.sh <newest dump> <db>` per database; the loss is up to a day; compare the invoices of the last day with the provider's settlement report (the reconciliation page) before sending anything to customers.

### Data was deleted or damaged by a bad deploy or an operator
Restore to the minute before it into a scratch server (as above, on another host or volume), look at the rows, copy back what is needed with SQL; do not restore over production unless everything after that minute is to be discarded. The audit log (`audit_events`) says who did what and when; it is in the same database, so it is in the copy.

### Redpanda is lost
1. Bring up the cluster, `make topics` (the install job does it on Kubernetes). Services that publish through an outbox resume and republish what had not left the database. The consumers must **not** start on the old progress: the aggregator and the notifier keep their offsets in **Postgres** (`aggregator_db.consumer_offsets`, `notifier_db.kafka_offsets`, written in the transaction of their effects) and limits-svc in **Redis** (`lim:off:*`), so a lost broker does not take them with it, and a consumer that resumes at an offset of the old log in a new, shorter log silently skips whatever the new log holds before that position. `TestAggregator/ALostBrokerNeedsTheConsumerProgressDeletedBeforeTheOutboxIsReplayed` loses an event exactly this way, then shows the fix. Before the consumers start:
   - `DELETE FROM consumer_offsets WHERE topic = 'usage.events.v1'` in `aggregator_db` and `DELETE FROM kafka_offsets WHERE topic IN (...)` in `notifier_db`, **as the database owner** (the services' roles have no DELETE on them: offsets only ever advance; that is deliberate), and delete the `lim:off:*` keys in Redis (`redis-cli --scan --pattern 'lim:off:*'`). analytics-svc keeps its offsets in Kafka and loses them with the broker.
   - Start the consumers: they read from the start of what exists; what they already applied is recognised (`processed_events`, the dedup on the event id) and counted once, what the outboxes published again is not counted twice, and what was published but never consumed before the loss is gone.
2. **The raw usage events that were in the lost topics are lost** ([stateful-components](stateful-components.md#gaps-in-the-order-they-matter), gap 1) unless the topic was exported ([export with rpk](stateful-components.md#exporting-and-importing-a-redpanda-topic-with-rpk)). Say so to the tenants whose open period is affected; closed periods keep their invoices.
3. Watch `ConsumerLagHigh`, `DLQNotEmpty`, `ReconcileDriftHigh` and the duplicates counter `aggregator_duplicates_skipped`; run the reconciliation from the Payments page.

**Tested:** the aggregator's part (one partition, five events, the stale-progress loss and the fix). **Not tested:** the notifier's and limits-svc's parts (they follow from their code), the loss of events relayed and not consumed (no copy exists), a broker lost under load.

### RabbitMQ is lost
Start it; services declare queues. Wait one saga-sweep interval; `SagaStuck` and the DLQ tell what did not resume (`payrudderctl dlq`). A charge is never sent twice for the same invoice (idempotency key); a refund whose outcome is unknown is **not** sent again (`RefundOutcomeUnknown`).

### Redis is lost
Start it. Rate limits start at zero; API keys are re-read from plans-svc; limit counters rebuild from the streams. Nothing to restore.

### ClickHouse is lost
`clickhouse-backup.sh restore <name>` into an empty server (the newest name: `clickhouse-backup.sh list`), or let analytics-svc rebuild from the stream if it still holds the history.

### MinIO / S3 is lost
`object-backup.sh restore` from the copy; invoice PDFs missing after that are rendered again from their invoices; assets (logos, uploads) come back only from the copy. The `backups` bucket itself must not be on the lost volume: if it was, the Postgres backups are gone too (see the last gap of [stateful-components](stateful-components.md#gaps-in-the-order-they-matter)).

### The secrets key is lost
[The break-glass](secrets-key-rotation.md#if-the-key-is-lost-break-glass). Read the key from a running replica **before** any restart.

### A whole cell or region is lost
[cells](cells.md) and [scaling](scaling.md): a cell is a Postgres database per service on some cluster; its restore is the Postgres scenario above for each database of the cell, and the tenants it held are reached again when `tenants.cell` points at the restored cluster.

## The game day

Run it on a scratch environment, never on production: a compose project of its own (`make backup-drill` makes one by itself), or a kind cluster. About half a day the first time; write the result into the table at the end and into plan 16. Quarterly is a suggestion (default, not agreed).

| Step | Command | Pass means |
|---|---|---|
| 1. The scripts | `make test-backup-scripts` and `make test-store-backups` | both end with "all passed" |
| 2. The alert rules | `make test-rules` | every rule fires in a unit test and Alertmanager routes a page, a warning and the Watchdog (`tests/alerts` keeps this true when a rule is added) |
| 3. Postgres to a chosen minute | `make backup-drill` | RESTORED line, invariants hold, migrations equal, roles ok, the metrics reached the Pushgateway, RTO inside the objective |
| 4. **A crash, not a clean stop** (not scripted) | in a scratch stack with PITR on: write rows in a loop, `docker kill -s KILL <postgres>` mid-write, wait for the restart, then restore to a minute before the kill on a second volume | the restored copy passes `invariants.sh`; the rows after the last shipped segment are the only ones missing |
| 5. The services on the restored copy (not scripted) | a full stack (`make up`), stop the services, point billing-svc and plans-svc at the restored database, start them | `curl :<metrics port>/readyz` answers 200 for each, `make e2e` passes |
| 6. The alerts reach a person | `make alert-test` | a page and a warning arrive at the receiver (Mailpit locally; the real receiver on a deployment); the Watchdog heartbeat arrives at the heartbeat monitor and the monitor pages when you stop Alertmanager |
| 7. Objects and analytics | delete a bucket's contents on the scratch MinIO, `object-backup.sh restore`; drop the ClickHouse database, `clickhouse-backup.sh restore <name>` | the same objects and rows |
| 8. Write it down | the table below | |

| Date | Environment | Step 3 RTO | Step 4 done | Step 5 done | Found / fixed |
|---|---|---|---|---|---|
| 2026-11-02 (scripts only) | compose project on a laptop | 33 s (small database) | no | no | step 3 now also checks migrations and the service roles; the alert test list found five rules without a firing test and one whose "firing" test only tested quiet |

The first row is what this repository has run; steps 4 and 5 are open.

## Firing every alert once

`tests/alerts` and `make test-rules` are the list: each rule of `deploy/prometheus/rules/payrudder.yaml` has a unit test in `deploy/prometheus/tests/payrudder_test.yaml` in which it fires with the labels and annotations the receiver will see, and most have a test in which it stays quiet; `tests/alerts` fails when a rule has no firing test or no entry in [the alert table](backup-restore.md#alerts) or in [observability](../08-observability.md). That proves the expressions and the labels, **not** the path to a person: for that, `make alert-test` (a page and a warning to the running Alertmanager) and the heartbeat of the `Watchdog` to the outside monitor, both on the real receiver of the deployment. A rule that was never fired by a real incident has thresholds that are guesses.
