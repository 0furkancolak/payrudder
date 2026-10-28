# 09 — E-invoice integrators

## The request
List 1 (3): add **other e-invoice integrators** (the owner said "başka fatura entegratörleri").

## Where we are
Phase 30/53: a simulator (sandbox), **Nilvera** (Turkish e-fatura/e-arşiv integrator) and a generic "webhook" integrator that posts each signed document to the tenant's own address. One integrator per environment (`einvoice_accounts` keyed by tenant and environment). UBL generation for Turkish e-invoices exists in the e-invoice module.

## Gaps
- **Nilvera has never been run against Nilvera's test environment**; schematron validation of the UBL-TR is not run.
- Only one real integrator; Turkey has many (Foriba/Sovos, Logo, Uyumsoft, Mysoft/ParaşütEntegrator, İzibiz, e-Logo, Nilvera, Paraşüt…). Other countries have their own regimes (Italy SDI, Poland KSeF, France Chorus/PDP, Spain Veri*Factu, Saudi ZATCA, India GST e-invoice, Mexico CFDI, Brazil NF-e, EU PEPPOL). None is modelled.
- One integrator per environment: the owner wants several of the same kind and per-country choice (10).
- The buyer's type (05) does not feed the document.

## Research first
1. Decide the **markets**: which countries need e-invoicing now.
2. For Turkey: the GİB UBL-TR schema, e-fatura vs e-arşiv, integrator APIs (list their public docs and test environments), "özel entegratör" requirements.
3. For other regimes: pick one at a time from the owner's list; read the official technical specification and test environment.
4. Extract an **integrator contract** (issue, status, cancel, credit note, PDF/XML retrieve, number series, idempotency, async acknowledgement).

## Decisions needed
Markets and integrators in order; test accounts; who holds the legal responsibility for numbering and archiving (the integrator or Payrudder).

## Work
1. Contract doc and conformance tests; migrate Nilvera to it with its test environment run.
2. Schematron/XSD validation in CI for the generated documents.
3. Add integrators one at a time (separate plans `09a-…`), each with a sandbox transcript.
4. Per-country integrator choice via 10.

## Done means
Per integrator: validation in CI, test-environment transcript, credit note and cancel flows, docs.

## Not verifiable here
Acceptance by tax authorities; integrator test environments need accounts.

## Risks
Legal consequences of wrong documents; ship per country only with a reviewer.
