# Go net/http

A `net/http` server (Go 1.22 routing patterns): `POST /usage` counts one usage event and `POST /webhooks/payrudder` verifies a delivery. Standard library plus the SDK.

```sh
export PAYRUDDER_API_KEY=qk_test_...            # a sandbox key
export PAYRUDDER_BASE_URL=http://localhost:42800
export PAYRUDDER_WEBHOOK_SECRET=whsec_...        # shown once when the webhook endpoint is created
go run .                                         # listens on :3000 (PORT changes it)
```

`go.mod` points the SDK at `../../sdk/go` with a `replace` directive because the module is not on a proxy yet; drop the directive when it is. If your checkout is part of the repository's Go workspace, run with `GOWORK=off`.

```sh
curl -X POST localhost:3000/usage -H 'Content-Type: application/json' -d '{"customer":"cus_1","meter":"api_calls"}'
```

Things to keep when you change it:

- The webhook handler reads the raw body and passes it to `payrudder.ConstructEvent` before parsing anything, because the signature covers the exact bytes.
- It answers `400` for a bad signature and `200` as soon as the delivery is verified; do the work after answering or on a queue, because Payrudder retries a delivery that is not answered with a 2xx.
- `POST /usage` sends an idempotency key (`Idempotency-Key` from your caller, else one made from the clock). A live key must send one per event; derive it from what happened.
