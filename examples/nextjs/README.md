# Next.js

A Next.js 16 app (App Router, TypeScript) with two route handlers: `POST /usage` counts one usage event and `POST /webhooks/payrudder` verifies a delivery. No pages: add yours next to them.

```sh
(cd ../../sdk/typescript && npm ci && npm run build)   # the SDK is not on npm yet
npm ci
export PAYRUDDER_API_KEY=qk_test_...            # a sandbox key
export PAYRUDDER_BASE_URL=http://localhost:42800
export PAYRUDDER_WEBHOOK_SECRET=whsec_...        # shown once when the webhook endpoint is created
npm run build && npm start                       # listens on :3000 (PORT changes it); `npm run dev` for development
```

```sh
curl -X POST localhost:3000/usage -H 'Content-Type: application/json' -d '{"customer":"cus_1","meter":"api_calls"}'
```

Things to keep when you change it:

- The webhook route reads the body **raw** (`request.arrayBuffer()`) because the signature covers the exact bytes. `request.json()` and then serialising it again makes every delivery fail.
- It answers `400` for a bad signature and `200` as soon as the delivery is verified; do the work after answering or on a queue, because Payrudder retries a delivery that is not answered with a 2xx.
- `POST /usage` sends an idempotency key (`Idempotency-Key` from your caller, else a random one). A live key must send one per event; derive it from what happened, not from the clock.
- The client is made on first use (`lib/payrudder.ts`), because `next build` loads every route without your settings and the SDK refuses to start without a key.
- Keep both routes on the server: the API key must never reach the browser, so do not give it a `NEXT_PUBLIC_` name.
- `next.config.ts` has two lines that exist only because the SDK is installed from this checkout (a link outside the folder); drop them when it comes from a registry.

`npm run check` type-checks it. The `PORT` the smoke test sets is the one `next start` reads.
