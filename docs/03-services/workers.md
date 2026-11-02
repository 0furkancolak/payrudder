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
| Language | The snapshot carries the customer's `locale`, a `default_locale` (the tenant's own `default_language` setting, frozen with the draft) and the tenant's `time_zone`. The PDF is drawn in the customer's language (en, tr, de, fr, es), else the default, else English, and the issue date is the day on the seller's calendar in `time_zone` (UTC when the snapshot has none); numbers, money and dates are written the way that language writes them (`internal/render/locale.go`, one Go map of words per language in `layouts.go`, a test that every language has every key). **Arabic** customers get the English layout: fpdf cannot shape Arabic or lay it out right to left. The language that was asked for and the layout that was drawn are written in the PDF's `Keywords` (`locale=ar layout=en`) |
| Fonts | Noto Sans Regular and Bold (SIL OFL 1.1, `internal/render/fonts/OFL.txt`), embedded with `go:embed` (about 1.2 MiB in the binary; a PDF carries only the glyphs it uses). A character the font has no glyph for (CJK, Arabic) is printed as `?` |

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
| Owned data | `notifier_db.sent_notifications(dedup_key PK, command_id, tenant_id, sent_at, smtp_message_id)`, `notifier_db.mail_suppressions(tenant_id, address)` |
| Idempotency | `dedup_key = command_id` (billing), `LimitExceeded.event_id` (limits) or `TrialWillEnd.event_id` (trials). SMTP isn't idempotent, so the row is inserted **before** sending with status `SENDING`. On redelivery with status `SENDING` older than 5 min, re-send (at-least-once email, duplicates possible and documented) |

**Tenant's own provider (Phase 53).** A live tenant can connect Nafru Email (`MailIntegrationService` of plans-svc). Before a live send the notifier asks `ResolveMailSender` for the tenant (`delivery.PlansProviders`, remembered for `NOTIFIER_MAIL_SWITCH_TTL`); when one is connected the mail goes through `mailx.Nafru`, Resend's wire shape (`POST {base_url}/emails`, `Authorization: Bearer <key>`, `Idempotency-Key` = the dedup key, JSON `from`, `to`, `subject`, `html`, `text`) from the tenant's From address instead of SMTP. Sandbox mail never does (ADR 0011), and a failed lookup is an error, so the mail is retried and never sent through the built-in sender against the tenant's choice. A refusal the provider will repeat (4xx other than 429: a wrong key, an unverified sender) is a poison message with the claim released; a connection refused before the request left is `NotSentError`; 429 and 5xx are retried. The provider's address must be https and public (`NOTIFIER_MAIL_ALLOW_PRIVATE` lifts that for development and tests).

