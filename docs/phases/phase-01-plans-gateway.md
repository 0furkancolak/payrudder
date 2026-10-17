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

- [x] `proto/quotify/plans/v1/*.proto` per [plans-svc.md](../03-services/plans-svc.md)
- [x] Migrations for `accounts`, `tenants` (environment, currency, timezone, grace; immutability trigger), `api_keys`, `customers`, `meters`, `plans`, `plan_versions`, `plan_prices`, `plan_entitlements`, `subscriptions`, `outbox`
- [x] Roles `plans_app`, `plans_system`, `plans_migrator`. RLS policies + `FORCE` on tenant tables
- [x] `pkg/pgxx`: `NewPools(appDSN, systemDSN)`, `InTenantTx`, goose migration runner
- [x] `pkg/tenant`: context key, gRPC server interceptor reading `x-quotify-tenant-id`, client interceptor propagating it
- [x] sqlc queries + store layer for plans-svc
- [x] `AdminService.CreateAccount`: sandbox + live tenant pair, required currency (supported ISO 4217 list + exponent table) / IANA timezone / grace, no defaults. `QUOTIFY_ALLOWED_ENVIRONMENTS`
- [x] Domain validation: meter aggregation/value_property rules, `quantity_scale` 0–6 fixed per code, decimal limits/tier bounds within scale, price config validation (tiers ascending, last tier open-ended)
- [x] Immutable `plan_versions` + `meters` (no UPDATE grants)
- [x] Subscription exclusion constraint + `ALREADY_EXISTS` mapping
- [x] `AdminService.CreateApiKey` (prefix + secret, sha256 hash; `test` only for sandbox, `live` only for live tenants) and `ResolveApiKey` (returns environment + timezone)
- [x] `SetPaymentMethod`: sandbox tenants accept only `pm_sandbox_*` tokens
- [x] gateway: `net/http` mux (decided: hand-written, see [gateway.md](../03-services/gateway.md#why-a-hand-written-http-layer-not-grpc-gateway)), `UseNumber` decoding, middleware chain, JSON ↔ proto mapping, error model (`{"error":{"code","message","details"}}`)
- [x] gateway: API-key cache in Redis (60 s), revocation deletes the cache key
- [x] gateway: GCRA Lua script + per-tier limits + fail-open local bucket
- [x] `quotifyctl seed` (account with explicit currency/timezone/grace, sandbox + live keys, 2 meters incl. one fractional, 1 plan, 10 customers)
- [x] `api/openapi/v1.yaml` + contract test over the route table
- [x] RLS test generator over `pg_policies`. CI check for `relforcerowsecurity`
- [x] Remove `services/hello` (its trace-propagation role is taken by gateway → plans-svc → Postgres traces; Kafka/AMQP carriers stay unit-tested in `pkg/otelx`)

## Deliverables

- plans-svc and gateway images, compose `app` profile entries.
- HTTP API reference section in `docs/03-services/gateway.md` updated with final shapes.

## Acceptance criteria

- [x] Integration: for **every** RLS table, tenant A's context can't read, update or insert tenant B's rows (generated test)
- [x] Integration: a query as `plans_app` without tenant context returns 0 rows
- [x] Integration: overlapping subscriptions for the same customer → second returns `ALREADY_EXISTS`
- [x] Unit: GCRA allows burst then rate. Fake clock
- [x] Integration: 600 events/s claimed against a `free` tier key → responses beyond burst are `429` with `Retry-After`
- [x] Integration: revoked key is rejected on the next request (cache invalidated)
- [x] Integration: invalid tiered price config (non-ascending tiers) → `400` with a field-level error
- [x] Integration: `CreateAccount` without currency or timezone → `INVALID_ARGUMENT`. Updating currency/timezone → rejected by trigger
- [x] Integration: a `qk_test_` key reads only sandbox data, a `qk_live_` key only live data (same account)
- [x] Integration: `values-sandbox` mode (`QUOTIFY_ALLOWED_ENVIRONMENTS=sandbox`) refuses to create a live tenant or key
- [x] CI fails if a new table with `tenant_id` lacks forced RLS (verified by a deliberately broken test migration in the check's own test)

## Dependencies

Phase 00.

## Risks / open questions

- **Decided:** hand-written HTTP layer. Revisit if the route count grows past ~30.
- `tenants`/`api_keys` excluded from RLS (operator-only tables, accessed by `internal/admin` on the system pool). plans_app reads its own tenant row through `current_tenant()`.
- **Found while building:** on a pooled connection, `current_setting('app.tenant_id', true)` returns `''` (not NULL) after an earlier transaction-local `set_config`, so `''::uuid` errored instead of returning zero rows. Policies now use `app_tenant_id()` with `NULLIF`. Covered by the pgxx integration test.
- **Found while building:** `otelpgx` was recording query parameters (key hashes, e-mails) on spans; disabled.
- `read` and `analytics` rate classes and all bursts were not in the original tier table; values added in gateway.md.
