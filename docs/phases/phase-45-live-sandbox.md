# Phase 45 — Live and sandbox that cannot be confused

## Why

Today an app has a sandbox tenant and a live tenant ([ADR 0011](../adr/0011-sandbox-environment.md)), and that is sound for isolation. What is thin is everything around it: nothing stops a half-configured live tenant from taking keys, the console can show live zeros next to sandbox data without saying which is which, config built in sandbox must be retyped for live, and the guarantee "a sandbox call never reaches a real provider" is one code path, not a layered one.

## Principles

1. **The tenant stays the unit of isolation.** No `environment` column on tenant tables (ADR 0011 holds). New guards sit at the edges.
2. **Fail closed.** A request, command or event whose environment is missing, or disagrees with its tenant's, is refused.
3. **Live is earned.** It is activated, not created.

## Scope

### A. Go-live gate
- `tenants.status` for a live tenant: `inactive → active`. A new live tenant is `inactive` and has no keys.
- Activation checklist, computed server-side (`GET …/go-live`): owner e-mail verified; legal/billing profile (name, country, tax id where required); at least one payment provider connected in live mode and its health check passed; at least one plan published; terms accepted. `POST …/go-live` activates only when every item is true; an operator can also suspend.
- Live keys (`qk_live_…`) and live webhooks are refused while `inactive`.

### B. Keys
- Prefix encodes the environment and is checked against the tenant's environment on every request (`qk_test_` on a live tenant is a hard 401, not a lookup miss).
- Restricted keys (scopes: ingest-only, read-only), `last_used_at`, rotation with an overlap window (old key valid N hours), revoke.

### C. Layered environment guard
- `pkg/env` is the single place that parses, compares and requires an environment; replaces the string literals scattered over services.
- gateway: tenant environment from the key, never from a header the caller sets. Commands and events carry `environment`; billing-svc, payments-worker and invoice-worker assert it equals the tenant's.
- payments-worker: sandbox ⇒ mock-psp (exists); add the reverse, live ⇒ never mock-psp, and a provider connection has `livemode` that must match. Startup refuses `PAYMENTS_LIVE_PROVIDER=mock`.
- Invoice number series `SBX-` vs real is enforced by a CHECK, not by convention.
- Cross-environment ids: a sandbox invoice/customer id sent to a live route returns 404, with a test per resource family.

### D. Promote configuration sandbox → live
- `POST …/promote` with a dry run that returns a diff: meters, plans and prices, taxes, coupons, webhook endpoints (secrets not copied; new secret generated), mail templates. Never customers, usage, invoices or payments.
- Idempotent (matched by stable key), one direction, audited; a conflict on a changed live object is shown, not overwritten.

### E. Sandbox lifecycle
- `POST …/sandbox/reset`: async job that clears customers, usage, invoices, payments and keeps configuration. Rate limited; only for sandbox.
- Sandbox retention TTL (config, default 90 d of inactivity: data purged, config kept).
- Scenario catalogue page: the `pm_sandbox_*` tokens with what each does, and one-click "simulate" on an invoice.

### F. Console
- Environment switcher in the top bar, always visible; sandbox has a persistent banner and a different accent stripe; the choice is part of the URL (`/app/t/<tenant>/…` already is) and never silently flips.
- Org home: remembers the last environment per app; when live is empty or inactive it shows the go-live checklist, not zeros (this also fixes the open "org home shows zeros" item).
- No view mixes environments; cross-environment totals are explicitly labelled and off by default.

### G. Deployment
- `PAYRUDDER_ALLOWED_ENVIRONMENTS` enforced in plans-svc and gateway (exists for the demo profile): test that `values-sandbox.yaml` cannot create a live tenant.
- The dev seed stays sandbox-only and refuses non-local gateways (exists); add a test that no live tenant or key is ever created by it.

## Acceptance

- Property test: random operations with a sandbox key never change or read a live row (RLS + gateway), run in integration.
- E2E: sandbox key against a live route → 401/404; an `inactive` live tenant cannot be issued a key; promote dry run shows a diff and applies it; reset leaves config.
- The payments-worker integration test with a fake real provider keeps asserting zero sandbox calls, and adds zero mock calls for live.
- Error keys and six-language strings for every new refusal.

## Out of scope
Per-tenant simulated clocks (ADR 0011 rejected them), a third "staging" environment.

## Tasks

