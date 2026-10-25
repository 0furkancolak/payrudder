# 06 — Multi-tenancy

## Isolation model

| Layer | Mechanism | Enforced by |
|---|---|---|
| Edge | API key → `tenant_id`. The tenant is **never** taken from the request body | gateway |
| Service-to-service | `x-payrudder-tenant-id` gRPC metadata, set only by gateway. Internal callers propagate it | `pkg/tenant` interceptor |
| Postgres | Shared schema + `tenant_id` column + **Row Level Security** | Postgres ([ADR 0004](adr/0004-shared-schema-rls-tenancy.md)) |
| Kafka | `tenant_id` partition key + `tenant-id` header. Consumers validate that payload and header agree | consumers |
| RabbitMQ | `tenant-id` header + `tenant_id` field in the command. Workers set it on their spans and metrics | workers |
| Redis | Key prefix includes `tenant_id` | gateway, limits-svc |
| ClickHouse | Mandatory `tenant_id` predicate in the query builder | analytics-svc |
| Resources | Per-tenant rate limits (GCRA), batch caps, outbox back-pressure, per-tenant lag metrics | gateway, ingest-svc |

## Environment guards (phase 45)

The tenant is the unit of isolation; the environment (sandbox or live) is asserted at every edge, never defaulted ([ADR 0023](adr/0023-live-and-sandbox-guards.md)):

| Edge | Guard |
|---|---|
| gateway | the environment is the key's tenant's: the prefix is checked against it on every request, and nothing the caller sends (header, query) names it |
| billing-svc | every saga command is stamped from the invoice's frozen snapshot and checked against the tenant (plans-svc); a snapshot with no environment sends nothing; CHECK `invoice_number_series` and a trigger on credit notes keep the `SBX-` series apart |
| payments-worker | a command with no environment, or another than its tenant's, is dead-lettered; sandbox is served by mock-psp and nothing else, live never by the mock; provider accounts have a checked mode |
| invoice-worker | refuses a command whose invoice number is in the other series |
| `pkg/env` | the one place that parses, compares and requires an environment (`env.Parse`, `env.Same`, `env.Directory`) |

## API keys

- Format: `qk_{live|test}_{prefix8}_{secret}`, where `secret` is 32 random bytes in base62 (≈ 190 bits).
- Stored as `sha256(secret)`. **Why not bcrypt/argon2:** those protect *low-entropy* passwords against brute force.
  A 190-bit random secret can't be brute-forced, and a slow hash would add ~50–100 ms to every request on the hot path.
  This matches common practice (GitHub, Stripe-style tokens).
- Lookup by `prefix` (indexed, non-secret), then constant-time compare of the hash.
- Plaintext is shown once, at creation. Revocation deletes the gateway cache key immediately.
- `test` keys resolve to the account's **sandbox tenant** and `live` keys to its live tenant. A sandbox is a separate `tenant_id`,
  so sandbox and live data are isolated by the same RLS and partitioning as any two tenants ([ADR 0011](adr/0011-sandbox-environment.md)).
  plans-svc rejects creating a `live` key for a sandbox tenant and vice versa, and a trigger in the database refuses it too. The gateway checks the prefix against the tenant's environment on every request: a `qk_test_` key whose tenant is live (or the reverse) is a hard `401`.
- A key has a **scope** (`full`, `ingest` = only `POST /v1/events`, `read` = only `GET`), a `last_used_at` and, when it was rotated, an `expires_at`: the old key keeps working for the overlap window (default 24 hours, at most 7 days) and then stops, with no cache eviction needed ([ADR 0023](adr/0023-live-and-sandbox-guards.md)).
- A **live** tenant is created `inactive`: no keys, no webhook endpoints, no invoices, until its go-live checklist is complete and the owner activates it. An operator can suspend an active tenant; its keys stop at once (`403 gateway.auth.tenant_not_active`).

## Partners (phase 47)

A partner of a seller's affiliate program is a person, not a tenant. They become a member of the seller's account with the role `affiliate`; every account-wide read (account, apps, tenants, keys, members) leaves that role out in SQL, so it reaches nothing of the account. What a partner sees comes from `AffiliatePortalService`, which finds every row through the person's own partner record. The program, partners, commissions, payouts and postings are tenant data under forced RLS, so a sandbox and a live program are separate; affiliate codes alone are unique across the platform because the public link carries no tenant.

