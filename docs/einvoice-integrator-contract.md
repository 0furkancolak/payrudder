# The e-invoice integrator contract

What an e-invoice integrator adapter in `pkg/einvoice/<name>` must do, so that a new integrator is an implementation of a checklist and not a new design. Written for plan 09 ([`plans/09-einvoice-integrators.md`](plans/09-einvoice-integrators.md)); the executable half is `pkg/einvoice/conformance`, and the check of the documents themselves is `scripts/einvoice-validate.sh`. The twin for payment providers is [payment-provider-contract.md](payment-provider-contract.md).

## The path of a document

`payments-worker` (`services/payments-worker/internal/einvoice`) maps the invoice or credit-note snapshot of an `IssueEInvoice` command to an `einvoice.Document`, chooses the legal channel (`DocumentType`: `e-fatura`, `e-arsiv`, `peppol`), has `pkg/einvoice/ubl` render it as UBL 2.1 and hands both to the tenant's adapter. It records the answer in `einvoice_issuances` (the idempotency record, one row per document UUID) and stores the UBL and the PDF. Sandbox tenants always use the `mock` adapter (ADR 0011).

## The interface

`einvoice.Provider`: `Capabilities`, `Issue`, `Status`, `Cancel`, `FetchPDF`, `FetchUBL`; `einvoice.BuyerChecker` is optional (Turkey: is this tax id an e-fatura user, and under which mailbox alias). An adapter is registered by name in an `einvoice.Registry` with a `Factory` that builds it from the tenant's sealed settings (a JSON object holding the credentials and options).

## What every adapter does

1. **Errors have exactly four meanings.** `*einvoice.PermanentError{Code, Message}` retrying will not change the answer: validation failed, the credentials are wrong, the tax id is unknown; the worker marks the document failed and shows `Code: Message` to the operator. `*einvoice.TransientError` the integrator could not be reached or said "try later" (timeout, connection error, 408, 429, 5xx); the worker retries with backoff. `einvoice.ErrNotFound` the integrator does not know the document. `einvoice.ErrUnsupported` the adapter cannot do this (cancel where the law forbids it, a PDF it does not render). Anything else is treated as transient. A **cancelled context is never a `PermanentError`**: that would fail a document for good because a worker shut down.
2. **Issue is idempotent on `Document.UUID`.** The UUID is also the UBL's `cbc:UUID` (the ETTN in Turkey). Repeating `Issue` for a UUID returns the existing document's result and never creates a second legal document; an integrator that answers "already exists" (Nilvera: 409) is asked for the status instead. The repeat must not move an issued document backwards.
3. **The answer has a shape.** After a successful `Issue`: `Ref{ExternalID, UUID, Type}` filled in (`UUID` and `Type` are the document's), `State` `pending` or `issued` (a refusal is a `PermanentError`, never `StateRejected` in a result), and `LegalNumber` set when `issued`.
4. **Asynchronous confirmation.** An integrator that confirms later answers `pending`. `Status` then reports `pending` until the authority has answered and `issued` after it, **with the legal number**: the worker does not complete a document without one, and it finds the number through `Status` (or the repeated `Issue`). An adapter whose status call carries no number has to ask another call for it (Nilvera: `GET {family}/{UUID}/Details`).
5. **Credit notes and cancellation.** A credit note is a `Document` with `Kind = KindCreditNote` and a `Reference` to the **legal number** of the original (the worker fills it from `einvoice_issuances`); it is issued like an invoice. `Cancel` cancels where the law allows it (Turkey: an e-arşiv within the allowed period; an e-fatura is reversed by a credit note, so `ErrUnsupported`). After a cancel, `Status` reads `cancelled`. `Capabilities.CanCancel` false means `ErrUnsupported` always.
6. **Retrieval.** `FetchPDF` returns a PDF (`%PDF-`) when `Capabilities.ReturnsPDF`, `ErrUnsupported` otherwise; `FetchUBL` returns the document the authority holds (sealed by the integrator), `ErrUnsupported` if the integrator does not give it back. Both answer `ErrNotFound` for a document the integrator does not know.
7. **Sandbox documents never leave.** `Document.Sandbox` is refused by every adapter but `mock` with a `PermanentError` (`Code: "sandbox"`) **before** any request is sent. The worker already routes sandbox tenants to the mock; this is the second lock.
8. **No credential in any error or message.** An integrator's own text may repeat the key it was sent; `Message` and `StatusResult.Detail` are shown to the operator and logged, so the adapter redacts its credential from them and truncates them. Errors of `net/http` carry the URL (which may carry a token): report only the kind of failure.
9. **A type the adapter does not send is a `PermanentError`** (`Capabilities.Types` is the list), never a silent conversion.
10. **Settings are validated when the adapter is built**, and a `base_url` goes through `safehttp.ValidateURL`: a tenant's settings must not make Payrudder call private addresses. Options that loosen that (`AllowPrivate`) are set by the deployment, never by a tenant.
11. **Nothing is invented.** Every call, field and error code is read from the integrator's current documentation (URL and date in the code comment and in the plan's research table); what the documentation does not settle is marked `UNVERIFIED` and the code does not act on a guess. An adapter that has never been run against the integrator's test environment says so in its package comment.

## The conformance suite

