# ADR 0023 — Live is earned, and the environments cannot be confused

- Status: Accepted (phase 45). Builds on [ADR 0011](0011-sandbox-environment.md), which stays true: a sandbox is a tenant, no table has an `environment` column.
- Date: 2026-10-25

## Context

ADR 0011 gives every app a sandbox tenant and a live tenant and keeps them apart with the isolation that already exists between tenants. It is sound, but everything around it was thin: a live tenant could take keys the moment it existed, a half-configured live tenant could be billed, the console could show live zeros next to sandbox data without saying which was which, configuration built in the sandbox had to be typed again for live, and "a sandbox call never reaches a real provider" was one code path.

## Decision

1. **The tenant stays the unit of isolation.** No `environment` column on tenant tables. The new guards sit at the edges.
2. **Fail closed.** A request, command or event whose environment is missing, or disagrees with its tenant's, is refused. `pkg/env` is the one place that parses, compares and requires an environment; there is no default.
3. **Live is earned.** `tenants.status` is `inactive`, `active` or `suspended`. A sandbox tenant is always `active` (a CHECK). A live tenant is created `inactive` and has no API keys and no webhook endpoints, and billing-svc's scheduler does not invoice it, until its go-live checklist is complete and the owner activates it.
   - The checklist is computed on the server: the owner's e-mail is verified; the legal profile is complete (legal name, country, and a tax id where the country needs one: Turkey and the EU); a payment provider is connected in live mode and works; a plan is published; the terms are accepted.
   - Four of the five items are facts in plans-svc's database. The provider item is a fact in payments-worker's database (`GET /admin/tenants/{id}/go-live`: a live-mode account whose sealed settings still open and still build an adapter). The gateway asks payments-worker and passes the answer to plans-svc, which only the gateway can call; the activation request carries no claim about providers. An unreachable payments-worker is a "no".
   - Only an owner activates. Activation recomputes the checklist inside the transaction that decides, on a locked tenant row, so two requests cannot both pass on stale data. A refused activation does not accept the terms.
   - An operator can suspend an active tenant (its keys stop at once: the gateway cache is evicted, and a genuine key of a tenant that is not active is answered `403 gateway.auth.tenant_not_active`, not the `401` of a bad key), reinstate it, or activate an inactive one without the checklist, which needs a written reason and is logged. A suspended tenant is not reactivated by its owner.
   - The database refuses what the code should never write: a trigger rejects a key whose environment disagrees with its tenant's and a live key of a tenant that is not active; another rejects an active webhook endpoint of such a tenant.
4. **Keys carry their environment, and a scope.** `qk_test_` keys belong to sandbox tenants and `qk_live_` keys to live ones; the gateway checks the prefix against the tenant's environment on every request and a disagreement is a hard `401` (there is no mapping that could be talked into it). The environment of a call is the key's tenant's, never a header or query parameter of the caller. A key has a scope: `full`, `ingest` (only `POST /v1/events`) or `read` (only `GET`/`HEAD`); anything else is `403 gateway.auth.scope_denied`. A key records `last_used_at`, written by plans-svc at most once a minute per key and told by each gateway replica at most once every five minutes per key (bounded memory, off the request path). Rotation issues a replacement of the same scope and sets `expires_at` on the old key (default 24 hours, at most 7 days); the gateway reads `expires_at` from the cached entry, so the old key ends on time without a cache eviction. A key that already ends cannot be rotated again.

5. **A layered environment guard** (`pkg/env`). It is the one place that parses, compares and requires an environment; the string literals "sandbox" and "live" are spelled there and nowhere else. `env.Directory` asks plans-svc for a tenant's environment once and keeps it (an environment never changes: a trigger refuses it), and a worker's check fails closed: no environment, or one that differs, is poison (`workerx.RefuseEnvironment`), a failed lookup is retried.
   - The gateway takes the environment from the key's tenant only.
   - billing-svc stamps each saga command from the invoice snapshot, never defaulting to live, and checks it against the tenant. A CHECK on `invoices` (`invoice_number_series`) ties the number series to the snapshot's environment, and a trigger does the same for credit notes: a sandbox number cannot be on a real invoice whatever wrote the row.
   - payments-worker refuses a command without an environment or against its tenant's, and routes in one place in both directions: a sandbox command is served by the sandbox provider and nothing else; a live command is never served by the mock. `PAYMENTS_LIVE_PROVIDER=mockpsp` needs `PAYMENTS_ALLOW_MOCK_LIVE=true` (or the demo) to start. A provider account has a livemode (iyzico and Paddle tell it from their endpoint): the one that does not match the environment is refused when stored and when used (`payments.account.livemode_mismatch`).
   - invoice-worker refuses a command whose invoice number is of the other series.
   - A sandbox id sent to a live route is "not found", for every resource family (plans-svc and billing-svc each have a test per family, in both directions), and a random walk of the tenant API as the sandbox tenant, with live ids mixed in, leaves a fingerprint of every live row unchanged.

Later parts of the phase (promoting configuration, the sandbox lifecycle and the console) are recorded in the sections below as they land.

## Consequences

- Every place that created a live tenant and then a key for it (the e2e, load, chaos and bench setups, `payrudderctl seed`) now activates the tenant first through `PUT /admin/v1/tenants/{id}/status` with a reason, the way support does. The dev seed (`payrudderctl dev-seed`) never needs it: it works in the sandbox only.
- Tenants that exist when migration `00045` runs keep working: they become `active`.
- billing-svc treats a tenant status it was not sent (plans-svc not yet upgraded) as active, so the two can be rolled out in either order.
- The API key cache (60 seconds) bounds how long a suspension takes if the eviction fails; the eviction itself is immediate.
