# Django

A Django 6 project (Python 3.12+) with three views and no models, templates or database: `POST /usage` counts one usage event with the SDK's client and `POST /webhooks/payrudder` verifies a delivery. To add it to an existing project, copy `payrudder_app/views.py` and the two routes in `payrudder_app/urls.py`.

```sh
python -m venv .venv && . .venv/bin/activate
pip install -r requirements.txt                  # the SDK is installed from this checkout (../../sdk/python), it is not on PyPI yet
export PAYRUDDER_API_KEY=qk_test_...            # a sandbox key
export PAYRUDDER_BASE_URL=http://localhost:42800
export PAYRUDDER_WEBHOOK_SECRET=whsec_...        # shown once when the webhook endpoint is created
gunicorn payrudder_app.wsgi --bind 127.0.0.1:3000
```

```sh
curl -X POST localhost:3000/usage -H 'Content-Type: application/json' -d '{"customer":"cus_1","meter":"api_calls"}'
```

Things to keep when you change it:

- Both views are `@csrf_exempt` and the CSRF middleware stays on in `settings.py`. A delivery carries no CSRF token and would be answered `403` without the exemption; `/usage` is meant for your own backend, not a browser form.
- The webhook view verifies `request.body`, the exact bytes, because the signature covers them. Do not rebuild the body with `json.dumps` of what you parsed.
- It answers `400` for a bad signature and returns as soon as the delivery is verified; do the work after answering (a task queue), because Payrudder retries a delivery that is not answered with a 2xx.
- `POST /usage` sends an idempotency key (`Idempotency-Key` from your caller, else a random one). A live key must send one per event; derive it from what happened, not from the clock.
- The client is made on first use (`client()`), so `manage.py check` and the start-up work without the settings. `settings.py` has `ALLOWED_HOSTS = ["*"]` and a random `SECRET_KEY` because nothing here uses either; set both in your own project.
- The SDK client here is the synchronous one. In an async Django view use `AsyncPayrudder` (the FastAPI template shows it).
