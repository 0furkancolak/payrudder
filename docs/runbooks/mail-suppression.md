# Mail suppression: bounces, complaints and the list

Plan 11. The notifier keeps a **suppression list per tenant**: an address on it is not mailed any more for that tenant (the mail is dropped as done, counted in `notifier_mail_dropped_total{category="suppressed"}`, and a billing command is answered as `suppressed` so its saga goes on). A **permanent (hard) bounce** or a **complaint** puts an address there; a soft bounce, a delay and a delivery only count. The list protects the sending domain's reputation, which every tenant that sends through the platform's sender shares.

**What is verified and what is not.** The list, the endpoint and the checks are tested against a fake provider (`pkg/mailx/fakenafru`) and Postgres. Nothing here has been run against Nafru Email or a real inbox. Nafru's webhook payload, signature and event names are **UNVERIFIED**; the endpoint takes Payrudder's own neutral event (`mailx.Event`), so a provider's notices need a small translating layer in front of it (below).

## Turning it on

The listener is off until it has an address and a secret:

| Variable | Meaning |
|---|---|
| `NOTIFIER_EVENTS_ADDR` | where the notifier listens (`:9111`); empty is off |
| `NOTIFIER_MAIL_EVENTS_SECRET` | at least 16 characters; every request is signed with it (`openssl rand -base64 32`). The notifier refuses to start with an address and no secret |
| `NOTIFIER_MAIL_EVENTS_ACCEPT_V1` | `false` by default. `true` also accepts the old signature (v1: timestamp and body only) **for the length of a rollout** from a gateway or sender that does not sign v2 yet; `notifier_mail_events_v1_signatures_total` counts what it accepted, so you see when the last sender has moved |

Expose the port only to what calls it (the translating layer, an operator's shell); it is not behind the gateway and does not use the console's login. The Helm chart and the compose file do not set these two: add them to the notifier's `env` and a Service for the port when you run the layer.

## The console and the tenant API

The gateway reaches the same listener to give a tenant its own view (`PAYRUDDER_NOTIFIER_EVENTS_URL`, `http://notifier:9111`, and `PAYRUDDER_NOTIFIER_MAIL_EVENTS_SECRET`, the same secret; both or neither, set on the gateway). With them the tenant API has `GET /v1/mail-suppressions` and `DELETE /v1/mail-suppressions/{address}` (the seller mails an address again), `GET /v1/mail-messages` (the tenant's recent mail, newest first, with what became of each) and `POST /v1/mail-previews/price-change` (the notice a customer would get, rendered by the notifier, nothing sent; the gateway adds the tenant's name, zone, currency and default language), and the console's mail page shows the two lists. The gateway always names the tenant from the credential. Without the two variables the two list routes answer **200 with an empty list and `connected: false`** (the page says "not connected on this deployment"; a 503 there made the browser log an error on every visit), while lifting an address and the price-change preview, which are actions, answer 503 `gateway.mail.unavailable`; a notifier that is configured but fails answers 503 on all four. The permission of the two lists and of lifting an address is `customers:read` / `customers:write` (they are customers' e-mail addresses; before security review 4 it was `settings:*`, which a `read_only` key holds and a custom key with `settings:read` alone no longer does); the price-change preview stays `settings:write`. **Where it is wired.** Compose: `NOTIFIER_EVENTS_ADDR=:9111` and `NOTIFIER_MAIL_EVENTS_SECRET` on the notifier, the URL `http://notifier:9111` and the same secret on the gateway (`NOTIFIER_MAIL_EVENTS_SECRET` in `.env` overrides the local default; the port is published on loopback only, `127.0.0.1:42920`, so that a gateway started by `make dev` reaches it). Helm: `mail.eventsSecret` (default a local value, **set your own**; empty switches both ends off); the notifier has a Service on 9111 and a NetworkPolicy that admits the gateway only, and the secret is in each service's Secret, not in plain environment (`tests/helm` checks it; with `secrets.existingSecrets` add `NOTIFIER_MAIL_EVENTS_SECRET` to the notifier's Secret and `PAYRUDDER_NOTIFIER_MAIL_EVENTS_SECRET` to the gateway's). Railway: `bootstrap.sh` generates the secret on the notifier and refers to it from the gateway, and the listener is reachable on the project's private network only (`tests/railway`). **Not run on a cluster or on Railway**: the wiring is rendered and checked, no gateway has called a notifier there. Elsewhere, set the two notifier variables and the two gateway variables and keep the port in reach of the gateway only.

