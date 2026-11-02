# 05 — Customer language, taxpayer or individual, documents by country

## The request
List 1: (11) customers' languages and invoices and similar documents must be **dynamic by country and language**; (12) a customer being a **taxpayer (business) or an individual** must be handled.

## Status (2026-11-02)
Second step in PR `feat/customer-documents`: the invoice PDF in five languages with an embedded font, a tax identity on it, the customer's phone and tax id check on the server, and the e-invoice buyer rule by customer type. What it did and what it left is in [Done in the second step](#done-in-the-second-step-2026-11-02), the defaults it picked in [Defaults (not agreed)](#defaults-not-agreed-2026-11-02). The leftovers that needed no owner decision (the tenant's time zone on documents and pages, a real default-language setting, mail amounts and dates in the mail's language) are in [The leftovers](#the-leftovers-2026-11-02); the VAT id checksums were [PR #316](#vat-id-checksums-2026-11-02).

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

- `pkg/taxid` (Go) and `web/admin/src/shared/lib/taxid.ts`: the TCKN and VKN checksums, and EU VAT ids by shape plus (since 2026-11-02) the check digits of 26 countries: AT BE BG CY CZ DE DK EE EL ES FI FR HR HU IE IT LT LU LV MT NL PL PT RO SE SI (BG, CZ and LV for their legal-entity forms only). SK and XI stay shape-only, and the VIES lookup is **not** done (see "VAT id checksums (2026-11-02)"). Both sides share vectors produced and judged by python-stdnum, an independent implementation.
- The customer tax form hints when the number does not look like the kind of number the customer should have (a TCKN for an individual, a VKN for a business in Turkey; the shape of a VAT id for a business in the EU). It **never blocks** the save: strictness is the owner's decision below.
- Not done and waiting for the owner: whether Payrudder calculates tax or only records it; which countries and languages are in scope; hard error versus warning for a wrong id (the server still accepts 5 to 20 letters and digits); the e-invoice buyer rules by type (needs plan 09); per-country document rules (needs an accountant); locale-aware number and date formats on PDFs, pages and mails.

