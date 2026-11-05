# Runbook: first deploy of the console on Cloudflare

Background: [ADR 0021](../adr/0021-console-on-cloudflare-workers.md). CI deploys nothing until the secrets in step 6 exist.

1. **Log in once, locally.** `cd web/admin && npm ci && npx wrangler login` (a browser opens). `npx wrangler whoami` shows the account id.
2. **Set the API address.** In `web/admin/wrangler.toml`, `[env.staging.vars]` and `[env.production.vars]` `API_ORIGIN` are the gateway's public HTTPS origin (no path); they already name `api-staging.payrudder.com` and `api.payrudder.com`, and `routes` the two console hosts ([staging-and-production](staging-and-production.md)). They are plain vars, not secrets. Delete `routes` to use the `*.workers.dev` address first.
3. **Gateway settings.**
   - `PAYRUDDER_APP_URL=https://<console host>` (where Google returns the browser); the Google redirect URI to register is `https://<console host>/gw/auth/google/callback`.
   - **Client address for rate limits.** Generate a secret of at least 32 characters (`openssl rand -hex 32`) and set it on the gateway as `PAYRUDDER_TRUSTED_PROXY_SECRET` (`PAYRUDDER_TRUSTED_PROXY_SECRET` after the rename; Helm `auth.trustedProxySecret`, Railway variable). Put the same value on the Worker in step 4a. With it the gateway believes the `X-Forwarded-For` the Worker writes from `CF-Connecting-IP`, so sign-in and one-time-code limits count per visitor. Without it they count the Worker's address, so one visitor's attempts block everyone. `PAYRUDDER_TRUSTED_PROXIES` (CIDRs) is only for a proxy with a fixed address; `PAYRUDDER_TRUST_FORWARDED_FOR=true` is for private peers (compose, a Tunnel connector). See the ADR.
   - CORS is not needed: the browser only talks to the console's origin, the Worker proxies the API.
4a. **Worker secret.** `cd web/admin && npx wrangler secret put EDGE_AUTH_SECRET --env staging` (and `--env production`), pasting the value from step 3. Secrets persist across deploys; this is done once per environment. `.dev.vars` may hold one for `cf:dev`. Check: after signing in wrongly 10 times from one browser, a second device can still sign in.
4. **Deploy staging.** `npm run cf:deploy:staging` (builds, then `wrangler deploy --env staging`). Check `https://<host>/healthz` (proxied to the gateway) and sign in.
5. **Production.** `npm run cf:deploy`.
6. **CI.** In the GitHub repository add the secrets `CLOUDFLARE_API_TOKEN` (template "Edit Cloudflare Workers"; add Zone / Workers Routes edit if you use routes) and `CLOUDFLARE_ACCOUNT_ID`. Then every push to `main` touching `web/admin` deploys to staging (`deploy-console.yml`), and a published GitHub release deploys production (`deploy-production.yml`, after the backend).
7. **Local try-out.** `npm run cf:dev`; put `API_ORIGIN=http://localhost:42800` in `web/admin/.dev.vars` to override the config (git-ignored).

Rollback: `npx wrangler rollback --env production`. Logs: `npx wrangler tail --env production`.
