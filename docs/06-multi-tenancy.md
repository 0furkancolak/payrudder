# 06 — Multi-tenancy

## Isolation model

| Layer | Mechanism | Enforced by |
|---|---|---|
| Edge | API key → `tenant_id`. The tenant is **never** taken from the request body | gateway |
| Service-to-service | `x-quotify-tenant-id` gRPC metadata, set only by gateway. Internal callers propagate it | `pkg/tenant` interceptor |
| Postgres | Shared schema + `tenant_id` column + **Row Level Security** | Postgres ([ADR 0004](adr/0004-shared-schema-rls-tenancy.md)) |
| Kafka | `tenant_id` partition key + `tenant-id` header. Consumers validate that payload and header agree | consumers |
| RabbitMQ | `tenant-id` header + `tenant_id` field in the command. Workers set it on their spans and metrics | workers |
| Redis | Key prefix includes `tenant_id` | gateway, limits-svc |
| ClickHouse | Mandatory `tenant_id` predicate in the query builder | analytics-svc |
| Resources | Per-tenant rate limits (GCRA), batch caps, outbox back-pressure, per-tenant lag metrics | gateway, ingest-svc |

## API keys

- Format: `qk_{live|test}_{prefix8}_{secret}`, where `secret` is 32 random bytes in base62 (≈ 190 bits).
- Stored as `sha256(secret)`. **Why not bcrypt/argon2:** those protect *low-entropy* passwords against brute force.
  A 190-bit random secret can't be brute-forced, and a slow hash would add ~50–100 ms to every request on the hot path.
  This matches common practice (GitHub, Stripe-style tokens).
- Lookup by `prefix` (indexed, non-secret), then constant-time compare of the hash.
- Plaintext is shown once, at creation. Revocation deletes the gateway cache key immediately.
- `test` keys hit the same system but are tagged. Enforcing a separate `test` mode with isolated data is out of scope.
  Open question: a `livemode` flag per event, like Stripe.

## Row Level Security

### Roles per service

| Role | RLS | Used by |
|---|---|---|
| `<svc>_migrator` | owner | migrations only |
| `<svc>_app` | **enforced** (`FORCE ROW LEVEL SECURITY`) | gRPC request handlers |
| `<svc>_system` | `BYPASSRLS` | Kafka consumers, outbox relays, schedulers, sweepers |

Cross-tenant work (a consumer batch that spans tenants, the billing scheduler) is legitimate and runs as `_system`.
The rule: **code that handles a tenant's request never holds a `_system` connection.** Each service has two
`pgxpool.Pool`s (`appPool`, `systemPool`). Only packages under `internal/worker/` and `internal/relay/` receive the
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

1. Operator: `POST /admin/v1/tenants` → tenant row + default settings.
2. Operator: `POST /admin/v1/tenants/{id}/api-keys` → plaintext key returned once.
3. Tenant: create meters, plans, customers, subscriptions → send events.
4. Off-boarding (out of scope for v1): documented as revoke keys → final billing run → data export → delete by
   `tenant_id` per service. RLS makes "delete everything of tenant X" a straightforward `DELETE ... WHERE tenant_id = $1` as `_system`.
