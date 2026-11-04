# Authentication

Every request to the tenant API (`/v1/*`) carries an **API key** as a bearer token:

```sh
curl https://YOUR-API-ADDRESS/v1/meters -H "Authorization: Bearer $PAYRUDDER_API_KEY"
```

A key looks like `qk_test_<prefix>_<secret>` or `qk_live_<prefix>_<secret>`. The console shows it **once**, when you create it on the app's **API keys** page; Payrudder keeps only a hash and cannot show it again. An app can have at most 10 active keys in each environment.

## One key, one environment

`qk_test_` keys act on the app's **sandbox**, `qk_live_` keys on its **live** environment; a key never reaches the other one, and a key whose prefix disagrees with its environment is refused with `401`. The two environments share nothing: customers, meters, plans, invoices and webhook endpoints are separate. See [Test mode](test-mode.html).

## Roles

A key has a **role** that decides what it may do, so a key can be as small as the code that holds it needs:

| Role | May |
|---|---|
| `admin` | everything the tenant API offers |
| `billing` | customers, subscriptions, credits and invoices; read events, the catalogue and usage |
| `read_only` | read everything (`GET`), change nothing |
| `ingest_only` | send usage events (`events:write`) and nothing else: the key to put in a service that only reports usage |
| `custom` | exactly the permissions chosen when the key was made |

A permission is `resource:action` (`customers:read`, `invoices:write`, `events:write`, `sandbox:write`, ...). A `GET` under `/v1/<resource>` needs `<resource>:read`, any other method `<resource>:write`. `audit:read` (the audit log, `GET /v1/audit-events`) is held by `admin` keys and by a `custom` key given it on purpose; `read_only` does not have it, because the log names people and addresses. A request the key is not allowed to make is `403` with the key `gateway.auth.permission_denied`, and the message names the permission it lacked. Give each service the narrowest role that works.

## Rotating and revoking

A key can also have a **rate limit of its own**, a share of your plan's limit for each kind of request: in the console give the key a *Rate limit (% of plan)* from 1 to 100 (or send `rate_percent` when you create it). A key at 20 may use a fifth of the limit and gets `429` with the key `gateway.rate_limit.exceeded` and a `Retry-After` header when that fifth is spent, even if your other keys have left room; the rest of the limit stays for them. Leave it empty and the key only draws on the whole limit, like today. A rotated key keeps its share.

Rotating a key (in the console, or `POST /app/v1/apps/{id}/api-keys/{key_id}/rotate`) makes a new key with the same role and environment and sets an end time on the old one, so you can deploy the new key before the old one stops: during that overlap both work, afterwards the old one is `401`. Revoking a key stops it. Rotate when a key may have leaked and whenever someone who knew it leaves.

## When a request is refused

| Answer | Key | Meaning |
|---|---|---|
| `401` | | the key is missing, wrong, revoked, past its end time, or belongs to the other environment |
| `403` | `gateway.auth.permission_denied` | the key's role lacks the permission the route needs |
| `403` | `gateway.auth.tenant_not_active` | the environment is not active: a live environment that has not gone live yet, or one that is suspended or banned (also when the operator banned the whole account) |
| `403` | `gateway.auth.tenant_read_only` | the environment is frozen: reads still work, anything else is refused |
| `421` | `gateway.region.wrong` | only on a deployment with regional gateways: the environment is served from another region. `X-Payrudder-Region` names it and, when the deployment knows it, `X-Payrudder-Region-Url` is the address of that region's gateway. Nothing was done with the request; send it there |
| `429` | `gateway.rate_limit.exceeded` | too many requests; wait the number of seconds in `Retry-After` and try again |
| `429` | `identity.auth.rate_limited` | the address you call from has had many keys or tokens refused in the last minute (30): it is turned away before any key is looked up, whatever it sends, until the minute passes. Check for a script that still uses a revoked or mistyped key; an IPv6 address counts as its whole /64 |

Rate limits depend on the tier of the account and are counted per class of route; usage events are counted **per event**, not per request, so batching does not get around them. See [Errors](errors.html) for the error format.

## Keep keys safe

- Use a key only from your servers. Never put one in a web page, a mobile app or a public repository: anyone who has it can do what its role allows.
- Keep it in an environment variable or a secret store, as the examples do (`PAYRUDDER_API_KEY`), not in the code.
- Webhooks are authenticated the other way round: Payrudder signs each delivery with the endpoint's own secret, and you verify it. See [Webhooks](webhooks.html).
