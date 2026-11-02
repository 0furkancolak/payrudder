# Packages and plans

What a customer buys is a **package**. The console says package, the API says **plan**: they are the same thing (`/v1/plans`). A package is a **code** and a series of numbered **versions**; the version holds the money.

## Package and version

`POST /v1/plans` makes the package: a `code` (what your app uses to refer to it) and a `name`. Nothing is priced yet. `POST /v1/plans/{code}/versions` publishes the next version, numbered 1, 2, 3, … A published version is **immutable**: a change of price is a new version, never an edit, and `GET /v1/plans/{code}/versions/latest` (or a number) reads one back.

A version holds:

- **Fixed fees**: `base_fee_minor` is the monthly fee in the account's currency; `fees` adds a fee for another currency or for the **yearly** interval. A yearly price is its own number, not twelve months, and a version without a fee for an interval and currency cannot be sold at it.
- **Usage prices**, one per meter (`prices`). A meter is priced in one of three ways: `per_unit` (one price for every unit), `tiered` (graduated tiers, each with an upper bound, a unit price and an optional flat fee; the last tier has no bound) or `package` (a price for each block of `package_size` units). `included` is a quantity of every billing period that is free: the model prices only what is above it. Amounts of money are integers in the currency's minor unit, sent as strings; unit prices are decimal strings.
- **Limits** (`entitlements`), one per meter: a `limit` in the meter's unit (`"0"` is unlimited), a window, a mode (hard, soft or notify only) and the percentages at which an alert fires (80 and 100 when left empty). A limit never refuses a usage event: the API's `allowed` flag and the alerts tell your app what to do, and a soft limit bills what is over it. Limits are enforced from a fast counter that is reconciled with the exact figures, so they are eventually consistent ([Usage metering](usage-metering.html)).
- **A trial** (`trial_days`, 0 to 365): a subscription to this version starts trialing for that long unless its own request says otherwise. When a trial ends inside a billing period, only the rest of the period is charged (`prorate_after_trial`, true for a version published now).

## Which version a customer is on

A subscription **keeps the version it started on**. Publishing a new version changes nothing for the customers already subscribed; a new subscription gets the latest unless it names a version. To move existing customers, schedule a **price change**: `POST /v1/plans/{code}/price-changes` moves the subscriptions of one version to another at their next renewal, no sooner than the notice you give (default 30 days, at least 14, at most 365), and each customer is told by mail in their language. `dry_run` counts and changes nothing. Subscriptions that cannot be moved are listed in `skipped` with the reason (`renews_soon`, `has_pending_change`, `run_by_provider`, `not_changeable`, `customer_blocked`). See [Subscriptions](subscriptions.html) for what happens to one subscription and [Versioning](versioning.html) for the other versions in the product.

## What a package does not do

- It does not hold the customer's tax: the account's tax rates are applied to every invoice by the customer's country ([Tax and e-invoices](tax-and-einvoices.html)).
- It cannot be archived or deleted through the API: a package stays listed (`GET /v1/plans`, which is not paged) and a version stays readable, because subscriptions refer to it.
- A discount is a **coupon** on a subscription, not part of the version (`POST /v1/subscriptions/{id}/coupon`); it takes something off a fixed fee that Payrudder invoices in advance.

In the console this is the **Packages** page; the [meters, packages and pricing guide](console-pricing.html) goes through the form. The calls in order are in the [usage-based billing tutorial](tutorial-usage-billing.html).
