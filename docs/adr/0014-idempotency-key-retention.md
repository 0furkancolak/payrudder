# ADR 0014 — Idempotency keys live in month partitions that are dropped, not deleted

- Status: Accepted (confirms the Phase 02 decision with a measurement)
- Date: 2026-10-19

## Context

ingest-svc deduplicates events by `(tenant, idempotency_key)`. A key is remembered for **at least 45 days and less than 76 days** after it was first seen (corrected
2026-11-01; this ADR said "at least two and less than three months", which is how far the lookup reaches, not how long keys are kept; see Decision). The 35 days of
allowed lateness are what the window has to cover: ingest refuses an event whose `event_time` is older than 35 days, so a repeat of an event can only be accepted
while it is at most 35 days after the event, and therefore at most 35 days after the key was first seen. The table must stay bounded: old keys have to go. Two ways were considered in Phase 02 ([data model](../05-data-model.md#ingest_db)):
monthly range partitions that are dropped when they expire (what the migration does), or one plain table pruned by a
batched `DELETE` job. The data model note promised a benchmark of both.

## Measurement

`tests/bench/idempotency_bench_test.go` (`make bench-idempotency`) fills 3 months with 3,000,000 keys (four writers, batches of
500 through the ingest path: look up the batch, `INSERT … ON CONFLICT DO NOTHING`), measures insert throughput on the current month,
then retires the oldest month while inserts continue. Postgres 17 in the local compose stack on an Apple M4 laptop (Docker VM
shared with the rest of the stack, so absolute numbers are modest; the comparison is what matters). One run, rounded:

| | partitioned (drop) | unpartitioned (batched `DELETE`, 10k rows) |
|---|---|---|
| fill, 3M rows | 14.7 s (204k rows/s) | 25.8 s (116k rows/s) |
| steady insert (current month) | 198k rows/s, batch p99 48 ms | 113k rows/s, batch p99 156 ms |
| retire the oldest month (1M keys) | **24 ms**, 1 statement | **8.4 s**, 101 statements |
| inserts during retirement | 149k rows/s, batch p99 145 ms | 78k rows/s, batch p99 389 ms |
| dead tuples left behind | 0 | 1,000,000 |
| `VACUUM` afterwards | 0.7 s | 5.0 s; the table does not shrink (1.0 GiB before and after) |

An earlier run (before the size query was fixed) showed the same ordering: retire 5 ms vs 11.5 s, steady insert 166k vs 93k rows/s.

## Decision

Keep monthly partitions and `maintain_idempotency_partitions()`. Dropping a partition is metadata-only, leaves no bloat and
takes no row locks, so retention costs nothing on the hot path; the plain-table variant pays for every expired key twice (a
delete now, a vacuum later) and slows inserts while it runs. The price of partitioning is the composite primary key
(`created_at` has to be part of it), which the write path already accounts for by checking the current and the two previous months (35 days of allowed lateness can span three calendar months).

**The two numbers.** A key's `created_at` is the start of the month it was first seen in, and its partition is that month's. `maintain_idempotency_partitions` drops a
partition when its month ended more than **45 days** ago (`maintenance.go`, `queries/maintenance.sql`). A key first seen in the last second of a month is therefore kept
45 days, one first seen in the first second of a 31-day month 76 days: that is the window. The lookup reaches back over three buckets (the current month and the two
before it), which is at least two and less than three months; it never binds, because the partition of the oldest bucket is gone before that. The 45 days are the 35 of
lateness plus **10 days** of margin (a maintenance run that is late, clock skew between replicas). The margin is the only thing that makes the retention safe, so it is
tested: `TestIngestSvc/KeyRetentionOutlivesTheLateWindow` runs the maintenance at the boundary and fails if a partition goes before `domain.MaxLateness` after its month's end,
or stays after 45 days. Raising `MaxLateness` past 45 days, or lowering the retention to it, needs a new decision here. (The comment in `00001_init.sql` that says "at least
two and less than three months" is the older wording; a baseline migration is not edited.)

## Consequences

- Retention never needs a job that scales with the number of keys; only the partition-maintenance function runs, daily.
- If the dedup window ever has to be shorter than a month, partitions per week are the next step, not `DELETE`.
