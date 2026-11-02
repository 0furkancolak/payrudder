# Before an integrator adapter is written: the checklist

Plan 09 ([`plans/09-einvoice-integrators.md`](plans/09-einvoice-integrators.md)). The adapter contract is [`einvoice-integrator-contract.md`](einvoice-integrator-contract.md) and its executable half is `pkg/einvoice/conformance`. This page is the part before both: **what to find out about an integrator, from its public documentation or by asking it, and which check of the suite will prove each answer once an adapter exists.** An adapter is not started while a row marked **needed** has no answer; a row the documentation does not settle is written down as UNVERIFIED in the plan's research table and in the adapter's package comment, and the code does not act on a guess.

Copy the table into the plan's research row for the integrator, fill the right-hand column with the answer, the URL and the date read.

| # | Ask | Why Payrudder needs it | Proved by | |
|---|---|---|---|---|
| 1 | How does a call authenticate (key, OAuth, mTLS), how long does a credential live, and how is it made? | The sealed settings and the test button (plan 10) | `errors-permanent-and-transient`, `errors-never-leak-credentials` | needed |
| 2 | Is there a **test environment** with its own credentials and company data, and how is an account got? | Nothing is verified without it; a transcript of a run is the "done" of an adapter | none: a transcript | needed |
| 3 | How is a document **issued**: the call, its body (UBL upload, a JSON model, the integrator's own), and what it answers (an id, a legal number, a state) | `Issue`, the shape of its answer | `issue-<type>`, `async-confirmation` | needed |
| 4 | Is `Issue` **idempotent** on the document's UUID (the ETTN in Turkey)? What does a repeat answer (409, the same document)? **What happens when two requests with one UUID arrive together?** | A second legal document under one UUID is the failure the contract exists to prevent | `issue-<type>` (the repeat), `issue-concurrent-is-one-document` | needed |
| 5 | Is confirmation **asynchronous** (accepted, then answered by the authority)? Which call gives the state, and does it carry the **legal number**? | The worker does not complete a document without a number | `async-confirmation` | needed |
| 6 | **Who numbers** the documents: a series registered with the integrator per company (and its format), or the integrator? Can a document carry Payrudder's own number as a reference? Are the series per year? | The legal number of a document and the UBL's `cbc:ID` (plan 09 "Decisions needed"; Turkey: series + year + 9 digits) | `issue-<type>` (the legal number) | needed |
| 7 | **Credit notes**: how is a document that corrects another issued (a type, a reference to the original's legal number or UUID)? Is it the same call? Which original states can be corrected? | The worker issues a credit note after the original is issued | `credit-note` | needed |
| 8 | **Cancellation**: is there a call, until when, for which document types, and what does the state read afterwards? Or is a credit note the only way? | `Cancel`, `CanCancel`, the state `cancelled` | `cancel`, `cancel-unknown-is-not-found` | needed |
| 9 | **Retrieval**: PDF and XML of an issued document (paths, content types, whether the XML is the sealed one the authority holds), and what an unknown document answers | `FetchPDF`, `FetchUBL`, the console's download | `retrieve`, `status-unknown-is-not-found` | needed |
| 10 | The **error body** and its catalogue: which codes mean "never retry" (validation, credentials, unknown tax id) and which mean "try later"; the HTTP statuses used; whether the text can repeat the key | The permanent and transient kinds; the redaction of a credential | `errors-permanent-and-transient`, `errors-never-leak-credentials` | needed |
| 11 | **Rate limits** and the answer to one (429, a header, a window); timeouts; maintenance windows | Backoff, and whether a slow answer is transient | `errors-permanent-and-transient`, `cancelled-context` | useful |
| 12 | Is there a **callback or webhook** when the authority answers, or is polling the only way? Is it signed? | Polling cost; a second path to the same state | none yet | useful |
| 13 | What does the integrator **require of the buyer record** (tax id, tax office, district, alias, whether the buyer is a registered e-document user)? Is there a lookup of a business by tax id? | The data a customer needs to carry (plan 05) and `BuyerChecker` | none: the document validation (`scripts/einvoice-validate.sh`) | needed |
| 14 | **Sandbox documents**: can a test environment be told apart from production by the key or the host, so that a test key never reaches the live path? | The sandbox lock (ADR 0011) and `Document.Sandbox` | `sandbox-documents-refused` | needed |
| 15 | **Archiving and legal responsibility**: who keeps the document for the legal period, who answers to the authority for numbering and content, and what happens to the documents if the contract ends | The seller's obligations; the answer is the integrator's contract, not code | none | needed |
| 16 | Cost model (per document, per month) and who holds the contract (each seller, or Payrudder as a reseller) | Whether tenants connect their own account (the model so far) or Payrudder does | none | useful |
| 17 | Which **document types** and countries it covers (e-fatura, e-arşiv, Peppol, a national format) and which `einvoice.DocumentType` each is | `Capabilities.Types`; `ChooseType` in the worker | `capabilities`, `issue-unsupported-type` | needed |
| 18 | Does the documentation say anything about **what it does not do** (a field it ignores, a rule it checks only at the authority)? | The UNVERIFIED marks | none | useful |

## When the documentation is not enough

The integrators read on 2026-11-02 and why none besides Nilvera qualifies are in the plan's research table. The generic **webhook** adapter is the way into any integrator that has an API and a tenant who can wire it: the tenant's endpoint receives the signed UBL and talks to the integrator, and `conformance.WebhookServer` is the executable specification of that protocol.

## After the answers

1. The research row in the plan, with URLs and dates; what is missing, listed.
2. The adapter and its `conformance_test.go` with a fake that replays **only** the documented shapes; the checks above must pass.
3. `scripts/einvoice-validate.sh` for the documents it builds (the integrator may check more than the schematron does).
4. A transcript of a run against the test environment; until then the adapter is a preview and says so in its package comment.
