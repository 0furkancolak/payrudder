# 18 — Quick integration for apps

## The request
List 2 (3): a **fast integration structure for applications**.

## Where we are
#255: a "Connect your app" page per tenant (developers section, with a tour): code to paste in curl, TypeScript, Python, Go and PHP built from the tenant's own meter and a customer id (escaped; only the shape of a key), a webhook verification snippet per language, the API address typed once and kept in the browser, and a checklist (meter, customer, first usage event counted in the last week, polled every 5 s). Unit tests of the snippet generator (incl. injection cases) and mock e2e.

## Gaps
- **The snippets were never executed.** They follow the SDK READMEs; no CI job compiles/runs them against a running stack, so they can rot.
- The SDKs are **pre-release and unpublished** (install lines point at a checkout), so "quick" is not quick: no `npm install @payrudder/sdk`.
- The page cannot create the key (keys are shown once on another page), cannot create the first meter/customer for the person, and the API address is typed by hand (the console doesn't know the public address).
- No framework quick-starts (Next.js, Express, FastAPI, Laravel, Rails…), no usage-event best practices (batching, retries, idempotency keys, clock skew), no test-mode event simulator, no webhook **local tunnel** helper or "send a test webhook" button in the checklist.
- No "integration health" over time (event rate, rejected events with error codes) beyond the first event.
- Onboarding (03) and this page overlap; the relationship is not designed.

## Research first
1. Watch a developer integrate (the owner or a colleague) from sign-up to first event on the real stack and note every stumble.
2. List what Stripe/Paddle quick-starts and "test events" tools provide; choose the first set.

## Decisions needed
Publishing the SDKs (package names, registry accounts) — currently "no releases until complete"; whether to ship framework templates.

## Work
1. CI job that extracts each snippet and runs it against the compose stack (TypeScript, Python, Go, PHP, curl) so they are always correct.
2. Key creation inline (shown once, copied, never stored by the page) and a one-click "create sample meter and customer" in sandbox.
3. "Send a test event" and "send a test webhook" buttons; rejected-event viewer with error codes and fixes.
4. Framework templates (two to start) in a `examples/` directory with their own CI.
5. Publish SDKs when the owner lifts the no-release rule; then real install lines.

## Done means
A new developer reaches a counted event and a verified webhook in under ten minutes on a clean stack, measured with a person, with the snippet CI green.

## Not verifiable here
The ten-minute claim without a person.

## Risks
Snippets drift from the SDKs: the CI job is the guard.

## Review addendum (2026-11-01)
- The install lines are wrong: TypeScript names `@payrudder/sdk` (unpublished, does not resolve) and PHP names `../quotify/sdk/php` (the old repo name). Fix both; add a check that compiles/imports every snippet against the local SDKs (no stack needed).
- Target: integrate in under 10 minutes (plan 22 phase 4 says 30: 10 is the goal, 30 the ceiling).

## Progress (2026-11-02, branch `feat/dx-docs-mcp`)

Done:
- **The snippets are compiled against the local SDKs, no stack needed** (`web/admin/src/features/connect/snippets.sdk.test.ts`, helper scripts in `sdk-check/`): TypeScript is type-checked with tsc against `sdk/typescript/src`; Python is compiled, its imports run and every `client.<resource>.<method>` is looked up on a real client; Go is vetted in a temporary module that replaces the SDK with `sdk/go`; PHP is linted (`php -l`; the `php:8.4-cli` image like `make sdk-php-check` when there is no native PHP) and every class and method it names is looked up in `sdk/php/src`; the curl command is run against a local server that answers like the API, and the JSON that arrives is compared with the values typed in. Every check runs twice, once with ordinary values and once with a hostile customer id and meter code. It runs in the console's test job where the tools exist and in `.github/workflows/snippets.yml`, where a missing tool fails (`SNIPPETS_REQUIRE_TOOLS=1`). Renaming a method in a snippet fails all four language checks (tried).
- **What the first run found, fixed:** the PHP webhook snippet imported `Payrudder\Webhook` (the class is `Payrudder\Webhook\Webhook`); the TypeScript webhook snippet did not compile under `strict` (`process.env.…` is `string | undefined`); a single quote in a customer id ended the PHP string (and a double quote came out with a stray backslash).
- **Install lines are honest.** TypeScript no longer names `@payrudder/sdk` as if it resolved: the line builds the package from a checkout, runs `npm pack` and installs the tarball (checked end to end: `npm ci`, `npm run build`, `npm pack`, `npm install <tgz>` in an empty project, `import { Payrudder, constructEvent }`). The PHP line used the old repository name (`../quotify/sdk/php`); it is `/path/to/payrudder/sdk/php` in the page, in `sdk/php/README.md` and in `docs/18-sdks.md`. Every line starts with "Not on <registry> yet".
- **"Send a test webhook"** under the webhook code of the Connect page (`POST /v1/webhook-endpoints/{id}/test`): choose the endpoint, send, and the page follows that delivery every few seconds until the endpoint has answered (queued, delivered with its status, or not accepted with its status and answer). A fifth checklist step, "a webhook endpoint", turns green when one exists and links to the webhooks page. Six languages (en, tr, de, fr, es, ar); three mock e2e tests.

Defaults (not agreed):
- The test goes to any endpoint of the list, paused or not (the API decides; its answer is shown).
- A delivery whose first attempt failed is shown as "not accepted" with the answer, although Payrudder will retry it.
- Install lines point at a checkout, not at a registry, until the owner lifts "no releases".

Not done (still open from the work list): key creation inline and the sample meter and customer, "send a test event", the rejected-event viewer, framework templates and their CI, integration health over time, publishing the SDKs. The snippets are compiled, not *run* against a stack (except curl): a CI job that runs each one against the compose stack is still missing.

Not verified: the Python, Go and PHP install lines were not executed (they are the lines of the SDK READMEs); the Docker fallback for PHP was not exercised; `snippets.yml` has not run on GitHub; the ten-minute claim needs a person.


## Progress (2026-11-02, branch `feat/sdk-cli-errors`)

Done here: the **webhook tunnel helper and the "send a test webhook" from a terminal** exist as the command line (`payrudder webhooks listen` receives, verifies and forwards deliveries to the developer's app, and with `--public-url` registers the developer's own tunnel as a temporary endpoint and deletes it on exit; `payrudder webhooks trigger ping` sends the test event and reports the answer; `events send` sends a usage event and `events tail` follows a customer's usage). See [22](22-platform-audit-and-roadmap.md) G16 for the defaults. No tunnel is provided: a hosted Payrudder needs the developer's own (cloudflared, ngrok). The three console items of the work list (inline key, sample meter and customer, test event) are in their own pull request (branch `feat/connect-first-steps`).

Still open: the CI job that runs each snippet against the compose stack, framework templates, the rejected-event viewer, integration health over time, publishing the SDKs. The install lines still say "not on <registry> yet" (`sdk-publish.yml` exists, manual and a dry run, never run).