`conformance.Run(t, conformance.Harness{...})` runs these checks against an adapter and a **fake integrator** that speaks the adapter's wire format:

| Check | What it shows |
|---|---|
| `capabilities` | a name, at least one valid type, `Supports` agrees with the list |
| `issue-<type>` | for each type the adapter sends: the answer's shape (3), the repeat is the same document and not a second one at the integrator (2), `Status` agrees with `Issue` |
| `issue-concurrent-is-one-document` | eight callers issuing one UUID at the same moment leave **one** document at the integrator and are all told about it (2); needs the fake's `Created` |
| `credit-note` | for each type: a credit note (a `Reference` to the original's legal number) is a document of its own, with the shape of any answer, one more document at the integrator, never the original again (5) |
| `cancel-unknown-is-not-found` | `Cancel` of a document nobody issued is `ErrNotFound` (or `ErrUnsupported`), never a success (1, 5) |
| `issue-unsupported-type` | a type outside `Capabilities.Types` is a `PermanentError` (9) |
| `status-unknown-is-not-found` | `ErrNotFound` for a UUID nobody issued, also for `FetchPDF` |
| `async-confirmation` | pending, then issued with a legal number after the integrator confirms (4) |
| `cancel` | `CanCancel`, `ErrUnsupported` and the `cancelled` state agree (5) |
| `retrieve` | a PDF is a PDF, the UBL is XML, otherwise `ErrUnsupported` (6) |
| `errors-permanent-and-transient` | 400, 401, 403, 422 are permanent; 408, 429, 500, 502, 503 are transient (1) |
| `errors-never-leak-credentials` | the integrator's error text repeats the key; no error carries it (8) |
| `sandbox-documents-refused` | refused before any request, except by the mock (7) |
| `cancelled-context` | a cancelled call is an error but not a `PermanentError` (1) |

The `Harness` is what an adapter's `conformance_test.go` supplies: `New` (a provider wired to a fresh fake), and, for what the fake can do, `Fail(status)`, `SetPending`/`Settle`, `Created` and `Requests`. A nil function skips the checks that need it (the mock has no wire). The suite tests itself: `conformance_test.go` feeds it adapters with one defect each and requires that the matching check fails.

| Adapter | Fake | Test |
|---|---|---|
| `mock` | none (in memory) | `pkg/einvoice/mock/conformance_test.go` |
| `webhook` | `conformance.WebhookServer`: speaks the protocol of `pkg/einvoice/webhook/doc.go`, verifies the HMAC signature, is idempotent on the UUID, and injects 4xx, 5xx, 429, slow answers and pending-then-issued | `pkg/einvoice/webhook/conformance_test.go` |
| `nilvera` | a Nilvera that replays **only** the shapes read from its public reference (`pkg/einvoice/nilvera/conformance_test.go`) | the same file |

A pass says the adapter honours the contract against the shapes the integrator's documentation describes. It does not say the integrator answers so: for that each adapter needs a transcript of a run against the integrator's test environment, which needs an account, and **none exists yet**.

## The documents

`pkg/einvoice/ubl` renders the UBL. `scripts/einvoice-validate.sh` (CI: `.github/workflows/einvoice-schematron.yml`, on every change under `pkg/einvoice`) builds sample documents and the committed golden files and checks them against:

- Peppol documents: the UBL 2.1 XSD (OASIS), the EN 16931 schematron (CEN, `eInvoicing-EN16931` release `validation-1.3.17`) and the Peppol BIS Billing 3.0 schematron (OpenPEPPOL `peppol-bis-invoice-3` tag `3.0.21`);
- UBL-TR documents (e-fatura, e-arşiv): GİB's UBL-TR 1.2.1 XSD and GİB's UBL-TR schematron, from its public e-Fatura and UBL-TR packages.

A document fails when the XSD rejects it or a schematron assertion fails without the flag `warning` or `information` (GİB's rules carry no flag, so all of them are errors). Artefacts are downloaded into a cache, pinned by version and sha256, and refused on a mismatch; nothing official is committed. The failures that are documented gaps rather than defects are in `scripts/einvoice-known-failures.txt`; a line there that no longer occurs fails the run, so the list can only shrink. What passes is **not** acceptance by a tax authority or a Peppol access point.

## Adding an integrator

The questions to put to an integrator **before** this list, each tied to the check that proves the answer, are in [einvoice-integrator-checklist.md](einvoice-integrator-checklist.md).

1. Read its public documentation and write the research row in plan 09 (auth, the calls for issue, status, cancel, credit note, PDF, XML, test environment, who owns the number series, idempotency, async acknowledgement), each with URL and date. If the documentation is not complete enough to build against an `httptest` fake, stop: list what is missing in the plan and ask for an account or the documentation.
2. The adapter in `pkg/einvoice/<name>` with `Register`, a `Factory`, `Capabilities`, and `UNVERIFIED` marks where the documentation is silent.
3. `conformance_test.go` with a fake that replays the documented shapes only, and the adapter's own tests for its quirks.
4. Registration in `payments-worker` (`cmd/payments-worker`), the console card (plan 10), `docs/03-services/workers.md`.
5. A transcript of a run against the integrator's test environment (plan 09 "Done means"); until it exists the adapter is `preview`.
