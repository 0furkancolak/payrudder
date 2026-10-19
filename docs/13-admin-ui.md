# Operator console

`web/admin` is a small Vue 3 and TypeScript console (Vite, TanStack Router, TanStack Query, vue-i18n) for an operator: it makes the state of the system visible during a demo and
offers the two actions an operator needs most, redriving dead letters and reading a saga. It is served by the Compose `app` profile at
<http://localhost:3001> and talks to **the gateway only**: no database, broker or service port.

| View | What it shows |
|---|---|
| Sign-in | The operator token (`QUOTIFY_OPERATOR_TOKEN`), kept in the tab's session storage. It is checked by asking the gateway for the tenant list. |
| Tenants | Every tenant: environment, tier, currency, time zone. |
| Tenant | Its invoices (kind, period, total, status, saga state), and a lookup for replay and re-rating jobs by id. |
| Invoice | Lines, a link to the PDF (presigned for five minutes) and the **saga timeline**: every transition with its cause and time, from `saga_events`. |
| Playground | The guided sandbox walk-through described below. |
| Dead letters | The depth of every RabbitMQ `*.dlq`, a peek at one queue's messages, and **Redrive** (with a confirmation) which moves them back with the retry counter reset. |

![The saga timeline of a paid invoice](img/admin-invoice-saga.png)

![The dead-letter queues](img/admin-dead-letters.png)

The local operator token is `local-operator-token-change-me-0123456789` (set by `QUOTIFY_OPERATOR_TOKEN`, which the Compose file defaults to that value). It is a development default: change it anywhere that is not your laptop.

## Playground

`/playground` is a guided walk through the product for someone who has only the operator token: create a sandbox account (currency, time zone and grace period are required, nothing is
preselected), create a meter, a plan with a base fee and a per-call price, a customer and a subscription, choose the mock payment outcome, send usage, close the period, and watch the
invoice go through its saga (with a link to the full timeline). It calls the same public API a customer would, with the sandbox tenant's own `qk_test_` key, so it exercises the real system end
to end; it never touches a live tenant. A declined payment leaves the invoice open in `PAYMENT_DECLINED` while dunning retries it. Playwright runs both the paid and the declined path.

## How it is built

- **Feature-based layout.** `src/features/<feature>` owns everything about one part of the product: its pages (`*.vue`), its TanStack Query hooks (`api.ts`), its route
  declarations (`routes.ts`) and a small public `index.ts`. The features are `auth`, `tenants`, `invoices`, `dead-letters` and `playground`; `src/app` only composes them (router, query client,
  i18n, layout) and `src/shared` holds what several features need (the API client, the session token, money and label helpers). A feature imports another only through its `index.ts`
  (the tenant page shows `invoices`' table).
- **TanStack Router** (`@tanstack/vue-router`, code-based routes). Every feature exports a function that creates its routes under a parent; the app mounts all but the sign-in page under one
  pathless route whose `beforeLoad` redirects a visitor without an operator token. The router is not registered for global type inference: registering it makes pages and the route tree
  depend on each other's types, a cycle TypeScript reports as an implicit `any`, so route paths are plain strings and the Playwright tests are what catches a wrong one.
- **TanStack Query** (`@tanstack/vue-query`) for every read and write. `unwrap()` turns an `openapi-fetch` result into data or an `ApiError` (the gateway's code and message), which is what a query function
  must throw; queries are keyed per tenant and invoice, a refused token or a missing resource is not retried, and the playground's invoice list polls while the saga runs.
- **Languages.** `vue-i18n` with six locales (English, Türkçe, Deutsch, Español, Français and العربية, which switches the page to right-to-left): `src/locales/*.json`. The language is the
  saved choice, else the browser's first supported language, else English; the switcher in the navigation changes `lang`, `dir` and the title, and remembers the choice. Backend enum values
  (invoice status and kind, environment, tier) are translated when the locale has a label and shown as they are when it does not, so a new backend value still appears. Amounts are
  formatted from minor units with the currency's exponent and the locale (`shared/lib/money.ts`; the digits are moved as text, never through a float). A unit test fails if a locale misses a key
  or changes a `{placeholder}`. Technical identifiers (saga states, ids, causes) are not translated.

- **Typed client.** `npm run gen` generates `src/lib/schema.d.ts` from [`api/openapi/v1.yaml`](../api/openapi/v1.yaml) with `openapi-typescript`;
  the client is `openapi-fetch` over those types, so a route or field that does not exist in the spec does not compile. CI regenerates the file and fails on a
  diff, and the ESLint config forbids `any`. The gateway does not serve the spec: the file in the repository is the source.
- **Same origin.** The browser calls `/gw/...`; nginx in the image (Vite's dev server in development) forwards it to `GATEWAY_URL` with the `Authorization` header and resolves the
  gateway on every request, so a recreated gateway is found again. The gateway needs no CORS configuration.
- **Operator routes added for it** (all behind the operator token, in the OpenAPI file): `GET /admin/v1/tenants`, `GET /admin/v1/tenants/{tenant_id}/invoices[/{invoice_id}[/pdf|/saga]]`,
  `GET /admin/v1/dead-letters`, `POST /admin/v1/dead-letters/redrive`. The tenant-scoped reads run the billing call in the tenant named in the path; the dead-letter
  and saga operations are `BillingAdminService` RPCs (billing-svc owns the saga log and the RabbitMQ topology).
- **Scope kept small on purpose.** Read-only views plus the redrive. The Kafka dead-letter topics are `quotifyctl dlq --broker kafka`; starting a replay or a re-rating is the
  admin API or `quotifyctl`, the console only looks jobs up. There is no authentication beyond the operator token, and nothing for tenants themselves.

## Tests

`web/admin/e2e/smoke.spec.ts` (Playwright, run by CI's end-to-end job against the Compose stack, `make web-e2e` locally) creates a sandbox account, closes a period and waits for
a paid invoice, then drives the browser: sign in with the operator token, open the tenant and the invoice, and check that the saga timeline has at least five transitions ending in
`COMPLETED`; a wrong token is refused and a signed-out visitor is sent to the sign-in page; a valid notification command put into `notification.send.dlq` appears in the dead-letter view
and disappears when redriven (the notifier delivers it; the saga it names is already final, so its reply is ignored).

`make web-check` runs install, ESLint, the type check (`vue-tsc`), the unit tests (Vitest: locale parity and money formatting), the OpenAPI type check and the build.
`web/admin/e2e/i18n.spec.ts` checks that the language follows the browser, can be changed and remembered, that Arabic is right-to-left, and that the signed-in pages and the playground are translated.

## Not covered

The console is not part of the Helm charts (reach it with `kubectl port-forward` of a locally built image, or run `npm run dev`); on Kubernetes the gateway's `/admin` routes are
the same ones and can be blocked from outside the namespace by a NetworkPolicy.
