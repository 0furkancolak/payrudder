# Phase 06 — RabbitMQ workers

## Goal

Replace the stub worker with real invoice-worker (PDF → MinIO), payments-worker (mock-psp) and notifier (Mailpit),
each idempotent and using the TTL retry tiers and dead-letter exchanges.

## Scope

**In:** shared worker skeleton in `pkg/rabbitx`, retry/DLX topology, the three workers, `tools/mockpsp`, limit-exceeded
emails in notifier (Kafka consumer), `quotifyctl dlq` for RabbitMQ, `pkg/blob` (S3 API), `PaymentProvider` routing and sandbox payment scenarios.

**Out of scope:** real PSPs, real email providers, PDF localization.

## Tasks

- [x] `pkg/rabbitx` worker skeleton: decode envelope, inbox check, handler, classify error, retry-publish / reply / nack
- [x] Retry topology: `billing.commands.retry`, `{queue}.retry.{5s,30s,2m}`, `billing.commands.dlx`, `{queue}.dlq`. Quorum queues with `x-delivery-limit=5`
- [x] Export `deploy/rabbitmq/definitions.json` from the Go topology definition (test asserts they match)
- [x] `pkg/blob`: S3 client (`aws-sdk-go-v2`) with endpoint/path-style config, contract tests against MinIO ([ADR 0013](../adr/0013-s3-compatible-object-storage.md))
- [x] invoice-worker: `go-pdf/fpdf` template, deterministic output, S3 put with deterministic key, sha256 idempotency, sandbox watermark + `environment=sandbox` object tag
- [x] `tools/mockpsp`: charges, refunds, status by idempotency key, failure/timeout/latency env knobs, `pm_sandbox_*` scenario tokens. Own DB
- [x] payments-worker: `PaymentProvider` interface, routing by `environment` (sandbox ⇒ mockpsp, hard-coded; live ⇒ `PAYMENTS_LIVE_PROVIDER`), HTTP client with timeouts, error mapping (402 → declined, 5xx → transient, timeout → errored), `payment_attempts`
- [x] notifier: SMTP client, `[SANDBOX]` prefix + sandbox SMTP sink, embedded templates, `sent_notifications` SENDING/SENT protocol
- [x] notifier: Kafka consumer on `usage.limit-exceeded.v1` (group `notifier`) → limit email
- [x] `quotifyctl dlq ls|show|redrive|purge --broker rabbitmq`
- [x] Remove stub-worker usage from e2e (kept in billing-svc's integration tests for speed)

## Deliverables

- Three worker images + mock-psp image. Mailpit shows invoice and limit emails. MinIO (via the S3 API) holds PDFs.
- `GET /v1/invoices/{id}/pdf` returns a presigned URL.

## Acceptance criteria

- [x] Integration: a redelivered `RenderInvoicePdf` → one object in S3 (MinIO), the same reply twice, no re-render (sha256 match)
- [x] Integration: mock-psp 5xx twice then success → `PaymentSucceeded` after 2 retries via the 5 s and 30 s tiers (timestamps verified)
- [x] Integration: `MOCKPSP_TIMEOUT_RATE=1` → saga `PAYMENT_UNKNOWN` → status query → `PAID`. mock-psp shows **one** charge
- [x] Integration: a handler that panics on a specific command → message in `{queue}.dlq` after the delivery limit (RabbitMQ counts returns: the first delivery plus 5 redeliveries = 6). Saga reaches `*_FAILED` via deadline (`TestSilentWorker…` in billing-svc)
- [x] Integration: undecodable message → DLQ immediately, no reply
- [x] Integration: `SendNotification` redelivered after SENT → no second email in Mailpit
- [x] Integration: `LimitExceeded` delivered twice → one email
- [x] E2E: seed → events → close → invoice PDF in MinIO, payment in mock-psp, email in Mailpit
- [x] E2E (sandbox): one customer per `pm_sandbox_*` token → each saga ends in the state listed in [workers.md](../03-services/workers.md#payments-worker), with exactly one successful charge where `PAID`
- [x] Integration: `PAYMENTS_LIVE_PROVIDER` set to a recording fake → zero calls for sandbox commands
- [x] Integration: `pkg/blob` contract suite passes against MinIO with `S3_FORCE_PATH_STYLE=true`

## Dependencies

Phase 05 (command contracts, saga). Phase 04 (limit events) for the notifier Kafka part.

## Risks / open questions

- **Decided:** S3-compatible storage, MinIO locally ([ADR 0013](../adr/0013-s3-compatible-object-storage.md)).
- **Decided:** mock-psp is the sandbox provider; live goes through `PaymentProvider` ([ADR 0011](../adr/0011-sandbox-environment.md)).
- At-least-once email (SENDING older than 5 min is re-sent) can produce rare duplicates. Accept, or require an idempotent provider API (Mailpit doesn't have one).

## Notes (as built)

- **Skeleton.** `pkg/workerx` (typed commands, inbox, error classification, reply with confirms before ack) on top of
  `pkg/rabbitx`. A handler returns a reply for business outcomes and an error for "not now" (retried through the 5 s / 30 s /
  2 min queues, then the `.dlq`); `workerx.ErrPoison` and undecodable bodies are dead-lettered at once with no reply; a
  panic requeues, so the quorum queue's delivery limit ends a crash loop. Duplicates of one command that are in flight in
  the same process are serialized, so the second finds the first's reply in the inbox.
- **invoice-worker** has no database: the deterministic key `{tenant}/{invoice}/v{n}.pdf` and the object's own `sha256`
  metadata are the idempotency record; renders of one key are serialized per process. Objects are tagged
  `environment=sandbox|live`; the bucket lifecycle (30 days) matches the sandbox tag. Sandbox PDFs carry the watermark.
- **payments-worker** keeps `payment_attempts` (STARTED before the provider call, DONE with the stored reply), routes by the
  command's environment (sandbox ⇒ mock-psp, hard-coded in `main`; live ⇒ `PAYMENTS_LIVE_PROVIDER`, unset ⇒
  `PaymentErrored{PROVIDER_NOT_CONFIGURED}`), and maps 402 ⇒ declined, 5xx ⇒ retry, client timeout ⇒ non-retryable
  `PaymentErrored` (billing queries the status; the worker never re-charges on a timeout).
- **mock-psp** (`tools/mockpsp`, `mockpsp_db`): idempotent charges by key, `GET /v1/charges?idempotency_key=` (status) and
  `?invoice_id=` (tests), refunds, the `MOCKPSP_*` knobs, and all six `pm_sandbox_*` scenarios.
- **notifier** sends over SMTP (`SMTP_ADDR`; sandbox mail goes to `SANDBOX_SMTP_ADDR` when set), renders embedded
  `html/template`s, and follows the SENDING/SENT protocol: the claim is inserted before sending, deleted when the send
  certainly failed (so the retry is immediate), and re-sent when older than `NOTIFIER_SENDING_STALE` (5 min, at-least-once).
  Limit alerts: a Kafka consumer with offsets in `notifier_db`, dedup on `LimitExceeded.event_id`, DLQ for undecodable events
  and unknown customers (plans-svc `GetCustomer` now also takes the internal `id`).
- **Gateway/billing:** `GET /v1/invoices/{id}/pdf` returns a presigned link (5 min) built by billing-svc from the stored
  object key (`S3_PUBLIC_ENDPOINT` is the host-reachable MinIO address). `QUOTIFY_DUNNING_SANDBOX` / `QUOTIFY_DUNNING_LIVE`
  configure the dunning delays; CI sets the sandbox ones to seconds.
- **quotifyctl dlq** (`ls|show|redrive|purge`, RabbitMQ only) talks to the broker directly: dead letters are not part of the
  public API. Kafka dead letters are inspected with Redpanda Console; `redrive` for Kafka is left to Phase 09.
