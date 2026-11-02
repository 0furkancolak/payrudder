# Express

An Express 5 app in TypeScript: `POST /usage` counts one usage event and `POST /webhooks/payrudder` verifies a delivery.

```sh
(cd ../../sdk/typescript && npm ci && npm run build)   # the SDK is not on npm yet
npm ci
export PAYRUDDER_API_KEY=qk_test_...            # a sandbox key
export PAYRUDDER_BASE_URL=http://localhost:42800
export PAYRUDDER_WEBHOOK_SECRET=whsec_...        # shown once when the webhook endpoint is created
npm start                                        # Node 22.18 or newer runs server.ts directly
```

```sh
curl -X POST localhost:3000/usage -H 'Content-Type: application/json' -d '{"customer":"cus_1","meter":"api_calls"}'
```

Things to keep when you change it:

- The webhook route reads the body **raw** (`express.raw`) because the signature covers the exact bytes. A JSON parser in front of it makes every delivery fail.
- It answers `400` for a bad signature and `200` as soon as the delivery is verified; do the work after answering or on a queue, because Payrudder retries a delivery that is not answered with a 2xx.
- `POST /usage` sends an idempotency key (`Idempotency-Key` from your caller, else a random one). A live key must send one per event; derive it from what happened, not from the clock.

`npm run check` type-checks it.
