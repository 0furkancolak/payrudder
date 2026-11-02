# Console guide: meters, packages and pricing

How a price is set up in the console: what is measured (meters), what is sold (packages and their versions), and the money rules around them (tax, payment retries, coupons). The API is in the reference for [meters](api/meters.html) and [plans](api/plans.html); the order of the calls is in the [usage-based billing tutorial](tutorial-usage-billing.html).

It was written from the console's own texts (every name in bold is a string of the English console) and its pages' code, not by following it on a screen with a person. Where they differ, the console is right.

## Meters

Under **Usage and products**, **Meters**: **A meter says what is measured and how usage events add up. A new version of a code can change everything except its decimal places; events are checked against the latest one.** A meter is needed before a package can price or limit usage.

**New meter** asks for:

- **Code**: what your app sends as the `meter_code` of an event (for example `api_calls`).
- **Aggregation**: **Sum** adds up the quantity of every event in the period; **Count** counts the events and ignores their quantity; **Maximum** keeps the highest quantity reported in the period (seats, storage); **Distinct values** counts how many different values of one event property appeared (active users); **Latest value** keeps the quantity of the most recent event of the period (seats at the end of the month).
- **Quantity property**: the event property that holds the quantity.
- **Unit**, a **Category** of your own, and the **Decimal places** (fixed for a code).
- **Only events with**: conditions on event properties; events that do not match every line are not counted. Empty counts every event.

**Publish version** adds a new version of a code. A distinct-values or latest-value meter **keeps that aggregation in every version. Use a new code to change it.** More on events and periods is in [Usage metering](usage-metering.html).

## Packages

**Packages**: **Publishing a package that exists adds a new version. A version is fixed once published; subscriptions keep the version they started on.** A change of price is therefore always a new version, never an edit.

**New package** takes a **Code**, a **Name** and the **Fixed fee per month** in the account's currency, with **Add a fee in another currency** for other currencies (each monthly or yearly). Then:

- **Usage prices**: Prices are in the account's currency, for what is measured, per billing period. **An included quantity is free; the price applies to what is above it.** Per meter you choose a model: **Per unit**, **Graduated tiers** (each tier has an upper bound, a unit price and an optional flat fee; the last tier is **Everything above**) or **Per package of units**. A subscription in another currency needs usage prices in it too.
- **Limits**: **A quota limits a meter over a window. Usage events are never refused: the API's allowed flag and the alerts tell your app what to do.** A limit has a window, a mode (hard, soft or notify only) and alert percentages. A soft limit bills what is over it, so its meter needs a usage price.
- **Free trial in days (optional)**: **A subscription to this package starts trialing for this many days, with no card; the first fee is invoiced when the trial ends.** A few days before it ends the customer is e-mailed and a `subscription.trial_will_end` webhook event is sent.
- **Charge the first period from the end of the trial**: when a trial ends in the middle of a billing period, only the rest of the period is charged.
- **Free tier**: no fixed fee, only the limits you set.

**Publish version** makes the version. Moving customers from one version to another is **Schedule a price change**: **Moves the customers of one version to another at their next renewal and tells each of them by mail, in their language. Nothing changes before then.** The notice is at least 14 days; the dialog lists what it will do and which subscriptions it leaves alone and why (they renew too soon, already have a change waiting, are run by a payment provider, or belong to a blocked customer).

The same is `POST /v1/plans` and `POST /v1/plans/{code}/versions`; the price change is `POST /v1/plans/{code}/price-changes`.

## Tax and retries

The page named **Tax and retries** holds two settings.

**Tax**: **Payrudder adds these rates on top of the price to the invoices it raises, by the customer's country (the rate without a country covers all others). You set the rates and are responsible for them; an invoice a provider issues is taxed by the provider.** Add a rate per country (and **Every other country**) with a name and a percentage, then **Save rates**. Payrudder is not a tax engine: see [Tax and e-invoices](tax-and-einvoices.html). The same is `PUT /v1/tax-rates`.

**Payment retries and chargebacks**: **When a charge is declined, Payrudder tries again after each delay below, then gives the invoice up as uncollectible. Delays are in hours (minutes in a sandbox account).** Up to six retries, each a whole number of hours from 1 to 720. With none set, the standard delays are shown. Switch on **Block the customer when a chargeback opens** to block a customer, with the category chargeback, as soon as their bank opens a dispute (no new purchases, open invoices can no longer be paid); it never lifts by itself, you lift it on the customer, and an inquiry does not block. Then **Save settings**. The same is `/v1/dunning-settings`.

## Coupons and promotion codes

**Coupons**: **A coupon takes a percentage or an amount off the fixed fee Payrudder invoices in advance: once, for a number of periods, or for as long as the subscription lasts. A subscription run by a provider uses a discount at the provider instead.** The duration is **First fee only**, **A number of fees** or **Every fee**, with an optional **Redemptions limit**. A coupon cannot be switched on again once deactivated.

**Promotions**: **A coupon says what is taken off. A customer types a promotion code: many codes can point to one coupon, each with its own limit, expiry and rules.** Codes are named one per line or generated; each can be limited to a number of uses, an expiry, a minimum amount, currencies, plans, **First purchase only** or **Once per customer**. **Redemptions** lists every use; a void or a full refund reverses it. The API is `/v1/coupons` and `/v1/promotion-codes`.
