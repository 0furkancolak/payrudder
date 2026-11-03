# 18 — Quick integration for apps

## The request
List 2 (3): a **fast integration structure for applications**.

## Where we are
#255: a "Connect your app" page per tenant (developers section, with a tour): code to paste in curl, TypeScript, Python, Go and PHP built from the tenant's own meter and a customer id (escaped; only the shape of a key), a webhook verification snippet per language, the API address typed once and kept in the browser, and a checklist (meter, customer, first usage event counted in the last week, polled every 5 s). Unit tests of the snippet generator (incl. injection cases) and mock e2e.

## Gaps
- ~~**The snippets were never executed.** They follow the SDK READMEs; no CI job compiles/runs them against a running stack, so they can rot.~~ They are compiled against the local SDKs (`snippets.yml`) and the `send` code of curl, TypeScript, Python and Go is run against a contract-checking fake made from the OpenAPI file (`snippets-run.yml`); see the progress parts below. **Still open:** a run against the compose stack and PHP run for real (only linted).
- The SDKs are **pre-release and unpublished** (install lines point at a checkout), so "quick" is not quick: no `npm install @payrudder/sdk`.
- The page cannot create the key (keys are shown once on another page), cannot create the first meter/customer for the person, and the API address is typed by hand (the console doesn't know the public address). *(2026-11-02: the key, the sample meter and customer and a test event are now made from the page; the address was still typed; since then it comes from the deployment's `PUBLIC_API_URL` when that is set, see the last progress section. See Work 2 and 3.)*
- ~~No framework quick-starts (Next.js, Express, FastAPI, Laravel, Rails…)~~ `examples/` has Express, FastAPI, Go `net/http`, Next.js, Laravel, Rails and Django, each with a smoke test in CI (other frameworks are not there: Spring, ASP.NET and the rest have no SDK and would need both a client and a signature check written out). ~~no test-mode event simulator~~ the Connect page sends a sandbox test event. ~~no webhook **local tunnel** helper or "send a test webhook" button~~ the button is on the page and the CLI's `webhooks listen` receives and forwards deliveries (a tunnel itself is not provided). **Still open:** a page of usage-event best practices (batching, retries, idempotency keys, clock skew).
- No "integration health" over time (event rate, rejected events with error codes) beyond the first event.
- Onboarding (03) and this page overlap; the relationship is not designed.

## Research first
1. Watch a developer integrate (the owner or a colleague) from sign-up to first event on the real stack and note every stumble.
2. List what Stripe/Paddle quick-starts and "test events" tools provide; choose the first set.

## Decisions needed
Publishing the SDKs (package names, registry accounts) — currently "no releases until complete"; whether to ship framework templates.

