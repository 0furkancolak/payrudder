# Go examples

Four small programs, one directory each: [send a usage event](send-usage/main.go), [create a customer](create-customer/main.go), [list invoices](list-invoices/main.go) and [verify a webhook](verify-webhook/main.go).

```sh
export PAYRUDDER_API_KEY=qk_test_...             # a sandbox key
export PAYRUDDER_BASE_URL=http://localhost:42800 # the default
cd sdk/go && go run ./examples/send-usage
```

`verify-webhook` also needs `PAYRUDDER_WEBHOOK_SECRET`, the secret of the endpoint that posts to it. In your own module, point it at a checkout of this repository as in the [SDK's README](../README.md) (nothing is published).

They are not run in CI (they need a gateway); they are **built, vetted and linted** with the module (`go build ./...`, `go vet ./...`, golangci-lint), so a renamed method or field fails the build.
