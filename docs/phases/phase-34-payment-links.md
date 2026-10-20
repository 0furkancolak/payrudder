# Phase 34 — Payment links and the customer payment page

## Goal

A tenant (the SaaS company) can hand its customer a link. The customer opens it **without signing in**, pays, and can later see what they paid. Two links:

- a **payment link**: for one open invoice, or ad hoc for a fixed amount that becomes an invoice when the customer first uses it;
- a **customer portal link**: one customer's invoices, payments, PDFs and receipts, read-only, with a pay button on what is still open.

Payment goes through the saga and the ledger that already exist; the page only shows what they say. The browser coming back from a provider never marks anything paid.

## Scope

**In:**

1. **One link flow.** The phase-23 invoice link (`invoice_links`) becomes `payment_links` with a `kind` (`invoice`, `adhoc`). `POST /v1/invoices/{id}/links` keeps working and makes a `kind=invoice` link; `GET /i/{token}` keeps serving it.
2. **Ad-hoc link** (`amount_minor`, `currency`, `description`, optional customer): the invoice (kind `LINK`, gap-free number, the same saga) is made when the payer presses *Continue*, not when the page is fetched, so a crawler or a link preview burns no number.
3. **Idempotent creation**: `idempotency_key` (body or `Idempotency-Key` header) per tenant; a replay returns the link made first, without the token.
4. **Single-use or reusable**: a single-use link is consumed by its first paid invoice; a reusable ad-hoc link makes one invoice per use.
5. **Hosted page** (gateway): amount, lines, seller, tenant logo and name, payment methods the invoice has (provider checkout, payment instructions of a manual provider, mock in sandbox/demo), receipt state (open / processing / failed / paid / void), six languages, mobile friendly, no script.
6. **Customer portal** (`/c/{token}`): invoices, payments (ledger rows: charge/refund, status, date, amount, provider), invoice PDFs, receipts, pay buttons.
7. **Operator API** (tenant key): create/list/get/revoke payment links, create/revoke portal links, list a customer's payments; OpenAPI; error keys.
8. **Events**: `payment_link.created`, `payment_link.paid`, `payment_link.expired`.
9. **Console**: *Payment links* page, *Create link* actions on the invoice and customer pages, six languages.

**Out (non-goals):**

- Partial payments, tips, quantities, coupons and tax on an ad-hoc amount: the amount is the total the customer pays. Tax on it is the tenant's to include.
- Saving a card from the page for later charges.
- Customer sign-in, a customer-editable profile, or any write other than "pay".
- QR codes (no new dependency), e-mailing the link (the tenant sends it).
- The bank-transfer provider itself (another PR). Its methods appear on the page through the provider contract below as soon as it exists.
- Sandbox runs of iyzico's and Paddle's hosted checkout: they are written from the public documentation and marked UNVERIFIED.

## Decisions

- **A link is a capability.** 256 random bits, only the SHA-256 stored, shown once; revocable; expires (default 30 days for ad hoc, 90 for an invoice link; at most 365). Unknown, expired and revoked answer the same page.
- **An ad-hoc link is a factory.** `/i/{token}` of an ad-hoc link shows what is asked (description, amount, seller) and a *Continue* button (`POST /i/{token}/start`). That creates the invoice and a child `invoice` link (`parent_id`), and redirects the payer to `/i/{child}`. Everything after that is the invoice page, so a reusable link never shows one payer another's receipt. A single-use link that already has an unpaid invoice hands out a fresh child link for the same invoice; once that invoice is paid the link is consumed.
- **Guest invoices.** An ad-hoc link without a customer makes an invoice for the nil customer id (`00000000-…`), named by the description; its customer name is empty and the payer is whoever the provider's page collects. A bound link uses the customer's name, e-mail, address and country.
- **Revoking an ad-hoc link voids its unpaid invoices** (nobody can pay them any more); a paid one is untouched. Expiry emits `payment_link.expired` (a scheduler pass) and voids likewise.
- **The saga learns to wait.** New state `AWAITING_PAYMENT` after `PDF_RENDERED` for an invoice whose snapshot says `collection: "link"` (every ad-hoc invoice). It has no deadline. New events: `PayRequested` (mock pay or "pay now": `AWAITING_PAYMENT`, `PAYMENT_DECLINED`, `UNCOLLECTIBLE` to `PAYMENT_REQUESTED` with a charge) and `Settled` (the provider confirmed a hosted payment: `AWAITING_PAYMENT`, `PAYMENT_REQUESTED`, `PAYMENT_UNKNOWN`, `PAYMENT_DECLINED`, `UNCOLLECTIBLE` to `PAID`). A late reply after `PAID` is ignored by the table, so a dunning charge and a hosted payment racing never mark it twice; a second real payment is a discrepancy for the operator (`SettleInvoicePayment` answers `settled=false`; payments-worker counts it).
- **Webhook and ledger drive state.** A hosted checkout writes a `pending` ledger row and an attempt with key `link:{invoice}:{n}`; the provider's confirmed notification writes the `succeeded` / `declined` row and, for a `link:` key, payments-worker calls `BillingService.SettleInvoicePayment` (amount and currency must equal the invoice). The browser's return only shows the state.
- **Mock pay** (sandbox tenant or `QUOTIFY_DEMO`): billing sets the snapshot's payment method to `pm_sandbox_success` and fires `PayRequested`; the charge command runs through payments-worker's mock provider like any other. A live tenant never gets a mock method.
- **Payment methods come from the provider catalogue.** Sandbox and demo: `mock` only (ADR 0011). Live: payments-worker builds the adapters of the tenant's accounts and offers `checkout` for the routed (else default) provider when it implements `payments.InvoiceCheckoutProvider` and can charge the currency, and one `instructions` method for every account whose adapter implements `payments.InstructionsProvider` (what a bank-transfer provider will implement). No list of provider names in the page.
- **Receipt state** on the page: `paid` (invoice paid), `void`, `processing` (saga charging, or the latest ledger row is `pending`), `failed` (the latest row is `declined`, or the saga is in dunning), else `open`. The page refreshes itself with `<meta http-equiv="refresh">` while `processing`.
- **The portal is scoped twice.** The token resolves to `(tenant, customer)`; every query then runs in that tenant's RLS transaction with the customer in the predicate; an invoice id from the URL must belong to both or the answer is the not-found page. Payment rows come from payments-worker's ledger by that customer's invoice ids; nothing else leaves the tenant.
- **Rate limits**: the existing 60/min per address for pages, 10/min for the POSTs that start a payment.
- **Security headers** as the hosted invoice page, with `form-action 'self'` and `img-src 'self'`. The logo is fetched by the gateway from a presigned URL and served from `/i/{token}/logo` (≤ 1 MiB, `image/png` or `image/jpeg`).

