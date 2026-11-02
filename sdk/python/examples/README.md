# Python examples

Four small programs: [send a usage event](send_usage.py), [create a customer](create_customer.py), [list invoices](list_invoices.py) and [verify a webhook](verify_webhook.py). They import `payrudder`, as your own code will.

Nothing is published: install the SDK from this checkout (`pip install ./sdk/python`), then

```sh
export PAYRUDDER_API_KEY=qk_test_...             # a sandbox key
export PAYRUDDER_BASE_URL=http://localhost:42800 # the default
python sdk/python/examples/send_usage.py
```

`verify_webhook.py` also needs `PAYRUDDER_WEBHOOK_SECRET`, the secret of the endpoint that posts to it. It uses the standard library's server so it needs nothing else; in a Flask or FastAPI handler the two lines that matter are the same (`construct_event(raw_body, header, secret)`).

They are not run in CI (they need a gateway). They are **type-checked** with `mypy --strict` against the installed package and linted and formatted with `ruff` like the rest of the SDK, so a renamed method or field fails the build.
