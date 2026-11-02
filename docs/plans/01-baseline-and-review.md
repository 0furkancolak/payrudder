# 01 — Baseline: make main green and review what was merged

## The request
Not in the lists; it is the precondition for them. The owner said the work was moving too fast and not coming out well.

## Where we are

**Status 2026-11-02 (branch `feat/prod-readiness-2`).** Everything found on 2026-10-28 (below) was fixed: the lint findings and the PHP style in #259, the eleven mock e2e tests in #246 and #259 (the first completed green `ci` on main after the Ops merge was #259, 2026-10-28 21:26 UTC), the stale statements of this plan in this change. What is done:

- **`make ci-local` matches CI**: besides lint, `make gen` with a clean diff, unit, alert-rule and integration tests and the console it now runs `make sdk-check` (the Go, TypeScript, Python and PHP SDK jobs: `sdk-go-check`, `sdk-ts-check`, `sdk-python-check`, `sdk-php-check`), `make test-docs` and `make mcp-check`. Each of the new targets was run on its own and passed on 2026-11-02 (Go SDK, TypeScript SDK, Python SDK on Python 3.15, docs-site, mcp); `sdk-php-check` needs Docker and was not run in this change; the whole `ci-local` was not run end to end (it takes hours with the integration tests). It is documented in [`docs/10-local-dev.md`](../10-local-dev.md). Not in it, on purpose: the stack-based jobs (`e2e`, `chaos`, `kind`), the `backup` and `helm` jobs, `einvoice-schematron` and `snippets` (each has its own target or runs only in CI), and the matrix of language versions (Node 20/22/24, Python 3.10/3.13: locally you get the one you have).
- **The review table exists**: [`docs/code-quality/review-236-257.md`](../code-quality/review-236-257.md) (#236–#257 one row each, #258–#330 one line each), made from `gh pr view` and the code on main, nothing re-run. Its findings are assigned: those still open are listed in the table's last column and sit in plans 02, 08, 10, 11, 12, 15, 16, 19, 20.
- **"Read GitHub CI after every merge" could not be done**: `ci` was configured to cancel an in-progress run for the same ref, and main merges came minutes apart, so of the latest 60 `ci` runs on main 49 were cancelled, 8 failed and 2 passed (the review table has the numbers, read on 2026-11-02). **`ci.yml` no longer cancels runs on main** (#334; a pull request's older run is still cancelled by its newer push). That first change (`cancel-in-progress: false` for main) was not enough: with one concurrency group for the branch GitHub still replaces a run that is *queued* behind a running one, so merges minutes apart still lost runs. **Since #347 every commit on main has a group of its own** (`ci-<ref>-<sha>`, the same for `sdk-*`, `docs`, `mcp`, `examples`, `snippets*`, `einvoice-schematron`): neither a running nor a queued run of a merge is replaced by the next merge, for every workflow, and a pull request's older run is cancelled as before (`deploy-console` and `sdk-publish` never cancel). **What is true now (read with `gh run list --branch main --workflow ci --limit 60` on 2026-11-02 around 16:35 UTC):** of the latest 60 `ci` runs on main 10 passed, 15 failed, 30 were cancelled and 5 had not finished; the newest green one is run #1098 (commit 498b154f, 15:11 UTC). The cancelled runs are not the old replacement bug (a per-commit group cannot be replaced by a newer run): the reviewer **cancels the queued runs of superseded commits by hand after a burst of merges**, because the runners cannot work through a run for each of twenty commits. So a cancelled `ci` run on main means "not run, superseded", never "green", and a commit inside a burst has no verdict of its own; the verdict of a burst is the run of its last commit, and what is red there is fixed before the next burst. Failures read on 2026-11-02: the `integration tests` job of run #1102 (`TestPlansSvc/RLSIsolationEveryTable/tenant_mail_routing`, fixed by the next commit, whose own run was then cancelled as superseded) and the console (`web`) job of the runs #1093 to #1102, whose first failing step was not read for each. `nightly` (a schedule, so it only runs on main) was red every night from 2026-10-20 to 10-28, green from 10-29 to 11-01, and red on 11-02 because the `cells` jobs of billing-svc and payments-worker failed (its `chaos-1m` and `k6` jobs passed on all four of the last nights; the failing `cells` logs were not read); `nightly-providers` has no run (it skips without secrets). The state of main changes with every merge: read it again, do not copy these numbers.

**Found on 2026-10-28 (the cause of this plan, all fixed since):** `ci` red on main since the "Ops" merge (#254): gofmt on `services/plans-svc/internal/grpcapi/register.go` and `internal/support/support.go`, `errorlint` on `internal/changes/command/pricechange.go:94`, `staticcheck` QF1001 on `pkg/mailx/resend.go:185` (fixed in #259); `sdk-php` red since #253 because the PHP models and resources were written by hand (the generator check is `make sdk-php-check`; there is no `make gen-php`); `ci` cancelled on several merges (#253, #255, #256); eleven mock e2e tests failing since the page-header change (#243); and a macOS toolchain whose cgo linking was broken (the Makefile now picks an SDK that links, so `-race` works locally). Twenty PRs (#236–#257) were merged in one day, most with a single pass of local tests.

## Gaps
- ~~No review of the merged work as a whole; each PR was judged by its own tests.~~ The review table is written (above).
- ~~Generated artefacts that CI checks were regenerated by hand each time; drift checks have not been run as CI runs them.~~ `make ci-local` runs `make gen` with a clean diff and the four SDK checks.
- ~~Local verification does not match CI (lint, race, PHP style).~~ It does, except for the jobs listed above as not part of it.

## Research first
1. List every failing job of the last ~15 runs of `ci`, `sdk-*`, `nightly`, `deploy-console` with the first error of each (`gh run view --log-failed`). Write the list into this plan.
2. Run in a clean checkout the exact commands of `.github/workflows/ci.yml` (and the SDK workflows), in the same container image where one is used. Record which pass.
3. For the eleven e2e failures: find the commit that broke each (bisect on #243's merge) and whether the test or the page is wrong.

## Decisions needed
None, except: may the other session's #243 be corrected (the failing tests are its page changes) or should it be asked first? (The same checkout is used by another Claude session.)

## Work
1. Fix the lint findings (gofmt, errorlint with `errors.Is`, QF1001) and push to main through a PR; confirm `ci` green on GitHub. **Done (#259).**
2. Run `php-cs-fixer fix` with the project's config (install composer via Docker if needed) on `sdk/php`, commit the result; make the PHP generator runnable in a container so hand-written models are never needed again. **Done: the target is `make sdk-php-check` (#259).**
3. Fix or update the eleven e2e tests, one commit each with the reason. **Done (#246, #259).**
4. Add a local `make ci-local` that runs the same lint/test/gen-drift commands as CI (in Docker where needed) and use it before every merge from now on. **Done, and since 2026-11-02 it includes the SDKs, docs-site and mcp.**
5. Review pass over #236–#257: for each PR, read the diff once more for correctness, security and consistency (error keys translated, i18n in six languages, docs updated, tests that assert behaviour not implementation). Write findings into a table in this file; each finding becomes a task in the plan it belongs to. **Done: [`review-236-257.md`](../code-quality/review-236-257.md) (the table lives there, not in this file); diffs were read in depth for #242 only, the rest from the PR bodies, file lists, later PRs and the code on main.**
6. Re-run `nightly` once and read it. **Not done here** (it needs GitHub; nobody has read a `nightly` result since the plan was written).

## Done means
- `ci`, `sdk-go`, `sdk-python`, `sdk-typescript`, `sdk-php`, `docs`, `mcp`, `deploy-console` all green on main, seen on GitHub, not assumed. **Open, and not true on 2026-11-02:** the newest runs on main of `ci`, `docs`, `sdk-python` and `sdk-php` were red or not finished, and `nightly` had failed that morning; `sdk-go`, `sdk-typescript`, `mcp` and `deploy-console` had passed on their newest completed run. This item stays open until one burst of merges ends with every workflow green on its last commit.
- `make ci-local` exists and matches CI. **Met.**
- The review table is written and every finding is assigned. **Met.**

## Not verifiable here
GitHub-only behaviour (caches, matrix images) until it runs there.

## Risks
The review will find real defects in what was merged; that is the point. Some will need a plan reopened (see 02, 06, 08, 12, 15).

## Review addendum (2026-11-01)
- **Status, not plan:** the lint findings were fixed in #259 (`pricechange.go` uses `errors.Is`); `make ci-local` exists (Makefile); the PHP generator check is `make sdk-php-check` (there is no `make gen-php`). The "Where we are" text above was rewritten on 2026-11-02 so that it no longer says otherwise.
- ~~**Gap:** `ci-local` runs only the PHP SDK check.~~ Closed 2026-11-02: it runs all four SDKs, `make test-docs` and `make mcp-check`, and `sdk-php-check` is documented in `docs/10-local-dev.md`.
- **Overlap:** this plan duplicates the open "phase 43" in `docs/phases/ROADMAP.md` (red main, nightly failing); treat them as one item.
- **Defined:** "baseline part 2-4" are (2) the review table of #236-#257 (done: `docs/code-quality/review-236-257.md`), (3) CI parity (`ci-local`, done), (4) a standing rule that GitHub CI is read after every merge (the rule is in the plans README; it could not be followed while main's runs were cancelled, fixed in `ci.yml` on 2026-11-02 and in the per-commit groups of #347; reading main after merges is still a person's job).
