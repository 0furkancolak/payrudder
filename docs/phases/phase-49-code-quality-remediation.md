# Phase 49 — Code-quality remediation (structure)

## Source

[docs/code-quality/README.md](../code-quality/README.md): 914 findings (67 high, 402 medium, 445 low). Correctness items are phase 44. This phase is the structural rest, in the order the report recommends. Each step is its own PR and must leave behavior unchanged (tests unchanged and green).

## Steps

1. **Error contract.** Add every `errkey.Default` key to the registry and `docs/error-keys.md`; give `convert.Err`, `Violations`, `invalid()` and payments-worker `fail()` keys; gateway `common.*` keys listed. Console and SDK errors become translatable. Extend `errors.test.ts` so a missing key fails the build.
2. **Shared packages.** `pkg/usage` (one usage-event rule shared by aggregator, limits-svc, analytics-svc, ending the three diverging copies), `pkg/cachex` (tenant and meter caches), `pkg/app` (bootstrap, DB pool), `period.Valid`, `money.FormatMinor`, `pkg/testkit` for harnesses. Also the successor rule that has three copies with different coupon behavior.
3. **ADR 0020 moves.**
   - plans-svc: gather `subscriptions` writes into one module; `admin` and `webhooks` become modules.
   - billing-svc: `runs` and `saga` into modules (`billingruns`, `rerating`, `creditnotes` stop being thin wrappers).
   - Other services: split single "service" packages (assets, ingest, payments-worker `webhook`).
   - Gateway: dashboard and overview math moves to the owning services' `query` modules.
4. **Cleanup.** Dead code and unused exports; one Dockerfile; long functions split (`buildDashboard` 335 lines, `runs/rate.go` 892, `webhook.Server` 818, `TestCorrectness` 316, `DashboardView.vue` 342); silent `_ =` errors handled or logged.
5. **Web.** The 62 medium findings in `web-admin.md`, grouped by feature folder.

## Rule

No step starts while `main` is red. A step that finds a real bug stops and files it as an addition to phase 44 instead of fixing it inside a refactor.

## Status

- Step 2 (shared packages) is done for: `pkg/usage`, `pkg/cachex` (with `pkg/meters`, `pkg/tenantinfo`), `pkg/app`, `pgxx.Settings`/`MigrateIfAsked`/`OpenPools`, `period.Valid`, `money.FormatMinor` and friends, `pkg/testkit`, the successor rule (`changes/command.SwitchTo`), `errkey.ParseUUID`/`domain.ParseID` and `pkg/page`. What each one replaced, and the stricter rule chosen where the copies had diverged, is in [ADR 0020](../adr/0020-modules-cqrs-errors-config.md).
- Left for later steps: gRPC client pool (theme 3), `tenantinfo` use in billing/gateway (theme 5), the Kafka offset store, outbox DDL, chaos hooks, Dockerfiles, `SubscriptionInterval` mappings, `pkg/strx` helpers, OTel instrument builder and idempotency-key rules (themes 10 to 19 of `cross-service.md`).
- Step 3/4, invoice-worker: `render.PDF` split into draw helpers with named layout constants, `Logos` narrowed to a `Get`-only interface, blob config helper in `main.go`. The lock-map race in the findings is already fixed on main (`workerx.KeyedMutex` counts holders and waiters), so no bug PR.