## VAT id checksums (2026-11-02)
This closes the "checksums of the VAT ids" item of the lists below (the bullet "`tax_id_validated_at`" and the "Not done" line of the second step still say "shape of a European VAT id" and "VAT id checksums": read them with this section).
- **API.** `taxid.ValidEUVATShape` (Go) and `validEuVatShape` (console) are unchanged: shape only. New `taxid.ValidEUVAT` and `validEuVat` = the shape plus, for a country with an algorithm, its check digits. `Valid(EUVAT, ...)`, `Detect` and `Recognised` (so `tax_id_validated_at` and the "VAT ID" label on the invoice PDF) and `Check`/`taxIdHint` use it: a business in the EU whose id fails the shape **or** the check digits gets the hint `eu_vat`. No new key, no proto or generated-file change, no new locale string.
- **Which countries.** Checked: AT BE BG CY CZ DE DK EE EL ES FI FR HR HU IE IT LT LU LV MT NL PL PT RO SE SI. Each is the public algorithm, ported from python-stdnum 2.2 with its extra rules: DE and HR ISO 7064 MOD 11,10; FR the old key (number mod 97), the new letter keys and the SIREN Luhn check; ES the DNI, NIE, K/L/M and CIF forms; IT Luhn plus the office range; NL the 11-proof or the mod 97-10 of the numbers issued since 2020; IE both the old form and the 2013 form with two letters. Both sides were also run once against about 115,000 numbers (random ones of each country's shape, valid ones found by search, one-character changes of them) judged by stdnum, with no difference; that script is not in the repo, the 144 vectors in the two test files are.
- **Shape-only, on purpose.** SK (stdnum's rule also accepts a person's birth number, which needs a date check) and XI (Northern Ireland uses the UK algorithm; it is not in the country hint either). Inside a checked country, the forms made of a person's birth date: CZ with 9 digits (not starting with 6) or 10, BG with 10 digits, the personal codes of LV (first digit 0 to 3). CZ checks the 8-digit legal entities and the 9-digit "special" numbers starting with 6; BG the 9-digit legal entities; LV the codes of legal persons.
- **Defaults (not agreed).** A wrong check digit is a **warning** (`eu_vat`), never a refusal, like the shape. A branch that is not verified is accepted on its shape, because a false warning on a real number is worse than a missed typo. The hint text is unchanged ("does not look like one"). A VAT id with a wrong check digit is no longer stamped with `tax_id_validated_at` when the tax data is saved, and the invoice PDF labels it "Tax ID", not "VAT ID".
- **What it does not mean.** These are local checks. A number that passes may not exist, may be deregistered, or may belong to someone else; a number that fails may be a rule stdnum has wrong or a form it does not know. No VIES, no network, no registry. The `tax_id_validated_at` already stored on a customer is not recomputed; `tax_id_warning` is, on every read, so an old VAT id with a wrong check digit now shows the warning while keeping its old validated time until the tax data is saved again.
- **Not verified.** Against a live registry (VIES or a national one): not run. Only the algorithms of python-stdnum 2.2 were compared; nobody checked them against the national tax authorities' own documents. The example `DE123456789` in the console's `taxid.hint.eu_vat` text (six locales) does not pass the German check digit itself; left as it was, a one-line change in each locale if the owner wants a number that passes.

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
- **The "tenant default" language** was the language of the seller's address country (TR to tr, DE and AT to de, FR to fr, ES to es; anything else, English) while a tenant had no language setting. **Replaced (2026-11-02) by a real setting, see [the leftovers](#the-leftovers-2026-11-02).**
- Money is the number followed by the **ISO currency code**, as before (no symbols table). Dates are written out in words, which no locale can misread.
- Phone: international form only, malformed refused (the tax id warns, the phone refuses: a bad phone breaks the provider call that needs it).
- `tax_id_validated_at` means "passed the local check", set on every save of a recognised id.
- A business e-arşiv buyer without a tax id is refused. **UNVERIFIED**: whether GİB or the integrators also require the tax office, and whether they reject a business under the consumer placeholder; the refusal is the cautious reading, and the office is sent as entered.

## The leftovers (2026-11-02)

Everything the second step left in "Not done" that needs no owner decision, as **defaults (not agreed)**. Branch `feat/locale-leftovers` (stacked on `feat/billing-money-rules`).

- **The time zone of the tenant dates the documents.** `InvoiceSnapshot.time_zone` (frozen with the draft in the invoice snapshot as `time_zone`, [ADR 0028](../adr/0028-money-rules-credit-note-tax-surplus-trial-year.md)) is what the PDF's issue date is written in (`invoice-worker/internal/render`, which now embeds `pkg/tzdata`: a distroless image has no zones and would have printed UTC without saying so), and `GetHostedInvoiceResponse.time_zone` / `GetPortalResponse.time_zone` what every day on the hosted invoice, the receipt and the portal is written in (`pageBase.Day`; the portal of a customer with no invoice yet takes the tenant's own zone). An invoice made before the zone was frozen is UTC, as before. The service-period lines ("Base fee - pro - 2026-10-12 to 2026-11-01") are text billing froze in the tenant's zone and are untouched. **Left in UTC on purpose:** the withdrawal pages (they label their times UTC) and the PDF's creation date (a machine value).
- **A real default-language setting.** `tenants.default_language` (plans-svc migration `00017`; en, tr, de, fr, es, ar, or empty for English) replaces the country map in `billing-svc/internal/rating/inputs/snapshot.go`. API: `Tenant.default_language`, `PUT /app/v1/apps/{id}/billing-profile` takes `default_language` (the owner's billing profile; left out keeps the stored one) and `PATCH /admin/v1/tenants/{id}` the same for the operator. **The migration gives every tenant that exists what the old map gave it** (TR tr, DE and AT de, FR fr, ES es, else empty), so no tenant's documents change language the day the setting appears; a new tenant starts with English and the console's billing-profile form has the select. The language of a customer who has none is now also the language of their **mails** (it was English before: the invoice mails, the trial, price-change, renewal, withdrawal and agreement mails take the customer's language, else the tenant's, else English), which is a change for a tenant that had a country-map language but sent English mail.
- **Mail amounts and dates in the mail's language** (`pkg/localefmt`, the table the PDF and the pages read): the invoice mails' `amount` and `period` (billing-svc `mailData`: `1,234.56` / `1.234,56`, `October 2026` / `Ekim 2026`), and in the notifier `trial_end_date`, `trial_end`, `effective_date`, `new_renewal`, `renewal_date`, `price`, `received_at` and `accepted_at` (`2 November 2026`, `2 Kasım 2026`; they were `2 Nov 2026` in every language). The time of day and the zone name stay as they were. **Not changed:** the numbers of `limit_exceeded.v1` (`used`, `limit`: quantities of a meter) and its `period`.

## UNVERIFIED and not done
- **Legal correctness of every printed word.** The titles (`FATURA` on a document that is not itself the e-Fatura or e-Arşiv invoice), the tax labels and the e-document note have not been read by an accountant, and the country rules of step 1 of "Research first" were not collected. Nothing says a Payrudder PDF is a legal invoice in any country.
- **Line descriptions are not translated.** `Base fee`, `Usage - meter - ...`, `Launch discount` are text billing-svc froze in English (and the plan's own names are the seller's). A translated line needs the line to be stored as a kind with parameters; a Turkish invoice still has English line names.
- Not done: ~~the hosted invoice page and the mails do not use these number and date formats~~ (done, see the leftovers); VIES (the VAT id checksums are done, see "VAT id checksums"); invoice number series by country (the legal number is the integrator's); a country to language and format default beyond the five countries above; ~~the customer form that adapts to type and country (it hints, it does not change shape); e2e of the form per country~~ (done, see [the customer form](#the-customer-form-2026-11-02)).
- **Also unverified for the leftovers:** a mail or a page read by a native speaker of tr, de, fr, es or ar (the console strings for the setting were written by the model), the PDF opened in a viewer with a non-UTC date, and the migration on a database with real tenants (tested on seeded rows only).
- Not run: GİB or integrator acceptance (plan 09 needs an integrator test environment), real mail with a PDF attached, a PDF opened in Acrobat or a printer. Looked at: pages of the five languages and the Arabic fallback converted with `pdftoppm` and text read back with `pdftotext` (poppler), by eye.

## The customer form (2026-11-02)

The new-customer sheet and the tax-data sheet now follow the customer's country and type (branch `feat/console-dx-3`). `web/admin/src/shared/lib/customerTax.ts` is the one table (`taxFormRules`, with its unit tests); `CustomerTaxFields.vue` draws it; the number checks stay `shared/lib/taxid.ts`, the mirror of `pkg/taxid` (TCKN, VKN, EU VAT ids with check digits), which only *hint*.

| Country, type | Tax id | Tax office | e-fatura box | Label |
|---|---|---|---|---|
| TR, individual | optional | not asked | yes | TCKN (11 digits) |
| TR, business | **required** | optional | yes | VKN (10 digits) |
| TR, type not set | optional | optional | yes | VKN or TCKN, and "choose the type above" |
| EU country, business | optional | not asked | no | VAT number; shape and check digits hinted |
| GB, business | optional (not checked) | not asked | no | VAT number |
| US, business | optional (not checked) | not asked | no | Tax ID (EIN) |
| any other country, business or type not set | optional | not asked | no | Tax ID |
| any country but TR, individual | **not asked** | not asked | no | none |
| no country | optional | optional | yes | the generic label (nothing is taken away) |

A field that is not asked for stays on screen while it holds a value, so it can be seen and cleared. The postal code of the address is called what its country calls it (postal code, ZIP code, postcode), shows an example and says when it does not look like one there (`postalCodeLooksOff`, as the seller address already did); it is accepted all the same. The create call has no tax fields, so the sheet saves them with `PUT /v1/customers/{id}/tax` after it; if that call fails the sheet stays, says the customer exists and does not offer "Create" again. Tests: `customerTax.test.ts`, `e2e-mock/customer-form.spec.ts` (TR, DE, US, GB, the failed tax call), `tax-id-hint.spec.ts`; six locales (`einvoice.customer.labels`, `.about`, `billing.customers.taxNotSaved`).

Defaults taken (not agreed): **the Turkish business VKN is required in the form** because payments-worker refuses an e-arşiv to a business without a tax id; nothing else is required, and the server does not require it (an API caller can still create the customer without it). A tax *office* is never required: whether GİB or the integrators demand it is UNVERIFIED (see the e-invoice buyer rule above). An individual outside Turkey is not asked for a tax id at all (a personal number is not something to collect). US and UK numbers are not validated (no rule is in `pkg/taxid`); adding one is a table row in both languages with shared vectors.

Not verified: the strings in German, French, Spanish, Turkish and Arabic were written by the model and read by nobody; the form was exercised against the mocked gateway only.
