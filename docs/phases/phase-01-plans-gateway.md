# Phase 01 — plans-svc + gateway

## Goal

Tenants can be created, authenticate with API keys and manage customers, meters, plans and subscriptions through a
rate-limited public HTTP API, with Postgres RLS isolating every row.

## Scope

**In:** plans-svc (all `PlansService` + `AdminService` RPCs), `plans_db` schema with RLS, `pkg/pgxx` (pools, `InTenantTx`),
`pkg/tenant` interceptors, gateway HTTP routes for plans resources, API-key auth, GCRA rate limiting, plans-svc outbox table
(relay deferred to Phase 04 when its consumer exists).

**Out of scope:** events, entitlement topic publishing (Phase 04), invoices, admin UI.

## Tasks

- [ ] `proto/quotify/plans/v1/*.proto` per [plans-svc.md](../03-services/plans-svc.md)
- [ ] Migrations for `tenants`, `api_keys`, `customers`, `meters`, `plans`, `plan_versions`, `plan_prices`, `plan_entitlements`, `subscriptions`, `outbox`
- [ ] Roles `plans_app`, `plans_system`, `plans_migrator`. RLS policies + `FORCE` on tenant tables
- [ ] `pkg/pgxx`: `NewPools(appDSN, systemDSN)`, `InTenantTx`, goose migration runner
- [ ] `pkg/tenant`: context key, gRPC server interceptor reading `x-quotify-tenant-id`, client interceptor propagating it
- [ ] sqlc queries + store layer for plans-svc
- [ ] Domain validation: meter aggregation/value_property rules, price config validation (tiers ascending, last tier open-ended)
- [ ] Immutable `plan_versions` + `meters` (no UPDATE grants)
- [ ] Subscription exclusion constraint + `ALREADY_EXISTS` mapping
- [ ] `AdminService.CreateApiKey` (prefix + secret, sha256 hash) and `ResolveApiKey`
- [ ] gateway: `net/http` mux, middleware chain, JSON ↔ proto mapping, error model (`{"error":{"code","message","details"}}`)
- [ ] gateway: API-key cache in Redis (60 s), revocation deletes the cache key
- [ ] gateway: GCRA Lua script + per-tier limits + fail-open local bucket
- [ ] `quotifyctl seed` (tenant, key, 2 meters, 1 plan, 10 customers)
- [ ] RLS test generator over `pg_policies`. CI check for `relforcerowsecurity`
- [ ] Remove `services/hello`

## Deliverables

- plans-svc and gateway images, compose `app` profile entries.
- HTTP API reference section in `docs/03-services/gateway.md` updated with final shapes.

## Acceptance criteria

- [ ] Integration: for **every** RLS table, tenant A's context can't read, update or insert tenant B's rows (generated test)
- [ ] Integration: a query as `plans_app` without tenant context returns 0 rows
- [ ] Integration: overlapping subscriptions for the same customer → second returns `ALREADY_EXISTS`
- [ ] Unit: GCRA allows burst then rate. Fake clock
- [ ] Integration: 600 events/s claimed against a `free` tier key → responses beyond burst are `429` with `Retry-After`
- [ ] Integration: revoked key is rejected on the next request (cache invalidated)
- [ ] Integration: invalid tiered price config (non-ascending tiers) → `400` with a field-level error
- [ ] CI fails if a new table with `tenant_id` lacks forced RLS (verified by a deliberately broken test migration in the check's own test)

## Dependencies

Phase 00.

## Risks / open questions

- Hand-written HTTP layer vs. `grpc-gateway`: planned hand-written (explicit middleware, small surface). Revisit if the route count grows past ~30.
- `tenants`/`api_keys` excluded from RLS (operator-only tables). Confirm this is acceptable.
