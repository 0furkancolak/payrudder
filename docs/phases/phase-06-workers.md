# Phase 06 — RabbitMQ workers

## Goal

Replace the stub worker with real invoice-worker (PDF → MinIO), payments-worker (mock-psp) and notifier (Mailpit),
each idempotent and using the TTL retry tiers and dead-letter exchanges.

## Scope

**In:** shared worker skeleton in `pkg/rabbitx`, retry/DLX topology, the three workers, `tools/mockpsp`, limit-exceeded
emails in notifier (Kafka consumer), `quotifyctl dlq` for RabbitMQ.

**Out of scope:** real PSPs, real email providers, PDF localization.

## Tasks

- [ ] `pkg/rabbitx` worker skeleton: decode envelope, inbox check, handler, classify error, retry-publish / reply / nack
- [ ] Retry topology: `billing.commands.retry`, `{queue}.retry.{5s,30s,2m}`, `billing.commands.dlx`, `{queue}.dlq`. Quorum queues with `x-delivery-limit=5`
- [ ] Export `deploy/rabbitmq/definitions.json` from the Go topology definition (test asserts they match)
- [ ] invoice-worker: `go-pdf/fpdf` template, deterministic output, MinIO put with deterministic key, sha256 idempotency
- [ ] `tools/mockpsp`: charges, refunds, status by idempotency key, failure/timeout/latency env knobs, magic customer IDs. Own DB
- [ ] payments-worker: HTTP client with timeouts, error mapping (402 → declined, 5xx → transient, timeout → errored), `payment_attempts`
- [ ] notifier: SMTP client, embedded templates, `sent_notifications` SENDING/SENT protocol
- [ ] notifier: Kafka consumer on `usage.limit-exceeded.v1` (group `notifier`) → limit email
- [ ] `quotifyctl dlq ls|show|redrive|purge --broker rabbitmq`
- [ ] Remove stub-worker usage from e2e (keep it for billing-svc unit/integration speed)

## Deliverables

- Three worker images + mock-psp image. Mailpit shows invoice and limit emails. MinIO holds PDFs.
- `GET /v1/invoices/{id}/pdf` returns a presigned URL.

## Acceptance criteria

- [ ] Integration: a redelivered `RenderInvoicePdf` → one object in MinIO, the same reply twice, no re-render (sha256 match)
- [ ] Integration: mock-psp 5xx twice then success → `PaymentSucceeded` after 2 retries via the 5 s and 30 s tiers (timestamps verified)
- [ ] Integration: `MOCKPSP_TIMEOUT_RATE=1` → saga `PAYMENT_UNKNOWN` → status query → `PAID`. mock-psp shows **one** charge
- [ ] Integration: a handler that panics on a specific command → message in `{queue}.dlq` after 5 deliveries. Saga reaches `*_FAILED` via deadline
- [ ] Integration: undecodable message → DLQ immediately, no reply
- [ ] Integration: `SendNotification` redelivered after SENT → no second email in Mailpit
- [ ] Integration: `LimitExceeded` delivered twice → one email
- [ ] E2E: seed → events → close → invoice PDF in MinIO, payment in mock-psp, email in Mailpit

## Dependencies

Phase 05 (command contracts, saga). Phase 04 (limit events) for the notifier Kafka part.

## Risks / open questions

- MinIO isn't in the original infra list. Planned as the PDF store. Alternative: `bytea` in a worker DB. **Needs confirmation.**
- At-least-once email (SENDING older than 5 min is re-sent) can produce rare duplicates. Accept, or require an idempotent provider API (Mailpit doesn't have one).
