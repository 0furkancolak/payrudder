# Command line

`payrudder` is a command line for the tenant API. It is the [Go SDK](sdks.html) behind a small set of commands, so it talks to the same `/v1` routes your code does, with an API key. Use it to send a usage event by hand, watch a customer's usage move, receive webhooks on your own machine, set up endpoints, meters, plan versions and subscriptions, and look at customers and invoices without opening the console.

## Install

It is **not published yet** (no release, no tag). Build it from a checkout of the repository; it needs Go 1.23 or newer and nothing else:

```sh
cd /path/to/payrudder/sdk/go
go install ./cmd/payrudder        # puts payrudder in $(go env GOBIN) or ~/go/bin
payrudder version
```

or run it without installing: `go run ./cmd/payrudder <command>`.

## Sign in

```sh
payrudder login
```

asks for an API key (`qk_test_…` for the sandbox, `qk_live_…` for live), checks it with the API, and only then stores it. Nothing is stored when the API refuses the key. To keep the key out of the terminal, pipe it in:

```sh
printf '%s\n' "$PAYRUDDER_API_KEY" | payrudder login --api-key-stdin --base-url https://YOUR-API-ADDRESS
```

- The key is stored in `config.json` in your user config directory (`~/Library/Application Support/payrudder` on macOS, `~/.config/payrudder` on Linux, `%AppData%\payrudder` on Windows), in a directory only you can enter and a file only you can read (mode `0600`). It is in the clear, like the config of other command lines: protect the disk, and revoke the key in the console when a laptop is lost. Set `PAYRUDDER_CONFIG_DIR` to keep it elsewhere. The command warns when the file can be read by others.
- A key that may not read meters (an ingest-only key) is accepted: it proves the key and the address.
- Without a login, `PAYRUDDER_API_KEY` is used, and when both exist the environment wins. The gateway address is `--base-url`, then `PAYRUDDER_BASE_URL`, then the one saved at login, then `http://localhost:42800`.
- `payrudder logout` removes the file. It does not revoke the key.

Every command below takes `--base-url` and prints what it does; the ones that list things take `--json`. `payrudder <command> -h` lists a command's flags.

## Send a usage event

```sh
payrudder events send --customer cus_42 --meter api_calls --quantity 3 --property model=gpt-x
```

```text
accepted   event 0192f0b4-6f6e-7c3a-9a3e-6a1f2b3c4d5e
```

