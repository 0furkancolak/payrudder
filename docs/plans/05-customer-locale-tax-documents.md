# 05 — Customer language, taxpayer or individual, documents by country

## The request
List 1: (11) customers' languages and invoices and similar documents must be **dynamic by country and language**; (12) a customer being a **taxpayer (business) or an individual** must be handled.

## Status (2026-11-02)
Second step in PR `feat/customer-documents`: the invoice PDF in five languages with an embedded font, a tax identity on it, the customer's phone and tax id check on the server, and the e-invoice buyer rule by customer type. What it did and what it left is in [Done in the second step](#done-in-the-second-step-2026-11-02), the defaults it picked in [Defaults (not agreed)](#defaults-not-agreed-2026-11-02).

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

## Done so far (2026-10-29)

- `pkg/taxid` (Go) and `web/admin/src/shared/lib/taxid.ts`: the TCKN and VKN checksums and the shape of EU VAT ids (checksums of the VAT ids and the VIES lookup are **not** done). Both sides share vectors produced and judged by python-stdnum, an independent implementation.
- The customer tax form hints when the number does not look like the kind of number the customer should have (a TCKN for an individual, a VKN for a business in Turkey; the shape of a VAT id for a business in the EU). It **never blocks** the save: strictness is the owner's decision below.
- Not done and waiting for the owner: whether Payrudder calculates tax or only records it; which countries and languages are in scope; hard error versus warning for a wrong id (the server still accepts 5 to 20 letters and digits); the e-invoice buyer rules by type (needs plan 09); per-country document rules (needs an accountant); locale-aware number and date formats on PDFs, pages and mails.

## Review addendum (2026-11-01)
- **Corrections:** six hosted templates, not five. The invoice PDF is rendered by `services/invoice-worker/internal/render/render.go` and is English only (core Helvetica with cp1252 translation); `InvoiceSnapshot` and `RenderInvoicePdf` carry no locale. `pkg/taxid` has no Go importer yet; the server's `ValidateTaxID` (`domain/einvoice.go`) validates the **seller's** legal info.
- **Already exists:** customer `tax_id`, `tax_office`, `e_invoice_registered`, `locale`, `customer_type` (`resources.proto`). Missing: validated-at, a phone field (plan 07 needs one for GSM), and use of `customer_type` in `payments-worker/internal/einvoice` (the buyer falls back through `consumerTaxID`).
- **Design choice needed (default):** the PDF library cannot shape Arabic. Default: ship PDFs in en, tr, de, fr, es with an embedded Unicode font; Arabic PDFs fall back to the English layout with Arabic-Indic digits off, noted in the plan. This is a default, not agreed.
- **Tax engine:** `docs/phases/ROADMAP.md` says a tax engine is "not built and not planned"; the default for this plan is **no engine**: tax is a rate per plan/country configured by the seller, as today.

## Done in the second step (2026-11-02)

**Customer** (API, proto, four SDKs, console)
- `phone`: a mobile number in international form, stored as E.164 (`+905321234567`). `pkg/phone` and `web/admin/src/shared/lib/phone.ts` read the same cases (spaces, dots, dashes and brackets dropped; a leading `+` or `00`; 7 to 15 digits). A national spelling (`0532 123 45 67`) is **refused** (`INVALID_ARGUMENT`, field `phone`): without a country it cannot be read, and plan 07 needs a number iyzico accepts. On `PUT /customers/{id}/contact` an unset phone keeps the stored one and an empty string clears it (`optional string phone` in the proto); a column check keeps the table to E.164 too.
- `tax_id_validated_at`: set by `PUT /customers/{id}/tax` when the id passes `pkg/taxid` (a TCKN or VKN checksum, the shape of a European VAT id); empty otherwise. It is a **local check, not a registry lookup**: no VIES, no GİB query.
- `tax_id_warning`: the key of what is odd about the stored id for this customer's country and type (`tr_individual`, `tr_business`, `tr_any`, `tr_is_tckn`, `tr_is_vkn`, `eu_vat`; empty = nothing). The same rules and keys as the console's form hint (`pkg/taxid.Check`, with the same vectors as `taxid.test.ts`). Worked out on every read from the stored id, country and type, so it follows a change of any of them. **A wrong id is stored and warned about, never refused** (the old 5 to 20 letters and digits shape check stays).
- Console: a mobile number field in the new-customer and contact sheets (checked as typed, the save waits for a good one), the number and a "does not look right" line in the customer's details.