- [x] A. Go-live gate: live tenants start `inactive`, checklist computed on the server (`GET/POST /app/v1/apps/{id}/go-live`), only an owner activates, an operator can suspend, reinstate or force-activate with a reason; live keys and webhooks refused while inactive (PR #89)
- [x] B. Keys: environment in the prefix checked against the tenant on every request (hard 401), scopes full / ingest / read, `last_used_at`, rotation with an overlap window, revoke (PR #89)
- [x] C. Layered guard: `pkg/env`, fail-closed workers and billing, `PAYMENTS_ALLOW_MOCK_LIVE`, provider `livemode`, invoice number series CHECK, 404 for ids of the other environment with a test per resource family (PR #110)
- [x] D. Promote sandbox to live: diff, dry run, only adds, conflicts shown and left alone, stale check by hash, audit table with a one-way trigger (PR #113)
- [x] E. Sandbox lifecycle: reset job across plans, ingest, aggregator, analytics, billing and payments with a coverage test per service, retention sweep, scenario catalogue and simulate (PR #127)
- [x] F. Console: environment switch always visible, sandbox stripe and banner, the environment is the address's then the last used then live when active, go-live checklist instead of zeros, promote preview and apply, reset and scenarios page, key scope / last use / rotation, six languages including right to left (PR #154)
- [x] G. Deployment and tests: `values-sandbox.yaml` cannot allow live (rendered chart read through `pkg/env`), plans-svc sandbox-only product path, the dev seed never touches a live tenant or makes a key (whole seed against a recording gateway), end-to-end acceptance through the gateway
- [x] H. Follow-up: the owner finishes the checklist alone (billing profile, terms, live provider with a health check and an audit trail), operator tenant pages show the sandbox marking, lazy pages no longer throw when left early

## Where each acceptance item is tested

| Acceptance | Test |
| --- | --- |
| Random operations with a sandbox key never change or read a live row | `services/plans-svc/internal/itest/crossenv_integration_test.go` (`SandboxKeyNeverTouchesLive`: random operations, live fingerprint of every tenant table unchanged); gateway `auth_test.go` (`TestAuthenticateRejects`, `TestSandboxOnlyDeploymentRejectsLiveKeys`) |
| Sandbox key against a live route is 401 / 404 | same file (`CrossEnvironmentIDs`, one per resource family); `tests/e2e/live_sandbox_e2e_test.go` |
| An inactive live tenant cannot be issued a key | `golive_integration_test.go`; `tests/e2e/live_sandbox_e2e_test.go` |
| Promote dry run shows a diff and applies it | `promote_integration_test.go`; `tests/e2e/live_sandbox_e2e_test.go` |
| Reset leaves configuration | `sandbox_integration_test.go`, one `sandboxreset` test per service; `tests/e2e/live_sandbox_e2e_test.go` |
| payments-worker with a fake real provider: no sandbox call to it, no live call to the mock | `services/payments-worker/internal/itest/payments_integration_test.go` |
| Error keys and six languages for every refusal | `pkg/errkey` (`docs/error-keys.md`), `web/admin/src/shared/lib/errors.test.ts` |
| `values-sandbox.yaml` cannot create a live tenant | `tests/helm` (`TestSandboxProfileCannotCreateALiveTenant`), plans-svc `SandboxOnlyDeployment` and `SandboxOnlyProductPath` |
| The dev seed never creates a live tenant or key | `tools/payrudderctl/devseed_live_test.go` |
| Console | `web/admin/e2e-mock/sandbox.spec.ts` (Playwright, mocked gateway), `nav.test.ts`, `environment.test.ts`, `golive.test.ts`, `promote.test.ts` |

## Decisions and limits

- **Live is earned, not copied.** Promote never copies customers, usage, invoices or payments, and never a webhook secret.
- **A person finishes the checklist alone.** The owner fills the seller's billing profile (`PUT /app/v1/apps/{id}/billing-profile`: legal name, country, first address line and city, a tax id where the country needs one; one key per missing part), accepts the terms (`POST /app/v1/apps/{id}/go-live/terms`, its own step now, not part of the activation click) and connects a live payment provider (`PUT /app/v1/apps/{id}/go-live/providers/{provider}`) on the go-live page. Only the owner changes anything; every other member reads. The provider connection is live-bound whatever the request says (phase 45 C `livemode` check), accepts only the provider's own documented hosts (a person may not point their secret keys at a host of their own), is refused unless the provider itself accepts the credentials (a read-only call per adapter: iyzico `/payment/bin/check`, Paddle `/event-types`; bank details have nothing to ask), is sealed and never echoed back, and the go-live checklist asks the provider again (30 s cache per version of the settings) so a revoked key stops counting. Every step is audited: `go_live_audit` in plans_db (profile, terms) and `provider_account_events` in payments_db (provider connections, with the person as actor); neither holds a value of the profile or a setting. Sandbox keeps the mock-psp.
- **Operator tenant pages** (`/tenants/<id>`) show the sandbox stripe and banner for a sandbox tenant, like the person's own pages.
- **Unverified against the real providers.** The two health calls are written from the providers' documentation and tested against fakes; neither was run against a real iyzico or Paddle host (no live or test credentials here). If one of them is not the cheapest authenticated read, only `CheckHealth` of that adapter changes.
- **Not verified here.** `tests/e2e/live_sandbox_e2e_test.go` compiles (`go vet -tags e2e`) and its requests, status codes, error keys and field names were read against the gateway's handlers and the proto messages, but it was not run against the compose stack (the machine's Docker VM holds three gigabytes and other projects' containers; a second full stack does not fit). The compose, Railway and chaos changes were only syntax-checked. Ingest `KafkaPausedLosesNothing` and the aggregator integration tests time out producing to Redpanda on this machine, identically on a clean main.
