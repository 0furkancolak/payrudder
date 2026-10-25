# Phase 46 — Coupons and promotion codes

## Where we are

`Coupon` (plans-svc) takes a percentage or an amount off the **fixed fee of a subscription**, with a duration (once / repeating / forever), a redemption cap and an expiry. It cannot be used for a credit purchase, a payment link or a one-off invoice, a customer types nothing (an operator applies it), there is no per-customer rule, and `redeemed` is a counter, not a record.

## Scope

1. **Coupon vs promotion code.** The coupon is the discount definition (what it takes off, for how long). A **promotion code** is what a customer types: many codes can point to one coupon, each with its own cap, expiry, and restrictions: first purchase only, one use per customer, minimum amount, allowed plans/packages, allowed currencies. Existing coupons migrate to a coupon plus one promotion code with the same code.
2. **Redemption ledger.** `coupon_redemptions(tenant, coupon, promotion_code, customer, target_kind, target_id, invoice_id, discount_minor, currency, created_at, reversed_at)`. `redeemed` becomes a count of it. A redemption is claimed with a conditional update so two checkouts cannot both take the last one (tested with 50 in parallel). A void or full refund reverses the redemption (the count is given back only for a code that says so; default no, as today).
3. **Where a code works.**
   - Subscription fee (exists, kept).
   - **Credit purchase**: percentage or amount off the price, or **bonus credits** (`bonus_percent_bps`): extra credits granted in the same lot.
   - **Payment link / hosted checkout / hosted invoice**: a "Promotion code" field; the total updates before payment; the discount is a negative invoice line (`DISCOUNT`, exists).
   - One-off invoice created by an operator.
4. **Rules.** One coupon per subscription or purchase (no stacking, v1); the discount never takes the total below zero; amount coupons apply only in their currency, with a clear refusal otherwise; tax is computed on the discounted base.
5. **Accounting.** A discount posts as contra-revenue in the phase 37 ledger; reports show gross, discounts and net per coupon.
6. **Console.** Pricing → Promotions: create coupon and codes (bulk generate N random codes), see redemptions and discount given, deactivate. The customer's checkout and the person's purchase flow show the field and the error in their language.
7. **Environments.** Coupons are tenant data, so sandbox and live are separate; phase 45 promote copies them.
8. **Affiliate hook.** A program (phase 47) may name a coupon to give referred customers; nothing else in this phase depends on it.

## Acceptance

- Concurrency test on the last redemption; per-customer and first-purchase rules; currency mismatch refused; tax on the discounted base; credit bonus lot has the right expiry.
- Error keys (`coupon.expired`, `.exhausted`, `.not_applicable`, `.currency_mismatch`, `.already_used`) in six locales.
- Migration of existing coupons is reversible and tested on seeded data.

## Tasks

- [x] Coupon vs promotion code; existing coupons migrated (`00030_promotions.sql`, reversible, tested up/down/up on seeded data)
- [x] Redemption ledger, claimed with conditional updates (50 parallel checkouts test), reversal on void and full credit (`restore_on_reversal` decides the count)
- [x] Credit purchase (discount and bonus credits in the same lot), payment links, hosted page field with a quote before Continue, operator-chosen code on a link
- [x] Rules: one coupon per purchase, never below zero, amount coupons only in their currency, first purchase, one per customer, minimum, plans/packs/currencies
- [x] Accounting: contra-revenue, gross/discount/net per coupon (`00032`), CSV
- [x] Console: Pricing promotions (coupons, bulk codes, redemptions), code fields on payment links and purchase links, coupon report; six languages
- [x] Error keys `coupon.*` in six locales (console and hosted pages)
- [x] Affiliate hook: `GrantCustomerCoupon` / `customer_coupons`, used by the next checkout without a code

## Decisions and limits

- **Tax.** A subscription fee invoice takes its tax on the discounted fee (existing test). A payment-link invoice carries no tax (its amount is what the payer pays), so there is nothing to take on a base. An existing invoice (kind invoice) is numbered and immutable, so it takes no code; an operator's one-off with a discount is an ad-hoc link with `promotion_code`.
- **Free totals.** A code that takes the whole price is refused (`billing.payment_link.free_total`): a provider cannot charge nothing.
- **Claim before invoice.** The redemption is claimed for the new invoice's id, then the invoice is made; if that fails the claim is reversed (the place stays used unless the code restores). A crash between the two leaves a redemption without an invoice, visible in the list.
- **Not verified here.** Live provider checkouts with a discounted amount; plans-svc and billing-svc together (billing was tested with a fake plans-svc, plans-svc with a real database); no e2e against the compose stack.
