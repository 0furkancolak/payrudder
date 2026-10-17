# Workers: invoice-worker, payments-worker, notifier

All three are RabbitMQ consumers that execute **commands** from billing-svc and reply on
`billing.replies`. They share a skeleton in `pkg/rabbitx`:

- `prefetch` (QoS) = 16 per consumer. Manual ack **after** the side effect and reply are durable.
- Publisher confirms on replies. Ack the command only after the reply publish is confirmed.
- Inbox dedup by `command_id` in the worker's own store. A redelivered command returns the stored reply.
- Errors are classified as **transient** (retry via TTL queue), **permanent** (reply failure, no retry) or
  **poison** (undecodable → parking-lot queue, no reply. Billing's step timeout surfaces it).
- Trace context comes from AMQP headers (`traceparent`). Each delivery is a consumer span linked to the producer.

Retry topology is in [04-messaging.md](../04-messaging.md#retry-and-dead-letter-topology).

## Command envelope

```proto
package quotify.commands.v1;

message CommandEnvelope {
  string command_id = 1;          // uuidv5(saga_id|step|attempt), dedup key
  string saga_id = 2;
  string tenant_id = 3;
  int32 attempt = 4;
  google.protobuf.Timestamp issued_at = 5;
  oneof command {
    RenderInvoicePdf render_invoice_pdf = 10;
    ChargePayment charge_payment = 11;
    RefundPayment refund_payment = 12;
    QueryPaymentStatus query_payment_status = 13;
    SendNotification send_notification = 14;
  }
}
message ReplyEnvelope {
  string command_id = 1;
  string saga_id = 2;
  oneof reply { PdfRendered pdf_rendered = 10; PdfFailed pdf_failed = 11; PaymentSucceeded payment_succeeded = 12;
                PaymentDeclined payment_declined = 13; PaymentErrored payment_errored = 14;
                PaymentRefunded payment_refunded = 15; PaymentStatus payment_status = 16;
                NotificationSent notification_sent = 17; }
}
```

---

## invoice-worker

**Responsibility.** Render an invoice PDF from the invoice snapshot carried *in the command* (lines,
totals, customer name, number), so the worker needs no billing DB access. It stores the PDF in MinIO.

| | |
|---|---|
| Consumes | queue `invoice.render` (`RenderInvoicePdf`) |
| Replies | `PdfRendered{object_key, sha256, size}` or `PdfFailed{reason}` |
| Owned data | MinIO bucket `invoices`, key `{tenant_id}/{invoice_id}/v{invoice_version}.pdf` |
| Idempotency | Deterministic object key. If the object exists with the same `sha256` → reply success without re-rendering. Rendering is deterministic (fixed fonts, no timestamps in content) |
| Library | `github.com/go-pdf/fpdf` (pure Go, no headless browser) |

Failure modes: MinIO unavailable → transient (retry). Template or data error → permanent (`PdfFailed`).
Scaling: CPU-bound, scale on queue depth (KEDA in Phase 10 is optional).

---

## payments-worker

**Responsibility.** Call the **mock-psp** HTTP API to charge, refund or query payments. It translates PSP
responses into replies. It holds no money logic: amounts come from the command.

| | |
|---|---|
| Consumes | queue `payment.charge`, `payment.refund`, `payment.status` |
| Replies | `PaymentSucceeded{psp_charge_id}`, `PaymentDeclined{decline_code}`, `PaymentErrored{retryable}`, `PaymentRefunded`, `PaymentStatus{found, state}` |
| Owned data | `payments_db.payment_attempts(command_id PK, invoice_id, idempotency_key, request, response, status)` |
| Idempotency | `Idempotency-Key` header to mock-psp = `invoice_id:charge:{dunning_attempt}`. Retries of the same command reuse it. `payment_attempts` stores the reply for redeliveries |

**mock-psp** (`tools/mockpsp`, its own container) simulates a real provider:

- `POST /v1/charges` with `Idempotency-Key`. The same key returns the original result (like Stripe).
- Env-configurable `MOCKPSP_DECLINE_RATE`, `MOCKPSP_ERROR_RATE`, `MOCKPSP_TIMEOUT_RATE`, `MOCKPSP_LATENCY_MS`.
- `MOCKPSP_TIMEOUT_RATE` **commits the charge and then hangs**. This is how `PAYMENT_UNKNOWN` gets tested.
- `GET /v1/charges?idempotency_key=…` for status queries. `POST /v1/refunds`.
- Magic customer IDs force outcomes (`cus_decline`, `cus_timeout`) for deterministic tests.

Error mapping: HTTP 402 → `PaymentDeclined` (permanent). 5xx / connection reset → transient retry
(3 attempts with 5 s, 30 s, 2 min TTL). Client timeout → `PaymentErrored{retryable=false}`, which makes billing go
`PAYMENT_UNKNOWN` and query status. Blind retries on a timeout are never done inside the worker.

---

## notifier

**Responsibility.** Send transactional emails (invoice available, payment failed, limit threshold
reached) through SMTP to **Mailpit** locally.

| | |
|---|---|
| Consumes | queue `notification.send` (`SendNotification{template, to, data}`). Also subscribes to `usage.limit-exceeded.v1` through a small Kafka→command bridge in notifier (see below) |
| Replies | `NotificationSent{message_id}` |
| Owned data | `notifier_db.sent_notifications(dedup_key PK, command_id, sent_at, smtp_message_id)` |
| Idempotency | `dedup_key = command_id` (billing) or `LimitExceeded.event_id` (limits). SMTP isn't idempotent, so the row is inserted **before** sending with status `SENDING`. On redelivery with status `SENDING` older than 5 min, re-send (at-least-once email, duplicates possible and documented) |

Limit alerts: notifier runs a Kafka consumer on `usage.limit-exceeded.v1` (group `notifier`) that sends directly.
It doesn't round-trip through RabbitMQ because there is no saga to reply to. The same `sent_notifications` dedup applies.
This is the one place a worker reads Kafka, and it's justified: the input is a *fact* ("threshold crossed")
and the email is the consumer's own side effect.

Templates: Go `html/template`, embedded with `embed.FS`, versioned by name (`invoice_paid.v1.html`).
