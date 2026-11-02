# TypeScript examples

Four small programs: [send a usage event](send-usage.ts), [create a customer](create-customer.ts), [list invoices](list-invoices.ts) and [verify a webhook](verify-webhook.ts). They import `@payrudder/sdk`, as your own code will.

The SDK is not published: build it from this checkout and install the tarball in your project (see [Install](../README.md#install-from-the-repository)). Then copy an example in and run it, for example with [tsx](https://tsx.is) (`npx tsx send-usage.ts`):

```sh
export PAYRUDDER_API_KEY=qk_test_...            # a sandbox key
export PAYRUDDER_BASE_URL=http://localhost:42800 # the default
npx tsx send-usage.ts
```

`verify-webhook.ts` also needs `PAYRUDDER_WEBHOOK_SECRET`, the secret of the endpoint that posts to it.

They are not run in CI (they need a gateway). They are **type-checked** against `src/` with the SDK's own settings (`npm run typecheck`) and linted with the rest of the package, so a renamed method or field fails the build.
