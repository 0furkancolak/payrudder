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
