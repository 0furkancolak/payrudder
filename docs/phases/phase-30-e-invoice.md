# Phase 30 — E-invoicing (e-fatura, e-arşiv, EU e-invoicing)

## Goal

A tenant that sells in Turkey (or to EU businesses) must hand every invoice and credit note to a government-approved channel: Turkey's GİB through an
integrator (e-fatura for registered businesses, e-arşiv for everyone else), the EU through a Peppol access point or a national platform. Quotify already
numbers, totals and renders invoices; this phase turns the same invoice into a legal e-document (UBL 2.1) and delivers it through the integrator the tenant has
a contract with. See [14-billing-platform.md](../14-billing-platform.md), [15-billing-lifecycle.md](../15-billing-lifecycle.md), [ADR 0016](../adr/0016-provider-capabilities-and-routing.md)
(capabilities and a registry, here applied to e-invoice providers) and [ADR 0020](../adr/0020-modules-cqrs-errors-config.md).

## What was NOT verified

Read this first. Nothing in this phase has been run against a real integrator or a government test portal.

- **The `nilvera` adapter is written from Nilvera's public documentation**, not against a Nilvera sandbox account. Paths, field names and status codes
  can differ from what the integrator really does; every such place is marked `UNVERIFIED` in `pkg/einvoice/nilvera`. The tests use `httptest` servers that
  replay the documented shapes, so they prove our code handles those shapes, not that Nilvera sends them.
- **The UBL is checked structurally** (well-formed XML, element order of the UBL 2.1 schema for the parts we emit, mandatory elements, arithmetic of the
  totals, namespaces), not with the official XSD/Schematron packages of UBL-TR 1.2 or EN 16931/Peppol BIS 3. Those need the full schema bundles; validating
  against them is the first thing to do with a real sandbox account (`docs` task at the end).
- **Tax semantics are minimal**: one rate per document, derived from the invoice's `TAX` line; exemption codes are a fixed default. Withholding (tevkifat),
  special consumption tax, reverse charge and intra-EU zero rating are not modelled (see non-goals).
- The `webhook` adapter's contract is ours, so it is fully specified and tested; whether a given integrator can be put behind it is for the tenant to find out.

## Scope

**In:**

- `pkg/einvoice`: provider-neutral `Provider` interface (issue a document built from an invoice or credit note snapshot, status, cancel where legal, fetch PDF
  and UBL), `Capabilities` (country, document kinds, whether the buyer's registration must be checked), a registry like `pkg/payments`, and a deterministic
  **UBL 2.1 builder** with two customisations: UBL-TR 1.2 (Turkey) and EN 16931 / Peppol BIS Billing 3.0 (EU). Invoices and credit notes (`CreditNote`, iade).
- Adapters: `mock` (always accepts, fake legal number and UUID), `nilvera`, `webhook` (generic escape hatch signed with `pkg/webhooksig`).
- A flow from billing-svc to payments-worker and back: finalized (or paid) invoices and every credit note become a document with a state the tenant sees.
- API, OpenAPI and error keys; console settings and per-invoice / per-credit-note status; six languages.

**Out (non-goals):**

- Calculating tax. Rates stay the tenant's ([phase 22](phase-22-billing-documents.md)); the document carries what the invoice says. No withholding (tevkifat),
  ÖTV, reverse charge, intra-EU zero rating, tax exemptions per line, multiple rates on one invoice.
- Receiving purchase invoices (inbox), e-irsaliye (despatch), e-müstahsil, e-SMM, and Peppol network participation by Quotify itself (an access point is an
  integrator reached through the `webhook` adapter).
- Re-issuing a legal document. An issued document is never modified; the correction is a credit note (iade) and a new invoice. Cancellation exists only where
  the integrator and the law allow it (e-arşiv within its rules) and is an explicit operator action.
- Backfilling: turning e-invoicing on does not create documents for invoices finalized earlier.
- Signing UBL ourselves (mali mühür / e-seal). The integrator seals; the `webhook` adapter's receiver must too.

## Decisions

1. **Where it runs: payments-worker, with state in billing-svc.** Needs: a database for idempotency, encrypted per-tenant credentials, an HTTP port the gateway
   already forwards operator calls to, a retry-able outbound call to a third party, and object storage for the UBL/PDF. payments-worker has the first four
   (`provider_accounts` sealed with `pkg/secretbox`, `payments_system`, `PAYMENTS_HTTP_ADDR`, `workerx`) and gains the fifth by the same `S3_*` settings the
   invoice-worker uses. invoice-worker has storage and nothing else: giving it a database, a key and an HTTP server would be a new deployment shape; a new
   service would cost another ~30 MB resident for no isolation we need. The ledger of what the integrator told us stays next to the credentials; the state the
   **tenant** sees is billing-svc's (it owns invoices, credit notes, RLS and the console's reads), so no tenant data is duplicated into `payments_db`.
