# 09 — E-invoice integrators

**Status (2026-11-02): the contract, its conformance suite and the validation of the generated documents are built; no new integrator, and nothing has run against a real integrator or authority.** The research of what each integrator publishes is below; an adapter is written only where the public documentation is complete enough, and for now that is Nilvera, which exists.

## The request
List 1 (3): add **other e-invoice integrators** (the owner said "başka fatura entegratörleri").

## Where we are
- **Integrator contract:** [`docs/einvoice-integrator-contract.md`](../einvoice-integrator-contract.md) writes down the `einvoice.Provider` interface and its error model (permanent, transient, not found, unsupported), idempotency on the document UUID, asynchronous confirmation with the legal number, credit notes and cancellation, retrieval, the sandbox lock and the rule about credentials. `pkg/einvoice/conformance` is the executable half: 11 checks over any adapter and a fake integrator. It runs against the `mock` provider, the `webhook` adapter (against `conformance.WebhookServer`, a fake that speaks the protocol of `pkg/einvoice/webhook/doc.go`, verifies the HMAC, and injects 4xx, 5xx, 429, slow answers and pending-then-issued) and **Nilvera** (against a fake that replays only the shapes of its public reference). The suite tests itself against adapters with one defect each.
- **Validation in CI:** `.github/workflows/einvoice-schematron.yml` runs `scripts/einvoice-validate.sh` on every change under `pkg/einvoice`: the Peppol documents against the UBL 2.1 XSD, the EN 16931 schematron and the Peppol BIS Billing 3.0 schematron, the UBL-TR documents against GİB's UBL-TR 1.2.1 XSD and schematron, for the committed golden files **and** for documents built fresh by `TestWriteSamples` (the goldens plus an exempt invoice, a zero-decimal currency and an e-fatura credit note). Locally: `make test-einvoice-rules`. Details under "Validation" below.
- Adapters: a simulator (`mock`, the sandbox), **Nilvera** (Turkish e-fatura/e-arşiv, never run against Nilvera's test environment, its public reference re-read on 2026-11-02, see below) and the generic **webhook** that posts each signed document to the tenant's own address. One integrator per environment (`einvoice_accounts` keyed by tenant and environment): several accounts and per-country choice are plan 10.
- The UBL builder (`pkg/einvoice/ubl`) now passes the official rules for Peppol BIS 3 and UBL-TR, after the fixes below.

## What the first run of the official rules found (2026-11-02)
The golden files that were merged "structurally checked" were run through the real rules and **failed**. Fixed in the builder, each with a test that fails without the fix (`TestPeppolRuleFixes`, `TestTRRuleFixes`) and goldens regenerated:

| Documents | Finding | Fix |
|---|---|---|
| Peppol | `PEPPOL-EN16931-R004`, `R007`: the specification and profile identifiers said `poid`; Peppol BIS 3 says `poacc` | `urn:fdc:peppol.eu:2017:poacc:billing:3.0` and `…:billing:01:1.0` |
| Peppol | `BR-CL-16`: payment means `ZZ` is not in UNCL4461; "mutually defined" is `ZZZ` | `ZZZ` |
| Peppol | `BR-CO-09`: the seller's VAT id had no country prefix | prefix with the country unless the id already starts with letters |
| Peppol | `PEPPOL-EN16931-R042`: the document-level discount had a base amount and no percentage | the base amount is left out |
| Peppol | `PEPPOL-EN16931-CL008`: the endpoint was the party's e-mail with scheme `EM`, which is not in Peppol's code list | `Party.EndpointScheme` and `Party.EndpointID` (the Peppol participant id) are written when set, nothing is invented when not |
| Peppol | an element `cbc:CountrySubdivisionName` does not exist in UBL (found by reading, no fixture had a region) | `cbc:CountrySubentity` |
| UBL-TR | the XSD wants `cac:Signature` before the seller | written (the signatory is the seller; the XAdES seal itself is the integrator's) |
| UBL-TR | the XSD wants the district (`cbc:CitySubdivisionName`) in every address | `Party.District`, written when set |
| UBL-TR | the XSD has no `cbc:PrepaidAmount` and no `cac:InvoicePeriod` on a line | both left out; `PayableAmount` is the total (a credit balance is a payment, not part of the document; UNVERIFIED with an accountant) |
| UBL-TR | schematron: a credit note's `cac:InvoiceDocumentReference` needs `cbc:DocumentTypeCode` `IADE` | written |
| UBL-TR | schematron: a TCKN party needs `cac:Person` with first and family name | the name is split at its last space; a single word has family name `-` (UNVERIFIED) |
| Peppol | `UBL-CR-005` (warning): a UBL invoice should not carry a UUID | **kept and accepted**: the UUID is the idempotency key and the webhook protocol promises it as `cbc:UUID`; a warning fails nothing |

The webhook adapter also failed two checks of the new suite and was fixed: it sent sandbox documents to the tenant's endpoint (every adapter but the mock must refuse them), and it passed an endpoint's `message` through unredacted although the endpoint knows the secret and may echo it. Nilvera failed the asynchronous check: its status answers carry no invoice number, so a document confirmed after `Issue` could never be completed; the adapter now reads `InvoiceNumber` from `GET {family}/{UUID}/Details` once the document is issued (the endpoint and field are documented; that `InvoiceNumber` is the legal number is still UNVERIFIED, as it already was for `Issue`).

**One documented gap remains and is allowed by `scripts/einvoice-known-failures.txt`** (it can only shrink; the run fails when the entry stops occurring): GİB's schematron requires `cbc:ID` in the legal format `^[A-Z0-9]{3}20[0-9]{2}[0-9]{9}$` (series, year, sequence), and the builder writes Payrudder's own number (`INV-2026-000123`). Nilvera's error catalogue points the same way (2064 series not active, 2065 series empty, 2100 series and year mismatch, 3009 series not defined for the company; **inferred, UNVERIFIED**): an uploaded XML is numbered by a series registered with the integrator. Who numbers is a decision (below).

## Validation
- **What runs:** `scripts/einvoice-validate.sh [peppol|tr] [dir]`. A document passes when the XSD accepts it and no assertion fails unless its flag is `warning` or `information` (ISO Schematron; GİB's rules carry no flag, so all are errors: an earlier draft of the script treated them as warnings and was silently green).
- **Artefacts, pinned and checked by sha256 before use** (downloaded into `~/.cache/payrudder/einvoice-validate`, never committed):

| Artefact | Version | sha256 |
|---|---|---|
| EN 16931 UBL validation artefacts (CEN TC434), `ConnectingEurope/eInvoicing-EN16931` release `validation-1.3.17` (2026-10-12), `en16931-ubl-1.3.17.zip`, the pre-compiled `xslt/EN16931-UBL-validation.xslt` | 1.3.17 | `bf02a0e3f6cbe692a4a28e70be7bdcaded5612b4169b543e13d6cb4f33c29e92` |
| Peppol BIS Billing 3.0 rules, `OpenPEPPOL/peppol-bis-invoice-3` tag `3.0.21` (2026-10-28), `rules/sch/PEPPOL-EN16931-UBL.sch` | 3.0.21 | `9616cfffb418f349b532c1e25d3c831b8af6a47f4f5607ca0df4b1336f48e5f3` |
| UBL 2.1 XSD, OASIS, `https://docs.oasis-open.org/ubl/os-UBL-2.1/UBL-2.1.zip` (only `xsdrt/` is used; 58 MB) | 2.1 | `60b80d76394a8a2add90723ecb8e0e2e9d826775de9749df37a72d60703f86ed` |
| GİB e-Fatura package, `https://ebelge.gib.gov.tr/dosyalar/kilavuzlar/e-FaturaPaketi%20(29).zip` (modified 2026-08-24; `schematron/UBL-TR_{Main,Common}_Schematron.xml`, `UBL-TR_Codelist.xml`) | "(29)" | `12901476cd424fd65c779a4fb44cee798f9921ce55b003f8a076c936ddf8b583` |
| GİB UBL-TR 1.2.1 package, `https://ebelge.gib.gov.tr/dosyalar/kilavuzlar/UBL-TR1.2.1_Paketi.zip` (modified 2026-01-12; `xsdrt/`, which differs from OASIS's) | 1.2.1 | `cb583941b8a8a239c59902c6bc455c0f75d48f2bb81d7d3fbe1ae827f981f7db` |
| Saxon-HE (Maven Central) | 12.9 | `8f3a9216a537367132293eacbba9df062eace8f8b16a184af59e2e4839d4cd41` |
| xmlresolver (Maven Central) | 5.3.3 | `1fe4d5b92f708dcdb82dbce12919e0171e6b5ca62c6dca6220483625098feb5f` |
| SchXslt (Maven Central; compiles ISO Schematron to XSLT; the publisher gives sha1 `daae9060a2b18b845fd15dfc1f238f9b68784d9b`, matched) | 1.10.1 | `4f4f21edab7b37f96ad59ae12a344d3510f1092ac46b6d81a4efa0120b73cb58` |

- **Licences (default, not agreed: download at CI time, never commit):** the CEN artefacts are EUPL-1.2 (the release notes and the `.sch` header say so), so committing them would be allowed; the Peppol `.sch` files carry "reproduced with permission from CEN" and the repository has no licence file, so redistribution is unclear; OASIS UBL is under OASIS's IPR policy (free to use, copy with notice); GİB publishes its packages for implementers with no licence text. Pinning by hash gives reproducibility without redistributing any of them.
- **Tooling:** Saxon-HE needs Java 11+ (GitHub's `ubuntu-latest` has it; the local run used Temurin 21), `xmllint` for the XSD, `curl`, `unzip`. The EN 16931 rules run from CEN's pre-compiled XSLT; the Peppol and UBL-TR schematron are compiled with SchXslt. GİB's schematron is written for the 2010 ISO skeleton (rules outside patterns, the `xs` prefix undeclared, XPath 2 in an unbound query language); `scripts/einvoice-tr-prep.xsl` repairs exactly those three things and changes no assertion. GİB's schematron takes a parameter `type`; e-arşiv documents are checked with `type=earchive`, as GİB's `History.txt` of 2017-04-28 says.
- **UBL-TR schematron is public** (the question left UNVERIFIED in the review addendum): GİB publishes it in the e-Fatura package and the XSD in the UBL-TR package on `ebelge.gib.gov.tr`. GitHub's runners **can** download from that host: the first run of the workflow (PR #290, 2026-11-02) fetched both packages and validated all 12 documents. A host that cannot reach it can set `EINVOICE_TR_OPTIONAL=1` to turn that download failure into a warning; CI does not.
- **The ExtensionContent placeholder:** UBL-TR carries the integrator's e-seal in `ext:ExtensionContent`, which the builder leaves empty; the XSD wants a child, so the script checks a copy with a placeholder. An unsealed document is not XSD-valid by design.
- **Not run:** the XSD of Peppol documents is OASIS's, not Peppol's own packaging; no Peppol access point or SMP lookup; no GİB test system (`test.efatura.gov.tr`).

## Integrator research (public sources only, read 2026-11-02)
"Complete enough" means: the public documentation gives auth, every call needed for issue, status, cancel (or says there is none), credit note, PDF and XML, and the errors, so an adapter can be built against an `httptest` fake that replays the documented shapes.

| Integrator | What is public | Complete enough? | What is missing |
|---|---|---|---|
| **Nilvera** | `https://developer.nilvera.com` (GitBook, an `llms.txt` index and one page per endpoint). Auth: `Authorization: Bearer <API key>` (OAuth2 reference token with scopes and expiry, made in the portal); test `https://apitest.nilvera.com`, live `https://api.nilvera.com`, separate keys and company data. Issue by UBL-TR XML: `POST /einvoice/Send/Xml` (query `Alias`, `TemplateUUID`; multipart `file`) and `POST /earchive/Send/Xml` (`TemplateUUID`), answer `{UUID, InvoiceNumber?}`; the same by JSON model or base64. Status: `GET /einvoice/Sale/{UUID}/Status` (`InvoiceStatus.Code` unknown/waiting/succeed/error, `Answer.AnswerCode`, `EnvelopeInfo`), `GET /earchive/Invoices/{UUID}/Status` (`StatusCode`, `ReportStatus`, `CancelStatus`); `…/Details` has `InvoiceNumber`; `…/pdf`, `…/xml` downloads; e-arşiv cancel `PUT /earchive/Invoices/{UUID}/Cancel` (no body); series API `/einvoice/Series`, `/earchive/Series`. Errors: `{Message, Errors:[{Code, Description, Detail}]}`, 409 for a duplicate (1001 ETTN, 1004, 1015), 422 for business rules (2015 schema/schematron). Test environment: yes. Idempotency: the document UUID (ETTN) is unique, a repeat is 409. Async: `waiting` → `succeed`, then the authority's answer. | **Yes, for what the adapter does.** The adapter exists (`pkg/einvoice/nilvera`); this reading confirmed its endpoints and fields, the error body and the duplicate codes, and closed the status-without-number gap via `/Details`. | Never run against the test environment. UNVERIFIED: that `InvoiceNumber` is the legal number; the content type of `/pdf`; the e-arşiv `/xml` path (the page titled "html" has samples that call `/xml`); when an e-arşiv can be cancelled (the page says nothing, 422 otherwise); no e-fatura cancel exists (an iade invoice reverses it); how the XML's `cbc:ID` must relate to a registered series (inferred only); whether a webhook/callback exists (none found in the e-fatura/e-arşiv index); rate limits (no 429 is listed, only 500/502/503/504). |
| **Foriba / Sovos** (Turkey) | `https://developer.sovos.com` and `https://api.fitbulut.com/servis/#/eFatura` are client-rendered portals; their text could not be read from here. Sovos's e-arşiv product page says e-arşiv is one-way (no answer from the buyer) and that the e-fatura exception codes apply. Community PHP SDKs (`unlembilisim/foribaapi`) show a SOAP interface with a web-service user and password and a test switch: **not** official. | **No.** | Official endpoint documentation, the test environment address and how credentials are issued (a Sovos contract or developer account). |
| **Logo** (e-Fatura, İşbaşı, ParaşütE-fatura) | No public API reference found; `developer.logo.com.tr` does not resolve; the products are sold with a customer contract. | **No.** | Documentation and a test account from Logo. |
| **Paraşüt** (accounting software with e-fatura / e-arşiv) | A full public OpenAPI at `https://apidocs.parasut.com/swagger.json` (v4.0.0): OAuth2 authorization code (`https://api.parasut.com/oauth/token`), `POST /{company_id}/e_invoices` and `…/e_archives` create the document **from an existing `sales_invoice`** (a relationship, no UBL), answer a trackable job (`GET …/trackable_jobs/{id}`: running/done/error), `GET …/e_invoice_inboxes` (is this VKN an e-fatura user), PDF downloads, e-arşiv `status` bounced/sent/printed/legalized and `cancellable_until`. | **The API is documented, but it is not an issuing transport.** There is no UBL upload or download, no cancel call, no idempotency key, and the number series are Paraşüt's. Using it means mirroring each customer, product and invoice into the seller's Paraşüt books: an accounting integration, a different product. Not built. | A decision that Payrudder should drive a seller's Paraşüt books; then a sandbox company. |
| **Uyumsoft**, **Mysoft** | No public API reference found; both are GİB-licensed özel entegratörler selling to customers under contract; only third-party wrappers appear. | **No.** | Their integration guides and test credentials. |
| **GİB directly** (e-arşiv portal, e-fatura) | The e-Arşiv Technical Guide v1.19 (October 2026, `https://ebelge.gib.gov.tr/dosyalar/kilavuzlar/e-Arsiv_Teknik_Kilavuzu_V.1.19.pdf`) and the e-Fatura packages. The portal (`earsivportal.efatura.gov.tr`) is a web application with **no documented API**; libraries that drive it automate its screens (unofficial, unsupported, not to be used for other people's tax documents). The authority's own web service is the e-Arşiv **report** service (SOAP: `sendDocumentFile`, `getBatchStatus`, `getUserList`; test WSDL `https://test.efatura.gov.tr/earsiv/services/EArsivWsPort?wsdl`) for taxpayers with their own systems and for approved özel entegratörler, signed with a mali mühür or NES, and entered only after GİB's written approval of a test run. | **No, and it is a licensing matter, not an adapter:** direct integration means Payrudder (or each seller) being an özel entegratör or a direct taxpayer system. | GİB approval, an e-seal, the e-Fatura Özel Entegrasyon Kılavuzu v1.7 and the Özel Entegratörleri Bilgi Sistemleri Denetimi Kılavuzu (public, listed on `ebelge.gib.gov.tr`, not read in depth), and a decision whether to become one. |

The generic **webhook** adapter stays the way into any integrator that has an API and a tenant who can wire it (Uyumsoft, Foriba, Logo…): the tenant's endpoint receives the signed UBL and talks to the integrator. The contract and `conformance.WebhookServer` now document that protocol by example.

**No new adapter was written**: no other integrator's public documentation is complete enough, and Paraşüt's complete documentation describes a different thing.

## Gaps (what is still thinner than it should be)
- **Nilvera has never been run against Nilvera's test environment.** The Nilvera items marked UNVERIFIED above can only be settled by it.
- **The documents lack data Payrudder does not hold:** the district of an address (UBL-TR XSD), the Peppol participant id of both parties (Peppol `R010`/`R020`), the legal-format number (above). `Party.District` and `Party.EndpointScheme`/`EndpointID` exist and the builder writes them, but the worker's `PartySnapshot` carries none, so **a real tenant's UBL-TR document is still XSD-invalid (no district) and a real Peppol document fails `R010`/`R020` (no participant id)** until the customer and seller data carry them (plan 05 owns the customer's data; the proto change is not in this plan).
- `ChooseType` sends every non-Turkish seller's document down the Peppol channel, but the only transport for Peppol is the webhook adapter: there is no access-point adapter.
- Only one real integrator; Turkey has many; other countries have their own regimes (Italy SDI, Poland KSeF, France PDP, Spain Veri*Factu, Saudi ZATCA, India GST e-invoice, Mexico CFDI, Brazil NF-e). None is modelled.
- One integrator per environment: several of the same kind and per-country choice are plan 10.
- The buyer's type (05) does not feed the document beyond TCKN versus VKN.

## Decisions needed (the owner's)
Markets and integrators in order; test accounts; **who numbers the documents** (a series registered with the integrator per tenant, with Payrudder's number kept as a reference, or Nilvera's JSON-model send, where the integrator numbers); who holds the legal responsibility for numbering and archiving (the integrator or Payrudder); whether Payrudder should become an özel entegratör.

## Defaults chosen (not agreed)
- Validate in CI against the published rules, artefacts pinned by sha256 and downloaded, never committed; a failing assertion is an error unless flagged `warning` or `information`.
- A Peppol document without a participant id is written without an endpoint rather than with a code Peppol rejects; the schematron then says so.
- UBL-TR: no `PrepaidAmount`, `PayableAmount` equals the total, no line period; TCKN names split at the last space.
- `UBL-CR-005` (UUID in the document) is accepted.
- A failed download of the GİB packages fails CI (strict); `EINVOICE_TR_OPTIONAL=1` is the local escape.
- No adapter without documentation that is complete enough; the webhook adapter is the answer for the rest.

## Work
1. ~~Contract doc and conformance tests~~ done (this plan's PR). Migrating Nilvera to it with its test environment run: the conformance part is done, the **run against Nilvera's test environment needs an account**.
2. ~~Schematron/XSD validation in CI for the generated documents~~ done; whether the GİB download works from GitHub is to be read from the first CI run.
3. Add integrators one at a time (separate plans `09a-…`), each with a sandbox transcript: none can start until an account or documentation exists (table above).
4. Per-country integrator choice via 10.
5. The data the documents need (district, participant ids, number series) and the decision on numbering.

## Done means
Per integrator: validation in CI, test-environment transcript, credit note and cancel flows, docs. For this plan as a whole: at least one real integrator's transcript, and the three data gaps closed.

## Not verifiable here
Acceptance by tax authorities or a Peppol access point; any integrator's test environment (all need accounts); that GİB keeps serving the pinned packages at those URLs (the sha256 check fails loudly if they change); the legal treatment of the choices above (prepaid amount, person names, numbering), which a Turkish accountant should read.

## Risks
Legal consequences of wrong documents; ship per country only with a reviewer. A green schematron run can be read as "the authority will accept it": it is not.

## Review addendum (2026-11-01)
- A Peppol BIS 3 / EN 16931 UBL builder already exists (`pkg/einvoice/ubl/build.go`, `TypePeppol`, testdata). The gap is **transport and validation**, not the document. Default: add a Peppol/EN 16931 schematron validation job to CI and an integrator contract with a mock conformance suite; real integrators wait for accounts. UBL-TR schematron availability is UNVERIFIED.
- Add the console, six-language strings and error keys per the definition of done in the README.
- Status of the addendum (2026-11-02): the schematron job and the contract with its mock conformance suite are done (above). **No API, console, locale or error-key change was needed or made:** nothing here adds an operation or a visible error; the console side of several integrators is [plan 10](10-integrations-page.md). The e-mail of the sandbox, a webhook event and a migration are not touched.
