# FastAPI

A FastAPI app: `POST /usage` counts one usage event and `POST /webhooks/payrudder` verifies a delivery. It uses the SDK's async client.

```sh
python -m venv .venv && . .venv/bin/activate
pip install -r requirements.txt                  # the SDK is installed from this checkout (../../sdk/python), it is not on PyPI yet
export PAYRUDDER_API_KEY=qk_test_...            # a sandbox key
export PAYRUDDER_BASE_URL=http://localhost:42800
export PAYRUDDER_WEBHOOK_SECRET=whsec_...        # shown once when the webhook endpoint is created
uvicorn main:app --port 3000
```

```sh
curl -X POST localhost:3000/usage -H 'Content-Type: application/json' -d '{"customer":"cus_1","meter":"api_calls"}'
```

Things to keep when you change it:

- The webhook route reads `await request.body()`, the exact bytes, because the signature covers them. Do not take the body as a parsed model.
- It answers `400` for a bad signature and returns as soon as the delivery is verified; do the work after answering (a background task or a queue), because Payrudder retries a delivery that is not answered with a 2xx.
- `POST /usage` sends an idempotency key (`Idempotency-Key` from your caller, else a random one). A live key must send one per event; derive it from what happened, not from the clock.