## Row Level Security

### Roles per service

| Role | RLS | Used by |
|---|---|---|
| `<svc>_migrator` | owner | migrations only |
| `<svc>_app` | **enforced** (`FORCE ROW LEVEL SECURITY`) | gRPC request handlers |
| `<svc>_system` | `BYPASSRLS` | Kafka consumers, outbox relays, schedulers, sweepers |

Cross-tenant work (a consumer batch that spans tenants, the billing scheduler) is legitimate and runs as `_system`.
The rule: **code that handles a tenant's request never holds a `_system` connection.** Each service has two
`pgxpool.Pool`s (`appPool`, `systemPool`). Only packages under `internal/worker/`, `internal/relay/` and (plans-svc) the operator modules `accounts`, `tenants` and `apikeys` (AdminService), `identity` and `golive` — cross-tenant
by nature — receive the
`systemPool`. A `depguard`/`go-arch-lint` rule in CI enforces the import boundary.

### Transaction helper

```go
// pkg/pgxx — sketch
func InTenantTx(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, fn func(pgx.Tx) error) error {
    return pgx.BeginTxFunc(ctx, pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
        if _, err := tx.Exec(ctx, `SELECT set_config('app.tenant_id', $1, true)`, tenantID.String()); err != nil {
            return err
        }
        return fn(tx)
    })
}
```

`set_config(..., true)` is transaction-local (equivalent to `SET LOCAL`), so a pooled connection never
leaks a tenant into the next request. That's the main RLS-with-pooling pitfall, and it's addressed by construction.

### RLS tests (Phase 1 acceptance)

- Querying as `_app` without `set_config` returns zero rows.
- Tenant A's context can't `SELECT`, `UPDATE` or `INSERT` (`WITH CHECK`) tenant B's rows. Table-driven over **every** RLS table,
  generated from `pg_policies`, so new tables can't be forgotten.
- A CI check fails if any table with a `tenant_id` column lacks `relforcerowsecurity = true`.

## Quotas and fairness

| Quota | Free | Standard | Enterprise | Enforced at |
|---|---|---|---|---|
| Ingest events/s (burst) | 100 (500) | 2,000 (10,000) | 10,000 (50,000) | gateway GCRA, per event |
| Max events per request | 100 | 500 | 500 | gateway |
| Max customers | 1,000 | 100,000 | unlimited | plans-svc |
| Max meters | 10 | 100 | 500 | plans-svc |
| Analytics queries/min | 30 | 300 | 1,200 | gateway |

Tier values are seed defaults. They're stored per tenant and can be overridden by the operator.
Fairness beyond rate limits (partition hot-spotting, lag isolation) is in [07-correctness.md §7](07-correctness.md#7-noisy-neighbor).

## Tenant lifecycle

1. Operator (or the admin UI onboarding form): `POST /admin/v1/accounts` with **explicitly chosen** `currency`, `timezone`,
   `grace_period` and `tier`. No defaults. Creates the account plus its sandbox and live tenants with the same settings.
   Currency and time zone are immutable afterwards, and grace can change for future periods. The tenant currency is the default: since phase 13 a
   subscription may be sold in another currency, fixed for its life ([ADR 0016](adr/0016-provider-capabilities-and-routing.md)).
2. Operator: `POST /admin/v1/tenants/{id}/api-keys` → plaintext key returned once (`qk_test_` for the sandbox, `qk_live_` for live; a live tenant must be active first: `PUT /admin/v1/tenants/{id}/status`, or the owner's go-live checklist in the console).
3. Tenant integrates in the sandbox: meters, plans, customers with `pm_sandbox_*` scenarios, events, `POST /v1/sandbox/periods/{p}/close`.
   Then it repeats the setup in live. Copying config from sandbox to live is a documented extension, not in v1.
4. A deployment running `values-sandbox.yaml` (`PAYRUDDER_ALLOWED_ENVIRONMENTS=sandbox`) creates only the sandbox tenant.
5. Off-boarding (out of scope for v1): documented as revoke keys → final billing run → data export → delete by
   `tenant_id` per service. RLS makes "delete everything of tenant X" a straightforward `DELETE ... WHERE tenant_id = $1` as `_system`.