2. **Two tables, one writer each.**
   - billing-svc `einvoice_documents` (RLS on `tenant_id`, like every tenant table): the document state machine, the frozen request (the protobuf
     `IssueEInvoice` command), attempts and backoff, and what the worker answered (legal number, external id, UBL and PDF object keys).
   - payments-worker `einvoice_accounts` (sealed settings, bound to tenant|provider|environment, no RLS like `provider_accounts`: only `payments_system`
     touches it) and `einvoice_issuances` (one row per document UUID: the idempotency record, the legal number and keys once issued).
3. **Trigger.** Tenant setting `e_invoice_on` (plans-svc, `off` | `finalized` | `paid`, default `off`) is frozen into the invoice snapshot when the draft is made,
   with the seller's legal data and the buyer's tax data. In `Finalize`, or when the saga reaches `Paid`, billing-svc inserts the `einvoice_documents` row and
   the `IssueEInvoice` command into the outbox **in the same transaction**; `(tenant, kind, source id)` is unique, so a replayed transition creates nothing.
   Credit notes always create a document when the tenant has e-invoicing on, in the transaction that creates the note, but only if the invoice they correct has
   a document that is not failed/cancelled; otherwise the note is not e-invoiced (a credit note for something the authority never saw has nothing to refer to).
4. **Document state:** `pending` → `issued` | `failed`; `failed` → `pending` (operator retry); `issued` → `cancelled` (operator cancel, only when the provider's
   capabilities say it is legal). `issued` and `cancelled` are final. `pending` means "a command is out or due"; a document waiting for backoff has
   `next_attempt_at` in the future.
5. **Retry and backoff.** The worker never throws for a business answer: a rejected document is a reply `permanent` (state `failed` at once: the data is wrong, a
   retry changes nothing until the operator fixes it), a timeout/5xx/429 is a reply `retryable` and billing-svc schedules the next attempt at +1 m, 5 m, 30 m,
   2 h, 12 h, then marks the document `failed` (6 attempts). A command that gets no reply is re-sent by the same sweeper after 10 minutes. The operator's retry
   resets the attempt counter and **rebuilds the parties** (seller legal data, buyer tax data) from plans-svc's current values: the usual reason a document
   fails is a missing tax id; the lines, amounts, number and date stay frozen.
6. **Idempotent, and a legal number is never reused.** The document UUID is `uuidv5(tenant|kind|source id)` and travels as the UBL `cbc:UUID`, so an integrator that dedups on UUID
   (all Turkish ones do) answers "exists" for a repeat; adapters turn "exists" into a status fetch, never into a second document. `einvoice_issuances.document_id`
   is the primary key and `(tenant_id, environment, legal_number)` is unique. Legal numbers come from the integrator's series (the mock fabricates
   `MCK{year}{9 digits}`); Quotify's own invoice number (`INV-2026-000123`) is the UBL `cbc:ID` only for e-arşiv/Peppol where the sender numbers, and a note
   otherwise. A failed document keeps its UUID, so a retry can never produce a second legal document for the same invoice.
7. **Sandbox can only reach the mock.** A sandbox tenant's documents always go to the `mock` adapter, whatever account is stored; an account whose environment is
   `sandbox` with another provider is refused. A live account may not be the mock (except in demo mode, like `PAYMENTS_LIVE_PROVIDER=mockpsp`). Sandbox
   documents are numbered `SBX…` by the mock and their objects are tagged `environment=sandbox` for the bucket's 30-day lifecycle rule.
8. **Credentials.** `provider_accounts` pattern: the settings JSON (API key, base URL, webhook URL and secret, mode) is sealed with AES-256-GCM
   (`QUOTIFY_SECRETS_KEY`), bound to tenant|provider|environment. The API never returns them (only `has_credentials` and non-secret fields). They are never
   logged; adapters redact the `Authorization` header and error bodies are truncated before they are stored as `last_error`.
9. **Company legal data** live on the tenant (plans-svc, migration 00015): legal name, tax id (VKN/TCKN or VAT id), tax office, MERSIS number, trade registry
   number. They are also printed on the PDF. A customer gets `tax_id`, `tax_office` and `e_invoice_registered` (migration 00015). Without a seller tax id a
   live document fails with `einvoice.seller.incomplete`; without a buyer tax id a Turkish e-arşiv document uses the legal placeholder `11111111111`
   (TCKN of an unknown consumer, as GİB allows for e-arşiv below the limit) and a EU document fails with `einvoice.buyer.incomplete`.
10. **Document kind.** Turkey: buyer `e_invoice_registered` → `e-fatura` (TEMELFATURA profile), else `e-arsiv`. The `nilvera` adapter can check the registration
    (`needs buyer registration check` capability) when the flag is unset. Elsewhere: `peppol` (EN 16931 / BIS 3, customization id
    `urn:cen.eu:en16931:2017#compliant#urn:fdc:peppol.eu:2017:poid:3.0`). Credit notes: `iade` invoice type in UBL-TR (an `Invoice` with `InvoiceTypeCode` IADE),
    `CreditNote` document in Peppol.
