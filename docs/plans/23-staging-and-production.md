# 23 — Staging and production on payrudder.com

## The request
2026-11-05: publish the application in **two environments, staging and production**, on **payrudder.com**, with **one level of subdomain only**
(`console-staging.payrudder.com`, never `console.staging.payrudder.com`), and the country/region/district lists read from **nafru-geo**
(`https://geo.nafru.com`).

**Status: step 1 built (2026-11-05): the repository changes below, with `TestBootstrapForAnEnvironment`; then the console's config as `wrangler.toml` and production on a published release (`deploy-production.yml`, `bootstrap.sh --deploy-only`, `TestBootstrapDeployOnly`). Nothing is deployed; no Railway project, Cloudflare zone or DNS record was created or checked.** The steps to run are in the runbook [staging-and-production](../runbooks/staging-and-production.md).

## Where we are
- **Geo is built** (#454): the gateway reads `PAYRUDDER_GEO_URL` (default `https://geo.nafru.com`) with `PAYRUDDER_GEO_API_KEY`; without the key the
  `/app/v1/geo/*` routes answer 503 and the address fields stay plain text ([geo-data](../geo-data.md)). nafru-geo takes one key per app from its Worker
  secret `API_KEYS`; its README already lists payrudder. nafru-geo now lives in its own repository (`nafrucom/nafru-geo`), not nafru-scout as
  `geo-data.md` still says.
- **Console**: a Cloudflare Worker ([ADR 0021](../adr/0021-console-on-cloudflare-workers.md)). `web/admin/wrangler.toml` already has `staging` and
  `production` environments with `example.com` placeholders; `deploy-console.yml` deploys staging on every push to `main` and production by hand, as
  GitHub Environments `staging` / `production` (before this plan; see decision 6). Runbook: [console-first-deploy](../runbooks/console-first-deploy.md).
- **Backend**: two targets exist, neither ever deployed: Railway (`deploy/railway`, `bootstrap.sh`, **one environment**) and Helm (`deploy/helm`).
  The Railway bootstrap also creates `admin-ui` (the console as a container), `mockpsp` and `mailpit`, and points `PAYRUDDER_APP_URL` at `admin-ui`.
- **Mail**: the notifier sends through Nafru when `NAFRU_BASE_URL` and `NAFRU_API_KEY` are set (#245); otherwise plain SMTP (`mailpit` on Railway,
  which delivers nothing).
- **Secrets**: published values are refused outside local/ci/kind (`Base.CheckSecret`); the bootstrap generates every secret once per project.
- **Security**: GitHub secret scanning and push protection are on (2026-11-05); a gitleaks pass over the tree and the whole history found no real secret.

## Decisions
The owner decided: two environments, `payrudder.com`, one subdomain level, geo from `geo.nafru.com`. The rest are **defaults (not agreed)**:

1. **Backend on Railway**, one project `payrudder` with two Railway environments, `staging` and `production`. Why: Railway environments are
   separate stacks (own services, volumes, variables, private network); Kubernetes needs a cluster to run. Helm stays for later.
   **Changed on the first run (2026-11-06, owner's decisions):** the resources are code (`deploy/railway/iac/.railway/railway.ts`, Railway's IaC),
   because `railway add` creates a service in one environment only and a second environment then refuses the names; the canvas is grouped (Data,
   Messaging, API, Workers, Sandbox). MinIO is replaced by Cloudflare R2 (`assets[-staging].payrudder.com` is the assets bucket's public domain;
   presigned links use R2's own endpoint, the only one they work on), mailpit by Nafru Email, and the init jobs by the services themselves
   (`pgxx.Provision` with `PAYRUDDER_PG_ADMIN_DSN`; plans-svc creates the Kafka topics with `PAYRUDDER_KAFKA_CREATE_TOPICS`). PgBouncer is in front
   of Postgres for the app role. `mockpsp`, RabbitMQ and Redpanda stay.
2. **Console on Cloudflare** (the Worker, ADR 0021), not the `admin-ui` container: the Railway `admin-ui` service is not created.
3. **Production opens sandbox-only**: `PAYRUDDER_ALLOWED_ENVIRONMENTS=sandbox`, provider sandbox keys. `mockpsp` and `mailpit` stay: sandbox tenants pay through the mock and live never reaches it (ADR 0023); sandbox mail goes to the catcher. Live payments are a separate
   go-live step (below), because neither the iyzico nor the Paddle adapter has been run against a real sandbox.
4. **Staging profile `small`** (no ClickHouse, no analytics-svc), **production `full`**.
5. **Every secret and key is per environment**, never copied between them (secrets key, OTP pepper, operator token, the nafru-geo key, the Nafru
   mail key, Google OAuth client, provider keys). A leak in staging then never opens production.
6. **The owner decided (2026-11-05): staging is every push to `main`, production is a published GitHub release.** Railway deploys `main` to staging
   itself ("wait for CI"), `deploy-console.yml` the console. A release runs `deploy-production.yml`: the backend with `bootstrap.sh --deploy-only`
   (a Railway project token of the production environment, `railway up --ci` per service) and then the console. No GitHub Environments: who may
   publish a release decides production. The console's config is `wrangler.toml` (owner's choice).

## Hostnames
`payrudder.com` is a Cloudflare zone. Cloudflare's free certificate covers one level of subdomain only, which is another reason for the owner's rule.

| Use | Production | Staging | Points at |
|---|---|---|---|
| Console (operator, product app, hosted pages under `/r/`) | `console.payrudder.com` | `console-staging.payrudder.com` | Worker custom domain |
| Public API, provider and mail webhooks | `api.payrudder.com` | `api-staging.payrudder.com` | Railway `gateway` custom domain |
| Presigned file links (invoice PDFs, assets) | `files.payrudder.com` | `files-staging.payrudder.com` | Railway `minio` custom domain |
| Documentation (later) | `docs.payrudder.com` | none | GitHub Pages (`docs.yml`, `DOCS_PUBLISH`) |
| MCP server (later, plan 20) | `mcp.payrudder.com` | `mcp-staging.payrudder.com` | not in this plan |

The apex `payrudder.com` and `www` are left for a landing page, not part of this plan.
**UNVERIFIED:** Railway custom domains behind Cloudflare's proxy (orange cloud) need SSL mode "Full"; start with the Railway records **DNS only** and
turn the proxy on only after checking that the certificate is issued and renewed.

## Geo (nafru-geo)
1. Make two keys, `openssl rand -hex 32` each, one for staging and one for production.
2. In `nafrucom/nafru-geo`, add both to the list and put it back:
   `printf '%s' "<existing keys>,<staging key>,<production key>" | bunx wrangler secret put API_KEYS`; update the local `.env.keys` (git-ignored).
3. On the Railway `gateway` of each environment: `PAYRUDDER_GEO_API_KEY=<that environment's key>`. `PAYRUDDER_GEO_URL` stays at its default.
4. Check: signed in, `GET https://api-staging.payrudder.com/app/v1/geo/countries` answers 200 with `attribution`; the customer address form offers
   regions for `TR`.
5. Revoking: take the key out of `API_KEYS`; the gateway keeps serving its last copy for up to a day (`geo.TTL`), then the fields fall back to plain text.

## Work

### Step 1 — repository changes (one PR)
- `web/admin/wrangler.toml`: the four hostnames above in `routes`, `API_ORIGIN` and `PUBLIC_API_URL`.
- `deploy/railway/bootstrap.sh`:
  - `--environment <name>`: links the Railway environment before doing anything.
  - `--console-url <https://…>`: sets `PAYRUDDER_APP_URL` from it instead of `admin-ui`.
    It also leaves out `admin-ui` and generates the gateway's `PAYRUDDER_TRUSTED_PROXY_SECRET`.
  - `--api-domain`, `--files-domain`: custom domains on `gateway` and `minio`, used in `PAYRUDDER_PUBLIC_URL` and `S3_PUBLIC_ENDPOINT`.
  - `--allowed-environments`: default `sandbox,live`, production passes `sandbox`.
  - `NAFRU_BASE_URL` and `NAFRU_API_KEY` from the environment onto the notifier (both or neither).
  - `GEO_API_KEY` from the environment onto the gateway.
  - Its static test (`make railway-check`) covers the new flags.
- `deploy/railway/README.md`: a "Two environments" section; `docs/geo-data.md`: nafru-geo's repository.
- New runbook `docs/runbooks/staging-and-production.md`: the steps below as a checklist, with rollback (Railway "Redeploy" of the previous deployment,
  `wrangler rollback --env production`).
- **Not done by this PR**: anything on Railway, Cloudflare, nafru-geo or Google.

### Step 2 — what the owner sets up (accounts and keys, outside the repository)
- Cloudflare: `payrudder.com` as a zone; an API token "Edit Cloudflare Workers" (+ Workers Routes) as `CLOUDFLARE_API_TOKEN` and
  `CLOUDFLARE_ACCOUNT_ID`, and `RAILWAY_TOKEN_PRODUCTION`, as repository secrets.
- Railway: a project on a plan with enough memory (`deploy/compose/memory-budgets.yaml`), the GitHub app installed for the repository.
- Google: one OAuth client per environment, redirect `https://<console host>/gw/auth/google/callback`.
- Nafru mail: one key per environment, the sender domain `payrudder.com` verified (SPF/DKIM records in Cloudflare).
- Providers: iyzico and Paddle **sandbox** keys for both environments.
- Error tracking: one GlitchTip or Sentry project per environment (`ERROR_TRACKING_URL`).
- `OPERATOR_EMAILS` for each environment.

### Step 3 — staging
1. `deploy/railway/bootstrap.sh --environment staging --profile small --console-url https://console-staging.payrudder.com …` (the whole command is in the runbook)
   with the staging keys in the environment; set the config-as-code path of each service (the CLI cannot).
2. Custom domains: `api-staging` on `gateway`, `files-staging` on `minio`; the CNAME records in Cloudflare.
3. Console: set `EDGE_AUTH_SECRET` on the Worker and the same value as `PAYRUDDER_TRUSTED_PROXY_SECRET` on the gateway, then run the workflow.
4. Provider webhooks (iyzico, Paddle sandbox) and the mail delivery webhook point at `https://api-staging.payrudder.com/…`.
5. **Check** (written into the runbook, each with what to look at):
   - every service is healthy;
   - sign-up, e-mail verification and two-factor sign-in, with the mail arriving through Nafru;
   - a tenant and an app, an API key, events sent with the TypeScript SDK;
   - month close gives an invoice and its PDF opens through `files-staging`;
   - a sandbox payment on iyzico and on Paddle, settled by the webhook;
   - the geo lists;
   - the hosted invoice page;
   - an operator sign-in;
   - migrations ran once (`migrations-version.sh`).

### Step 4 — production (sandbox only)
The same as staging with `--environment production --profile full --allowed-environments sandbox`, its own keys, the production hosts, and:
- Railway Postgres backups on, and one restore drill into a scratch environment ([backup-restore](../runbooks/backup-restore.md)) **before** the
  first tenant;
- alerting on, at least error tracking and Railway's own alerts (Prometheus/Alertmanager on Railway is not set up, see below);
- the same checks as staging.

### Step 5 — go-live (separate, later)
Live keys for iyzico/Paddle, `--allowed-environments sandbox,live`, the provider's live webhooks, a real small payment and its refund. This step waits
for plans 06/07 to have run against the real sandboxes.

## Not covered / risks
- Railway was never used for this stack: the bootstrap's own "What is verified" list applies, and a first run will find things.
- Prometheus, Grafana, Loki and Alertmanager run in compose and Helm, not on Railway; production starts with error tracking and Railway metrics only.
- Redpanda, RabbitMQ, MinIO and ClickHouse on Railway are single containers with a volume: no replication. Fine for staging and a sandbox-only
  production; to be reviewed before go-live (plan 16, plan 17).
- Railway's IPv6 private network: `REDPANDA_BIND=::` may be needed on a new environment.
- Cost per environment is not estimated here.
