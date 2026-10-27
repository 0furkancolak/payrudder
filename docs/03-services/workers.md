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
package payrudder.commands.v1;

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
| `SANDBOX` | `mockpsp` (sandbox endpoint), or the tenant's own **test-mode** account at a provider (below) | **No.** Hard-coded: a sandbox tenant can never reach a provider's live mode, whatever the config says |
| `LIVE` | `PAYMENTS_LIVE_PROVIDER` | Yes. v1 ships only `mockpsp`, used for live tenants in the local demo. Real adapters are a non-goal. The interface is where one plugs in. If unset, live charges reply `PaymentErrored{retryable=false, code=PROVIDER_NOT_CONFIGURED}`. **Live never reaches the mock** (phase 45): the worker refuses to start with `PAYMENTS_LIVE_PROVIDER=mockpsp` unless `PAYMENTS_ALLOW_MOCK_LIVE=true` (compose and the default Helm values say it, a sandbox install does not) or `QUOTIFY_DEMO`, and at run time a live command routed to the mock is answered `PROVIDER_NOT_CONFIGURED` |

A command is checked before it is routed (phase 45): one with no `environment`, or whose `environment` differs from its tenant's (asked of plans-svc and kept, `QUOTIFY_PLANS_ADDR`), is poison and goes to the dead-letter queue; it is never read as live. invoice-worker refuses a command whose invoice number is in the other series (`SBX-` is sandbox). A provider account has a mode of its own at the provider (iyzico and Paddle have a test host): a live-environment account that points at a test host, or a sandbox-environment account at a live host, is refused when stored (`payments.account.livemode_mismatch`) and never used.

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
| Consumes | queue `notification.send` (`SendNotification{template, to, data}`). Also subscribes to `usage.limit-exceeded.v1` and `subscription.trial-will-end.v1` through small Kafka consumers in notifier (see below) |
| Replies | `NotificationSent{message_id}` |
| Owned data | `notifier_db.sent_notifications(dedup_key PK, command_id, sent_at, smtp_message_id)` |
| Idempotency | `dedup_key = command_id` (billing), `LimitExceeded.event_id` (limits) or `TrialWillEnd.event_id` (trials). SMTP isn't idempotent, so the row is inserted **before** sending with status `SENDING`. On redelivery with status `SENDING` older than 5 min, re-send (at-least-once email, duplicates possible and documented) |

**Tenant's own provider (Phase 53).** A live tenant can connect Nafru Email (`MailIntegrationService` of plans-svc). Before a live send the notifier asks `ResolveMailSender` for the tenant (`delivery.PlansProviders`, remembered for `NOTIFIER_MAIL_SWITCH_TTL`); when one is connected the mail goes through `mailx.Resend` (`POST {base_url}/emails`, `Authorization: Bearer <key>`, `Idempotency-Key` = the dedup key, JSON `from`, `to`, `subject`, `html`, `text`) from the tenant's From address instead of SMTP. Sandbox mail never does (ADR 0011), and a failed lookup is an error, so the mail is retried and never sent through the built-in sender against the tenant's choice. A refusal the provider will repeat (4xx other than 429: a wrong key, an unverified sender) is a poison message with the claim released; a connection refused before the request left is `NotSentError`; 429 and 5xx are retried. The provider's address must be https and public (`NOTIFIER_MAIL_ALLOW_PRIVATE` lifts that for development and tests).

Limit alerts: notifier runs a Kafka consumer on `usage.limit-exceeded.v1` (group `notifier`) that sends directly.
It doesn't round-trip through RabbitMQ because there is no saga to reply to. The same `sent_notifications` dedup applies.
This is the one place a worker reads Kafka, and it's justified: the input is a *fact* ("threshold crossed")
and the email is the consumer's own side effect.

Trial reminders: a second Kafka consumer on `subscription.trial-will-end.v1` (group `notifier-trials`, own DLQ `subscription.trial-will-end.dlq.v1`, `sent_notifications.kind = 'trial'`) mails the customer that their trial ends soon (`trial_will_end.v1`). The end of the trial is written in the tenant's time zone. The template is in the mail category `system` (it is in no family of `pkg/mailcategory`), so a tenant switches it off with the other system mail. Like all customer mail it is English only; a customer has no language on file.

