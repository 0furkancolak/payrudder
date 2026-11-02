# Invoice and credit-note rules by country

Plan 05 ([`plans/05-customer-locale-tax-documents.md`](plans/05-customer-locale-tax-documents.md)), "Research first" step 1: what an invoice has to carry in the countries Payrudder's sellers and customers are in, how documents are numbered, in which language, what a credit note must say, and **what the PDF Payrudder draws does and does not print**. Written 2026-11-02 under the owner's instruction to proceed with defaults; **nothing here is legal or tax advice, and nothing here says a Payrudder PDF is a legal invoice in any country** (the Turkish legal e-document is the integrator's, plan 09).

## How to read the status column

| Status | Meaning |
|---|---|
| READ | The primary source (statute, regulation or the authority's own page) was fetched on 2026-11-02 and the rule is from that text. The URL is in the row. |
| SECONDARY | Only commentary (an accountant's page, a vendor, a forum) was found; the primary text was not read. A figure from there may be wrong. |
| NOT READ | Stated from general knowledge of the rule; no source was opened in this session. Treat as a pointer to where to look. |
| UNVERIFIED | Needs a lawyer or an accountant of that country before anything depends on it. Every row that is not READ is also UNVERIFIED. |

## What the PDF prints today (`services/invoice-worker/internal/render`)

Printed: the document title in the customer's language (INVOICE, FATURA, RECHNUNG, FACTURE, FACTURA), Payrudder's own number, the service period, the issue date (in the tenant's time zone), the seller's name, address lines, tax id (labelled by what the number is: VKN, TCKN, VAT ID, else Tax ID) and tax office, the customer's name, address lines, e-mail, tax id and tax office, one row per line (description, quantity with unit, amount; **tax is a line** of kind TAX whose description carries the seller's tax name and the rate, as billing wrote it), the subtotal before tax, the total, bank-transfer instructions when the invoice is paid by transfer, a note that the legal e-document is issued separately (Turkish seller with e-invoicing on), the credit carried to the customer's balance, and the sandbox watermark.

**Not printed, because Payrudder does not hold the data or has no engine:** a due date or payment terms; late-payment interest and recovery fee (France); the seller's legal form and share capital, company registry number (the e-invoice party carries a registry number, the PDF does not print it); the date of supply as its own line (only the period); the tax base and tax amount **by rate** as a summary (each TAX line shows its own amount); any reverse-charge, exemption (article), margin-scheme or small-business wording (no tax engine: [defaults](plans/05-customer-locale-tax-documents.md#defaults-not-agreed-2026-11-02)); and a **credit-note PDF**: none is drawn (a credit note exists as a record and as the integrator's e-document; plan 05 step 4 and plan 09 keep this open). Line descriptions stay in the language billing froze (English).

## The European baseline (applies to DE, FR, ES, and to a seller in any member state)

| Requirement | Rule | Source | Status |
|---|---|---|---|
| Content of an invoice | Directive 2006/112/EC, Article 226: date; a sequential number that identifies the invoice uniquely; the supplier's and the customer's VAT identification numbers in the cases it names; full names and addresses; quantity and nature of the goods or services; date of supply; taxable amount per rate or exemption, unit price net of VAT and any discount not in the price; the VAT rate and the amount payable; the reference to the exemption or to the reverse charge where they apply; and further items for the special schemes | <https://eur-lex.europa.eu/legal-content/EN/TXT/?uri=CELEX:32006L0112> (the fetch returned no text; the list is from the Directive as generally cited) | NOT READ, UNVERIFIED |
| Number | A sequence that makes the invoice unique; the Directive allows several series | as above | NOT READ |
| Credit note | A document that modifies an invoice "in a specific and unambiguous manner" refers to it (Article 219) | as above | NOT READ |
| Electronic invoice | Directive 2014/55/EU and EN 16931: the structured format that public buyers must accept; Peppol BIS Billing 3.0 is a profile of it (plan 09 validates against it) | plan 09 | READ there |

## Turkey (TR)

| Requirement | Rule | Source | Status |
|---|---|---|---|
| Who issues the legal document | An e-fatura (registered users) or e-arşiv invoice by an integrator or GİB's portal; the PDF Payrudder draws is a copy, and says so | plan 09; GİB e-Arşiv Technical Guide v1.19 | READ in plan 09 |
| Content of an invoice (VUK article 230) | Date, **series and sequence number**; the issuer's name or trade name and address; the customer's name or trade name and address; the nature, quantity, unit price and amount of the goods or services; the VAT. Whether the tax office and tax number of both parties are required in every case differs between the sources found | Commentary on VUK 230 (lexpera, muhasebetr, alomaliye); the statute at mevzuat.gov.tr was **not** read | SECONDARY, UNVERIFIED |
| Number format of an e-document | `cbc:ID` is **series + year + 9 digits** (`^[A-Z0-9]{3}20[0-9]{2}[0-9]{9}$`), the series registered with the integrator; Payrudder's number (`INV-2026-000123`) is a reference, not the legal number | GİB UBL-TR schematron, plan 09 | READ in plan 09 |
| Buyer's tax id | An individual without one is the placeholder `11111111111`; a business without one is refused (`einvoice.buyer.incomplete`) | `payments-worker/internal/einvoice`; whether GİB or the integrators also require the tax office is UNVERIFIED | code, UNVERIFIED |
| Language | Commonly cited: documents in Turkish, a foreign language beside it is allowed. The PDF prints Turkish for a Turkish customer | not read | NOT READ, UNVERIFIED |
| Credit note | An e-fatura is reversed by an **iade** invoice referring to the original (UBL-TR `IADE`, plan 09); an e-arşiv may be cancelled within the allowed period (Nilvera: when is not documented) | plan 09 | READ there |
| Retention | Commonly cited: five years (VUK 253) | not read | NOT READ, UNVERIFIED |

## Germany (DE)

| Requirement | Rule | Source | Status |
|---|---|---|---|
| Content (§ 14 Abs. 4 UStG) | Full name and address of both parties; the supplier's **tax number or VAT ID**; issue date; a **consecutive number unique to the issuer**; quantity and common trade description, or scope and kind of service; date of delivery or service; the consideration by tax rate and exemption and any agreed reduction; the tax rate and amount, or a note of the exemption; for § 14b cases a note on the recipient's retention duty; the word **"Gutschrift"** where the recipient or a third party issues the document | <https://www.gesetze-im-internet.de/ustg_1980/__14.html> | READ |
| Electronic invoice | An electronic invoice is issued in a **structured format** following the European standard (Directive 2014/55/EU), or one agreed between the parties; the recipient's consent is needed unless the law prescribes electronic invoicing (§ 14 Abs. 1). The obligation to **receive** and later to **send** structured invoices between businesses is phased in; the dates were not read | same | READ (the phase-in: NOT READ) |
| Number | Consecutive, unique (Nr. 4); the law does not require gap-free | same | READ |
| Language | The text read says nothing about the language | same | READ (silence) |
| Credit note | A **Gutschrift** under § 14 Abs. 2 is an invoice the *customer* issues for the supplier's supply, by prior agreement, labelled "Gutschrift". A seller's correction (a cancellation or credit invoice, "Stornorechnung") refers to the original; its form is not in the text read | same | READ / UNVERIFIED for the seller's own |
| Retention | § 14b: not read. Commonly cited: eight years from 2025 (ten before) | not read | NOT READ, UNVERIFIED |

## France (FR)

| Requirement | Rule | Source | Status |
|---|---|---|---|
| Seller | Company: name, **SIREN**, registered office, **legal form and share capital**; VAT number where applicable; a sole trader: the name with "EI" and the SIREN | <https://entreprendre.service-public.gouv.fr/vosdroits/F31808> | READ |
| Buyer | A business: its name; a private person: name and address; the buyer's VAT number when it is a VAT-registered business (not required up to 150 EUR excluding VAT) | same | READ |
| Invoice | Issue date and a **unique number from a continuous chronological sequence**; date of sale or of the service (or of the deposit); the buyer's order number if there is one; the description, quantities, **unit price excluding VAT** and VAT rate (or the exemption); total excluding and including VAT; price increases and directly linked discounts | same | READ |
| Payment | **Due date**; the terms of an early-payment discount or "Escompte pour paiement anticipé : néant"; the **late-payment penalty rate**; the **fixed recovery fee of 40 EUR** (business customers only) | same | READ |
| VAT mentions | Small-business exemption "TVA non applicable, art. 293 B du code général des impôts"; reverse charge "Auto-liquidation" | same | READ |
| Consumers | For certain goods, the existence and duration (at least two years) of the legal guarantee | same | READ |
| Coming | From 1 September 2026 e-invoicing adds: the customer's SIREN (business), the delivery address if it differs, the nature of the operation (goods or services) and the VAT-on-debits option statement | same | READ |
| Language, credit note (**avoir**), retention | Not on the page | same | NOT READ, UNVERIFIED |

## Spain (ES)

| Requirement | Rule | Source | Status |
|---|---|---|---|
| Content (Real Decreto 1619/2012, art. 6) | Number and, where used, **series, correlative within each series**; issue date; names of issuer and recipient; the issuer's **NIF** (the recipient's in the cases named: intra-EU exempt supplies, reverse charge); addresses; description, taxable base, **unit price excluding VAT** and discounts; the VAT rate and the VAT amount **shown separately**; the date of the operation if it differs; for exempt operations a reference to the provision or the statement that it is exempt; for reverse charge **"inversión del sujeto pasivo"**; "facturación por el destinatario" for recipient-issued invoices; separate bases by rate and by exempt or not | <https://www.boe.es/buscar/act.php?id=BOE-A-2012-14696> | READ |
| Simplified invoice (art. 4 and 7) | Up to 400 EUR including VAT (3,000 EUR for listed retail operations) or a rectifying invoice: number, date, issuer's NIF and name, kind of goods, the rate, the total; the recipient's NIF and the tax amount when a business asks | same | READ |
| Credit note (**factura rectificativa**, art. 15) | Required when the original does not meet art. 6 or 7, VAT was wrong, or the tax base is modified; a **new invoice** that identifies the rectified one, issued once the issuer knows the cause and within four years of accrual; it must meet art. 6 or 7 and show the rectification | same | READ |
| Language and currency (art. 12) | Invoices **may be issued in any language** (the authority may ask for a translation); any currency, but **the VAT amount must be in euros** | same | READ |
| Retention (art. 19) | For the period of the General Tax Law (Ley 58/2003); the period is not in the text read; records must stay legible and authentic and be accessible to the authority | same | READ / the period NOT READ |

## United Kingdom (GB)

| Requirement | Rule | Source | Status |
|---|---|---|---|
| VAT invoice | The VAT number and the VAT shown separately; the full list of particulars (unique number, time of supply, date of issue, names and addresses, description, rate and amounts, VAT in sterling) is in VAT Notice 700 section 16, **not in the text read** | <https://www.gov.uk/vat-record-keeping/vat-invoices> (partial), <https://www.gov.uk/guidance/vat-guide-notice-700> section 16 | SECONDARY / NOT READ, UNVERIFIED |
| Credit note (Notice 700, 18.2.3) | A valid credit note reflects a genuine mistake, overcharge or agreed reduction, **issued within 14 days of the refund**; it shows an identifying number and date, the supplier's name, address and VAT number, the customer's name and address, a description, quantity and amount for each description, the total credited excluding VAT, the **rate and amount of VAT credited**, and **the number and date of the original VAT invoice(s)** | <https://www.gov.uk/guidance/vat-guide-notice-700> | READ |
| Zero-rated supplies | An invoice for a zero-rated supply is not a "VAT invoice" (3.5) but is kept as an invoice | same | READ |
| Retention | Section 19 points to Notice 700/21; the period was not in the text read (commonly cited: six years) | same | NOT READ, UNVERIFIED |

## Saudi Arabia (SA)

| Requirement | Rule | Source | Status |
|---|---|---|---|
| E-invoicing | Standard (B2B) invoices are cleared by ZATCA, simplified ones reported; both in a structured (UBL) format with a QR code and a cryptographic stamp. **Payrudder does nothing of this**: no ZATCA adapter exists (plan 09: not modelled) | vendor commentary | SECONDARY, UNVERIFIED |
| Language | Several sources say the tax invoice must be **in Arabic**, other languages allowed beside it, the XML's technical elements may stay English; they disagree on the article (53 or 66 of the implementing regulations). The PDF **cannot print Arabic** (it falls back to the English layout), so a Saudi seller's PDF is not an invoice in the sense of that rule | ClearTax, ZATCA community forum; the regulation text was not read | SECONDARY, UNVERIFIED |

## United Arab Emirates (AE)

| Requirement | Rule | Source | Status |
|---|---|---|---|
| Tax invoice | Commentary on Cabinet Decision 52 of 2017 article 59: twelve particulars for a full tax invoice, fewer for a simplified one, and the words **"Tax Invoice"** on the document; a credit note is a **"Tax Credit Note"** (article 60) that references the original and the reason | vendor and accountant commentary; the Cabinet Decision was not read | SECONDARY, UNVERIFIED |
| Language | Sources disagree: Arabic or bilingual, or Arabic not mandatory; the authority may ask for a translation | as above | SECONDARY, UNVERIFIED |
| E-invoicing | A mandate has been announced (a 2027 date appears in commentary); Payrudder has no adapter | as above | SECONDARY, UNVERIFIED |

## United States (US)

There is **no federal invoice format**: sales-tax receipts and invoices are a state matter, and a B2B invoice is a commercial document. No source was opened. The PDF's layout and its English wording are used as they are; a seller that collects sales tax should have an accountant read its state's rules. NOT READ, UNVERIFIED.

## Numbering, in one table

| Where | The number | Payrudder |
|---|---|---|
| EU (Directive 2006/112/EC art. 226) | Unique, sequential (one or more series) | NOT READ. Payrudder's own number looks like `INV-YYYY-NNNNNN`; whether the sequence is gap-free was not checked here |
| DE | Consecutive, unique to the issuer; gap-free is not required (§ 14 Abs. 4 Nr. 4) | READ |
| FR | Unique, **continuous chronological sequence** | READ |
| ES | Correlative **within each series** (art. 6.1.a) | READ |
| TR | e-document: series (3 characters) + year + 9 digits, series registered with the integrator; the legal number is the integrator's | READ in plan 09; Payrudder's number is a reference |
| GB, SA, AE, US | A unique number (GB: Notice 700); the other formats were not read | UNVERIFIED |

## Credit-note wording, by the language of the document

What a Payrudder credit note document would have to be called, and what the sources above call it. **No credit-note PDF is drawn today**; this is the table to build one from.

| Language | Seller's credit note | Notes |
|---|---|---|
| en | Credit note (GB: "credit note", Notice 700); AE: "Tax Credit Note" (SECONDARY) | |
| tr | İade faturası (the e-document type `IADE`) | the legal document is the integrator's |
| de | Stornorechnung / Gutschrift. **"Gutschrift" is the legal label of a document the customer issues** (§ 14 Abs. 4 Nr. 10): a seller's credit note must not be called Gutschrift without an accountant's word | UNVERIFIED which word a seller should use |
| fr | Avoir (facture d'avoir) | the rule was not read |
| es | Factura rectificativa (art. 15 RD 1619/2012) | READ |
| ar | the English layout is drawn (no Arabic in the PDF) | |

## Number, date and currency formats

Decided and implemented in `pkg/localefmt` (the table the PDF, the hosted pages and the mails read), by the document's **language**, not by the customer's country: `1,234,567.89 EUR` (en), `1.234.567,89 EUR` (tr, de), `1 234 567,89 EUR` with narrow no-break spaces (fr), `1234,56` but `12.345,67` (es: grouping from five digits), the date in words (`2 Kasım 2026`, `2. November 2026`, `2 de noviembre de 2026`), the currency as its ISO code. Sources: the conventions of CLDR for each language, **not read here** (the values were taken from the existing code and checked by the golden files in `services/invoice-worker/internal/render/testdata`). A reader in another country who uses the language in another convention (Swiss German, Canadian French) is not modelled.

## What a person has to confirm before anything depends on this

1. For **every country the owner's customers are in**: an accountant of that country reads its section and the PDF (a sample in the customer's language) against it. Nothing in the repository says a Payrudder document is a valid invoice.
2. The Turkish **content list of VUK 230**, whether the tax office is required, the language rule and the retention: the statute, not commentary.
3. Whether a seller in **DE, FR or ES** can use the PDF as its invoice without a due date, payment terms, the legal form and capital (FR), the tax base by rate, and the reverse-charge or exemption wording. The defaults chosen say Payrudder records the tax the seller configures and does not word the treatment.
4. Which word a German seller's credit note carries; the French and Spanish credit-note layout; a credit-note PDF at all.
5. The Arabic requirement (SA, AE): whether a seller in those countries can use an English-layout PDF, and whether Arabic PDFs are wanted (needs a shaping library or a headless renderer: [plan 05](plans/05-customer-locale-tax-documents.md)).
