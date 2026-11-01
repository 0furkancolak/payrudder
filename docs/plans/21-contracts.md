# 21 — Contracts: distance sales and other agreements for the seller's customers

## The request
Owner, 2026-11-01: add a contracts section to the plans so a seller (a Payrudder tenant) can present a **distance sales contract** ("mesafeli satış") or other agreements to *their* customers.

## Where we are
Nothing exists for a seller's customers (searched the gateway, portal, checkout, payment links, customer and settings code). What exists is adjacent:
- The tenant owner accepts **Payrudder's own** terms at go-live: `tenants.terms_accepted_at/_by` (`services/plans-svc/migrations/00001_init.sql`, `golive/command`). It is a timestamp and a user id with no text version, hash, IP or user agent, so it is the wrong shape for customer evidence. Do not reuse it.
- Seller legal data exists (`LegalInfo`: legal name, tax id, tax office, MERSIS, trade registry, address in `resources.proto`, edited on the e-invoice page).
- Customer language and kind (individual / business) exist (`CustomerLocales` = en, tr, de, fr, es, ar). Hosted pages render in the customer's language (`hosted*_i18n.go`, `customerLang`).
- Hosted pages have no terms link or checkbox; the only privacy text covers the referral cookie.
- The gateway reads the client IP (`hosted.go`, trusted `X-Forwarded-For` helper in `session.go`); the user agent is not captured.
- `assets-svc` stores binaries; `notifier` sends customer mails in six languages; webhook event names live in `plans-svc/internal/webhooks/model`.

## Legal basis (this is **not** legal advice; sources are secondary where marked)
- **Turkey, Mesafeli Sözleşmeler Yönetmeliği** (RG 27.11.2014 No. 29188, amended 2022): pre-information before the contract and part of it, in writing or on a durable medium; the consumer confirms receipt or the contract is not concluded; the order control must state that it creates a payment obligation; 14-day withdrawal; exceptions for services performed instantly in electronic form and for services started with the consumer's approval; records kept for a period the sources give as 3 years. Source: a secondary mirror (alomaliye.com, 2026-11-01). **UNVERIFIED**: article numbers, the 3-year period, which 2022 additions apply from 2025. Check on mevzuat.gov.tr before any text is shipped.
- **EU Consumer Rights Directive 2011/83/EU**: 14-day withdrawal; for digital content/services the consumer gives prior express consent and acknowledges losing withdrawal, and gets confirmation on a durable medium. Directive (EU) 2023/2673 adds an online withdrawal function, applying from 19 June 2026 per the sources found (national transposition varies). **UNVERIFIED**: exact article wording (EUR-Lex text was not readable here).
- EU-wide auto-renewal rules: none found; national rules differ. **UNVERIFIED.**

Stance: Payrudder supplies **templates and acceptance evidence**, not legal guarantees. Default templates carry a visible "template, have a lawyer review" banner. Each seller is responsible for its text.

## Gaps this plan closes
No way to publish seller-specific agreements; no record that a customer saw and accepted a specific text; no immediate-start/withdrawal-waiver evidence; no withdrawal request flow; no renewal notice.

## Research first
1. Read the official Turkish regulation and the EU texts (mevzuat.gov.tr, EUR-Lex) and replace every UNVERIFIED above, with URL and date.
2. Read how Paddle (merchant of record terms), Stripe Checkout (`consent_collection`, `custom_text`), Chargebee and Lemon Squeezy capture consent, to align the field names.
3. Trace every path that starts a payment, to place the "agreement required" gate: hosted invoice page, ad-hoc payment link, payment link, credit pack, portal plan change, embedded checkout via API/SDK.

## Decisions needed (owner)
- Which kinds ship first (proposal: distance sales, pre-information form, terms of service, privacy notice; later DPA, refund policy, auto-renewal terms).
- Whether the platform ships default template **text** per language, or only the structure and variables (proposal: structure plus clearly marked starter text for tr and en, other languages by the seller).
- Retention period default and who may erase IP data (seller is controller; proposal: configurable, default 3 years pending verification).
- How a withdrawal request is decided (proposal: the seller decides, Payrudder records and routes to refund/credit note).

## Design (proposal)
**Data (plans-svc, tenant-scoped, RLS like the neighbours)**
- `contract_templates`: tenant, optional app, `kind`, `locale`, `current_version_id`.
- `contract_versions`: immutable; body (restricted markdown, escaped variables only), variable schema, sha256 hash, author, time; "material" or "minor" flag. Editing creates a new version.
- `contract_acceptances`: append-only (no UPDATE/DELETE grants, enforced by the same test that guards other immutable tables); customer, version, hash, kind, locale requested and served, context (checkout / payment link / portal / API with invoice or subscription id), exact checkbox text, resolved variables, rendered snapshot (asset id), IP, user agent, time, and which fields were server-observed vs client-asserted (API path).
- Separate consent fields: `immediate_start_consent` and `withdrawal_waiver_acknowledged` with their texts; never merged with general acceptance.
- `withdrawal_requests`: customer, subscription/invoice, received_at, status, link to refund or credit note.

**Where it applies**
- Hosted pages and payment links: pre-information and contract shown immediately before the pay button; unticked checkbox per required kind; a separate unticked checkbox for immediate start/waiver where the seller sells digital services; the button text states the payment obligation; the server refuses to start a payment without a matching acceptance.
- Portal: list of accepted versions, re-acceptance when a material version is published, a withdrawal button.
- API/SDK: `required_contracts[]` on checkout/payment-link creation; `POST /v1/customers/{id}/contract-acceptances` for embedded checkouts.
- Business customers (`customer_type = business`) get the B2B variant; unknown type is treated as consumer (safer default).

**After acceptance**: render HTML/PDF snapshot, store in `assets-svc`, mail it in the customer's language (durable medium), expose it in the portal and API. Renewals: an auto-renewal notice mail before each renewal (lead time is the seller's setting), stored as an event. Webhooks: `contract.accepted`, `contract.version_published`, `contract.withdrawal_requested`.

**Console**: a Contracts page (templates by kind and locale, editor with a variable palette and live preview per locale, version diff, publish confirmation showing which apps/plans are affected, banner on starter text); an acceptance log in the customer sheet; a go-live check "required contracts published" for sellers that sell to consumers.

## Work, in order
1. Replace UNVERIFIED legal points with sourced text; write the spec (data, gate matrix by path).
2. Migrations and domain (templates, versions, acceptances append-only, withdrawal requests) with isolation and immutability tests.
3. Gateway: gate on every payment-starting path from step 1.3, IP/user-agent capture, snapshot render and store, acceptance mail.
4. API, proto, four SDKs, webhooks, error keys.
5. Console page, customer sheet log, go-live check, six-language strings.
6. Withdrawal request flow into refund/credit note; renewal notice.
7. Starter templates for the agreed languages, marked as templates.

## Done means
Every payment-starting path in the matrix has a test that payment cannot begin without a matching acceptance; a published version cannot be edited; an acceptance row cannot be updated or deleted; the snapshot mailed equals the stored hash; the console shows the log; docs describe the legal limits.

## Not verifiable here
Legal sufficiency of any text; that a court or authority accepts the evidence; real mail rendering of PDFs; real provider behaviour when payment is refused mid-flow.

## Risks
A wrong default text exposes sellers: banner, disclaimer, no machine-translated legal text. Re-acceptance prompts can block renewals: material/minor flag and a grace window. IP address is personal data: configurable retention and erasure policy documented.
