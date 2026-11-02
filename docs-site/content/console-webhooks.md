# Console guide: webhook endpoints

How to add a webhook endpoint, watch what it receives and fix it, in the console. What a delivery contains and how to verify it is in [Webhooks](webhooks.html); the [tutorial](tutorial-webhooks.html) runs a receiver on your machine. The API is in the [reference](api/webhook-endpoints.html).

It was written from the console's own texts (every name in bold is a string of the English console) and its pages' code, not by following it on a screen with a person. Where they differ, the console is right.

## Where it is

Under **Developers**, **Webhooks**. The page says what the endpoints are for: **Payrudder sends a signed JSON POST to these HTTPS endpoints when something happens in the account: an invoice is paid, a credit note is issued, a subscription changes. Verify the Payrudder-Signature header with the endpoint's secret; failed deliveries are retried for about a day and a half.**

An environment that is not live yet cannot have endpoints (the console says to finish the [go-live checklist](console-go-live.html) first).

## Adding an endpoint

Fill in the **Endpoint URL**, a **Description** and the **Events** to receive, then **Add endpoint**. **Tick none to receive every event.**

- Use an `https` address. A plain `http` address is accepted in the sandbox for testing (**This URL uses http, which is accepted in the sandbox for testing. Live endpoints should use https.**), and in live the console warns that **events with customer and invoice data would travel unencrypted**.
- **Copy the signing secret now: it is shown only once.** Put it where your receiver reads `PAYRUDDER_WEBHOOK_SECRET` (the templates in `examples/` do), and verify every delivery with it.

## The list and one endpoint

Each endpoint shows its URL, its description, whether it is **Active** or **Paused**, and **All events** or the events it asked for. The actions are:

- **Send test**: sends a signed `ping` event to the endpoint and shows the answer. The **Connect your app** page has the same button next to the verification code.
- **Pause** and **Resume**: a paused endpoint is not sent events.
- **Rotate secret**: **The old secret stops working at once: your receiver must be given the new one, or it will reject every delivery.** The new secret is shown once.
- **Delete**: **Delete this endpoint and its delivery history?**

Several endpoints can be ticked to **Pause**, **Resume** or **Delete** them together; deleting asks first, and **Their secrets cannot be recovered.**

## Deliveries

**Deliveries** lists what was sent to an endpoint, with the **Event**, the **Status** (**Pending**, **Delivered** or **Failed**), the number of **Attempts**, the **Last answer** of your receiver and, for one that is waiting, when the **Next try** is. A status filter narrows the list. **Send again** sends a delivery once more (`POST /v1/webhook-deliveries/{id}/redeliver`).

When deliveries fail, look at **Last answer** first: a `400` means your receiver rejected the signature (the body was changed before it was checked, the wrong secret, or a clock more than five minutes off); a `404` means the path; no answer at all means the address cannot be reached from Payrudder. Deliveries are at least once and not in order, so a receiver ignores an event id it has already handled. See [Webhooks](webhooks.html) for the retry schedule and the event types.
