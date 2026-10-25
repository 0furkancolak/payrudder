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
  Environment environment = 6;    // SANDBOX | LIVE, from the tenant; workers never infer it
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
totals, customer name, number), so the worker needs no billing DB access. It stores the PDF in **S3-compatible object
storage**: MinIO in Compose and kind, any S3 endpoint elsewhere.

| | |
|---|---|
| Consumes | queue `invoice.render` (`RenderInvoicePdf`) |
| Replies | `PdfRendered{object_key, sha256, size}` or `PdfFailed{reason}` |
| Owned data | Bucket `invoices` (`S3_BUCKET`), key `{tenant_id}/{invoice_id}/v{invoice_version}.pdf`. Sandbox tenants share the bucket under their own tenant IDs, with an S3 lifecycle rule expiring them after 30 days (object tag `environment=sandbox`) |
| Storage client | `pkg/blob` interface (`Put`, `Head`, `PresignGet`) over `aws-sdk-go-v2/service/s3` with `S3_ENDPOINT`, `S3_REGION`, `S3_FORCE_PATH_STYLE=true` for MinIO. Only S3 API calls, no MinIO-specific SDK, so switching to AWS S3, R2 or Ceph is configuration only. CI runs the integration tests against MinIO |
| Idempotency | Deterministic object key. If the object exists with the same `sha256` → reply success without re-rendering. Rendering is deterministic (fixed fonts, no timestamps in content) |
| Library | `github.com/go-pdf/fpdf` (pure Go, no headless browser) |

A "SANDBOX" watermark is drawn on sandbox invoices. If the tenant has a `READY` `invoice_logo` asset, the command carries its
object key and the worker reads it with `Get` from the same bucket.

Failure modes: object storage unavailable → transient (retry). Template or data error → permanent (`PdfFailed`).
Scaling: CPU-bound, scale on queue depth (KEDA in Phase 10 is optional).

---

## payments-worker

**Responsibility.** Charge, refund or query payments through a `PaymentProvider` and translate the result into replies.
It holds no money logic: amounts come from the command.

```go
// pkg/payments (sketch)
type PaymentProvider interface {
    Charge(ctx context.Context, r ChargeRequest) (ChargeResult, error)   // r.IdempotencyKey is mandatory
    Refund(ctx context.Context, r RefundRequest) (RefundResult, error)
    Status(ctx context.Context, idempotencyKey string) (StatusResult, error)
}
```

**Routing by environment** ([ADR 0011](../adr/0011-sandbox-environment.md)):

| Command `environment` | Provider | Configurable? |
|---|---|---|
| `SANDBOX` | `mockpsp` (sandbox endpoint) | **No.** Hard-coded, so a sandbox tenant can never reach a real provider, whatever the config says |
| `LIVE` | `PAYMENTS_LIVE_PROVIDER` | Yes. v1 ships only `mockpsp`, used for live tenants in the local demo. Real adapters are a non-goal. The interface is where one plugs in. If unset, live charges reply `PaymentErrored{retryable=false, code=PROVIDER_NOT_CONFIGURED}` |

| | |
|---|---|
| Consumes | queue `payment.charge`, `payment.refund`, `payment.status` |
| Replies | `PaymentSucceeded{psp_charge_id}`, `PaymentDeclined{decline_code}`, `PaymentErrored{retryable}`, `PaymentRefunded`, `PaymentStatus{found, state}` |
| Owned data | `payments_db.payment_attempts(command_id PK, invoice_id, environment, provider, idempotency_key, request, response, status)` |
| Idempotency | `Idempotency-Key` header to mock-psp = `invoice_id:charge:{dunning_attempt}`. Retries of the same command reuse it. `payment_attempts` stores the reply for redeliveries |

**mock-psp** (`tools/mockpsp`, its own container) simulates a real provider:

- `POST /v1/charges` with `Idempotency-Key`. The same key returns the original result (like Stripe).
- Env-configurable `MOCKPSP_DECLINE_RATE`, `MOCKPSP_ERROR_RATE`, `MOCKPSP_TIMEOUT_RATE`, `MOCKPSP_LATENCY_MS`.
- `MOCKPSP_TIMEOUT_RATE` **commits the charge and then hangs**. This is how `PAYMENT_UNKNOWN` gets tested.
- `GET /v1/charges?idempotency_key=…` for status queries. `POST /v1/refunds`.
- **Sandbox payment scenarios.** A sandbox customer's `payment_method` is a scenario token that makes the outcome deterministic,
  so tenants can rehearse their own handling of every case (the same idea as test card numbers):

  | Token | Behaviour | Saga path it exercises |
  |---|---|---|
  | `pm_sandbox_success` (default) | Charge succeeds | `PAID` |
  | `pm_sandbox_declined` | `402 card_declined` on every attempt | dunning ×3 → `UNCOLLECTIBLE` |
  | `pm_sandbox_insufficient_funds_once` | First attempt declined, then succeeds | dunning retry → `PAID` |
  | `pm_sandbox_error_then_success` | 5xx twice, then succeeds | worker TTL retries (5 s, 30 s) → `PAID` |
  | `pm_sandbox_timeout_committed` | Commits the charge, then hangs past the client timeout | `PAYMENT_UNKNOWN` → status query → `PAID` |
  | `pm_sandbox_timeout_not_committed` | Hangs without committing | `PAYMENT_UNKNOWN` → status query → re-charge → `PAID` |

  In sandbox, dunning delays are compressed (+1 min, +3 min, +7 min instead of days), so a full dunning cycle is visible in minutes.
