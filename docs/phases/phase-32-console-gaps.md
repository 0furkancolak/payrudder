# Phase 32 — Console gaps: apps, seller address, translated error keys

Closes what phases 24 and 25 left to the console: the API had apps, the seller address and stable error keys, the console had no page for the first two and showed the server's English sentence for every error. See [13-admin-ui.md](../13-admin-ui.md), [ADR 0020](../adr/0020-modules-cqrs-errors-config.md) and [error-keys.md](../error-keys.md).

## Scope

- [x] **Plan** (this file), the console document and the roadmap updated.
- [x] **Apps tab.** On a tenant: the apps of its account (name, slug, sandbox and live tenant with links, created), a form to create one (name, optional slug checked with the backend's rule, currency, time zone, grace period), and per app the **API keys** of each environment: create (the plaintext is shown once and can be dismissed), revoke with a confirmation, revoke by id.
- [x] **Seller address tab.** A form for the address printed as the seller on invoices (`PATCH /admin/v1/tenants/{id}`): country first, then the fields the country asks for (state and ZIP code in the United States, county and postcode in the United Kingdom, no region for Turkey, Germany, France…), validation that is the backend's (`domain.ValidateAddress`: 120 characters, 20 for the postal code, one line, a two-letter ISO country), a hint (never a refusal) when a postal code does not look like the country's, and a **preview** of the seller block as the invoice prints it (`pkg/address.Lines`).
- [x] **Error keys in the console.** `shared/lib/errors.ts`: `errorText` / `useErrorText()` turn an API error into text by `errors.<key>` in the six locale files, passing the envelope's first detail as `{field}` and `{detail}`, and fall back to the server's message when the locale has no entry. Every place that showed `toApiError(x).message` now calls it (about fifty call sites; the console has no toasts yet, and a toast would call the same function).
- [x] **Translations** of every key in `docs/error-keys.md` and of the `common.*` defaults, in English, Türkçe, Deutsch, Español, Français and العربية.
- [x] **Tests.** Vitest: the error helper, the address rules and lines (mirroring the Go tests), and the key-coverage test; Playwright against the mocked gateway: apps, keys, the address form, an error shown in another language, an unknown key falling back.
- [x] `npm run lint`, `typecheck`, `test`, `build` and `e2e:mock` pass.

## Decisions

- **Where the pages live.** Apps and the seller address are tabs of the tenant page, like the billing tabs. The API has no list of accounts; a tenant carries its `account_id`, so "the apps of this account" is reached from any of its tenants, and each app lists its two tenants as links. A top-level accounts page would need a new operator route.
- **Renaming and deleting an app are not built.** The API has `CreateApp` and `ListApps` only; adding the console buttons without the routes would be a lie, and deleting an app (customers, invoices, keys) is a data decision that needs its own design. The page says so. Likewise there is no route that lists an app's API keys: the page keeps the keys made in this visit (prefix and id, never the plaintext after it is dismissed) and can revoke any key by id.
- **Key list is the Go registry's output, read from the repository.** The coverage test reads `docs/error-keys.md` (generated from the code and kept equal to it by `go test ./pkg/errkey`) and the `common.*` literals in `pkg/errkey/errkey.go`. A hand-kept list in TypeScript would be a third copy; a generated JSON file would need a build step both languages must run. Reading the two files the Go side already guards is the least machinery, and it fails in both directions: a key without a translation, and a translation of a key that no longer exists.
- **Keys nest in the locale files** (`errors.billing.credit_note.not_paid`), the same shape as every other message, so the parity test of the locale files covers them with no special case.
- **Params.** The envelope has no parameter map, only `details[{field, description}]`. The first detail becomes `{field}` and `{detail}`; no message uses them yet. When a service starts sending parameters (the webhook endpoint limit is in the English sentence only), they are added to the envelope and to `ApiError.params` together with the message that uses them.
- **Country is normalised, then validated.** The form capitalises the country before checking it, as the customer form does, so `se` is accepted and sent as `SE`; what is refused is what the API would refuse.
- **Postal code patterns are hints.** The backend accepts any text up to 20 characters; blocking in the console would be stricter than the API.
- **Sign-in keeps its prefix.** The sign-in error is shown as `CODE: text`, with the text translated.

## Not built

- Rename and delete of an app; a list of API keys; a list of accounts (all need operator routes first).
- Toasts: the sonner component is in the kit but no page uses it; they would use `useErrorText()`.
- Server-sent error parameters (see Decisions).
- The pages were verified against the mocked gateway, not against a running stack.
