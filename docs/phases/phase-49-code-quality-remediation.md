# Phase 49 — Code-quality remediation (structure)

## Source

[docs/code-quality/README.md](../code-quality/README.md): 914 findings (67 high, 402 medium, 445 low). Correctness items were phase 44. This phase is the structural rest, in the order the report recommends. Each step leaves behavior unchanged (tests unchanged and green).

## Steps

1. **Error contract.** Every error a client can see has a key, in the registry and `docs/error-keys.md`; the console translates it.
2. **Shared packages.** One rule or helper in `pkg/` where services had copies.
3. **ADR 0020 moves.** Every service is modules with `command/`, `query/` and one adapter.
4. **Cleanup.** Dead code, one Dockerfile, long functions, swallowed errors.
5. **Web.** The medium findings in `web-admin.md`, grouped by feature folder.

## Rule

No step starts while `main` is red. A real bug found on the way is fixed in a commit of its own, with a test, and called out in the PR (or filed in a phase of its own when the fix needs a decision).

## Status

What each step replaced, and the stricter rule chosen where copies had diverged, is in [ADR 0020](../adr/0020-modules-cqrs-errors-config.md); what became of every billing-svc finding is in [billing-svc-status.md](../code-quality/billing-svc-status.md).

### Done

- **Step 1.** `errkey.Default` keys, `convert.Err`, `domain.Violations`/`Invalid`, the services' `invalid()`, payments-worker `Fail`/`refuse.New` and the gateway's own `gateway.*` and `common.*` keys are all in the registry. `pkg/errkey` fails when a returned key is not listed or has no text in one of the six console locales, and `web/admin/src/shared/lib/errors.test.ts` fails the build for a missing, stale or differently-placeholdered translation. The reference and note of a manual payment have their own keys (`billing.payment.bad_reference`, `billing.payment.bad_note`; they used to report `bad_amount`). The repository has no SDK; the OpenAPI error schema documents `key`.
- **Step 2.** `pkg/usage`, `pkg/cachex` (with `pkg/meters`, `pkg/tenantinfo`), `pkg/app`, `pgxx.Settings`/`OpenPools`, `period.Valid`, `money.FormatMinor`, `pkg/testkit`, the successor rule (`changes/command.SwitchTo`), `errkey.ParseUUID`, `domain.ParseID`, `pkg/page`, `grpcx.NewTenantClient` (every service), `kafkax.PGOffsets`, `pkg/chaos`, `pkg/strx`, `otelx.Instruments` (every service but billing-svc), `subscription.IntervalFromProto`, `health.ServeServer`.
- **Step 3.** aggregator, ingest-svc, assets-svc, limits-svc, analytics-svc, notifier, payments-worker, plans-svc, billing-svc and the gateway are modules wired by one `grpcapi` (or `main`) each; where the service is a gRPC server a test fails when a method is served by no module (not for `IdentityService` and `WebhookService`: their handlers embed `Unimplemented`, so a reflective check cannot tell).
- **Step 4.** One `deploy/docker/go.Dockerfile`; the long functions named in the report are split (`TestCorrectness`, `buildDashboard`, `render.PDF`, `runs/rate.go`, `webhook.Server`, `DashboardView.vue`, `InviteMember`, provider-migration `Start`); swallowed `_ =` errors are handled or logged. Real bugs found and fixed on the way, each with a test: the dunning defaults shown to a tenant (#164), a re-rating credit without a currency (#170), a re-rating that could not be undone (#171), the change preview's timing, address validation walking a map, the gateway's operator-actor header, the batch larger than its share of the rate-limit burst, the unbounded public-page limiter table, the cacheable 404 of an unpublished plan, 413 on six JSON routes, concurrent Google sign-ins, assets-svc background jobs outliving `Run` (and a job interval of zero panicking the ticker), `payrudderctl replay start --wait` panicking on an answer without an id, the chaos monkey racing its own revivals. Already fixed on `main` (the report's "own PR" items): aggregator replay goroutines are waited for, a bad tenant id no longer bypasses the notifier's mail switch, the invoice-worker lock map.
- **Step 5.** The 62 medium findings of `web-admin.md` are done or listed below: shared `useFormat`, environment, dates and storage helpers; `ErrorText` (with `box`) and `firstError`; `FormField`, `PageHeader` (badge and subtitle slots), `AuthMethodPicker`, `CopyField`, `GroupChips`, the shared `Tabs`, `SegmentedControl`, `Donut` and `Table`; `usePresignedOpen`, `useLeave`, `useSeededRef`, `useAttemptKey`; typed request bodies; named limits (`shared/api/limits.ts`, `shared/lib/rules.ts`); narrow cache invalidation and one key factory per feature; the dashboard, credits, subscriptions, accounting and payment-link pages split into cards and sheets; the shell worked out once in the root layout and provided; dead components and exports removed; invoice line kinds, tiers and saga times in the reader's language.

### Left, no product decision needed

Each is a refactor of its own and was not started, for the reason given.

- **Other work streams are in these files.** limits-svc (`apply.go` recursion and its network IO under a lock, counter TTL after a correction, `main.run`), the aggregator (one sqlc package per module, `Lateness` shared with ingest-svc), `pgxx` (shared SQLSTATE constants), billing-svc (`tenantinfo` instead of the six `GetTenant` + `LoadLocation` copies, `otelx.Instruments` in `billingruns`, `invoices`, `replies` and `sagas`, its `syncProvider`), the subscriptions page panels, `ProvidersPage`, `ProviderSheet`, `IntegrationsPage` (live-environment option words, `liveTenant`, the connections table).
- **Go fallbacks that duplicate the YAML default** (notifier `stale()` and poll interval, analytics consumer group and batch, the aggregator's consumer group, assets-svc TTLs, quotas and general types, `SessionTTL` and the other struct knobs nothing sets). The integration harnesses build the services without loading the YAML, so removing the fallback means changing those tests.
- **payments-worker**: one `transfers` package holding the status rule that `handler` and `transfers/query` both apply; a bound on the resolver cache (needs a fake `store.Accounts`); the itest fixture `newEnv` and one HTTP client for its tests (a rewrite of tests).
- **plans-svc**: one sqlc package per module; query packages as module-internal mappers; mail TTL placeholders and `Dispatcher` settings in YAML; one clock per request; `AcceptInvitation`, `syncProvider` and `priceToStored` split.
- **Tests and tools**: a shared HTTP and fixture package for the tests, the `eventually` helper in e2e, the magic-number sweep in `payrudderctl`.
- **Web**: `resolveContext` as a pure function of path, search and apps (the shell is provided once instead); the raw checkboxes (low); a `<QueryState>` for loading, error and empty (each list handles the three cases today).

### Left, needs a decision

- limits-svc: the `lim:ent:*` Redis mirror nothing reads.
- analytics-svc: an unknown `InvoiceFact` kind to the DLQ (a behaviour change).
- Idempotency-key rules (ingest 128 chars and a pattern, billing 128 and 200, assets none): unifying changes what is accepted.
- The gateway's `SubscriptionInterval` month/empty and `AddDate` end-of-month divergences (what a customer sees).
- The paging helper: lists differ on an oversize page (refuse, clip), so unifying changes accepted input.
- `AcceptInvitation` through `openSession` (it would also record a login and delete stale sessions).
- Gateway: ARPU per period instead of per month, the YTD comparison period, the overview's "last 30 days" revenue that is all time, folding a bank transfer's pending row with its receipt in `foldLedger`, treating a `succeeded` charge as paid in `refineState`, the operator and tenant twin handlers, `Limiter.Allow` without its error, the `windowLimiter` rename, `Deps` in sub-structs.
- Web: `window.confirm` (19 uses) to a themed confirm dialog (every e2e spec that accepts the native dialog changes with it); write gating of the shared pages for a viewer (the layout shows a read-only note and the server refuses the write; `fieldset :disabled` would also disable filters and paging); one palette for `Badge` (operator) and `StatusPill` (product), which disagree on `void` and `expired`; one set of period presets (the accounting page counts whole months, the dashboard and `dateRange` count to today).

### Decided against

- Moving the gateway's dashboard and overview math into the owning services: it composes billing, plans, payments-worker (HTTP only) and limits, so no one service owns it, and a move needs new RPCs and fakes in tests this phase keeps unchanged. The assemble functions take plain data, so the move is a copy when wanted.
- Charging malformed ingest bodies: `TestIngestEventsBadRequests` pins that rejected requests are not charged.
- Outbox DDL in `pkg/outbox`: the service migrations are applied history, and a `SchemaSQL` constant nobody can reference adds nothing.
- E-invoice key constants in `pkg/einvoice`: `refuse.New` and `errkey.New` take the key as a literal because the generator of `docs/error-keys.md` finds it there.
- A shared ticker helper: the four loops differ in whether they run first and what a failure does.
- Web: `CurrencyTabs` stays hand-written (its arrow-key map is tested and its tabs carry the "own currency" mark); the accounting toggle buttons stay `aria-pressed` buttons (they are toggles, not tabs); the `as never` casts of router `search` and `to` stay (the router's types are the deliberate decision of `router.ts`); the dev-only "Dev seed" hint and the sign-in page's decorative preview figures stay untranslated.

### Verified here

go vet and go test for `pkg`, the gateway, assets-svc, ingest-svc, payments-worker, plans-svc, billing-svc, `tools` and `tests`; `-tags integration` for payments-worker, assets-svc and plans-svc against Docker; `vue-tsc`, eslint, vitest (582) and the full mock Playwright run (380). Not run: the Kafka-dependent integration suites (ingest-svc `TestIngestSvc` fails on the outbox drain without Kafka), the `chaos` suites (vetted with the tag only).
