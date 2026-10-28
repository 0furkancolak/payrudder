# 05 — Customer language, taxpayer or individual, documents by country

## The request
List 1: (11) customers' languages and invoices and similar documents must be **dynamic by country and language**; (12) a customer being a **taxpayer (business) or an individual** must be handled.

## Where we are
- #240: `customers.locale` (en, tr, de, fr, es, ar) and `customers.customer_type` (individual, business) in the API, SDKs and console; mails (5 templates × 6 languages) chosen by the customer's locale; hosted pages use `?lang=` → the customer's locale → the browser.
- Unchanged: the customer type does not yet drive any behaviour.

## Gaps
- **The customer type changes nothing.** Not the e-invoice buyer rules (TCKN 11 digits for an individual, VKN 10 digits for a business in Turkey; VAT id and B2B reverse charge in the EU), not tax display, not which invoice template/series, not validation of the tax id, not the e-fatura vs e-arşiv choice (Turkey).
- Invoice **PDFs and the hosted invoice** are in the customer's language only partly (the page chrome; labels, number/date/currency formats, legal lines, tax breakdown wording are not locale-aware beyond what the six translations hold).
- Six languages are a fixed set; a country → language/format default (a customer in Brazil, Japan, Poland…) is not modelled. No locale fallback chain, no number/date format by locale on documents, no right-to-left layout check of the PDF.
- Tax: a per-country engine does not exist (the tenant has `tax_rates`; no VAT-by-place-of-supply, no OSS, no tax id validation by VIES, no Turkish KDV/stopaj specifics beyond what e-invoice adapters need).
- The Paddle adapter is merchant-of-record (Paddle does tax); the iyzico/Stripe path leaves tax to Payrudder: the rules per country are undefined.

## Research first
1. Write down, per country the owner's customers are in (start: TR, DE, FR, ES, SA/AE for Arabic, US/UK), what an invoice must carry (mandatory fields, tax id formats, numbering rules, language requirements, retention) — from the tax authority sources, with dates.
2. Turkish e-document rules: e-fatura vs e-arşiv, TCKN/VKN, invoice types (SATIŞ, İADE…), tax exemption codes; what the e-invoice adapters (09) require from the buyer record.
3. EU: B2B reverse charge, OSS thresholds, VAT id validation (VIES) — decide whether Payrudder calculates or only records tax.
4. Inventory today's invoice rendering (PDF in billing-svc, hosted invoice, mails, receipts) and list every string and format in them.

## Decisions needed
- Is Payrudder **responsible for calculating tax** (a tax engine), or does it record what the seller configures? (Largest scope decision of this plan.)
- Which countries and languages are in scope now; how a new language is added (process, not code).
- Validation strictness for tax ids (hard error vs warning).

## Work
1. Spec: per-country document rules table (decided scope only), stored in `docs/`.
2. Data model: customer tax profile (type, tax id kind and value, validated-at), country-driven defaults for language/currency/format.
3. Domain: tax id validators per kind (TCKN/VKN checksums, EU VAT format, optional VIES), buyer rules for e-invoice by type.
4. Rendering: locale-aware formatting (dates, numbers, money, addresses) in PDF, hosted invoice, mails; a message catalogue with a missing-key test; Arabic RTL PDF.
5. Console: customer form that adapts to type and country (shows VKN/TCKN vs VAT id; explains each).
6. Tests: golden PDFs per language; validator tables; e2e of the customer form per country.

## Done means
For each in-scope country a sample invoice (PDF + page + mail) exists in the customer's language and passes the checklist from step 1, reviewed by the owner (and, for Turkey, ideally by an accountant).

## Not verifiable here
Legal correctness (needs an accountant); e-invoice acceptance by the tax authority (needs the integrator's test environment, 09).

## Risks
Tax is where silent errors cost money: nothing ships without the owner's explicit scope and a reviewer.