## Work
1. CI job that extracts each snippet and runs it against the compose stack (TypeScript, Python, Go, PHP, curl) so they are always correct. **Done against a fake made from the OpenAPI file instead of the stack (default, not agreed: see the `feat/dx-docs-2` progress part), for curl, TypeScript, Python and Go; PHP is linted, not run; the compose stack is not used.**
2. Key creation inline (shown once, copied, never stored by the page) and a one-click "create sample meter and customer" in sandbox. **Done 2026-11-02 (branch `feat/connect-first-steps`):** on the Connect page a person who has the app can create an **ingest-only** key for the environment on screen (the secret is shown once, held in memory until the person says it is copied, never put in storage or in the code on the page; the "An API key" step turns green from the keys that exist); in the sandbox, one click makes the meter `api_calls` and the customer `cus_1` for whichever of the two is missing (never on top of what exists, never in live). Not done at that time: the API address was typed by hand (now from `PUBLIC_API_URL`, see the last progress section). An operator looking at a tenant has no app of their own on that page and is not offered a key (the operator's key route makes a key of full power, which is not what a quick start should hand out).
3. "Send a test event" and "send a test webhook" buttons; rejected-event viewer with error codes and fixes. **Test event done 2026-11-02 (same branch):** a sandbox-only button sends one event of quantity 1 for the chosen meter and customer, as the signed-in person (`POST /v1/events` through the console session, which the gateway allows for an owner or admin and refuses a viewer), and shows accepted or the rejected `error_code` with the API's own message (English: the 11 codes have no per-language hint yet); the checklist's own poll turns "counted" green. The test webhook was done before (above). The rejected-event viewer is not.
4. Framework templates (two to start) in a `examples/` directory with their own CI. **Done: seven (Express, FastAPI, Go `net/http`, Next.js, Laravel, Rails, Django), one job each in `examples.yml`.**
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
- **The snippets are compiled against the local SDKs, no stack needed** (`web/admin/src/features/connect/__tests__/snippets.sdk.test.ts`, helper scripts in `__tests__/sdk-check/`): TypeScript is type-checked with tsc against `sdk/typescript/src`; Python is compiled, its imports run and every `client.<resource>.<method>` is looked up on a real client; Go is vetted in a temporary module that replaces the SDK with `sdk/go`; PHP is linted (`php -l`; the `php:8.4-cli` image like `make sdk-php-check` when there is no native PHP) and every class and method it names is looked up in `sdk/php/src`; the curl command is run against a local server that answers like the API, and the JSON that arrives is compared with the values typed in. Every check runs twice, once with ordinary values and once with a hostile customer id and meter code. It runs in the console's test job where the tools exist and in `.github/workflows/snippets.yml`, where a missing tool fails (`SNIPPETS_REQUIRE_TOOLS=1`). Renaming a method in a snippet fails all four language checks (tried).
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

## Progress (2026-11-02), branch `feat/dx-docs-2` (with plan 19)

Done:
- **The quick-start snippets are run, not only compiled.** `docs-site/fakeapi.mjs` is a stand-in for the API that is made from `api/openapi/v1.yaml`: it matches the route and verb, wants a bearer key, checks the body against the request schema (required fields, types, enums, patterns, formats, limits) and answers with the file's own example of the success response (one result per event for usage events). `docs-site/snippets.run.test.mjs` runs the `send` code that `snippets.ts` makes for **curl, TypeScript (the SDK built from `sdk/typescript`, run as `.mts`), Python (`sdk/python`) and Go (`sdk/go` through a `replace`)**, with ordinary values and with a hostile customer id and meter code, and checks that the right requests arrived (the customer first, then the event), with the key the snippet shows or reads, a body that fits the contract and the customer and meter exactly as typed. `.github/workflows/snippets-run.yml` runs it on a change to the snippets, the fake, the OpenAPI file or any SDK, with every tool required (`SNIPPETS_REQUIRE_TOOLS=1`). **What it proves:** the code a developer pastes starts, reaches the address it was given, sends a request the contract accepts and reads the answer. **What it does not prove:** that the real gateway accepts it (no database, no key check, no business rule). Tried: renaming `event_time` in a snippet fails curl and TypeScript with the contract's own message. PHP stays linted and looked up (`snippets.yml`), not run: the SDK has no autoloader outside Composer.
- **Framework templates under `examples/`:** `express/` (Express 5, TypeScript, Node 22.18+ runs `server.ts`), `fastapi/` (the SDK's async client) and `go-nethttp/` (standard library plus the SDK). Each has `POST /usage` (one usage event with an idempotency key) and `POST /webhooks/payrudder` (the raw body, verified before anything is trusted), a README that says what to keep when changing it, and installs the SDK from this checkout (nothing is published). `examples/smoke.mjs` starts an app next to the fake API and checks over HTTP that a delivery signed the documented way is accepted, that a changed body, a wrong secret, a ten-minute-old timestamp and a missing header are refused, and that `POST /usage` reaches the API as a valid event with the app's key. `.github/workflows/examples.yml` compiles each (`tsc`, `compileall`, `go vet`) and runs the smoke test. Tried: dropping `express.raw` makes the Express smoke test fail on the signed delivery.
- **Hints for the event error codes**, in the console and in the docs. The plan said 11 codes; the API sends 16 now (the correction codes were added). `connect.eventErrors.<CODE>` in the six locales says what to do about each; the Connect page shows the hint under a rejected test event; the errors page of the developer site gets a "What to do" column made from the **English locale** (one source, so the two cannot disagree). A type check fails the console build when the API gains a code that has no hint, and a docs test fails for a code without one.
- **The API address from configuration.** The Worker var `PUBLIC_API_URL` (also read by `vite preview`, so the compose and Railway containers) becomes `window.__PAYRUDDER__.api` in `/env.js`, cleaned to an http(s) address (no credentials, query or fragment). The Connect page starts its code with it unless the person typed another address in this browser (a typed address wins). Compose defaults it to the gateway's own `PAYRUDDER_PUBLIC_URL`; `wrangler.jsonc` has placeholders per environment; the Railway README lists it. Unset: the page asks, as before.

Defaults (default, not agreed):
- **A fake made from the OpenAPI file, not the compose stack, is the CI gate.** A job against the stack needs the whole compose file, a database, a sandbox key and minutes of start-up on a shared runner; the fake runs in seconds and fails on the same drift in the contract. Running the snippets against `make dev` by hand stays the check for the gateway itself.
- The templates are Express, FastAPI and Go `net/http` (the three of the work list); the SDK is installed from the checkout, so no template names a registry.
- `PUBLIC_API_URL` is its own setting, not `API_ORIGIN`: the console may proxy to a private address that a developer cannot call.
- The hints are one sentence of advice per code, written from the codes' meanings in the OpenAPI file and the docs' errors page.

Not done: a job that runs the snippets against the compose stack; PHP run for real; other frameworks (Next.js, Laravel, Rails); the rejected-event viewer; integration health over time; a local tunnel helper beyond the CLI's; publishing the SDKs.

Not verified: the `snippets-run` and `examples` workflows ran green on GitHub in the pull request (the three templates and the four snippet languages, on Python 3.12 and Node 24), but nothing has run against a real gateway; the FastAPI template was also run locally on Python 3.15; no template was run against a real gateway or with a real key; the fake accepts what the OpenAPI file says, so a rule the file does not state (the 35-day limit, a unique `external_id`) is not tested; the hints in Turkish, German, French, Spanish and Arabic were written by the same author as the English ones and read by no native speaker; the Worker was not deployed (its `/env.js` is tested as a function and the page was run against a replaced `/env.js` in the mock e2e); the ten-minute claim still needs a person.

## Progress (2026-11-02), branch `docs/guides-migration-templates-3`

Done: **two more framework templates under `examples/`**, wired like the first three (`examples.yml` has a job each, ending in `examples/smoke.mjs` against the contract-checking fake):
- `nextjs/`: Next.js 16 (App Router, TypeScript, Node 22.18+), route handlers `POST /usage` and `POST /webhooks/payrudder` (the raw bytes through `request.arrayBuffer()`), `GET /healthz`. The client is made on first use because `next build` loads every route without the settings. Two lines of `next.config.ts` exist only because the SDK is installed from this checkout (a link outside the folder: `turbopack.root` and `serverExternalPackages`). The job runs `tsc`, `next build` and the smoke test.
- `laravel/`: Laravel 13 (PHP 8.3+) trimmed to an API (`bootstrap/app.php`, `routes/api.php` without a prefix so no CSRF or session stands in front of a webhook, two invokable controllers, `config/payrudder.php`), the PHP SDK as a Composer path repository. Settings are read in the config file and handed to the client, because Laravel does not put `.env` values into the process environment, which is where the SDK looks. Every error is answered as JSON. The job runs `composer update`, `php -l` and the smoke test with PHP's built-in server.
- Tested here: `docs-site/examples.test.mjs` fails when a folder of `examples/` is not in the README's table or has no job of that name, when the webhook routes stop reading the body raw, when a Payrudder setting gets a `NEXT_PUBLIC_` name, or when a version is not pinned. Tried: removing a table row fails it.

Defaults (not agreed): exact versions of the top-level dependencies, no `composer.lock` (the SDK is a path repository that a copy of the folder will not have; FastAPI pins its top level the same way); `php -S` rather than `artisan serve` in the smoke test; the templates install the SDK from the checkout, so none names a registry.

Verified locally: `npm ci`, `tsc`, `next build` and the smoke test for Next.js (Node 24); `composer update` (Docker `composer:2`), `php -l` and the smoke test for Laravel on PHP 8.5, and the app answering `/healthz`, a refused signature and a refused body on PHP 8.4 (Docker). **Not run:** either workflow on GitHub; either template against a real gateway or a real key; Laravel on PHP 8.3 (the lowest it allows); Next.js on Node 22.

Not done: Rails and other frameworks, a job against the compose stack, publishing the SDKs.

## Progress (2026-11-02), branch `feat/cli-dx-3` (CLI)

The command line also creates what a product is built from: `payrudder webhooks create`, `meters create`, `subscriptions create` and `plans publish -f version.json`, so the quick start can be done from a terminal with no console. Details, defaults (the publish file is checked before sending, the three calls without an idempotency key or a unique key are not retried) and what was not verified are in [22](22-platform-audit-and-roadmap.md) G16. Still not in the CLI: coupons, changing or deleting, listing meters and plans.

## Progress (2026-11-02), branch `feat/leftovers-4-quickstart`

Done: **two more framework templates under `examples/`**, wired like the others (a job each in `examples.yml`, ending in `examples/smoke.mjs` against the contract-checking fake, so both run offline and need no account):
- `rails/`: Rails 8.1.4 in API mode (Ruby 3.2+), served by Puma in production mode (which eager-loads, so a constant that does not autoload fails the smoke test, not only a user). **There is no Ruby SDK**, so the app carries what the SDKs would: `app/lib/payrudder/client.rb` (one `Net::HTTP` call to `POST /v1/events`, timeouts, the API's error key kept) and `app/lib/payrudder/webhook.rb` (`t=` and `v1=` parsed, the HMAC over `"<t>.<raw body>"`, a constant-time compare, the five-minute tolerance of `docs/16-webhooks.md`; **an empty secret is refused with an exception**, not accepted, because an HMAC with an empty key can be forged by anyone). The webhook controller reads `request.raw_post`; `filter_parameters` keeps a delivery's `data` out of the request log. Run on Ruby 4.0.6 (smoke test) and Ruby 3.4 (Docker `ruby:3.4`: the app boots in production mode and the signature check accepts a signed body and refuses a changed one; the smoke test itself was not run on 3.4).
- `django/`: Django 6.1.2 (Python 3.12+) with gunicorn, the SDK's synchronous client from the checkout, both views `@csrf_exempt` with the CSRF middleware left on (the case of a real project). `manage.py check` runs in CI without the Payrudder settings.
- **The smoke test is stricter for all seven.** Its delivery was a compact ASCII body, which a handler that parses it and writes it out again reproduces byte for byte in most languages (Rails' params re-serialised passed the old test: found by breaking the Rails controller on purpose). It is now indented and has non-ASCII text (`Çok güzel: café`), so any re-serialisation changes the bytes and the signature no longer fits. Tried: re-serialising the body fails Rails and Django; the five existing templates still pass (Express, FastAPI, Go, Next.js and Laravel were each run again locally).
- `docs-site/examples.test.mjs` also reads the Rails and Django sources: the raw body, the CSRF decision, exact pins, and the Rails check's empty-secret refusal, tolerance and HMAC call against `docs/16-webhooks.md`.

Defaults (not agreed): exact versions of the top-level gems and packages, no `Gemfile.lock` (like the other templates; add yours); Ruby 3.4 in CI; Rails with no database, views or Active Job (the README says to enqueue the work); no template for a language the SDKs do not cover other than Rails (Spring, ASP.NET, Phoenix: each would also need a client and a signature check written out and a CI toolchain; add one when a customer asks).

Not done: a Ruby SDK (the Rails client is not published or generated from the OpenAPI file), a job against the compose stack, publishing the SDKs, the rejected-event viewer, integration health over time.

Not verified: the `rails` and `django` jobs have not run on GitHub; neither template ran against a real gateway or key; the Rails smoke test ran on Ruby 4.0.6 and the app was booted on 3.4, not smoke-tested there; Django ran on Python 3.14 here and CI uses 3.12; `bin/rails server` was started once by hand (development mode: `/healthz` answered and a bad `/usage` was refused), not in CI.

## Progress (2026-11-03), branch `docs/usage-best-practices`

- **"Sending usage well" page** (`docs-site/content/usage-best-practices.md`, Concepts): batching, keys that survive a retry, what to retry, `event_time`, customer first, string quantities, corrections, limits as eventual, sandbox vs live keys. Every rule restates `usage-metering.md`, `idempotency.md` and `errors.md`; nothing new is promised. Docs-site tests pass (58).
- Still open from this plan: a job against the compose stack, PHP snippet run for real, rejected-event viewer, integration health over time, publishing the SDKs.
