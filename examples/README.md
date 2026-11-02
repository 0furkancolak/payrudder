# Starting points for an app

Seven small web apps, each doing the two things an app does with Payrudder: **count usage** (`POST /usage` sends one usage event for a customer) and **receive webhooks** (`POST /webhooks/payrudder` checks the signature of a delivery before it trusts it). Copy the one that matches your stack and change it.

| Directory | Stack | Run it |
|---|---|---|
| [`express/`](express/) | Node 22.18+ (TypeScript), Express 5 | `npm ci && npm start` |
| [`fastapi/`](fastapi/) | Python 3.10+, FastAPI | `pip install -r requirements.txt && uvicorn main:app` |
| [`go-nethttp/`](go-nethttp/) | Go 1.23+, `net/http` | `go run .` |
| [`nextjs/`](nextjs/) | Node 22.18+, Next.js 16 (App Router, TypeScript) | `npm ci && npm run build && npm start` |
| [`laravel/`](laravel/) | PHP 8.3+, Laravel 13 | `composer install && php artisan serve` |
| [`rails/`](rails/) | Ruby 3.2+, Rails 8.1 (API mode; no Ruby SDK, so a small client of its own) | `bundle install && bin/rails server` |
| [`django/`](django/) | Python 3.12+, Django 6 | `pip install -r requirements.txt && gunicorn payrudder_app.wsgi` |

They share the settings the SDKs read themselves: `PAYRUDDER_API_KEY` (a `qk_test_...` key of the sandbox), `PAYRUDDER_BASE_URL` (your Payrudder address, `http://localhost:42800` on a local stack) and, for the webhook route, `PAYRUDDER_WEBHOOK_SECRET` (the `whsec_...` secret shown once when the endpoint was created). To reach your machine from Payrudder use `payrudder webhooks listen` (see the command line page) or a tunnel of your own.

**Nothing here is published.** The SDKs are not on npm, PyPI or a module proxy yet, so each template installs the SDK from this checkout (a `file:` dependency, a path requirement, a `replace` directive, a Composer path repository). Build the TypeScript SDK first for Express and Next.js: `cd sdk/typescript && npm ci && npm run build`. There is no Ruby SDK: the Rails template has a small client and the signature check of its own (`app/lib/payrudder/`), written from [Webhooks](../docs/16-webhooks.md) and the OpenAPI file.

## How they are checked

`.github/workflows/examples.yml` compiles each template (`tsc`, `next build`, `python -m compileall`, `go vet`, `php -l`, `ruby -c`) and runs `node examples/smoke.mjs`, which starts the app next to a stand-in for the API made from `api/openapi/v1.yaml` and checks over HTTP that:

- a delivery signed the documented way is accepted, and a changed body, a wrong secret, an old timestamp and a missing header are refused;
- `POST /usage` reaches the API as a usage event that fits the contract, with the app's key.

That proves the template starts and uses the SDK correctly. It does **not** prove anything about a real gateway, a real key or a real delivery: run it against your sandbox before you rely on it.
