# ADR 0011 — Sandbox environment with a mock payment flow

- Status: Accepted
- Date: 2026-10-17

## Context

Tenants need a safe place to integrate: send events, define meters and plans, close a period and watch the invoice, payment
and email flow, including failure paths (declines, dunning, ambiguous timeouts). They must not wait a month for a period to end,
and nothing done there may ever reach a real payment provider, a real customer mailbox, or live invoice numbering.

The same platform also has to run on Kubernetes (kind + Helm), and a public demo deployment should be possible without any
risk of real charges.

## Decision

**Tenant-level sandbox (data plane).**
- Each **account** owns two tenants: `environment = sandbox` and `environment = live`, created together by `CreateAccount` with
  the same currency, time zone and grace. `qk_test_…` keys resolve to the sandbox tenant, `qk_live_…` keys to the live tenant.
- Because a sandbox is just another `tenant_id`, every existing isolation mechanism applies unchanged: RLS, Kafka partition key,
  rate limits, quotas, per-tenant generations. No `environment` column is added to tenant-owned tables.
- The environment travels with each request (gateway metadata) and each command (`CommandEnvelope.environment`).

**Mock payment flow.**
- payments-worker routes `SANDBOX` commands to mock-psp, **hard-coded** and not configurable.
- Sandbox customers carry `pm_sandbox_*` scenario tokens (success, declined, decline-once, error-then-success,
  timeout-committed, timeout-not-committed). Each one drives a specific saga path ([workers.md](../03-services/workers.md#payments-worker)).
- `POST /v1/sandbox/periods/{period}/close` closes the current period early (aggregator `ClosePeriodEarly`, recorded cutoff),
  then the normal billing run and saga execute. Dunning delays are compressed to minutes.
- Sandbox invoices use their own number series (`SBX-`), carry a PDF watermark, and emails get a `[SANDBOX]` prefix and go
  only to the sandbox SMTP sink.

**Deployment-level sandbox (Kubernetes).**
- The Helm chart deploys mock-psp as a first-class component, so the tenant sandbox works in kind exactly as in Compose.
- `values-sandbox.yaml` is a deployment profile for a public demo in namespace `payrudder-sandbox`: `CreateAccount` creates only
  sandbox tenants (`live` disabled by `PAYRUDDER_ALLOWED_ENVIRONMENTS=sandbox`, enforced in plans-svc and gateway),
  `PAYMENTS_LIVE_PROVIDER` unset, SMTP pointed at Mailpit, shorter retention.

## Alternatives considered

| Option | Why not |
|---|---|
| `environment` column on every table, one tenant | Every query, index, RLS policy and Kafka key must include it. One missed predicate mixes test and live data |
| Separate sandbox deployment only | Doubles the operational surface, and tenants can't switch keys to compare. Kept as an *additional* Helm profile, not the only mechanism |
| Env-wide failure rates only (`MOCKPSP_*_RATE`) | Random, so tenants can't reproduce a path on demand. Kept for chaos tests |
| Test clocks (simulated time per tenant) | More general, but every time-dependent component would need a per-tenant clock. Early close covers the main need with one recorded cutoff |

## Addendum (phase 45)

The decision above stands. [ADR 0022](0022-live-and-sandbox-guards.md) adds what sits around it: a live tenant starts `inactive` and is activated by a go-live checklist, API keys carry a scope and a rotation overlap, and the environment is asserted at every edge (`pkg/env`).

## Consequences

- Sandbox shares infrastructure with live, so a sandbox load spike consumes shared capacity. It's mitigated by the tenant tier
  (sandbox tenants default to `free` limits) and the same noisy-neighbor controls.
- Early close changes `cutoff_at` before the period ends. That's safe because the cutoff is recorded and the classification uses it,
  but it relies on bounded clock skew (5 s margin) between the ingest and aggregator databases. This applies only to sandbox tenants.
- The routing rule "sandbox ⇒ mock-psp" is a single code path with a unit test and an integration test that sets
  `PAYMENTS_LIVE_PROVIDER` to a fake real provider and asserts it receives zero sandbox calls.
