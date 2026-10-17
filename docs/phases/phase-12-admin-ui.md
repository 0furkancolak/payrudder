# Phase 12 — Minimal admin UI (optional)

## Goal

A small Next.js/TypeScript operator console that makes the system's state visible during a demo: tenants, usage,
invoices with saga history, DLQs and replay jobs.

## Scope

**In:** Next.js (App Router) + TypeScript in `web/admin`, calling the gateway's admin and tenant APIs (operator token),
read-only views, plus two actions: DLQ redrive and saga retry.

**Out of scope:** tenant self-service portal, authentication beyond an operator token in local env, design system work.

## Tasks

- [ ] `web/admin` scaffold (Next.js, TypeScript strict, ESLint), typed API client generated from an OpenAPI spec exported by gateway
- [ ] Gateway: `GET /admin/v1/openapi.json` (hand-maintained or generated from route table)
- [ ] Views: tenants list, tenant detail (meters, plans, customers), customer usage chart (analytics API), invoices list
- [ ] Invoice detail: lines, saga state, `saga_events` timeline, PDF link
- [ ] DLQ view (both brokers) with redrive action (confirmation dialog)
- [ ] Replay/re-rating jobs view (status, diff summary)
- [ ] Compose `app` profile entry. Playwright smoke test
- [ ] CI job: `pnpm lint typecheck build`, Playwright smoke

## Deliverables

- Admin UI at `http://localhost:3001` in the compose stack, used in the demo recording.

## Acceptance criteria

- [ ] Playwright: login with operator token → open an invoice → saga timeline shows ≥ 5 transitions
- [ ] Playwright: redrive a DLQ'd message → it disappears from the DLQ view
- [ ] Type check and lint pass in CI. No `any` in the API client
- [ ] UI works with the gateway only (no direct DB or broker access)

## Dependencies

Phase 11 (replay/re-rating APIs), Phase 07 (analytics), Phase 06 (DLQ tooling).

## Risks / open questions

- Scope creep: hard limit of one week. Cut the replay view first.
- Admin APIs through the public gateway vs. a separate admin port: planned same gateway, `/admin` prefix, operator token, blocked by NetworkPolicy in K8s.
