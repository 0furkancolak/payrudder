# Tutorial: receive webhooks on your machine

Payrudder tells your application what happened (an invoice was paid, a subscription ended) by posting a signed JSON message to an address you register. This tutorial gets one such message to a handler on your own computer and shows how to see what went wrong when it does not arrive. The scheme itself (event types, headers, retries) is in [Webhooks](webhooks.html); here you do it.

You need a key with the webhook permissions and the [command line](cli.html), signed in (`payrudder login`).

## 1. A handler that verifies

Write the endpoint your application will have: read the **raw** body, verify the `Payrudder-Signature` header with the endpoint's secret, answer `2xx` quickly, and ignore an event id you have seen before (deliveries are at least once). The code for your language is at the bottom of this page, made by the same generator as the Connect page of the console. Start it on `localhost:3000`, at `/webhooks/payrudder`.

## 2. Get deliveries to your machine

Payrudder posts to a registered address, so that address must reach your computer. Pick one:

- **Payrudder runs on your computer** (the development compose file): register `http://host.docker.internal:4242/` as an endpoint in the console's **Webhooks** tab (the development stack allows `http://` and private addresses; a production one never does), then run the receiver:

  ```sh
  payrudder webhooks listen --forward-to http://localhost:3000/webhooks/payrudder --secret whsec_…
  ```

  `whsec_…` is the secret the console showed **once** when you created the endpoint.

- **Payrudder is hosted**: open a tunnel to port 4242 (`cloudflared tunnel --url http://localhost:4242` or `ngrok http 4242`) and let the command register it for you:

  ```sh
  payrudder webhooks listen --public-url https://YOUR-TUNNEL-ADDRESS/ --forward-to http://localhost:3000/webhooks/payrudder
  ```

  It makes a temporary endpoint, takes its secret from the answer, and deletes the endpoint when you press Ctrl-C.

The receiver verifies each delivery before it forwards it, and forwards it **unchanged** (body and headers), so your handler's own check runs on the real signature. What it prints:

```text
Listening on http://127.0.0.1:4242 for Payrudder deliveries.
10:21:40  ping   0192…  attempt 1  signature ok  -> HTTP 200 (4ms)
```

## 3. Send an event

```sh
payrudder webhooks trigger ping
```

sends the one event the API can send on request, a `ping`, and waits for the answer of the endpoint. For a real event, make the thing happen: in the sandbox, [close a period](tutorial-usage-billing.html) and the invoice's events (`invoice.finalized`, `invoice.paid`) follow.

## 4. When it does not arrive

```sh
payrudder webhooks deliveries --watch
```

lists the deliveries with their state, the number of attempts and the HTTP status your endpoint answered, and keeps printing each one that appears or changes. A delivery that was not accepted is retried after 1 minute, 5 minutes, 30 minutes, 2 hours, 6 hours and 24 hours, then given up; the console's Webhooks tab can send one again by hand. The usual causes:

| You see | Likely cause |
|---|---|
| `REFUSED: … signature does not match` in the receiver | the wrong secret, or the body was changed before verification (a framework that parsed and re-encoded the JSON): verify the **raw** bytes |
| `REFUSED: … outside the tolerance` | your computer's clock is more than five minutes off |
| `-> … connection refused` | your handler is not running on the `--forward-to` address |
| HTTP 4xx or 5xx from your handler | your handler failed: Payrudder gets the same status and will retry |
| nothing at all | the registered address does not reach this computer (a tunnel that stopped, a firewall), or the endpoint is paused or subscribed to other event types |

## 5. Before you go live

Register the endpoint of your deployed application (`https://`, a public address) for the live environment; the secret is per endpoint and per environment. You can keep the verification code: it is the same.

## The code

The handler in each language, built from the SDKs of this repository (the install lines are the honest ones; the SDKs are not on a registry yet, see [SDKs](sdks.html)):
