# Phase 43 — Repair main's CI and the nightly runs

## Goal

`main` has been red since 2026-10-21 and the nightly runs fail every day. Nothing else merges with confidence until this is green. No feature work.

## Scope

1. **`end-to-end (compose)`**: `product-real.spec.ts` "six languages, Arabic RTL, dark mode, phone and tablet width" fails with horizontal overflow on `/app/t/<tenant>/payments` at 375 px. Fix the page (table wrapper `overflow-x-auto`, `min-w-0` on flex children), not the test.
2. **Nightly `correctness (1M events, chaos monkey)`** fails after ~1000 s. Read the verifier's `errors` list, find whether it is a product bug (duplicate or lost usage) or a harness bug (timeout, chaos too aggressive), and fix the cause.
3. **Nightly `load scenarios (k6, short)`** fails. Same triage: threshold too tight for the CI runner, or a regression.
4. `make ci-local` (lint, unit, integration, generated-code check, web tests) is the gate before every merge; document it in `docs/10-local-dev.md` if it is not already.

## Acceptance

- `ci` on `main` green; `nightly` green twice in a row.
- A regression test for the overflow (Playwright, 375 px, every product route in a list so a new page is covered automatically).

## Not in scope

Changing thresholds to hide a real regression. If a threshold is moved, the PR says why and records the measured number.