The message log is `sent_notifications` of the tenant (its `delivery_state` is what the provider reported through `POST /v1/mail-events`: `delivered`, `delayed`, `bounced`, `soft_bounced`, `complained`; a permanent bounce or a complaint is not overwritten by a later delivery notice, and lifting a suppression does not change it). Mail the notifier recorded without a tenant is not in it. The log keeps what the notifier keeps (no expiry yet).

## The calls

Every request is signed with the shared secret (`pkg/webhooksig`, **v2**): header `Payrudder-Signature: t=<unix>,v2=<hex>`, where `v2` is HMAC-SHA256 of `v2\n<t>\n<METHOD>\n<path>\n<query>\n<body>` with the secret: the method in capitals, the path as sent (escaped), the query string as sent (empty when there is none, the line stays), and the exact request body (empty for GET and DELETE). The signature therefore belongs to one request: **a signature captured from a GET of the list is no good for a DELETE of a suppression**, which the old v1 signature (`<t>.<body>`) allowed for five minutes because both bodies are empty. A signature more than five minutes old, a v1-only signature (unless `NOTIFIER_MAIL_EVENTS_ACCEPT_V1`), or a missing one is `401`.

```sh
body='{"type":"bounced","message_id":"<the provider id>","bounce_type":"hard"}'
t=$(date +%s)
sig=$(printf 'v2\n%s\nPOST\n/v1/mail-events\n\n%s' "$t" "$body" | openssl dgst -sha256 -hmac "$NOTIFIER_MAIL_EVENTS_SECRET" -hex | sed 's/^.* //')
curl -sS -X POST "$NOTIFIER/v1/mail-events" -H "Payrudder-Signature: t=$t,v2=$sig" -d "$body"
# a GET with a query: printf 'v2\n%s\nGET\n/v1/mail-suppressions\ntenant_id=%s\n' "$t" "$TENANT"  (the query is signed as sent)
```

**Rolling out v2 (default, not agreed).** The gateway (`internal/mailadmin`) and `fakenafru` sign v2 only. Upgrade the notifier first with `NOTIFIER_MAIL_EVENTS_ACCEPT_V1=true` (the gateways of the old release keep working), then the gateways, then watch `notifier_mail_events_v1_signatures_total` stop rising and remove the variable. A notifier of the old release behind a gateway of the new one answers `401` (it finds no v1 signature), so the order matters. Anything of yours that signs these calls (a translating layer, a script) has to move to v2 before the variable is removed.

| Call | Does |
|---|---|
| `POST /v1/mail-events` | A delivery event: `{"type": "bounced" \| "complained" \| "delivered" \| "delayed", "message_id": "...", "recipient": "...", "bounce_type": "hard" \| "soft", "occurred_at": "...", "detail": "..."}`. `message_id` is the id the provider returned when it accepted the mail (the notifier stored it). `bounce_type` is required on `bounced`. The address suppressed is the one the notifier recorded for that mail, never one the event names; an event whose `recipient` is another address, or whose id is unknown, changes nothing. Answers `{"matched": bool, "suppressed": bool}`; sending it twice is harmless |
| `GET /v1/mail-suppressions?tenant_id=<uuid>[&after=<address>][&page_size=1..500]` | The tenant's list in address order, `next_after` for the next page |
| `PUT /v1/mail-suppressions/{tenant_id}/{address}` | Suppress an address by hand (reason `manual`); optional body `{"detail": "..."}` |
| `DELETE /v1/mail-suppressions/{tenant_id}/{address}` | Lift a suppression (`404` when it is not there). The address is mailed again from the next mail |
| `GET /v1/mail-messages?tenant_id=<uuid>[&after=<token>][&page_size=1..500]` | The tenant's mail, newest first, `next_after` for the next page |
| `POST /v1/mail-previews/price-change` | `{locale, customer_name, tenant_name, plan_code, effective_time, new_renewal_minor, currency, timezone}`: the notice as `pricechange.Preview` writes it, `{subject, html, text}` |