11. **Money** is minor units end to end; the builder formats with the currency exponent and never uses floats. Rates are basis points; the rate of a
    `TAX` line is read from its label ("VAT 20%") and falls back to tax / base.
12. **Credit note lines** are the invoice's lines scaled to the credited fraction (largest remainder, so the sum is exact), keeping a reference to the invoice
    (`BillingReference`) with its legal number, which the worker looks up in `einvoice_issuances`; a credit note whose invoice is not issued yet replies
    `retryable` ("original not issued") and waits its backoff.

## Flow

```
Finalize / saga Paid / CreateCreditNote (billing-svc, one tx)
   └─ INSERT einvoice_documents(pending, request=IssueEInvoice) + outbox ─▶ RabbitMQ einvoice.issue
                                                                              │
payments-worker: handler  ◀───────────────────────────────────────────────────┘
   1. einvoice_issuances row for the document UUID? issued → reply the stored result (no provider call)
   2. resolve provider (sandbox → mock; live → sealed account), build UBL (pkg/einvoice), store UBL in S3
   3. provider.Issue → legal number, external id; fetch PDF → S3; record issuance; reply EInvoiceResult
   └─ reply ─▶ billing-svc.replies ─▶ einvoice_documents: issued | pending(+backoff) | failed
billing-svc sweeper: pending and due → re-send (attempt+1); no reply for 10 min → re-send
```

## API

Operator (console), `Authorization: Bearer`, under `/admin/v1/tenants/{tenant_id}`:

| Method and path | What |
|---|---|
| `GET /e-invoices?invoice_id=&credit_note_id=&status=&page_size=&page_token=` | list documents |
| `GET /e-invoices/{id}` | one document |
| `GET /e-invoices/{id}/files/{xml\|pdf}` | `{url, expires_at}`, a 5-minute presigned link |
| `POST /e-invoices/{id}/retry` | failed → pending, parties refreshed |
| `POST /e-invoices/{id}/cancel` | issued → cancelled where the provider allows (`einvoice.cancel.unsupported` otherwise) |
| `GET /e-invoice-accounts` | the tenant's accounts without secrets |
| `PUT /e-invoice-accounts/{environment}` | `{provider, mode, credentials:{…}}`; sandbox accepts `mock` only |
| `DELETE /e-invoice-accounts/{environment}` | remove |
| `PATCH /admin/v1/tenants/{id}` | now also `legal{…}` and `e_invoice_on` |

Tenant (API key): `GET /v1/e-invoices`, `GET /v1/e-invoices/{id}`, `GET /v1/e-invoices/{id}/files/{xml|pdf}`, `PUT /v1/customers/{external_id}/tax`
(`{tax_id, tax_office, e_invoice_registered}`). The webhook events `einvoice.issued` and `einvoice.failed` are not added (non-goal; the document list is the
interface).

Error keys: `einvoice.account.missing`, `einvoice.account.invalid`, `einvoice.account.sandbox_mock_only`, `einvoice.seller.incomplete`, `einvoice.buyer.incomplete`,
`einvoice.document.not_found`, `einvoice.document.not_failed`, `einvoice.document.not_issued`, `einvoice.cancel.unsupported`, `einvoice.file.not_ready`,
`einvoice.provider.rejected`, `einvoice.provider.unavailable`, `einvoice.setting.invalid`, `plans.customer.tax_invalid`.

## Tasks

- [x] Plan (this file)
- [ ] `pkg/einvoice`: types, `Provider`, `Capabilities`, registry, errors
- [ ] `pkg/einvoice/ubl`: UBL-TR 1.2 and EN 16931 / BIS 3 builders, golden files, structural checks
- [ ] Adapters `mock`, `nilvera` (UNVERIFIED against a sandbox), `webhook`, with `httptest` tests
- [ ] Protos: tenant legal data and `e_invoice_on`, customer tax data, `IssueEInvoice` command and `EInvoiceResult` reply, billing e-invoice RPCs; `make gen`
- [ ] plans-svc: migration 00015, tenant settings, customer tax RPC
- [ ] billing-svc: migration `einvoice_documents` (RLS), `einvoices` module (command/ query/ handler), snapshot, trigger on finalize/paid/credit note, replies, sweeper
- [ ] payments-worker: migration, sealed accounts, `einvoice.issue` consumer, S3 storage, HTTP admin for accounts
- [ ] Gateway routes + OpenAPI + contract test
- [ ] Console: settings form, status badge, retry and download on invoice and credit-note pages; six languages; tests
- [ ] Tests: unit, golden UBL, adapter httptest, integration (invoice finalized → document issued with the mock), console
- [ ] Docs: roadmap, error keys, data model, config, deploy env
- [ ] Follow-up (needs real accounts): run the UBL through the official UBL-TR and Peppol schematron; one full run against Nilvera's test portal