The event is sent as now, unless you pass `--time` (an RFC 3339 time, not older than 35 days). It carries an idempotency key, generated unless you give `--idempotency-key`, so repeating the command with the same key never counts twice (`duplicate`). A rejected event prints its `error_code` (see [Errors](errors.html#event-codes)) and the command exits 1.

## Follow a customer's usage

```sh
payrudder events tail --customer cus_42
```

```text
10:15:02  usage of cus_42 in 2026-11
10:15:02  api_calls                3 (3 events)
10:15:08  api_calls                5 (+2 events)
```

The API does not list events (they are counted, not kept for reading), so this asks for the customer's **usage totals** every two seconds (`--interval`) and prints a line when a meter moves. Stop it with Ctrl-C. `--period 2026-10` follows another month.

## Receive webhooks on your machine

```sh
payrudder webhooks listen --forward-to http://localhost:3000/webhooks/payrudder --secret whsec_…
```

starts a receiver on `127.0.0.1:4242`. For every delivery it checks the signature with the endpoint's secret (`--secret`, or `PAYRUDDER_WEBHOOK_SECRET`), prints one line, and passes the delivery on to your app **unchanged**: the same body, the same `Payrudder-Signature` and event headers, so your own verification code runs for real. The answer your app gives is the answer Payrudder gets, so a handler that fails is retried by Payrudder as in production. A delivery whose signature does not verify is refused with 400 and never reaches your app.

```text
Listening on http://127.0.0.1:4242 for Payrudder deliveries.
Verified deliveries are forwarded to http://localhost:3000/webhooks/payrudder.
10:21:40  invoice.paid   0192…  attempt 1  signature ok  -> HTTP 200 (4ms)
```

Payrudder **pushes** deliveries (the API does not hand out the delivered bodies, so there is nothing to fetch later), which means the address it posts to must reach your machine:

- **A stack on your own computer** can post to it directly when `PAYRUDDER_WEBHOOK_ALLOW_PRIVATE` is on (the development compose file turns it on; it allows `http://` and private addresses). Register the receiver as an endpoint in the console. From a container the address is usually `http://host.docker.internal:4242/`; if the container cannot reach your loopback (Linux), start the receiver with `--host 0.0.0.0`, which makes it reachable from your network.
- **A hosted Payrudder** needs a tunnel in front of your machine (`cloudflared tunnel --url http://localhost:4242`, `ngrok http 4242`, or your own). Then let the command register it:

  ```sh
  payrudder webhooks listen --public-url https://YOUR-TUNNEL-ADDRESS/ --forward-to http://localhost:3000/webhooks/payrudder
  ```

  It creates a temporary endpoint for that address (all events, or `--events invoice.paid,invoice.voided`), uses the secret the creation returned, and **deletes the endpoint when you stop it**. The key needs the `webhooks:write` permission. If the process is killed without a chance to clean up, the endpoint stays: it is named "payrudder CLI: webhooks listen (temporary)" in the console's Webhooks tab.

`payrudder webhooks deliveries [--endpoint ID] [--state failed]` shows how each delivery went (attempts, the answer of your endpoint); with `--watch` it keeps asking and prints each delivery that appears or changes. `payrudder webhooks list` lists the endpoints.

## Send a test event

```sh
payrudder webhooks trigger ping
```

sends a signed `ping` to your endpoint (the only endpoint, or the one named with `--endpoint`) and waits for its answer: `Delivered: your endpoint answered HTTP 200.`, or `Not accepted: HTTP 500 …`, which exits 1. `ping` is the only event the API can send on request; every other event happens when the thing it announces happens (an invoice is paid, a subscription is cancelled), and in the sandbox you can make it happen with [Test mode](test-mode.html).

## Send a delivery again

```sh
payrudder webhooks redeliver 0192f0b4-6f6e-7c3a-9a3e-6a1f2b3c4d5e
```

takes the id of a delivery from `payrudder webhooks deliveries` and asks Payrudder to send it again **from the start**, whatever became of it (a delivery that failed for good, or one that was delivered and that your handler lost). The API queues it and answers at once; the command prints the id of the delivery and the endpoint, and `payrudder webhooks deliveries --endpoint ID --watch` shows how the endpoint answers. The event keeps its id, so a handler that ignores an event it has seen (see [Idempotency](idempotency.html)) ignores this one too: redeliver to retry a failure, not to replay a success.

## Customers and invoices

```sh
payrudder customers list --limit 10
payrudder invoices list --customer cus_42 --status open
payrudder invoices list --period 2026-10 --json
```

```sh
payrudder customers create --id cus_42 --name "Ada Lovelace" --email ada@example.com --country TR --locale tr --type individual
payrudder plans create --code pro --name Pro
```

`customers create` needs `--id` (your own id for the customer, the `external_id`), `--name` and `--email`; `--country`, `--currency`, `--locale` and `--type` are sent only when given. Creating a customer is not deduplicated by a key: the external id is the natural key, so running the command again answers `409` and exits 1 instead of making a second customer. `plans create` makes the plan itself, a code and a name; its prices and entitlements are a **version**, published with `plans publish` (next section). Both take `--json`.

## Set up billing from the terminal

The commands that make the things a product is built from. All take `--json` and `--base-url`.

```sh
payrudder webhooks create --url https://example.com/webhooks/payrudder --events invoice.paid,invoice.voided
payrudder meters create --code api_calls --aggregation sum --value-property n --unit calls --filter model=gpt-x,gpt-y
payrudder plans publish --code pro -f pro-v1.json
payrudder subscriptions create --customer cus_42 --plan pro --interval year --trial-days 14
```

- **`webhooks create`** registers an endpoint (`--url` must be https and public; `--events` is a comma-separated list, all events when left out; `--description` is shown in the console). The signing secret `whsec_…` is in the answer **once** and is printed once: store it, it cannot be read again, only rotated. The key needs `webhooks:write`.
- **`meters create`** needs `--code`, `--aggregation` (`sum`, `count`, `max`, `unique` or `last`) and `--unit`. `--value-property` names the event property that holds the quantity (for `unique`, the property whose distinct values are counted), `--scale` the decimal places (fixed per code; `0` is a choice, left out is the API's default), `--category` a group to file it under, and `--filter property=value1,value2` (repeatable) counts only the events whose property has one of the values. Creating a meter whose code exists makes its **next version**, it is not a `409`.
- **`plans publish`** reads a JSON file that is exactly the body of `POST /v1/plans/{code}/versions` (see [the API reference](api/plans.html)) and publishes the **next version** of the plan. `-f -` reads the standard input; `--file` is the same as `-f`. A version is immutable and cannot be undone, so the file is checked before anything is sent: a field the API does not know (a typo like `base_fee` would otherwise publish a free version), a second document, or a file that is not JSON stops the command with exit 1.

  ```json
  {
    "base_fee_minor": "1900",
    "trial_days": 14,
    "prices": [{ "meter_code": "api_calls", "per_unit": { "unit_price": "0.002" } }],
    "entitlements": [{ "meter_code": "api_calls", "limit": "100000" }],
    "fees": [{ "currency": "EUR", "base_fee_minor": "1900", "interval": "SUBSCRIPTION_INTERVAL_YEAR" }]
  }
  ```

- **`subscriptions create`** needs `--customer` and `--plan`; `--plan-version` (default: the latest), `--interval month|year`, `--currency`, `--trial-days` (`0` is no trial; left out is the version's own), `--quantity` (seats), `--fee-timing advance|arrears`, `--provider`, `--coupon` and `--start-time` are sent only when given. A customer has at most one overlapping subscription, so a repeat is the API's `409` (exit 1).

These calls have **no idempotency key** (see [Idempotency](idempotency.html)), and for `webhooks create`, `meters create` and `plans publish` a repeat would make a second endpoint or another version. The command therefore does **not retry** them after a timeout or a `5xx`: it prints the error and exits 1, and you look (`payrudder webhooks list`, the console) before running it again.

Tables show the first `--limit` rows (25 by default). Invoice totals are in the currency's **minor units**, as the API sends them.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | it worked (also Ctrl-C of a command that runs until stopped) |
| 1 | it failed: an API error (printed with its error key and request id, see [Errors](errors.html)), a rejected event, an endpoint that did not accept the test event, a key the API refused |
| 2 | the command line could not be understood; nothing was sent |

## Not in it

Changing or deleting what you created (endpoints, meters, plans, subscriptions), coupons, listing meters and plans, the operator and console routes, and a `trigger` for events other than `ping`. Those are in the API, the SDKs and the console. This command line is pre-release like the SDKs.
