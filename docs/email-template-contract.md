# E-mail template contract

What Payrudder sends, as a contract between Payrudder (the sender) and an e-mail provider that may render the mail itself (Nafru Email: a customer adds Payrudder in Nafru's "applications" and edits the templates there). It is the inventory of plan 11 ([`plans/11-email-nafru.md`](plans/11-email-nafru.md)), kept in step with the code by a test.

**Status.** Payrudder renders every mail itself (Go `html/template` files, six languages) and sends finished `subject`, `html` and `text`. Everything about Nafru rendering a mail from a template and variables is **NOT BUILT and UNVERIFIED**: Nafru's template contract has not been seen. This page says what Payrudder would send for it and what Nafru would have to implement; it promises nothing on Nafru's side.

## The message

Payrudder sends through a Resend-compatible API (`POST {base}/emails`, `Authorization: Bearer <key>`; shape from Resend's public API reference, read 2026-11-02). Per message:

| Field | Value |
|---|---|
| `from` | the tenant's From address when it connected its own provider, else the platform's `MAIL_FROM` |
| `to` | one address (the customer) |
| `subject`, `html`, `text` | the rendered mail, in the customer's language (`locale`), prefixed `[SANDBOX] ` for a sandbox tenant (sandbox mail goes to a sink, never to the provider) |
| `Idempotency-Key` header | the **dedup key** of the mail: the command id (billing mail), the limit event id, the trial event id or the price-change event id. A repeat of the key must answer with the first message's id; Payrudder retries with the same key after a lost answer |
| tag `payrudder_template` | the template name with the dot written as an underscore (`invoice_paid_v1`: a tag value takes ASCII letters, digits, `_` and `-` only) |
| header `X-Payrudder-Template` | the template name as it is below (`invoice_paid.v1`) |

UNVERIFIED: that Nafru shows the tag and lets an application match on it or on the header. Payrudder sends both and does not depend on either.

Every mail also carries `locale` (`en`, `tr`, `de`, `fr`, `es`, `ar`; empty or unknown is English; selects the template file and the subject) among its data. Bank-transfer details, when there are any, ride in the same data (the keys below).

## The twelve templates of tenants' customers

Sent by the notifier for a tenant, to that tenant's customers. A tenant can switch a **category** off (`pkg/mailcategory`, the mail page of the console); a template in no family is `system`. One category is the other way round: `customer_notices` (the notice to a blocked customer) is **off until the tenant switches it on**. Each exists in all six languages (`services/notifier/internal/templates/files/<name>[.<locale>].html`) with a translated subject. A new wording is a new version (`.v2`), never an edit of a sent one.

| Template | Category | Sent when | Variables |
|---|---|---|---|
| `invoice_paid.v1` | `payment_receipts` | an invoice is paid (billing-svc saga, command `SendNotification`) | `amount`, `currency`, `customer_name`, `invoice_number`, `period`, `tenant_name` |
| `payment_failed.v1` | `payment_failed` | an invoice became uncollectible after dunning (billing-svc saga) | `account_holder`, `amount`, `bank_name`, `currency`, `customer_name`, `iban`, `invoice_number`, `period`, `reference`, `tenant_name` |
| `invoice_transfer.v1` | `system` | the saga starts waiting for a bank transfer, and again for every dunning retry (billing-svc saga) | `account_holder`, `amount`, `bank_name`, `bic`, `currency`, `customer_name`, `iban`, `invoice_number`, `period`, `reference`, `tenant_name` |
| `payment_action_required.v1` | `system` | the provider needs the customer present to go on with a charge (Stripe's 3-D Secure); sent once, when the saga starts waiting (billing-svc saga) | `amount`, `currency`, `customer_name`, `invoice_number`, `pay_url`, `period`, `tenant_name` |
| `limit_exceeded.v1` | `usage_alerts` | a usage threshold of a customer's meter is crossed (Kafka `usage.limit-exceeded.v1`) | `customer_name`, `limit`, `meter_code`, `period`, `tenant_name`, `threshold_pct`, `used` |
| `trial_will_end.v1` | `system` | a customer's trial ends soon (Kafka `subscription.trial-will-end.v1`) | `customer_name`, `plan_code`, `tenant_name`, `trial_end`, `trial_end_date` |
| `price_change.v1` | `system` | a price change was scheduled for a customer's subscription (Kafka `subscription.price-change.v1`) | `customer_name`, `effective_date`, `new_renewal`, `plan_code`, `tenant_name` |
| `contract_accepted.v1` | `system` | a customer accepted one of the seller's agreements; the mail is their copy (Kafka `contract.accepted.v1`, plan 21), with a PDF of the same page attached when the page can be drawn | `accepted_at`, `customer_name`, `has_pdf` (set when the PDF is attached, else absent), `snapshot_html`, `snapshot_sha256`, `tenant_name` |
| `renewal_notice.v1` | `system` | a subscription of a customer who accepted the seller's agreements renews in the seller's notice days (Kafka `subscription.renewal-notice.v1`, plan 21) | `cancel_text`, `customer_name`, `interval`, `plan_code`, `price`, `renewal_date`, `tenant_name` |
| `contract_withdrawal_received.v1` | `system` | a customer used the withdrawal function of a hosted page; the mail is the acknowledgement of receipt (Kafka `contract.withdrawal-received.v1`, plan 21) | `customer_name`, `received_at`, `statement`, `tenant_name` |
| `customer_blocked.v1` | `customer_notices` | the seller blocked the customer (Kafka `customer.block-notice.v1`, plan 13); sent only when the tenant switched `customer_notices` on. It never says why: the seller's category and note are not in the fact | `customer_name`, `service_suspended` (set when the block also suspends the service, else absent), `tenant_name`, `until` (the end of the block in the tenant's time zone, absent when the block has none) |
| `customer_unblocked.v1` | `customer_notices` | the block was lifted by the seller, or ended and the sweep cleared it (the same topic); same switch | `customer_name`, `tenant_name` |

Notes on the variables, all strings:

- **Money** is formatted for the reader before it is sent, in the language of the mail (`pkg/localefmt`): `amount` `11.16` in English and `11,16` in German, `1,500.00` and `1.500,00` (with the currency's exponent), `currency` the ISO code, `new_renewal` `12.00 USD` and `12,00 USD` (a no-break space before the code). **Dates** are formatted in the tenant's time zone and the mail's language: `effective_date` and `trial_end_date` `6 November 2026`, `6 Kasım 2026`, `6. November 2026`, `trial_end` `6 November 2026, 21:30 (Europe/Istanbul)`. The language is the customer's own, else the tenant's `default_language`, else English; `period` of an invoice mail is `October 2026` / `Ekim 2026` for a month. `period` is a billing period id (`2026-10`) or, for a limit over a day, hour, week or year, that window (`2026-11-05`); `lifetime` for a lifetime limit (the invoice mails write a month in words; `limit_exceeded.v1` still gets the id). A provider that renders dates by locale gets the formatted text, not a timestamp.
- `pay_url` (`payment_action_required.v1`) is the link to the invoice's page (`PAYRUDDER_PUBLIC_URL` of billing-svc plus `/i/<token>`, a link that works 30 days); it is absent when billing-svc has no public address, and the template then tells the customer to use the link they were sent. The mail is `system`, so a seller cannot switch it off: the customer is the only one who can finish the payment.
- **Bank details** (`account_holder`, `bank_name`, `bic`, `iban`, `reference`) are present only when the invoice is paid by bank transfer. `invoice_transfer.v1` needs them; `payment_failed.v1` shows its transfer paragraph only when `iban` is set.
- The billing saga also sends `invoice_id`, `sandbox` (`true`/`false`) and `locale` with every invoice mail; the templates do not read the first two. `limit_exceeded.v1` is given `locale` too.
- A variable a template does not receive reads as empty; a template never fails for a missing one.
- Customer-controlled text (`customer_name`, `tenant_name`) is HTML-escaped by Payrudder's renderer; a provider that renders must escape it too. `cancel_text` (`renewal_notice.v1`) and `statement` (`contract_withdrawal_received.v1`) are plain text the seller or the customer wrote, shown with their line breaks (`white-space: pre-line`); `snapshot_html` (`contract_accepted.v1`) is the one variable that is HTML on purpose: the agreement page Payrudder rendered and hashed (`snapshot_sha256`), inserted unescaped; a provider that renders must insert it as it is, because the fingerprint belongs to exactly those bytes.

The category of a template is `mailcategory.ForTemplate`; `invoice_issued` is a category with no template yet (the mail that announces an invoice is `invoice_transfer.v1`, which is `system` today).

A test (`services/notifier/internal/templates/contract_test.go`) fails when a template reads a variable that this table does not list, or the table lists one the template does not read, so this page cannot drift from the files.

## What a rendering provider would have to implement (UNVERIFIED, NOT BUILT)

If Nafru renders from a template, Payrudder would send, instead of `subject`/`html`/`text`: `template` (the name above), `locale`, `variables` (the table's keys) and the dedup key as `Idempotency-Key`. Nafru would need to: hold one application "Payrudder" with the seven templates in six languages (and fall back to English for an unknown `locale`); accept those variables, escaping them; keep tag/header matching if it routes by them; answer a repeated `Idempotency-Key` with the first message's id (24 hours is Resend's window; the dedup key must be honoured at least until Payrudder's retries stop, about 2.5 minutes for a command and until success for the Kafka consumers); report **bounces and complaints** to Payrudder (see [`runbooks/mail-suppression.md`](runbooks/mail-suppression.md)); and verify the sending domain of a tenant that sends from its own address (SPF, DKIM, DMARC). Until the owner supplies Nafru's contract the default stays "Payrudder renders"; the per-tenant switch between the two modes is part of the open work of plan 11.

## Mail to people (not tenants' customers)

plans-svc writes eight more mails itself, to the people who use the console, in the same six languages (`services/plans-svc/internal/identity/usermail`). They are not tenant mail: no category, no tenant switch, no suppression list, no allowance; they carry no template tag.

| Kind | Sent when | Variables |
|---|---|---|
| `verify` | an address must be proven | `link` |
| `reset` | a password is reset | `link` |
| `invite` | someone is invited to an account | `link`, `account`, `inviter`, `role` |
| `taken` | someone tried to sign up with an address that has an account | none |
| `code` | a one-time sign-in code | `code` |
| `ticket_new` | an account opened a support ticket (to the operators) | `link`, `account`, `ticket`, `subject`, `status`, `priority` |
| `ticket_reply` | an operator answered a ticket | `link`, `ticket`, `subject`, `status`, `priority` |
| `ticket_status` | a ticket's status changed | `link`, `ticket`, `subject`, `status`, `priority` |
