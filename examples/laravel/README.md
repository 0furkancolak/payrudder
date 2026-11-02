# Laravel

A Laravel 13 app (PHP 8.3+) with two routes and no pages: `POST /usage` counts one usage event and `POST /webhooks/payrudder` verifies a delivery. It is a trimmed skeleton (`bootstrap/app.php`, `routes/api.php`, two invokable controllers, `config/payrudder.php`); to add it to an existing app, copy the controllers, the config file and the two routes.

```sh
composer install            # the PHP SDK is not on Packagist yet: composer.json points a path repository at ../../sdk/php
cp .env.example .env        # then fill in the three PAYRUDDER_ settings: a sandbox key, your Payrudder address, the endpoint's whsec_ secret
php artisan serve           # listens on 127.0.0.1:8000
```

```sh
curl -X POST localhost:8000/usage -H 'Content-Type: application/json' -d '{"customer":"cus_1","meter":"api_calls"}'
```

Things to keep when you change it:

- The routes are in `routes/api.php` with no prefix, so the `web` group (session, cookies, CSRF) is not in front of the webhook. In an app that has a `web` group, put the webhook route outside it or exempt it from CSRF: a delivery has no token and would be answered `419`.
- The webhook controller verifies `$request->getContent()`, the raw body, because the signature covers the exact bytes. Do not rebuild the body from `$request->all()`.
- It answers `400` for a bad signature and `200` as soon as the delivery is verified; do the work after answering, in a queued job, because Payrudder retries a delivery that is not answered with a 2xx.
- `POST /usage` sends an idempotency key (`Idempotency-Key` from your caller, else a random one). A live key must send one per event; derive it from what happened, not from the clock.
- The settings are read in `config/payrudder.php` and handed to the client. Laravel does not copy `.env` into the process environment, so the SDK's own reading of `PAYRUDDER_API_KEY` would miss a key that only `.env` holds; `env()` outside a config file returns `null` after `php artisan config:cache`.
- `composer.lock` is not committed: the SDK comes from a path, which a copy of this folder will not have. Commit yours.

`php -l` on the files is the only static check it has. The smoke test runs it with PHP's built-in server (`php -S`), not `artisan serve`.