## Contracts

### billing-svc

`proto/quotify/billing/v1/billing.proto` (additive): `CreatePaymentLink`, `GetPaymentLink`, `ListPaymentLinks`, `RevokePaymentLink`, `CreatePortalLink`, `RevokePortalLinks`, `SettleInvoicePayment` on `BillingService`; `StartLinkPayment`, `RequestHostedPayment`, `GetPortal`, `GetHostedLogoUrl` and the extended `GetHostedInvoice` / `GetHostedInvoicePdfUrl` on `HostedInvoiceService`.

### payments-worker (HTTP, `X-Internal-Token`)

- `GET /admin/tenants/{tenant}/invoice-methods?environment=live&currency=EUR&country=DE&provider=paddle` → `{"methods":[{"id":"checkout","kind":"checkout","provider":"paddle"},{"id":"instructions:banktransfer","kind":"instructions","provider":"banktransfer"}]}`. Empty `provider` means the tenant's default account.
- `POST /admin/tenants/{tenant}/invoice-payments` `{invoice_id, saga_id, number, amount_minor, currency, method, provider, country, customer_email, customer_name, description, return_url, environment}` → `{"kind":"redirect","url":"…"}` or `{"kind":"instructions","reference":"…","lines":[{"label":"IBAN","value":"…"}]}`.
- `POST /admin/tenants/{tenant}/ledger/lookup` `{"invoice_ids":[…≤200]}` → `{"rows":[{"id","invoice_id","provider","environment","kind","status","amount_minor","currency","occurred_at"}]}`.

### pkg/payments

`InvoiceCheckoutProvider.CreateInvoiceCheckout(CheckoutInvoiceRequest) (Checkout, error)` (Paddle: a transaction with a non-catalogue item and `custom_data.quotify_key`; iyzico: checkout form initialise), `InstructionsProvider.PaymentInstructions(InstructionsRequest) (Instructions, error)`.

### Gateway

Tenant API: `POST|GET /v1/payment-links`, `GET|DELETE /v1/payment-links/{id}`, `POST|DELETE /v1/customers/{external_id}/portal-links`, `GET /v1/customers/{external_id}/payments`. Public: `/i/{token}` (+ `/start`, `/pay`, `/pdf`, `/receipt`, `/logo`), `/c/{token}` (+ `/logo`, `/invoices/{id}`, `/invoices/{id}/pdf`, `/invoices/{id}/receipt`, `/invoices/{id}/pay`).

## Tasks

- [x] Plan, proto contract, OpenAPI contract
- [x] billing-svc: migration `payment_links` (rename of `invoice_links`), `portal_links`, `invoices.kind LINK`, nullable plan version
- [x] billing-svc: sqlc queries; `paymentlinks` / hosted modules (command, query), ad-hoc invoice, portal
- [x] billing-svc: saga `AWAITING_PAYMENT`, `PayRequested`, `Settled`; `SettleInvoicePayment`; mock pay
- [x] billing-svc: tenant events (created, paid, expired), expiry pass, revoke voids unpaid ad-hoc invoices
- [x] plans-svc: the three event types in the catalogue
- [x] pkg/payments: `InvoiceCheckoutProvider`, `InstructionsProvider`; Paddle and iyzico checkouts
- [x] payments-worker: methods, invoice-payments, ledger lookup, settle call after a confirmed `link:` charge
- [x] gateway: pages (six languages), routes, operator API, rate limits, OpenAPI
- [x] console: *Payment links* page, create-link actions, six languages, mocked-gateway specs
- [x] tests: unit; billing integration (create, open, pay with mock, saga, ledger, isolation, expiry, revoke, single-use); payments-worker integration; gateway; console
- [x] docs: 14, 16, error keys, ROADMAP
- [ ] PR `feat/payment-links`, CI green

## Exit criteria

A sandbox tenant creates an ad-hoc link, opens it, presses Continue and Pay, the invoice goes `AWAITING_PAYMENT` → `PAYMENT_REQUESTED` → `PAID`, the ledger has the charge, the page shows *Paid* and the receipt, and the tenant's webhook endpoint gets `payment_link.paid`. A portal token of one customer or tenant cannot read another's invoice. The existing invoice-link tests, saga table tests and end-to-end suites pass unchanged.
