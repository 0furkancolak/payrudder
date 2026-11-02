# Mail suppression: bounces, complaints and the list

Plan 11. The notifier keeps a **suppression list per tenant**: an address on it is not mailed any more for that tenant (the mail is dropped as done, counted in `notifier_mail_dropped_total{category="suppressed"}`, and a billing command is answered as `suppressed` so its saga goes on). A **permanent (hard) bounce** or a **complaint** puts an address there; a soft bounce, a delay and a delivery only count. The list protects the sending domain's reputation, which every tenant that sends through the platform's sender shares.

**What is verified and what is not.** The list, the endpoint and the checks are tested against a fake provider (`pkg/mailx/fakenafru`) and Postgres. Nothing here has been run against Nafru Email or a real inbox. Nafru's webhook payload, signature and event names are **UNVERIFIED**; the endpoint takes Payrudder's own neutral event (`mailx.Event`), so a provider's notices need a small translating layer in front of it (below).

## Turning it on

The listener is off until it has an address and a secret:

| Variable | Meaning |
|---|---|
| `NOTIFIER_EVENTS_ADDR` | where the notifier listens (`:9111`); empty is off |
| `NOTIFIER_MAIL_EVENTS_SECRET` | at least 16 characters; every request is signed with it (`openssl rand -base64 32`). The notifier refuses to start with an address and no secret |

Expose the port only to what calls it (the translating layer, an operator's shell); it is not behind the gateway and does not use the console's login. The compose file sets them (on the notifier and, as `PAYRUDDER_NOTIFIER_EVENTS_URL` and `PAYRUDDER_NOTIFIER_MAIL_EVENTS_SECRET`, on the gateway) with a local secret and publishes no port. The Helm chart and Railway do not set them: add them to the notifier's `env` and a Service for the port when you run the layer.

## The console and the tenant API

The gateway reaches the same listener to give a tenant its own view (`PAYRUDDER_NOTIFIER_EVENTS_URL`, `http://notifier:9111`, and `PAYRUDDER_NOTIFIER_MAIL_EVENTS_SECRET`, the same secret; both or neither, set on the gateway). With them the tenant API has `GET /v1/mail-suppressions` and `DELETE /v1/mail-suppressions/{address}` (the seller mails an address again), `GET /v1/mail-messages` (the tenant's recent mail, newest first, with what became of each) and `POST /v1/mail-previews/price-change` (the notice a customer would get, rendered by the notifier, nothing sent; the gateway adds the tenant's name, zone, currency and default language), and the console's mail page shows the two lists. The gateway always names the tenant from the credential. Without the two variables those routes answer 503 `gateway.mail.unavailable` and the page says so. The permission is `settings:read` / `settings:write`. **The compose file wires neither end** (the notifier's listener has no address in it, and neither does the gateway); nor do Helm and Railway, which have no Service for the notifier's port: until they do, the pages show "not connected". Set `NOTIFIER_EVENTS_ADDR`, `NOTIFIER_MAIL_EVENTS_SECRET` on the notifier and the two gateway variables, and put the notifier's port in reach of the gateway only.

The message log is `sent_notifications` of the tenant (its `delivery_state` is what the provider reported through `POST /v1/mail-events`: `delivered`, `delayed`, `bounced`, `soft_bounced`, `complained`; a permanent bounce or a complaint is not overwritten by a later delivery notice, and lifting a suppression does not change it). Mail the notifier recorded without a tenant is not in it. The log keeps what the notifier keeps (no expiry yet).

## The calls

Every request is signed like Payrudder's own webhooks (`pkg/webhooksig`): header `Payrudder-Signature: t=<unix>,v1=<hex>`, where `v1` is HMAC-SHA256 of `<t>.<body>` with the secret, the body being the exact request body (empty for GET and DELETE). A signature more than five minutes old, or a missing one, is `401`.

