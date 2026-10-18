# Phase 07 — analytics-svc + assets-svc

## Goal

Stream usage, limit and invoice facts into ClickHouse and serve fast, tenant-scoped time-series and top-N queries.
Ship assets-svc: multipart, multi-file uploads to S3-compatible storage ([assets-svc.md](../03-services/assets-svc.md)).

## Scope

**In:** ClickHouse schema + migration runner, Go batch consumer for three topics, DLQ routing, `AnalyticsService` RPCs,
gateway analytics route, query builder with mandatory tenant predicate. assets-svc (direct presigned multipart sessions,
proxied streaming `multipart/form-data`, sweeper, verifier, quota), `pkg/blob` multipart extensions, gateway asset routes.

**Out of scope:** arbitrary user-defined queries, dashboards for tenants (Phase 12 may show a chart).

## Tasks

- [x] ClickHouse migrations: `usage_events`, `usage_hourly` + MV, `limit_events`, `invoice_facts`
- [x] Tiny migration runner (`schema_migrations` table in ClickHouse)
- [x] Consumer: batch by 10k rows or 1 s per topic, `clickhouse-go/v2` batch insert, commit after insert
- [x] DLQ for undecodable records (`x-dlq-consumer=analytics`), honouring `x-redrive-target`
- [x] Query builder: typed filters, `tenant_id` required by construction (no raw SQL path)
- [x] `QueryUsage` (hour/day granularity, from `usage_hourly`, exact mode from `usage_events FINAL` for ranges ≤ 1 day)
- [x] `TopCustomers`, `GetIngestStats` (events/s, late %, duplicate % per tenant)
- [x] Gateway `GET /v1/analytics/usage`

**assets-svc**

- [x] `proto/quotify/assets/v1`, `assets_db` migrations + RLS (`upload_sessions`, `assets`, `storage_usage`)
- [x] `pkg/blob`: `CreateMultipart`, `PresignPart` (optional `x-amz-checksum-sha256`), `ListParts`, `CompleteMultipart`, `AbortMultipart`, `PutStream`
- [x] `CreateUploadSession` (≤ 50 files, part-size rule, first 100 presigned parts, `Idempotency-Key`), `PresignParts`, `GetUploadFile` (resume)
- [x] `CompleteFile`: `ListParts` verification → complete → `HeadObject` → quota update + `READY` in one transaction. Idempotent
- [x] `UploadAsset` client stream → upload manager. Content sniffing on the first chunk. Abort on stream error
- [x] Gateway `POST /v1/assets`: `multipart.Reader` streaming, ≤ 20 files / 25 MiB each / 100 MiB total, per-file results
- [x] Sweeper (expired sessions → abort), background sha256 verifier, orphan reconciler. Bucket rule `AbortIncompleteMultipartUpload`
- [x] Purpose allowlists and limits, filename sanitizer, storage quota per tier
- [x] plans-svc: `invoice_logo_asset_id` tenant setting. invoice-worker draws the logo

## Deliverables

- analytics-svc image. Usage charts queryable through the public API.

## Acceptance criteria

- [x] Integration: 10k events with 5% duplicate deliveries → `usage_events FINAL` totals equal aggregator totals exactly
- [x] Integration: the drift between `usage_hourly` and `FINAL` totals is reported (measured, not asserted zero)
- [x] Unit: query builder refuses to build a query without `tenant_id` (compile-time via constructor, plus runtime test)
- [x] Integration: tenant A can't see tenant B's series through the API
- [x] Integration: ClickHouse stopped for 30 s → no data loss after recovery (lag drains, totals exact)
- [x] Integration (MinIO): 3 files in one session (200 MiB, 20 MiB, 1 KiB) uploaded with 8 parallel parts each → all `READY`, sizes and sha256 match
- [x] Integration: upload interrupted after half the parts → `GetUploadFile` lists the uploaded parts → only missing parts re-sent → `READY`
- [x] Integration: `CompleteFile` with a missing or wrong ETag → `REJECTED`, no object left. Calling `CompleteFile` twice → same asset
- [x] Integration: session expired → sweeper aborts the S3 multipart upload (`ListMultipartUploads` empty) and marks `ABORTED`
- [x] Integration: `POST /v1/assets` with 20 files (one with a spoofed content type) → 19 `created`, 1 `CONTENT_TYPE_MISMATCH`. Gateway heap growth bounded (measured)
- [x] Integration: client disconnects mid-stream → S3 multipart aborted, asset `ABORTED`
- [x] Integration: tenant B can't complete, presign or download tenant A's upload (`NOT_FOUND`). Quota exceeded → `STORAGE_QUOTA_EXCEEDED`
- [x] Load (informal): 30-day hourly series for one tenant returns in < 300 ms p95 (**target**, recorded in Phase 9)