- The env-wide rates above stay for chaos and load tests. Scenario tokens take precedence over them.

Error mapping: HTTP 402 → `PaymentDeclined` (permanent). 5xx / connection reset → transient retry
(3 attempts with 5 s, 30 s, 2 min TTL). Client timeout → `PaymentErrored{retryable=false}`, which makes billing go
`PAYMENT_UNKNOWN` and query status. Blind retries on a timeout are never done inside the worker.

---

## notifier

**Responsibility.** Send transactional emails (invoice available, payment failed, limit threshold
reached) through SMTP to **Mailpit** locally. Sandbox notifications get a `[SANDBOX]` subject prefix and, in any non-local
deployment, are delivered only to Mailpit (`SANDBOX_SMTP_URL`), never to the customer's real address.

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

## As built (Phase 06)

Packages: `pkg/workerx` (skeleton), `pkg/payments` (provider interface + mock-psp client), `pkg/blob` (S3), services
`services/{invoice-worker,payments-worker,notifier}` and `tools/mockpsp`. Metrics: `worker_commands_handled{queue,result}`,
`worker_commands_replayed`. See the notes in [phase 06](../phases/phase-06-workers.md#notes-as-built) for the decisions that
differ from the sketches above.

## As built: payments-worker and providers (Phase 14)

payments-worker also serves HTTP on `PAYMENTS_HTTP_ADDR` (default `:8095`), only when `QUOTIFY_SECRETS_KEY` is set: `POST /webhooks/{provider}` (provider notifications, confirmed
with the provider before anything is recorded), and, behind `X-Internal-Token`, tenants' provider accounts and the payment ledger. The gateway forwards to it
(`QUOTIFY_PAYMENTS_URL`, `QUOTIFY_PAYMENTS_INTERNAL_TOKEN`). A live tenant's commands go to its default provider account, else to `PAYMENTS_LIVE_PROVIDER`;
sandbox commands always go to mock-psp. See [phase 14](../phases/phase-14-provider-contract-iyzico.md).

Phase 29 adds `banktransfer`: its charge calls nothing and answers `PaymentErrored{PENDING}`, the status query reads the ledger (`PENDING`, `SUCCEEDED` when the confirmed receipts add up to the charge, `DECLINED` after the payment
window or a rejection), and the operator endpoints `GET /admin/tenants/{tenant}/invoices/{invoice}/transfer`, `POST …/transfer/receipts` (idempotent per bank reference) and `POST …/transfer/reject` confirm or give up on a transfer.
`PAYMENTS_TRANSFER_WINDOW_DAYS` (14) is the window when the tenant's account sets none. See [phase 29](../phases/phase-29-bank-transfer.md).

Phase 15 adds Paddle: `POST /webhooks/{provider}/{tenant}/{environment}` (a notification destination per account, verified by signature), the tenant's provider subscriptions
and hosted checkouts under `/admin/tenants/{tenant}/`, and `QUOTIFY_PLANS_ADDR`, through which subscriptions a provider runs are linked to ours. See [phase 15](../phases/phase-15-paddle.md).

Phase 45 adds `GET /admin/tenants/{tenant}/go-live` (internal token): `{"ready": bool, "providers": [...]}`, true when the tenant has a live-mode account whose sealed settings still open and still build an adapter. The gateway asks it for the go-live checklist; plans-svc never reads provider data. See [ADR 0022](../adr/0022-live-and-sandbox-guards.md).

## As built: e-invoices in payments-worker (Phase 30)

payments-worker also consumes `einvoice.issue` (`IssueEInvoice`: issue or cancel one document) and answers with an `EInvoiceResult` (`issued`, `cancelled`, `failed` with
`retryable` and an error key). The choice of this worker over invoice-worker or a new service is in [phase 30](../phases/phase-30-e-invoice.md#decisions): it already has the
database, the sealed per-tenant accounts and the HTTP port the gateway forwards to, and gains only the `S3_*` settings for the UBL and PDF files (`{tenant}/einvoice/{document}/document.xml|pdf`
in the invoices bucket). A sandbox tenant's documents always go to the `mock` adapter; a live tenant's go to its sealed account (`nilvera`, `webhook`), and `QUOTIFY_DEMO` forces the mock.
`einvoice_issuances` is the idempotency record (one row per document UUID, written before the integrator is called, the legal number unique per tenant and environment).
Operator endpoints: `GET /admin/tenants/{tenant}/e-invoice-accounts`, `PUT|DELETE …/e-invoice-accounts/{environment}`. Settings: `S3_ENDPOINT`, `S3_REGION`, `S3_BUCKET`, `S3_ACCESS_KEY`,
`S3_SECRET_KEY`, `S3_FORCE_PATH_STYLE`, `EINVOICE_ALLOW_PRIVATE_URLS` (development only).

