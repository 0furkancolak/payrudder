# Phase 12 — Minimal admin UI (optional)

## Goal

A small Vue/TypeScript operator console that makes the system's state visible during a demo: tenants, usage,
invoices with saga history, DLQs and replay jobs.

## Scope

**In:** Vue 3 + TypeScript (rewritten from the first Next.js version in v0.2.0) in `web/admin`, calling the gateway's admin and tenant APIs (operator token),
read-only views, plus two actions: DLQ redrive and saga retry.

**Out of scope:** tenant self-service portal, authentication beyond an operator token in local env, design system work.

## Tasks

- [x] `web/admin` scaffold (Vue 3, TypeScript strict, ESLint, feature-based), typed API client generated from `api/openapi/v1.yaml` (checked in; the gateway does not serve it)
- [ ] (not done, the checked-in YAML is the source) Gateway: `GET /admin/v1/openapi.json` (hand-maintained or generated from route table)
- [ ] (not done) Onboarding form: create an account with **required** currency (supported list), time zone (IANA picker) and grace. No preselected values
- [ ] (not done) Sandbox/live toggle per account. Sandbox actions: set payment scenario per customer, "close period now"
- [~] Views: tenants list and invoices list done; meters/plans/customers and the usage chart are not
- [x] Invoice detail: lines, saga state, `saga_events` timeline, PDF link
- [~] DLQ view with redrive action (confirmation dialog): RabbitMQ only, Kafka DLQs stay in `payrudderctl dlq`
- [~] Replay/re-rating jobs: lookup by job id only, no listing
- [x] Compose `app` profile entry. Playwright smoke test
- [x] CI job: `npm` lint, typecheck, build, Playwright smoke

## Deliverables

- Admin UI at `http://localhost:42301` in the compose stack, used in the demo recording.

## Acceptance criteria

- [x] Playwright: login with operator token → open an invoice → saga timeline shows ≥ 5 transitions
- [x] Playwright: redrive a DLQ'd message → it disappears from the DLQ view
- [x] Type check and lint pass in CI. No `any` in the API client
- [x] UI works with the gateway only (no direct DB or broker access)

## Dependencies

Phase 11 (replay/re-rating APIs), Phase 07 (analytics), Phase 06 (DLQ tooling).

## Risks / open questions

- Scope creep: hard limit of one week. Cut the replay view first.
- Admin APIs through the public gateway vs. a separate admin port: planned same gateway, `/admin` prefix, operator token, blocked by NetworkPolicy in K8s.

## Notes (as built)

See [13-admin-ui.md](../13-admin-ui.md). Cut for time, in the order the risk section suggested: onboarding, sandbox controls, tenant detail beyond invoices, the usage chart, Kafka DLQs and a replay job listing. `[~]` means partly done.
