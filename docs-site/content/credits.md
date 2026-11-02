# Credits and balances

Two different things are called credit. They never mix.

## A credit balance in money

When an invoice has to be reduced without refunding a card, the amount goes to the customer's **credit balance**, in the invoice's currency, and pays their next invoices: a credit note ([Invoices](invoices.html)), the unused part of a fee when a subscription ends with `credit_unused=true` or moves to a cheaper price, and late-usage corrections that come to more than the invoice. It is applied automatically when the next invoice is finalized, as a `CREDIT` line. You see it in `GET /v1/customers/{external_id}/upcoming-invoice` (`credit_minor` is what the invoice would use, `credit_surplus_minor` what would be added). Giving money back to a card is a different act: a credit note with `refund_to_card` ([Invoices](invoices.html)).

## Prepaid credits

Credits are a currency of your own: a customer buys or is given a number of them and your app **spends** them (generated pages, API calls, anything you count in whole units). They are separate from meters and invoices.

- **Balance:** `GET /v1/customers/{external_id}/credits` is what the customer can spend now (credits whose time has passed are left out) and `lots`, where it comes from, the ones that expire first at the top. A lot has a `source`: `purchase` (a pack that was paid for), `promo` or `manual`.
- **Give:** `POST /v1/customers/{external_id}/credits/grant`, for a promotion, a goodwill gesture or a sale made outside Payrudder. With an `idempotency_key` (or the `Idempotency-Key` header) a repeat adds nothing and answers `replayed: true`; the same key with other values is `billing.credits.key_reused`.
- **Spend:** `POST /v1/customers/{external_id}/credits/consume`. It takes the credits that expire first. With too few credits nothing is taken and the answer is `409 billing.credits.insufficient`. An idempotency key is **required**, so a retried call returns the first result instead of charging twice ([Idempotency](idempotency.html)).
- **History:** `GET /v1/customers/{external_id}/credits/ledger` lists every change, newest first: `grant`, `consume` and `expire`.
- **Sell:** a **credit pack** is a number of credits for a price in one currency (`POST /v1/credit-packs`; `activate` and `deactivate` put it on sale or withdraw it, credits already sold are not touched). A customer buys it through a payment link and the credits are granted when the payment is confirmed (webhook `credits.purchased`).
- **Warn:** `PUT /v1/credits/settings` sets a `low_threshold`; a consume that takes a customer's balance from at least that number to fewer announces `credits.low` once ([Webhooks](webhooks.html)). 0, the default, turns it off.

In the console this is **Credits**: a customer's balance, the packs you sell and the low-balance alert.
