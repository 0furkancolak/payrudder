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

- [ ] ClickHouse migrations: `usage_events`, `usage_hourly` + MV, `limit_events`, `invoice_facts`
- [ ] Tiny migration runner (`schema_migrations` table in ClickHouse)
- [ ] Consumer: batch by 10k rows or 1 s per topic, `clickhouse-go/v2` batch insert, commit after insert
- [ ] DLQ for undecodable records (`x-dlq-consumer=analytics`), honouring `x-redrive-target`
- [ ] Query builder: typed filters, `tenant_id` required by construction (no raw SQL path)
- [ ] `QueryUsage` (hour/day granularity, from `usage_hourly`, exact mode from `usage_events FINAL` for ranges ≤ 1 day)
- [ ] `TopCustomers`, `GetIngestStats` (events/s, late %, duplicate % per tenant)
- [ ] Gateway `GET /v1/analytics/usage`

**assets-svc**

- [ ] `proto/quotify/assets/v1`, `assets_db` migrations + RLS (`upload_sessions`, `assets`, `storage_usage`)
- [ ] `pkg/blob`: `CreateMultipart`, `PresignPart` (optional `x-amz-checksum-sha256`), `ListParts`, `CompleteMultipart`, `AbortMultipart`, `PutStream`
- [ ] `CreateUploadSession` (≤ 50 files, part-size rule, first 100 presigned parts, `Idempotency-Key`), `PresignParts`, `GetUploadFile` (resume)
- [ ] `CompleteFile`: `ListParts` verification → complete → `HeadObject` → quota update + `READY` in one transaction. Idempotent
- [ ] `UploadAsset` client stream → upload manager. Content sniffing on the first chunk. Abort on stream error
- [ ] Gateway `POST /v1/assets`: `multipart.Reader` streaming, ≤ 20 files / 25 MiB each / 100 MiB total, per-file results
- [ ] Sweeper (expired sessions → abort), background sha256 verifier, orphan reconciler. Bucket rule `AbortIncompleteMultipartUpload`
- [ ] Purpose allowlists and limits, filename sanitizer, storage quota per tier
- [ ] plans-svc: `invoice_logo_asset_id` tenant setting. invoice-worker draws the logo

## Deliverables

- analytics-svc image. Usage charts queryable through the public API.

## Acceptance criteria

- [ ] Integration: 10k events with 5% duplicate deliveries → `usage_events FINAL` totals equal aggregator totals exactly
- [ ] Integration: the drift between `usage_hourly` and `FINAL` totals is reported (measured, not asserted zero)
- [ ] Unit: query builder refuses to build a query without `tenant_id` (compile-time via constructor, plus runtime test)
- [ ] Integration: tenant A can't see tenant B's series through the API
- [ ] Integration: ClickHouse stopped for 30 s → no data loss after recovery (lag drains, totals exact)
- [ ] Integration (MinIO): 3 files in one session (200 MiB, 20 MiB, 1 KiB) uploaded with 8 parallel parts each → all `READY`, sizes and sha256 match
- [ ] Integration: upload interrupted after half the parts → `GetUploadFile` lists the uploaded parts → only missing parts re-sent → `READY`
- [ ] Integration: `CompleteFile` with a missing or wrong ETag → `REJECTED`, no object left. Calling `CompleteFile` twice → same asset
- [ ] Integration: session expired → sweeper aborts the S3 multipart upload (`ListMultipartUploads` empty) and marks `ABORTED`
- [ ] Integration: `POST /v1/assets` with 20 files (one with a spoofed content type) → 19 `created`, 1 `CONTENT_TYPE_MISMATCH`. Gateway heap growth bounded (measured)
- [ ] Integration: client disconnects mid-stream → S3 multipart aborted, asset `ABORTED`
- [ ] Integration: tenant B can't complete, presign or download tenant A's upload (`NOT_FOUND`). Quota exceeded → `STORAGE_QUOTA_EXCEEDED`
- [ ] Load (informal): 30-day hourly series for one tenant returns in < 300 ms p95 (**target**, recorded in Phase 9)

## Dependencies

Phase 02 (usage topic). Phases 04/05 for the limit and invoice topics (the consumer can start with usage only).
Phase 06 (`pkg/blob`, MinIO wiring) for assets-svc.

## Risks / open questions

- Two services in one week is tight. If it runs long, assets-svc's verifier and orphan reconciler move to Phase 11.
- Presigned part URLs can't enforce a part's size. Sizes are verified at completion from `ListParts`, and a tenant can only waste its own quota until then.
- Exact vs. rollup semantics might confuse API users. The response includes `"precision": "estimated" | "exact"`.
- Should the rollup dedup via `AggregatingMergeTree` with `uniqExact(event_id)`? Too costly at volume. Revisit if drift is significant.