## Dependencies

Phase 02 (usage topic). Phases 04/05 for the limit and invoice topics (the consumer can start with usage only).
Phase 06 (`pkg/blob`, MinIO wiring) for assets-svc.

## Risks / open questions

- Two services in one week is tight. If it runs long, assets-svc's verifier and orphan reconciler move to Phase 11.
- Presigned part URLs can't enforce a part's size. Sizes are verified at completion from `ListParts`, and a tenant can only waste its own quota until then.
- Exact vs. rollup semantics might confuse API users. The response includes `"precision": "estimated" | "exact"`.
- Should the rollup dedup via `AggregatingMergeTree` with `uniqExact(event_id)`? Too costly at volume. Revisit if drift is significant.

## Notes (as built)

**analytics-svc.** ClickHouse tables as sketched, plus `customer_external_id` on `usage_events` (top-N answers carry the
external id without lookups). `internal/chmigrate` is the tiny runner (`schema_migrations`, statements split on a
semicolon at the end of a line). The consumer (`internal/consumer`) reads three topics in one group, batches by 10 000
rows or 1 s, inserts, then commits; while ClickHouse is down it keeps retrying the same batch (backoff to 10 s) without
polling further, so nothing is skipped and nothing commits. Undecodable records go to the DLQ of their topic with
`x-dlq-consumer=analytics`; records carrying `x-redrive-target` for another group are skipped. `internal/query` is the
only place SQL is built: `ForTenant` refuses the nil tenant, every query puts `tenant_id = ?` first and binds it as the first
argument (a unit test asserts it for every variant); the cross-tenant ingest statistics need `AllTenants(operator=true)`
and cannot build series queries. Day buckets use the tenant's time zone. "Late" in the ingest stats means ingested more
than 24 h after the event time (the exact classification is the aggregator's).
Measured drift on 10 000 events with 5 % duplicate deliveries: the hourly rollup over-counts by 5.04 % (it sees every
delivery); `FINAL` and the exact series match the aggregator's totals to the unit.

**assets-svc.** `internal/policy` (purpose allowlists and limits, part sizing, filename sanitiser, content sniffing),
`internal/service` (sessions, presigned parts, resume, completion, proxied stream, quota), `internal/background` (sweeper,
verifier, orphan reconciler). Decisions: the quota is checked at session creation (early answer) and authoritatively in the
completion transaction; `CompleteFile` holds the asset row lock across its S3 calls, so concurrent completions serialise and
the second finds a READY asset; a part-set that differs from what S3 lists (count, numbers, ETags, sizes) rejects the file and
aborts the multipart upload; a crash between `CompleteMultipartUpload` and the database update is repaired by the retry
(`NoSuchUpload` + `HeadObject`). The proxied path sniffs the first 512 bytes before anything reaches S3, cuts a stream off at
its purpose limit and aborts the S3 upload when the client's stream breaks. Application error codes travel as gRPC
`ErrorInfo.reason` and become the `code` of the gateway's error envelope (`STORAGE_QUOTA_EXCEEDED`, ...).
Gateway: `POST /v1/assets` streams `multipart/form-data` (per-route body limit of 101 MiB, 20 files, 25 MiB each, one `write`
token per extra file); `PUT /v1/tenant/invoice-logo` sets the tenant's invoice logo, which billing-svc puts in the invoice
snapshot and invoice-worker draws from the assets bucket (only from the command tenant's own prefix).
`pkg/blob` gained the multipart toolkit (`CreateMultipart`, `PresignPart`, `ListParts`, `CompleteMultipart`, `AbortMultipart`,
`ListMultipartUploads`, `PutStream`, `Open`, `GetRange`, `PresignDownload`, `Keys`). Tenant metadata now carries the tier
(`x-quotify-tier`) for quotas.
Deviations: the heap-growth measurement of the form upload and the 1 GiB direct upload memory target are left to Phase 09's
load work; `usage_hourly` p95 is likewise a Phase 09 measurement.
