# Rails

A Rails 8.1 API app (Ruby 3.2+) with three routes and no pages, models or database: `POST /usage` counts one usage event and `POST /webhooks/payrudder` verifies a delivery. **There is no Ruby SDK**, so the app has two small classes of its own, `app/lib/payrudder/client.rb` (one `Net::HTTP` call to `POST /v1/events`) and `app/lib/payrudder/webhook.rb` (the signature check of [Webhooks](../../docs/16-webhooks.md)); to add it to an existing app, copy `app/lib/payrudder/`, the two controllers, the routes and the three settings from `config/application.rb`.

```sh
bundle install
export PAYRUDDER_API_KEY=qk_test_...            # a sandbox key
export PAYRUDDER_BASE_URL=http://localhost:42800
export PAYRUDDER_WEBHOOK_SECRET=whsec_...        # shown once when the webhook endpoint is created
bin/rails server -p 3000                         # or: bundle exec puma -b tcp://127.0.0.1:3000
```

```sh
curl -X POST localhost:3000/usage -H 'Content-Type: application/json' -d '{"customer":"cus_1","meter":"api_calls"}'
```

Things to keep when you change it:

- The app is `config.api_only` with `ActionController::API`: no session, cookie or CSRF check stands in front of the webhook. In an app that has a browser side, keep the webhook controller on `ActionController::API`, or `skip_forgery_protection` in it: a delivery has no token and would be answered `422`.
- The webhook controller verifies `request.raw_post`, the exact bytes, because the signature covers them. Do not rebuild the body from `params`.
- It answers `400` for a bad signature and `200` as soon as the delivery is verified; do the work after answering (Active Job), because Payrudder retries a delivery that is not answered with a 2xx. A missing `PAYRUDDER_WEBHOOK_SECRET` is a `500`, not a `400`: an empty secret would accept any forged signature, so the check refuses to run without one.
- `POST /usage` sends an idempotency key (`Idempotency-Key` from your caller, else a random one) in the event. A live key must send one per event; derive it from what happened, not from the clock. The client does not retry: a retry of the same call is safe because of that key, and it is your job here (the SDKs retry `429` and `5xx` for you).
- `config.filter_parameters` hides a delivery's `data` from the request log, and `wrap_parameters false` keeps the body from being logged a second time under another key.
- The settings are read once, in `config/application.rb`, and handed to the client. `Gemfile.lock` is not committed (the top level is pinned exactly instead, like the other templates); commit yours.

The smoke test runs it with Puma in production mode, which eager-loads every constant (a file `app/lib` cannot autoload fails there, not in `rails server`'s lazy development mode).