**Invoice PDF** (`services/invoice-worker/internal/render`)
- Languages en, tr, de, fr, es, picked by the customer's locale, else the language of the seller's country, else English. One Go map of words per language (`layouts.go`), a test that every language has every key and that every character is in the font.
- Noto Sans Regular and Bold (SIL OFL 1.1; licence file next to the fonts, line in `NOTICE`) embedded with `go:embed`: **+1.26 MiB in the binary** (37.56 MB to 38.89 MB), 24 KB for a one-page PDF. The Turkish `ğ ş ı İ` print; a character the font lacks (CJK, Arabic) prints as `?`, as before.
- Numbers, money and dates by the language's own conventions (`locale.go`): `1,234,567.89 EUR` (en), `1.234.567,89 EUR` (tr, de), `1 234 567,89 EUR` with narrow no-break spaces (fr), `1234,56` but `12.345,67` (es, grouping from five digits), quantities too (`1.234,500 GB-h` in tr: the same figure read as 1.234 in a Turkish document would be a hazard), the issue date as `2 Kasım 2026`, `2. November 2026`, `2 de noviembre de 2026`, the period `2026-10` as `Ekim 2026`. Golden tests per language (`testdata/invoice_<lang>.txt`; `go test -update` rewrites them, read the diff).
- **Arabic customers get the English layout** (fpdf does not shape Arabic or lay it out right to left). The language asked for and the layout drawn are recorded in the PDF's `Keywords` (`locale=ar layout=en`).
- The snapshot (`InvoiceSnapshot` in the proto, frozen by billing-svc when the draft is made) carries `locale`, `default_locale`, `customer_type`, and the tax id, tax office and country of both parties, whether or not the tenant e-invoices. Invoices drafted before this change have none of it and render in English as before.
- Tax identity lines under each party: the id labelled for what it is (a Turkish VKN or TCKN, a European VAT id, else "Tax ID") and the tax office. The label comes from `taxid.Detect`, not from the country.
- Legal wording: **only** for a Turkish seller whose tenant e-invoices (`e_invoice_on` finalized or paid) a note that the legal e-Fatura or e-Arşiv document is issued separately by the integrator with its own number. Nothing for the EU beyond the VAT-id labels: **no reverse-charge or OSS wording, because there is no tax engine and the lines do not say which treatment applied**.

**E-invoice buyer by customer type** (`payments-worker/internal/einvoice`)
- `customer_type` reaches the document. An e-arşiv buyer **without a tax id**: an individual (or a customer whose type nobody entered, as before) is the placeholder consumer `11111111111` that the code already used; a **business is refused** with `einvoice.buyer.incomplete` (`buyer.tax_id: ...`, not retried) and never given the placeholder. The sandbox follows the same rule (its default fills the placeholder for individuals only). E-fatura and Peppol are untouched.

## Defaults (not agreed) (2026-11-02)
- **No tax engine.** Tax is a rate the seller configures per plan, as today (`docs/phases/ROADMAP.md`: not built and not planned).
- **Five PDF languages; Arabic prints in the English layout.** Adding a language is a new `layout` in `layouts.go` plus its golden file; the font must cover its script (Noto Sans: Latin, Greek, Cyrillic).
- **The "tenant default" language is the language of the seller's address country** (TR to tr, DE and AT to de, FR to fr, ES to es; anything else, English), because a tenant has no language setting. **This changes what exists**: a Turkish seller's customer with no language set now gets a Turkish PDF where it got an English one. The seller can pin a customer to English in the contact sheet. A real tenant setting would replace the map in `billing-svc/internal/rating/inputs/snapshot.go`.
- Money is the number followed by the **ISO currency code**, as before (no symbols table). Dates are written out in words, which no locale can misread.
- Phone: international form only, malformed refused (the tax id warns, the phone refuses: a bad phone breaks the provider call that needs it).
- `tax_id_validated_at` means "passed the local check", set on every save of a recognised id.
- A business e-arşiv buyer without a tax id is refused. **UNVERIFIED**: whether GİB or the integrators also require the tax office, and whether they reject a business under the consumer placeholder; the refusal is the cautious reading, and the office is sent as entered.

## UNVERIFIED and not done
- **Legal correctness of every printed word.** The titles (`FATURA` on a document that is not itself the e-Fatura or e-Arşiv invoice), the tax labels and the e-document note have not been read by an accountant, and the country rules of step 1 of "Research first" were not collected. Nothing says a Payrudder PDF is a legal invoice in any country.
- **Line descriptions are not translated.** `Base fee`, `Usage - meter - ...`, `Launch discount` are text billing-svc froze in English (and the plan's own names are the seller's). A translated line needs the line to be stored as a kind with parameters; a Turkish invoice still has English line names.
- Not done: the hosted invoice page and the mails do not use these number and date formats; VIES and VAT id checksums; invoice number series by country (the legal number is the integrator's); a country to language and format default beyond the five countries above; the customer form that adapts to type and country (it hints, it does not change shape); e2e of the form per country.
- Not run: GİB or integrator acceptance (plan 09 needs an integrator test environment), real mail with a PDF attached, a PDF opened in Acrobat or a printer. Looked at: pages of the five languages and the Arabic fallback converted with `pdftoppm` and text read back with `pdftotext` (poppler), by eye.
