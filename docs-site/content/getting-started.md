# Getting started

Payrudder meters what your customers use, prices it, invoices it and collects the money. This page takes you from an empty app to a first usage event.

## 1. Create an app and a key

Sign in to the console and create an app. Every app has two environments: the **sandbox** (nothing is real: payments run against a simulator) and **live**. Create an API key on the app's **API keys** page. A key starts with `qk_test_` (sandbox) or `qk_live_` (live), is shown **once**, and acts on its own environment only.

Send it as a bearer token:

```sh
curl https://YOUR-API-ADDRESS/v1/meters -H "Authorization: Bearer $PAYRUDDER_API_KEY"
```

`YOUR-API-ADDRESS` is the address of your Payrudder deployment's gateway (`http://localhost:42800` on a local stack). The console's **Connect your app** page builds the code below from your own meter and address.

## 2. Define what you count

A **meter** says what is counted and how it adds up (`sum` of API calls, `max` of storage, `unique` users). Create one in the console or with `POST /v1/meters`.

## 3. Send usage

```sh
curl https://YOUR-API-ADDRESS/v1/events \
  -H "Authorization: Bearer $PAYRUDDER_API_KEY" -H "Content-Type: application/json" \
  -d '{"events":[{"customer_external_id":"cus_42","meter_code":"api_calls","quantity":"1","event_time":"2026-11-03T10:15:00Z","idempotency_key":"evt-1"}]}'
```

Up to 500 events per request. The answer has one result per event, in order: `accepted`, `duplicate` (the same `idempotency_key` again) or `rejected` with an `error_code`. A bad event does not fail the batch. Live keys must send an `idempotency_key` for every event, so a retry never counts twice. `quantity` is an exact decimal: send it as a string.

## 4. Package, price, subscribe

Publish a **package** (a plan with a fixed fee, per-unit prices for meters and limits), then subscribe a customer to it with `POST /v1/subscriptions`. At the end of the period Payrudder turns the usage into an invoice, collects it through the payment provider you connected, and sends the receipt, in the customer's language.

## 5. Hear about it

Register a webhook endpoint (`POST /v1/webhook-endpoints`) and verify each delivery with the SDK, see [Webhooks](webhooks.html).

## Errors, retries, limits

Every error has a stable `error.key` (see [Error keys](error-keys.html)): translate the key, not the message. A `429` carries `Retry-After`. The SDKs retry `429` and `5xx` for you, with the same idempotency key.

## SDKs

TypeScript, Python, Go and PHP: [SDKs](sdks.html). They are generated from the same OpenAPI file as the [API reference](api/index.html). To let an AI assistant read your account, see the [MCP server](mcp.html).
