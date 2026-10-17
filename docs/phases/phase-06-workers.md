# Phase 06 — RabbitMQ workers

## Goal

Replace the stub worker with real invoice-worker (PDF → MinIO), payments-worker (mock-psp) and notifier (Mailpit),
each idempotent and using the TTL retry tiers and dead-letter exchanges.

## Scope

**In:** shared worker skeleton in `pkg/rabbitx`, retry/DLX topology, the three workers, `tools/mockpsp`, limit-exceeded
emails in notifier (Kafka consumer), `quotifyctl dlq` for RabbitMQ, `pkg/blob` (S3 API), `PaymentProvider` routing and sandbox payment scenarios.

**Out of scope:** real PSPs, real email providers, PDF localization.

## Tasks

- [ ] `pkg/rabbitx` worker skeleton: decode envelope, inbox check, handler, classify error, retry-publish / reply / nack
- [ ] Retry topology: `billing.commands.retry`, `{queue}.retry.{5s,30s,2m}`, `billing.commands.dlx`, `{queue}.dlq`. Quorum queues with `x-delivery-limit=5`
- [ ] Export `deploy/rabbitmq/definitions.json` from the Go topology definition (test asserts they match)
- [ ] `pkg/blob`: S3 client (`aws-sdk-go-v2`) with endpoint/path-style config, contract tests against MinIO ([ADR 0013](../adr/0013-s3-compatible-object-storage.md))
- [ ] invoice-worker: `go-pdf/fpdf` template, deterministic output, S3 put with deterministic key, sha256 idempotency, sandbox watermark + `environment=sandbox` object tag
- [ ] `tools/mockpsp`: charges, refunds, status by idempotency key, failure/timeout/latency env knobs, `pm_sandbox_*` scenario tokens. Own DB
- [ ] payments-worker: `PaymentProvider` interface, routing by `environment` (sandbox ⇒ mockpsp, hard-coded; live ⇒ `PAYMENTS_LIVE_PROVIDER`), HTTP client with timeouts, error mapping (402 → declined, 5xx → transient, timeout → errored), `payment_attempts`
- [ ] notifier: SMTP client, `[SANDBOX]` prefix + sandbox SMTP sink, embedded templates, `sent_notifications` SENDING/SENT protocol
- [ ] notifier: Kafka consumer on `usage.limit-exceeded.v1` (group `notifier`) → limit email
- [ ] `quotifyctl dlq ls|show|redrive|purge --broker rabbitmq`
- [ ] Remove stub-worker usage from e2e (keep it for billing-svc unit/integration speed)

## Deliverables

- Three worker images + mock-psp image. Mailpit shows invoice and limit emails. MinIO (via the S3 API) holds PDFs.
- `GET /v1/invoices/{id}/pdf` returns a presigned URL.

## Acceptance criteria

- [ ] Integration: a redelivered `RenderInvoicePdf` → one object in S3 (MinIO), the same reply twice, no re-render (sha256 match)
- [ ] Integration: mock-psp 5xx twice then success → `PaymentSucceeded` after 2 retries via the 5 s and 30 s tiers (timestamps verified)
- [ ] Integration: `MOCKPSP_TIMEOUT_RATE=1` → saga `PAYMENT_UNKNOWN` → status query → `PAID`. mock-psp shows **one** charge
- [ ] Integration: a handler that panics on a specific command → message in `{queue}.dlq` after 5 deliveries. Saga reaches `*_FAILED` via deadline
- [ ] Integration: undecodable message → DLQ immediately, no reply
- [ ] Integration: `SendNotification` redelivered after SENT → no second email in Mailpit
- [ ] Integration: `LimitExceeded` delivered twice → one email
- [ ] E2E: seed → events → close → invoice PDF in MinIO, payment in mock-psp, email in Mailpit
- [ ] E2E (sandbox): one customer per `pm_sandbox_*` token → each saga ends in the state listed in [workers.md](../03-services/workers.md#payments-worker), with exactly one successful charge where `PAID`
- [ ] Integration: `PAYMENTS_LIVE_PROVIDER` set to a recording fake → zero calls for sandbox commands
- [ ] Integration: `pkg/blob` contract suite passes against MinIO with `S3_FORCE_PATH_STYLE=true`

## Dependencies

Phase 05 (command contracts, saga). Phase 04 (limit events) for the notifier Kafka part.

## Risks / open questions

- **Decided:** S3-compatible storage, MinIO locally ([ADR 0013](../adr/0013-s3-compatible-object-storage.md)).
- **Decided:** mock-psp is the sandbox provider; live goes through `PaymentProvider` ([ADR 0011](../adr/0011-sandbox-environment.md)).
- At-least-once email (SENDING older than 5 min is re-sent) can produce rare duplicates. Accept, or require an idempotent provider API (Mailpit doesn't have one).