Templates: Go `html/template`, embedded with `embed.FS`, versioned by name (`invoice_paid.v1.html`).

## As built (Phase 06)

Packages: `pkg/workerx` (skeleton), `pkg/payments` (provider interface + mock-psp client), `pkg/blob` (S3), services
`services/{invoice-worker,payments-worker,notifier}` and `tools/mockpsp`. Metrics: `worker_commands_handled{queue,result}`,
`worker_commands_replayed`. See the notes in phase 06 for the decisions that
differ from the sketches above.

## As built: payments-worker and providers (Phase 14)

payments-worker also serves HTTP on `PAYMENTS_HTTP_ADDR` (default `:8095`), only when `PAYRUDDER_SECRETS_KEY` is set: `POST /webhooks/{provider}` (provider notifications, confirmed
with the provider before anything is recorded), and, behind `X-Internal-Token`, tenants' provider accounts and the payment ledger. The gateway forwards to it
(`PAYRUDDER_PAYMENTS_URL`, `PAYRUDDER_PAYMENTS_INTERNAL_TOKEN`). A live tenant's commands go to its default provider account, else to `PAYMENTS_LIVE_PROVIDER`;
sandbox commands go to mock-psp, except a charge that the tenant's own sandbox account at a provider takes (next paragraph). See phase 14.

Phase 29 adds `banktransfer`: its charge calls nothing and answers `PaymentErrored{PENDING}`, the status query reads the ledger (`PENDING`, `SUCCEEDED` when the confirmed receipts add up to the charge, `DECLINED` after the payment
window or a rejection), and the operator endpoints `GET /admin/tenants/{tenant}/invoices/{invoice}/transfer`, `POST …/transfer/receipts` (idempotent per bank reference) and `POST …/transfer/reject` confirm or give up on a transfer.
`PAYMENTS_TRANSFER_WINDOW_DAYS` (14) is the window when the tenant's account sets none. See phase 29.

Phase 15 adds Paddle: `POST /webhooks/{provider}/{tenant}/{environment}` (a notification destination per account, verified by signature), the tenant's provider subscriptions
and hosted checkouts under `/admin/tenants/{tenant}/`, and `PAYRUDDER_PLANS_ADDR`, through which subscriptions a provider runs are linked to ours. See phase 15.

Phase 45 adds `GET /admin/tenants/{tenant}/go-live` (internal token): `{"ready": bool, "providers": [...], "accounts": [...]}`, true when the tenant has a live-mode account whose sealed settings still open, still build an adapter and, for an adapter that can ask its provider (`payments.HealthChecker`: iyzico `/payment/bin/check`, Paddle `/event-types`; both unverified against the real hosts, tested against fakes), whose provider accepts the credentials now (the answer is remembered 30 s per version of the settings). Each entry of `accounts` says `working`, `checked` (the provider was asked) and, when it does not work, `problem` (`unreadable`, `refused`, `unreachable`); no setting is in it. The gateway asks it for the go-live checklist; plans-svc never reads provider data.

A person connects their own live account through the gateway (`PUT /app/v1/apps/{id}/go-live/providers/{provider}`), which calls `PUT /admin/tenants/{tenant}/provider-accounts/{provider}?environment=live&verify=true` with `X-Payrudder-Actor: user:<id>`. `verify=true` refuses an endpoint that is not one of the provider's documented hosts (`payments.account.endpoint_not_allowed`: the platform would send the person's secret keys wherever they pointed it), refuses the provider's test host for a live account, and asks the provider to accept the credentials before they are stored (`payments.account.health_check_failed`, `payments.account.health_check_unreachable`). Every change is a row of `provider_account_events` (provider, environment, connected/replaced/removed, actor, whether the provider vouched; never a setting), written in the same transaction. See [ADR 0023](../adr/0023-live-and-sandbox-guards.md).

