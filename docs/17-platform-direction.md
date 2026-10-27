# Platform direction: product, accounts, isolation and operations

Requirements the owner added on 2026-10-20, what each means for the code, and in which phase it is built. Phases 19–23 finished the billing features; these phases make the product something that can be renamed, sold as a service by its owner, run on little memory and developed on comfortably.

## The product in one paragraph

An application integrates Payrudder to take **payments through a provider** (iyzico, Paddle; a mock in demo mode) and to **track usage**, and to follow every payment and usage event of its users. Payrudder is a self-hosted product for individual use; it is licensed so that nobody else may sell it as a service (ADR 0019); its owner may.

## Requirements and where they land

| # | Requirement | Decision | Phase |
|---|---|---|---|
| 1 | The product name may change | One `pkg/brand` holds the name and slug that user-visible text, e-mails, PDFs, headers and the console read. Internal identifiers (module path, `PAYRUDDER_` variables, wire names) are an internal naming and change with `tools/rename`, a checked script, not by hand | 24 |
| 2 | Customer address management | A billing address (lines, city, region, postal code, country) on customers and a seller address on the tenant, in the API, the console, the invoice snapshot, the PDF and the hosted page | 25 |
| 3 | Tenants isolated like pgcells | ADR 0018: pooled mode with RLS stays (default); a directory with cells and schema-per-tenant is added as an explicit second mode, built in stages. The first stage puts placement into the tenant directory | 28 |
| 4 | Tenant id and app id; a tenant manages its applications | The account is the customer's organisation. **Apps** are added between account and tenant: an account has apps, an app has a sandbox and a live tenant (the unit of isolation that exists today). API keys belong to an app environment | 25 |
| 5 | Clean code, no needless code, module-based CQRS, error keys | ADR 0020: each service is split into modules (a directory per capability) with `command` and `query` packages inside; handlers stay thin; every error carries a stable `key` (`billing.credit_note.not_paid`) next to the gRPC/HTTP code, and the console translates keys. Existing code is moved when it is touched, not in one rewrite | 24 + each phase |
| 6 | Config defaults come from YAML, public ones especially | Each service embeds a `config.default.yaml`; environment variables override it; secrets are never in YAML. The files are the documented list of public settings | 24 |
| 7 | Very little RAM, stateless | Services keep no state across requests (they already hold none that matters); memory limits are set (`GOMEMLIMIT` from the container limit), idle pools are small, and a measured budget per service is published | 27 |
| 8 | A better dev environment: `make setup` and `make dev` are different things; do not containerise the apps | `make setup` installs tools and starts the infrastructure (Postgres, Redpanda, RabbitMQ, Redis, MinIO, Mailpit) in Docker. `make dev` runs the Go services and the console on the host with reload, against that infrastructure. Application images are built for CI and release only | 27 |
| 9 | Kubernetes installation | The Helm charts exist; a documented one-command install (`make k8s-install`), a values file for small clusters and the checks CI already runs | 27 |
| 10 | Sold only for individual use, not as SaaS by others | ADR 0019: Elastic License 2.0 from this release on. Earlier releases stay Apache-2.0 | 24 |
| 11 | A demo mode | `PAYRUDDER_DEMO=true`: a seeded demo app, the mock provider only, a banner in the console, mails to the local catcher, and a reset command | 26 |
| 12 | Send mail depending on the provider | A provider declares whether it sends its own receipts and invoices (Paddle does, as merchant of record; iyzico does not). Payrudder sends payment and invoice e-mails only for payments it ran itself, and says which in the log | 26 |
| 13 | Suitable for an edge environment | Read endpoints that are safe to cache say so (`ETag`, `Cache-Control`); the gateway and the hosted pages are stateless, listen on a plain port, answer `/healthz`, and the image stays small. Running the whole platform at the edge is not the goal: the decision paths an edge would answer (entitlements, hosted invoice pages) are | 27 |

## Not built, on purpose

- **Quantity (seat) changes and tax-inclusive prices** were gaps listed after phase 22. They add a data-model change each; with the above as the priority and "no needless code" as the rule, they wait until an application needs them.
- **Running every service on an edge runtime** (workers without a TCP stack): the services need Postgres, Kafka and RabbitMQ. Edge here means the cacheable read paths and the stateless gateway.

## Payrudder as a SaaS that bills itself (phase 55)

The owner may sell Payrudder as a service ([ADR 0019](adr/0019-license.md)), so the product sells itself with its own engine ([ADR 0024](adr/0024-payrudder-as-a-saas.md)) and not with a side system.

- **A platform tenant** (an account marked `platform`, created at start when `PAYRUDDER_SAAS=on`, hidden from the operator's tenant list) owns the plans **Free, Pro and Scale**: a monthly fee in the platform currency (`PAYRUDDER_SAAS_CURRENCY`) and entitlements on four meters: `events` (a calendar month), `apps`, `members`, `api_keys`. Seeded once; after that the operator edits them as the packages of that tenant (a new version applies to organisations that move to the plan afterwards).

  | | Free | Pro | Scale |
  |---|---|---|---|
  | Fee | 0 | 29 | 99 |
  | Events a month | 10,000 (hard) | 1,000,000 (hard) | 10,000,000 (notify) |
  | Apps / members / API keys | 1 / 2 / 4 | 5 / 10 / 20 | 25 / 50 / 100 |

- **Every organisation is a customer** of it with a subscription: Free by default, a trial (`PAYRUDDER_SAAS_TRIAL_DAYS`, 14) for the first move to a paid plan. Its invoices, payment links, customer portal and dunning are the ordinary ones.
- **Limits** are enforced where the resource is created (apps, invitations, API keys: `429 plans.saas.quota.*`, under the account's lock) and on ingest volume (live tenants only: `429 gateway.saas.events_quota` over a hard events quota; the accepted events are metered into the platform tenant). Soft and notify limits are the quota engine's alerts and billing, never a refusal. The console turns these keys into an upgrade prompt.
- **Organisation settings, Plan and billing** (`/app/settings/plan`): the plan, usage against quotas, the plans on offer with upgrade and downgrade, the invoices (with a payment link for an open one) and the customer portal (payment method). **Platform, Organisations** (`/organisations`): every organisation with plan, state and usage, a manual override (plan without a checkout, limits of its own), and a door to the platform tenant's packages.
- **`PAYRUDDER_SAAS=off`** (the default; self-hosters): none of it: no platform tenant, no limits, no plan page. `make setup` and `make dev` switch it on, with the platform tenant in the sandbox environment so the mock provider takes the payments; the dev seed puts its accounts on Scale.