An address is compared case-insensitively. Suppression is **per tenant**: one tenant's customer who complained is still mailed by another tenant. Mail sent through a tenant's own provider is checked against the list too (the list is Payrudder's record), but the bounces of that provider's account only reach it if the tenant routes them to this endpoint.

## The provider's webhook, translated by the gateway

The gateway has the layer for a **Resend-shaped, Svix-signed** webhook: `POST https://<gateway>/inbound/mail-events` (public; it does not exist until `PAYRUDDER_MAIL_WEBHOOK_SECRET` is set on the gateway, with `PAYRUDDER_NOTIFIER_EVENTS_URL` and `PAYRUDDER_NOTIFIER_MAIL_EVENTS_SECRET`). In the provider's dashboard make a webhook for the events `email.bounced`, `email.complained`, `email.delivered` and `email.delivery_delayed` at that address and give the gateway the signing secret it shows (`whsec_...`). The gateway checks the signature (a request more than five minutes off the clock is refused: keep the clock right), turns each event into the neutral one below (one for each recipient) and posts it, signed, to the notifier. It answers 200 to an event that says nothing about delivery, 401 to a bad signature and 503 when the notifier cannot be reached, so the provider retries. Where to set it: compose passes `PAYRUDDER_MAIL_WEBHOOK_SECRET` from `.env` to the gateway, the Helm chart renders `mail.webhookSecret` into the gateway's Secret (it needs `mail.eventsSecret`; [docs/12](../12-kubernetes.md)), and the Railway bootstrap sets it from `MAIL_WEBHOOK_SECRET` ([README](../../deploy/railway/README.md)). Unset, the route answers 404 and the gateway logs at start that it is off. Wired and rendered under test; **never run against a mail provider or a cluster**. **UNVERIFIED for Nafru** (its events, payload and signature were not read); a webhook of another shape needs its own translation (`pkg/mailx.ParseResendWebhook` is the one). The secret verifies the platform's own provider account; a tenant's own Nafru account has a secret of its own, which the gateway does not know.

## Translating another provider's notices

A provider's webhook is not this body. Resend's, for example (public documentation, read 2026-11-02, <https://resend.com/docs/dashboard/webhooks/event-types> and the pages for `email.bounced` and `email.complained`), is `{"type": "email.bounced", "created_at": ..., "data": {"email_id": ..., "to": [...], "bounce": {"type": "Permanent", "subType": ..., "message": ...}}}` signed with Svix headers (`svix-id`, `svix-timestamp`, `svix-signature`). A layer in front of the notifier verifies the provider's signature, maps `email.bounced` with a permanent bounce to `{"type":"bounced","bounce_type":"hard"}` (a temporary one to `soft`), `email.complained` to `complained`, `email.delivered` and `email.delivery_delayed` to `delivered` and `delayed`, takes `data.email_id` as `message_id`, and posts the result signed with the shared secret. For Nafru the layer above applies only if its webhook is this shape; (UNVERIFIED which events Nafru emits, how it signs them, and whether `email_id` is the id its send call returns).

## Watching it

`notifier_mail_events_total{type,outcome}` (outcome `suppressed`, `counted` or `unmatched`: a high `unmatched` rate means the ids the provider reports are not the ones the send returned), `notifier_mail_dropped_total{category="suppressed"|"quota"}`, `notifier_mail_rate_limited_total`, `notifier_mail_rate_limit_fallback_total` (a rate-limit decision taken on the replica's own buckets because Redis could not be reached). `notifier_mail_events_v1_signatures_total` (requests accepted on the old v1 signature while `NOTIFIER_MAIL_EVENTS_ACCEPT_V1` is on: zero means every sender signs v2 and the flag can go). The list is `notifier_db.mail_suppressions`; the mail it was sent for is `sent_notifications` (`tenant_id`, `smtp_message_id`, which holds the provider's id).

## Not built

Automatic expiry of a soft-bounce streak (a repeated soft bounce does not suppress); unsubscribe links for non-transactional mail (Payrudder sends only transactional mail); suppression of mail sent before migration 00003 (those rows have no tenant, so their bounces are counted as `unmatched` and suppress nothing).