**The sandbox can be connected to a provider's test mode** (phase 54). The same `provider_accounts` table holds a sandbox tenant's account (`environment = sandbox`, sealed with the tenant, provider and environment bound in like every account), so there is no new table and no migration, and the reset keeps it (`provider_accounts` and `provider_account_events` are in `sandboxreset.Kept`). A person connects it through the gateway (`PUT /app/v1/apps/{id}/sandbox/providers/{provider}`, iyzico and Paddle only: a bank transfer has no test mode), which fills in the provider's test host, adds `?environment=sandbox&verify=true` and the actor, and makes the first account the default. The mode of an account is checked from its endpoint and, new in phase 54, from the format of its key (`payments.KeyModer`: a Paddle `pdl_live_` key is live and `pdl_sdbx_` is test, an iyzico `sandbox-` key is test); a mismatch is refused when stored and when used (`payments.account.livemode_mismatch`), before the key is sent anywhere. `GET /admin/tenants/{tenant}/provider-health?environment=` lists one environment's accounts and whether each works (the same answer as `go-live`, which stays the live one and the only one that makes a tenant ready).

A sandbox **charge** goes to the tenant's sandbox account when its payment method is the provider's own token (not a `pm_sandbox_` scenario) and the subscription's provider, or else the tenant's default sandbox account, has one; otherwise it goes to mock-psp, so the scenarios keep working beside a connection. A **refund** or **status query** follows the provider the invoice was charged through. The sandbox guard admits only the mock or an account adapter whose endpoint and key are not live (`ErrWrongProvider` otherwise), and a live command reads live rows only, so a sandbox account is never used for a live charge. Not wired for the sandbox: provider-run subscriptions, the catalogue and migrations (those are live features), and the e-invoice and e-mail integrations, which stay on the simulator and the sandbox inbox.

payments-worker's part of a sandbox reset (phase 45 E) is `POST /admin/tenants/{tenant}/sandbox-reset` with `{"environment":"sandbox"}` (internal token): it deletes the tenant's attempts, ledger rows, receipts, provider subscriptions and events and e-invoice issuances, and keeps its provider accounts.

## As built: e-invoices in payments-worker (Phase 30)

payments-worker also consumes `einvoice.issue` (`IssueEInvoice`: issue or cancel one document) and answers with an `EInvoiceResult` (`issued`, `cancelled`, `failed` with
`retryable` and an error key). The choice of this worker over invoice-worker or a new service is in phase 30: it already has the
database, the sealed per-tenant accounts and the HTTP port the gateway forwards to, and gains only the `S3_*` settings for the UBL and PDF files (`{tenant}/einvoice/{document}/document.xml|pdf`
in the invoices bucket). A sandbox tenant's documents always go to the `mock` adapter; a live tenant's go to its sealed account (`nilvera`, `webhook`), and `PAYRUDDER_DEMO` forces the mock.
`einvoice_issuances` is the idempotency record (one row per document UUID, written before the integrator is called, the legal number unique per tenant and environment).
Operator endpoints: `GET /admin/tenants/{tenant}/e-invoice-accounts`, `PUT|DELETE …/e-invoice-accounts/{environment}`, and (phase 53) `POST …/e-invoice-accounts/{environment}/check`, which builds the adapter from the stored settings and asks it whether the credentials work (`{ok, checked: credentials|configuration, problem: refused|unreachable}`; nothing is issued or stored). Settings: `S3_ENDPOINT`, `S3_REGION`, `S3_BUCKET`, `S3_ACCESS_KEY`,
`S3_SECRET_KEY`, `S3_FORCE_PATH_STYLE`, `EINVOICE_ALLOW_PRIVATE_URLS` (development only).


## In a cell: payments-worker (phase 28, [ADR 0018](../adr/0018-tenant-isolation-modes.md))