```sh
body='{"type":"bounced","message_id":"<the provider id>","bounce_type":"hard"}'
t=$(date +%s)
sig=$(printf '%s.%s' "$t" "$body" | openssl dgst -sha256 -hmac "$NOTIFIER_MAIL_EVENTS_SECRET" -hex | sed 's/^.* //')
curl -sS -X POST "$NOTIFIER/v1/mail-events" -H "Payrudder-Signature: t=$t,v1=$sig" -d "$body"
```

| Call | Does |
|---|---|
| `POST /v1/mail-events` | A delivery event: `{"type": "bounced" \| "complained" \| "delivered" \| "delayed", "message_id": "...", "recipient": "...", "bounce_type": "hard" \| "soft", "occurred_at": "...", "detail": "..."}`. `message_id` is the id the provider returned when it accepted the mail (the notifier stored it). `bounce_type` is required on `bounced`. The address suppressed is the one the notifier recorded for that mail, never one the event names; an event whose `recipient` is another address, or whose id is unknown, changes nothing. Answers `{"matched": bool, "suppressed": bool}`; sending it twice is harmless |
| `GET /v1/mail-suppressions?tenant_id=<uuid>[&after=<address>][&page_size=1..500]` | The tenant's list in address order, `next_after` for the next page |
| `PUT /v1/mail-suppressions/{tenant_id}/{address}` | Suppress an address by hand (reason `manual`); optional body `{"detail": "..."}` |
| `DELETE /v1/mail-suppressions/{tenant_id}/{address}` | Lift a suppression (`404` when it is not there). The address is mailed again from the next mail |
| `GET /v1/mail-messages?tenant_id=<uuid>[&after=<token>][&page_size=1..500]` | The tenant's mail, newest first, `next_after` for the next page |
| `POST /v1/mail-previews/price-change` | `{locale, customer_name, tenant_name, plan_code, effective_time, new_renewal_minor, currency, timezone}`: the notice as `pricechange.Preview` writes it, `{subject, html, text}` |

An address is compared case-insensitively. Suppression is **per tenant**: one tenant's customer who complained is still mailed by another tenant. Mail sent through a tenant's own provider is checked against the list too (the list is Payrudder's record), but the bounces of that provider's account only reach it if the tenant routes them to this endpoint.

## Translating a provider's notices

A provider's webhook is not this body. Resend's, for example (public documentation, read 2026-11-02, <https://resend.com/docs/dashboard/webhooks/event-types> and the pages for `email.bounced` and `email.complained`), is `{"type": "email.bounced", "created_at": ..., "data": {"email_id": ..., "to": [...], "bounce": {"type": "Permanent", "subType": ..., "message": ...}}}` signed with Svix headers (`svix-id`, `svix-timestamp`, `svix-signature`). A layer in front of the notifier verifies the provider's signature, maps `email.bounced` with a permanent bounce to `{"type":"bounced","bounce_type":"hard"}` (a temporary one to `soft`), `email.complained` to `complained`, `email.delivered` and `email.delivery_delayed` to `delivered` and `delayed`, takes `data.email_id` as `message_id`, and posts the result signed with the shared secret. For Nafru the same layer is needed once its webhook contract is known; **it is not written** (UNVERIFIED which events Nafru emits, how it signs them, and whether `email_id` is the id its send call returns).

## Watching it

`notifier_mail_events_total{type,outcome}` (outcome `suppressed`, `counted` or `unmatched`: a high `unmatched` rate means the ids the provider reports are not the ones the send returned), `notifier_mail_dropped_total{category="suppressed"|"quota"}`, `notifier_mail_rate_limited_total`, `notifier_mail_rate_limit_fallback_total` (a rate-limit decision taken on the replica's own buckets because Redis could not be reached). The list is `notifier_db.mail_suppressions`; the mail it was sent for is `sent_notifications` (`tenant_id`, `smtp_message_id`, which holds the provider's id).

## Not built

Wiring of the listener in compose, Helm and Railway; automatic expiry of a soft-bounce streak (a repeated soft bounce does not suppress); unsubscribe links for non-transactional mail (Payrudder sends only transactional mail); suppression of mail sent before migration 00003 (those rows have no tenant, so their bounces are counted as `unmatched` and suppress nothing).