**Allowance, suppression and rate (plan 11, [runbook](../runbooks/mail-suppression.md)).** Before a live send for a tenant, after the dedup claim and the choice of sender, `delivery.Mailer` asks in this order: the tenant's **suppression list** (`mail_suppressions`: an address that bounced for good, complained or was suppressed by hand is dropped as done, `notifier_mail_dropped_total{category="suppressed"}`); the organisation's **`mails` allowance** (`delivery.SaasQuota`: the tenant's `ResolveIngestPolicy` from plans-svc, then limits-svc `CheckEntitlement` of the `mails` meter as the platform tenant, both remembered a few seconds; a reached hard limit drops the mail as done, `category="quota"`); and the **per-tenant rate limit** (`delivery.Limiter`, a token bucket, `NOTIFIER_MAIL_RATE_PER_MINUTE` 600 and `NOTIFIER_MAIL_RATE_BURST` 200 by default; with `PAYRUDDER_REDIS_URL` set the buckets are one per tenant in Redis (`delivery.RedisBuckets`, the gateway's GCRA script, Redis' own clock), shared by every replica, and a Redis that cannot be reached leaves each replica on buckets of its own, counted in `notifier_mail_rate_limit_fallback_total`; without it the limit is per replica; an over-limit mail answers `ErrRateLimited`, which is an ordinary retryable error: a command is retried three times (5 s, 30 s, 2 min) and then dead-lettered, a Kafka consumer holds its partition and retries the batch, `notifier_mail_rate_limited_total`). The last two apply only to mail the **platform's sender** sends: sandbox mail, mail without a tenant (the platform's own) and mail through a tenant's own provider are neither limited nor counted. After a successful send the mail is metered: one event of the `mails` meter, quantity 1, idempotency key `saas-mails:<dedup key>`, ingested into the platform tenant for the organisation's customer row (`PAYRUDDER_INGEST_ADDR`, `PAYRUDDER_LIMITS_ADDR`; both or neither). Thresholds and alerts are the quota engine's (ADR 0024); nothing is stored in the notifier. Every one of these fails open except the suppression list, whose failure retries the mail. `NOTIFIER_EVENTS_ADDR` (with `NOTIFIER_MAIL_EVENTS_SECRET`) starts the listener for bounce and complaint notices and the operator's suppression calls. `mailx.Nafru` (`pkg/mailx/nafru.go`) is the sender: Resend's wire shape plus a template tag and header that are UNVERIFIED at Nafru.

Limit alerts: notifier runs a Kafka consumer on `usage.limit-exceeded.v1` (group `notifier`) that sends directly.
It doesn't round-trip through RabbitMQ because there is no saga to reply to. The same `sent_notifications` dedup applies.
This is the one place a worker reads Kafka, and it's justified: the input is a *fact* ("threshold crossed")
and the email is the consumer's own side effect.

Contract mail (plan 21): three more Kafka consumers, each with its own group, offsets table rows and DLQ: `contract.accepted.v1` (`-contracts`, `contract_accepted.v1`, `sent_notifications.kind = 'contract'`), `subscription.renewal-notice.v1` (`-renewals`, `renewal_notice.v1`, kind `renewal`) and `contract.withdrawal-received.v1` (`-withdrawals`, `contract_withdrawal_received.v1`, kind `withdrawal`). All three templates are in the category `system`, in six languages, and are not held back by the seller's mail switch; the dedup key is the fact's `event_id`. Migration `00004_contract_mail_kinds.sql` allows the three kinds (the check allowed four, so the first copy of an accepted agreement could never be recorded).

Customer notices (plan 13): a seventh Kafka consumer, `customer.block-notice.v1` (`-block-notices`, own DLQ `customer.block-notice.dlq.v1`, `sent_notifications.kind = 'customer_notice'`, migration `00006_customer_notice_kind.sql`), mails the customer that the seller blocked them (`customer_blocked.v1`, with a line when the block also suspends the service and the end of the block when it has one) or lifted the block (`customer_unblocked.v1`). Both templates are in the category `customer_notices`, which is **off until the seller switches it on** (the mail page of the console, `PUT /v1/mail-settings`), so by default the consumer reads the fact and sends nothing; a fact that was read while the category was off is not sent later. The fact never carries the seller's category or note.

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

Plan 22 G7 adds the reconciliation workflow ([`docs/reconciliation.md`](../reconciliation.md)): a queue of kept discrepancies with an owner, a note and a history, a schedule
(`PAYMENTS_RECONCILE_INTERVAL`, 6h, 0 turns it off; needs `PAYRUDDER_BILLING_ADDR`), settlement lines (read from Stripe's balance transactions and payouts, or imported as CSV)
that tell "paid" from "settled", and the lag after which a payment the provider never reported is a finding (`PAYMENTS_SETTLEMENT_LAG`, 7d).

Plan 22 G8 adds [`docs/provider-health.md`](../provider-health.md): a circuit breaker per provider account (`PAYMENTS_BREAKER_FAILURES` 5, `PAYMENTS_BREAKER_OPEN_FOR` 30s) that
makes new charges fail fast while the provider is down (the open window is shared by the replicas through Redis when `PAYRUDDER_REDIS_URL` is set), a periodic health check of every account (`PAYMENTS_HEALTH_INTERVAL` 5m, 0 turns it off; `PAYMENTS_HEALTH_TIMEOUT` 10s),
and a failover to a second configured account that happens only when the first provider definitely did not process the charge, never after a timeout.

Plan 08 and 22 add three things to the charge path. The provider's own reference of a charge (a Stripe PaymentIntent id) is kept with the attempt (`payment_attempts.provider_ref`) and a status query asks the provider by it (`payments.RefStatuser`), searching by the key only when there is none. A charge that needs the customer present (Stripe's 3-D Secure: `requires_action`, or the decline `authentication_required`) is answered `PaymentActionRequired`, not declined (no ledger row), and a status query that finds it waiting answers `STATE_ACTION_REQUIRED` until the window of the pending payments (`PAYMENTS_TRANSFER_WINDOW_DAYS`) has passed, then a decline: billing's saga mails the customer and waits ([billing-svc](billing-svc.md), `PAYMENT_ACTION_REQUIRED`). A provider's dispute notification (Stripe's `charge.dispute.*`, Paddle's chargeback adjustments) is a step of a case: billing is told first (`RecordPaymentDispute`: the webhook `payment.disputed`, an audit entry, the optional block of the customer), then the ledger gets a row of kind `dispute` and `payment_disputes` the case as it stands; a notification that billing cannot take is a 503 that leaves nothing behind. `GET /admin/tenants/{tenant}/disputes` lists the cases (the gateway serves `GET /v1/disputes`). A step the ledger already holds is not told again.

Phase 15 adds Paddle: `POST /webhooks/{provider}/{tenant}/{environment}` (a notification destination per account, verified by signature), the tenant's provider subscriptions
and hosted checkouts under `/admin/tenants/{tenant}/`, and `PAYRUDDER_PLANS_ADDR`, through which subscriptions a provider runs are linked to ours. See phase 15.

**The buyer and the payer's return** (plan 07, iyzico). A provider that vets the payer (`Capabilities.NeedsBuyer`) is sent the customer: a charge command carries `customer_external_id`, a hosted payment the external id and the payer's address (`customer_external_id`, `payer_ip` of `POST /admin/tenants/{tenant}/invoice-payments`), and payments-worker reads the customer record from plans-svc (`internal/buyer`). A customer record that lacks what the provider needs declines a charge as `buyer_incomplete:<fields>` and answers a hosted start `422 payments.invoice.buyer_incomplete`. `GET .../invoice-methods?customer=` adds the `stored_card` method (the customer's saved card, authenticated at their bank) for a provider that implements `payments.ThreeDSProvider`; starting it answers `{"kind":"html","html":"..."}`, the bank's page. A provider that posts the result of a payment to a callback URL (iyzico) is given `callback_url` with the attempt's key in place of `{key}`; `POST /admin/invoice-payments/return {"key", "form"}` (internal token) is where the gateway hands the posted form (`internal/returns`): the attempt is found by its key, the provider is asked what the payment is, the same ledger row its notification writes is appended, and billing settles the invoice. A payment already in the ledger is answered from it, so the form posted twice, or after the notification, changes nothing. **Instalments of the saved card** (plan 07): `GET .../invoice-methods?amount_minor=` gives the `stored_card` method an `installments` list (`count`, `each_minor`, `total_minor`) when the provider implements `payments.InstallmentOffer` (iyzico: the account's `enabled_installments` above 1, at the BIN of the saved card, as iyzico quotes them now) and answers; `POST .../invoice-payments` takes `installments` (2 to 12, only for `stored_card`), checks it against a fresh quote (`409 payments.invoice.installment_unavailable`), starts the 3-D Secure payment with `installment` and the quote's total as `paidPrice`, and keeps both in the attempt (`paid_minor`) so that the return confirms with the same total and the ledger books the invoice's price. **Stored cards:** `GET .../customers/{external_id}/cards` and `DELETE .../customers/{external_id}/cards/{token}` (`internal/storedcards`) list and delete the cards of the provider whose card storage the customer's payment method names (`payments.CardStore`, today iyzico); the user key is read from the customer, never from the request, and a token that is not in that user key's list is `404 payments.card.not_found` with nothing sent to the provider; the customer's payment method is not changed.

Phase 45 adds `GET /admin/tenants/{tenant}/go-live` (internal token): `{"ready": bool, "providers": [...], "accounts": [...]}`, true when the tenant has a live-mode account whose sealed settings still open, still build an adapter and, for an adapter that can ask its provider (`payments.HealthChecker`: iyzico `/payment/bin/check`, Paddle `/event-types`; both unverified against the real hosts, tested against fakes), whose provider accepts the credentials now (the answer is remembered 30 s per version of the settings). Each entry of `accounts` says `working`, `checked` (the provider was asked) and, when it does not work, `problem` (`unreadable`, `refused`, `unreachable`); no setting is in it. The gateway asks it for the go-live checklist; plans-svc never reads provider data.

A person connects their own live account through the gateway (`PUT /app/v1/apps/{id}/go-live/providers/{provider}`), which calls `PUT /admin/tenants/{tenant}/provider-accounts/{provider}?environment=live&verify=true` with `X-Payrudder-Actor: user:<id>`. `verify=true` refuses an endpoint that is not one of the provider's documented hosts (`payments.account.endpoint_not_allowed`: the platform would send the person's secret keys wherever they pointed it), refuses the provider's test host for a live account, and asks the provider to accept the credentials before they are stored (`payments.account.health_check_failed`, `payments.account.health_check_unreachable`). Every change is a row of `provider_account_events` (provider, environment, connected/replaced/removed, actor, whether the provider vouched; never a setting), written in the same transaction. See [ADR 0023](../adr/0023-live-and-sandbox-guards.md).

**E-invoice accounts** (plan 10, [ADR 0026](../adr/0026-integration-accounts.md)): an environment holds several, each named by its key as a payment account is (`nilvera`, `nilvera~tr`). One is the default (the first connected, or the one `POST /admin/tenants/{tenant}/e-invoice-accounts/{environment}/default?account=` names); `GET/PUT .../{environment}/routing` maps a buyer's country to an account, and a buyer of any other country goes to the default. A document records the account it began with, and a repeat or a cancel goes through that account whatever routing says now. `DELETE`, `.../check` and `.../default` name the account with `?account=` (or, for `DELETE`/`check`, the body); with just one account the name may be left out. The default cannot be removed while others exist (`einvoice.account.default_in_use`). Changes of payment and e-invoice accounts, default moves and routing changes are rows of `provider_account_events` (a `kind` of `payment` or `einvoice`), read newest first with `GET /admin/tenants/{tenant}/provider-account-events?kind=&limit=&before=`.

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

With `PAYRUDDER_CELLS` set ([runbook](../runbooks/cells.md); it needs `PAYRUDDER_PLANS_ADDR`, the directory being plans-svc's) a tenant that the directory places in a cell has all eleven of its tables (`payment_attempts`, `payment_ledger`, `provider_accounts`, `provider_events`, `provider_subscriptions`, `provider_objects`, `manual_receipts`, `einvoice_accounts`, `einvoice_routing`, `einvoice_issuances`, `provider_account_events`) in its own schema `t_<id>` of the cell's `payments_db`, built by `migrations/tenant` (the nine public migrations as they end up, written once for a schema). Off by default; no tenant is placed in a cell.

**No row-level security.** payments-worker has none today (only `payments_system` touches the tables, and every statement names its tenant), and the tenant set has none either, so **the schema is the whole boundary** in a cell: every store method takes the tenant it acts for, and `store.at` / `store.Tx` run it where the router says that tenant is, in a transaction whose `search_path` is `"t_<id>", shared`. A transaction belongs to one tenant: it is opened for it and ends with it. A statement that names no tenant is refused when cells are on (`store.ErrNoTenant`) instead of being answered from the pooled database. Tested: two tenants of one cell, one of another and a pooled one write the same keys, event ids and bank references and see none of each other's rows (`TestStoresStayInTheirTenantsPlace`), and no tenant schema holds a row of another tenant after any scenario. A statement that names another tenant's schema explicitly would be allowed by the grants; no code path does, and there is no policy behind it.

**The four jobs.**

- *Command worker.* The reply inbox of the worker (`workerx.TenantInbox`, new and optional) is asked with the tenant of the command; the attempt, the ledger row and the stored reply are in the tenant's place. A tenant that is moving makes the command fail with `tenantdir.ErrNotServing`, which is retried through the delay queues; a failed provider lookup for such a tenant is not answered `PROVIDER_NOT_CONFIGURED`, a reply billing would act on. The provider is never called for a tenant that cannot be written.
- *Provider webhook server.* A URL that names the tenant (Paddle) decides the place at once. A URL that does not (iyzico) carries only the idempotency key of the charge, and the tenant that made it is found without looking in any tenant's schema: the pooled `payment_attempts` first, then **`shared.charge_keys`, one table per cell database**, a key-to-tenant index that a trigger on `payment_attempts` fills in the same transaction as the attempt (so a move that copies attempts into a cell fills it too; an entry left behind by a move still names the right tenant, and the directory says where that is). One query per place, never one per tenant. The notification is then kept in that tenant's inbox (the pooled `provider_events` keeps only the notifications that belong to no tenant, such as the ones about a key nobody made), confirmed with that tenant's own sealed account, and recorded in its ledger. A tenant that is moving answers `503 payments.tenant.unavailable` before anything is recorded, so the provider retries. Account secrets are sealed and opened with the same tenant|provider|environment context as before, and the signature or confirmation of a notification is the provider adapters' code, unchanged.
- *Transfers (bank-transfer matching).* Pending rows, closures, receipts and the sums are the tenant's own; the receipt transaction (ledger row, receipt row, advisory lock on the invoice) runs in the tenant's place, and two tenants with the same invoice id or bank reference do not meet.
- *Sandbox reset.* One transaction in the tenant's place deletes its rows, and in a cell also its keys from the cell's `shared.charge_keys`; it is refused while the tenant moves.

The job inventory has no loop over tenants here (each of the four acts for the tenant of its command, notification or request), so "no tenant skipped, none twice, none mixed" is the property of each unit of work: `TestCommandsRunInTheirTenantsPlace` runs a command for every tenant at once and counts the provider's calls, attempts and ledger rows. `ITEST_CELLS=1 go test -tags integration ./internal/itest` runs the whole suite with every tenant in a cell.

**Moves.** `payrudderctl cells move` works between the pooled database and cells and between cells (`TestMovesOfPaymentsTenants`). A move **into the pooled database is refused** by `pgxx.Move`: `manual_receipts.ledger_id` refers to `payment_ledger.id`, an identity column, which moving into the shared pooled sequences would renumber. A tenant that has to go back to the pooled database needs that foreign key and its identity handled first.

## As built: the audit log in payments-worker (plan 22, G4)

`audit_events` (migration `00006`, twin in `tenant/00007`; no row-level security, like the rest of payments_db) is written next to `provider_account_events` when a provider account is connected, replaced, removed or made the default, with the actor the gateway sent (`audit.Middleware` reads the `X-Payrudder-Audit-*` headers). `GET /admin/tenants/{tenant}/audit-events` and `GET /admin/audit-events` answer the gateway's fan-out with the same message `AuditService` uses. Nothing of the account's settings is in an entry. See [audit-log-and-two-factor.md](../audit-log-and-two-factor.md).

## The notifier attaches the PDF copy of an accepted agreement (plan 21)

When a customer accepts an agreement the notifier asks plans-svc for the PDF drawn from the stored page of that acceptance (hash checked first; drawn on request, never stored; see [21](../plans/21-contracts.md), "The PDF copies") and attaches it to the mail as `agreement-<first 12 characters of the hash>.pdf` (`mailx.Message.Attachments`: SMTP sends MIME parts, Resend its `attachments` array). A mail without the PDF is sent when the PDF cannot be drawn; the copy is also downloadable from the portal and the console.
