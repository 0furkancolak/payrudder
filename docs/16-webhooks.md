# Webhooks and hosted invoices

What a tenant's own systems can learn from Quotify without polling, and what a tenant can hand to its customers without building a page. Built in [phase 23](phases/phase-23-remaining-gaps.md).

## Webhook endpoints

A tenant registers up to 20 HTTPS endpoints (`POST /v1/webhook-endpoints`, or the console's **Webhooks** tab). Each has a URL, an optional description and the event types it wants (none ticked: every type). The create response carries the signing secret
(`whsec_…`) **once**; it is stored sealed with `QUOTIFY_SECRETS_KEY` and cannot be read back, only rotated (`POST …/rotate-secret`: the new secret is returned once and the old one keeps signing for 24 hours so a receiver can switch without losing a delivery).

The URL must be `https://`, must not carry credentials and must resolve to a **public** address. The check is made when the connection is opened, after DNS, so a name that points inward (or changes its answer) is refused; redirects are not followed and no proxy is used (`pkg/safehttp`).
A development stack may lift the address rule with `QUOTIFY_WEBHOOK_ALLOW_PRIVATE=true` (the compose file does; the Helm chart never does).

## Event types

| Type | When | `data` |
|---|---|---|
| `invoice.finalized` | an invoice gets its number | `invoice_id`, `number`, `customer_id`, `customer_external_id`, `period_id`, `currency`, `total_minor`, `status` |
| `invoice.paid` · `invoice.voided` · `invoice.uncollectible` | the invoice reaches that state | the same |
| `credit_note.created` | a credit note is issued | `credit_note_id`, `number`, `invoice_id`, `invoice_number`, `customer_id`, `customer_external_id`, `currency`, `total_minor`, `reason`, `disposition` (`credit`/`refund`), and for a refund `refund_state` |
| `credit_note.refunded` · `credit_note.refund_failed` | the provider settled (or refused) a refund to the card | the same, with the outcome (`refund_error` on failure) |
| `subscription.created` | a subscription is created | `subscription`, as the API returns it |
| `subscription.changed` | a plan or interval change took effect (now, or at the end of the period) | `subscription` (the new one), `previous_subscription_id`, `quote` |
| `subscription.change_scheduled` | a change was set for the end of the period | `subscription`, `quote` |
| `subscription.cancelled` | it ended (`credited_minor` when the unused fee was credited) | `subscription` |
| `subscription.cancellation_scheduled` · `subscription.cancellation_withdrawn` | cancel at period end was set / taken back | `subscription` |
| `subscription.paused` · `subscription.resumed` | | `subscription` |
| `subscription.moved_to_provider` | a provider migration took over | `subscription`, `previous_subscription_id`, `to_provider` |

A delivery is a JSON `POST`:

```json
{ "id": "0192…", "type": "invoice.paid", "created": "2026-10-20T10:00:00Z", "tenant_id": "0192…", "data": { "invoice_id": "…", "number": "INV-2026-000007", "total_minor": "1116", "status": "paid" } }
```

Money is a string of minor units, as in the REST API. Events are delivered **at least once and not in order**: use `id` to ignore a repeat, and fetch the object from the API when you need its present state. A `ping` event is sent by `POST …/test`.

## Verifying a delivery

Every delivery carries

```
Quotify-Signature: t=1700000000,v1=<hex>[,v1=<hex>]
Quotify-Event-Id: <id>   Quotify-Event-Type: <type>   Quotify-Delivery-Attempt: <n>
```

where each `v1` is `HMAC-SHA256(secret, "<t>.<raw body>")` in hex (two while a rotated secret is still accepted). Compute it over the **raw** bytes, compare in constant time, and reject a `t` more than five minutes from your clock.

```go
import "github.com/0furkancolak/quotify/pkg/webhooksig"

err := webhooksig.Verify(r.Header.Get(webhooksig.Header), rawBody, secret, time.Now(), 0)
```

```js
const crypto = require("node:crypto");
function verify(header, rawBody, secret, now = Date.now() / 1000) {
  const parts = Object.fromEntries(header.split(",").map((p) => p.split("=")));
  const t = Number(parts.t);
  if (Math.abs(now - t) > 300) return false;
  const want = crypto.createHmac("sha256", secret).update(`${t}.`).update(rawBody).digest("hex");
  return header.split(",").filter((p) => p.startsWith("v1=")).some((p) => {
    const got = Buffer.from(p.slice(3), "hex"), w = Buffer.from(want, "hex");
    return got.length === w.length && crypto.timingSafeEqual(got, w);
  });
}
```

```python
import hmac, hashlib, time
def verify(header: str, raw_body: bytes, secret: str, tolerance=300) -> bool:
    parts = [p.split("=", 1) for p in header.split(",")]
    t = int(next(v for k, v in parts if k == "t"))
    if abs(time.time() - t) > tolerance:
        return False
    want = hmac.new(secret.encode(), f"{t}.".encode() + raw_body, hashlib.sha256).hexdigest()
    return any(hmac.compare_digest(v, want) for k, v in parts if k == "v1")
```

## Delivery

Answer `2xx` within 10 seconds. Anything else (a status, a timeout, a refused connection) is retried after **1 minute, 5 minutes, 30 minutes, 2 hours, 6 hours and 24 hours**; after the seventh attempt the delivery is `failed`. `GET /v1/webhook-deliveries?endpoint_id=` shows each delivery with its attempts and the last answer,
`POST /v1/webhook-deliveries/{id}/redeliver` sends one again from the start, and a paused endpoint (`active: false`) receives nothing new. Deliveries and events are kept for 30 days.

Where events come from: subscription events are written in the same transaction as the change, so an event exists exactly when the change does. Invoice and credit note events are written to billing-svc's `tenant_event_outbox` in the transaction that changes the invoice and handed to plans-svc (which owns the endpoints) by a loop that retries until plans-svc has recorded them;
the event id is derived from the occurrence, so a repeat is harmless. If no endpoint wants a type, nothing is stored.

## Hosted invoice page

`POST /v1/invoices/{id}/links` (`{"expires_in_days": 90}`, 1–365) returns `{token, url, expires_at}` for a finalized invoice. The tenant sends the `url` to its customer; `GET /i/{token}` shows the invoice (lines, service periods, subtotal, total, status) and `GET /i/{token}/pdf` redirects to a five-minute download link
once the PDF is rendered. No sign-in: the token is 256 random bits and only its SHA-256 is stored. `DELETE /v1/invoices/{id}/links` revokes every link of the invoice. An unknown, expired or revoked token answers the same 404 page.

The page is rendered by the gateway with escaped HTML, in English, Turkish, German, French, Spanish or Arabic (`?lang=` or the browser's language), and sends `Cache-Control: no-store`, `X-Robots-Tag: noindex`, `Referrer-Policy: no-referrer` and a CSP that allows nothing to load from elsewhere. It is limited to 60 requests a minute per address
(the connection's; a proxy in front of the gateway must be the address the limiter sees). The link starts with `QUOTIFY_PUBLIC_URL` when the gateway has one (compose: `http://localhost:42800`; Helm: `global.publicURL`), else it is relative.