With `PAYRUDDER_CELLS` set ([runbook](../runbooks/cells.md); it needs `PAYRUDDER_PLANS_ADDR`, the directory being plans-svc's) a tenant that the directory places in a cell has all ten of its tables (`payment_attempts`, `payment_ledger`, `provider_accounts`, `provider_events`, `provider_subscriptions`, `provider_objects`, `manual_receipts`, `einvoice_accounts`, `einvoice_issuances`, `provider_account_events`) in its own schema `t_<id>` of the cell's `payments_db`, built by `migrations/tenant` (the nine public migrations as they end up, written once for a schema). Off by default; no tenant is placed in a cell.

**No row-level security.** payments-worker has none today (only `payments_system` touches the tables, and every statement names its tenant), and the tenant set has none either, so **the schema is the whole boundary** in a cell: every store method takes the tenant it acts for, and `store.at` / `store.Tx` run it where the router says that tenant is, in a transaction whose `search_path` is `"t_<id>", shared`. A transaction belongs to one tenant: it is opened for it and ends with it. A statement that names no tenant is refused when cells are on (`store.ErrNoTenant`) instead of being answered from the pooled database. Tested: two tenants of one cell, one of another and a pooled one write the same keys, event ids and bank references and see none of each other's rows (`TestStoresStayInTheirTenantsPlace`), and no tenant schema holds a row of another tenant after any scenario. A statement that names another tenant's schema explicitly would be allowed by the grants; no code path does, and there is no policy behind it.

**The four jobs.**

- *Command worker.* The reply inbox of the worker (`workerx.TenantInbox`, new and optional) is asked with the tenant of the command; the attempt, the ledger row and the stored reply are in the tenant's place. A tenant that is moving makes the command fail with `tenantdir.ErrNotServing`, which is retried through the delay queues; a failed provider lookup for such a tenant is not answered `PROVIDER_NOT_CONFIGURED`, a reply billing would act on. The provider is never called for a tenant that cannot be written.
- *Provider webhook server.* A URL that names the tenant (Paddle) decides the place at once. A URL that does not (iyzico) carries only the idempotency key of the charge, and the tenant that made it is found without looking in any tenant's schema: the pooled `payment_attempts` first, then **`shared.charge_keys`, one table per cell database**, a key-to-tenant index that a trigger on `payment_attempts` fills in the same transaction as the attempt (so a move that copies attempts into a cell fills it too; an entry left behind by a move still names the right tenant, and the directory says where that is). One query per place, never one per tenant. The notification is then kept in that tenant's inbox (the pooled `provider_events` keeps only the notifications that belong to no tenant, such as the ones about a key nobody made), confirmed with that tenant's own sealed account, and recorded in its ledger. A tenant that is moving answers `503 payments.tenant.unavailable` before anything is recorded, so the provider retries. Account secrets are sealed and opened with the same tenant|provider|environment context as before, and the signature or confirmation of a notification is the provider adapters' code, unchanged.
- *Transfers (bank-transfer matching).* Pending rows, closures, receipts and the sums are the tenant's own; the receipt transaction (ledger row, receipt row, advisory lock on the invoice) runs in the tenant's place, and two tenants with the same invoice id or bank reference do not meet.
- *Sandbox reset.* One transaction in the tenant's place deletes its rows, and in a cell also its keys from the cell's `shared.charge_keys`; it is refused while the tenant moves.

The job inventory has no loop over tenants here (each of the four acts for the tenant of its command, notification or request), so "no tenant skipped, none twice, none mixed" is the property of each unit of work: `TestCommandsRunInTheirTenantsPlace` runs a command for every tenant at once and counts the provider's calls, attempts and ledger rows. `ITEST_CELLS=1 go test -tags integration ./internal/itest` runs the whole suite with every tenant in a cell.

**Moves.** `payrudderctl cells move` works between the pooled database and cells and between cells (`TestMovesOfPaymentsTenants`). A move **into the pooled database is refused** by `pgxx.Move`: `manual_receipts.ledger_id` refers to `payment_ledger.id`, an identity column, which moving into the shared pooled sequences would renumber. A tenant that has to go back to the pooled database needs that foreign key and its identity handled first.
