# Operator console

`web/admin` is a small Next.js (App Router) and TypeScript console for an operator: it makes the state of the system visible during a demo and
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

## Playground

`/playground` is a guided walk through the product for someone who has only the operator token: create a sandbox account (currency, time zone and grace period are required, nothing is
preselected), create a meter, a plan with a base fee and a per-call price, a customer and a subscription, choose the mock payment outcome, send usage, close the period, and watch the
invoice go through its saga (with a link to the full timeline). It calls the same public API a customer would, with the sandbox tenant's own `qk_test_` key, so it exercises the real system end
to end; it never touches a live tenant. A declined payment leaves the invoice open in `PAYMENT_DECLINED` while dunning retries it. Playwright runs both the paid and the declined path.

## How it is built

- **Typed client.** `npm run gen` generates `src/lib/schema.d.ts` from [`api/openapi/v1.yaml`](../api/openapi/v1.yaml) with `openapi-typescript`;
  the client is `openapi-fetch` over those types, so a route or field that does not exist in the spec does not compile. CI regenerates the file and fails on a
  diff, and the ESLint config forbids `any`. The gateway does not serve the spec: the file in the repository is the source.
- **Same origin.** The browser calls `/gw/...`, a route handler in the Next.js server that forwards to `GATEWAY_URL` with the `Authorization` header. The gateway needs no CORS
  configuration and the console works wherever the gateway is reachable from the Next.js process.
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

`make web-check` runs install, ESLint, the type check, the OpenAPI type check and the build.

## Not covered

The console is not part of the Helm charts (reach it with `kubectl port-forward` of a locally built image, or run `npm run dev`); on Kubernetes the gateway's `/admin` routes are
the same ones and can be blocked from outside the namespace by a NetworkPolicy.
