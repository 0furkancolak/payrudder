# PHP examples

Four small scripts: [send a usage event](send_usage.php), [create a customer](create_customer.php), [list invoices](list_invoices.php) and [verify a webhook](verify_webhook.php).

Nothing is published: require the SDK from this checkout as the [SDK's README](../README.md#install) shows (or run `composer install` here), then

```sh
export PAYRUDDER_API_KEY=qk_test_...             # a sandbox key
export PAYRUDDER_BASE_URL=http://localhost:42800 # the default
php examples/send_usage.php
```

`verify_webhook.php` is a request handler: serve it (`PAYRUDDER_WEBHOOK_SECRET=whsec_... php -S localhost:3000 examples/verify_webhook.php`) and register its address as an endpoint.

They are not run in CI (they need a gateway). They are analysed by **PHPStan at level max** against the SDK's source and styled by php-cs-fixer like the rest of the package, so a renamed method or field fails the build.
