# Phase 47 — Affiliate program

## Goal

An app owner can run a partner program: partners get a link and a code, customers they bring are remembered, the partner earns a commission on what those customers pay, and the owner pays them out. Built on the redemption ledger (phase 46) and accounting (phase 37).

## Model (all tenant-scoped, so sandbox and live are separate)

| Table | Purpose |
|---|---|
| `affiliate_programs` | one per tenant: `commission_kind` (percent_bps / fixed), value, currency, `commission_term` (first N invoices / N months / lifetime), `attribution_window_days` (default 30), `hold_days` (refund window, default 30), `min_payout_minor`, `referred_coupon_id` (optional discount for the referred customer), status |
| `affiliates` | a partner: person (identity user) or just an e-mail, `code` unique in the tenant, status (pending / active / suspended), payout details (free text + method: manual / bank transfer) |
| `affiliate_clicks_daily` | aggregated counts per affiliate per day (no per-visitor rows, no IP stored) |
| `affiliate_attributions` | `customer_id` → `affiliate_id`, `source` (link / code), `attributed_at`; **first touch wins, set once**, never moved |
| `affiliate_commissions` | one row per paid invoice per attributed customer: `invoice_id` unique, amount, currency, status `pending → approved → paid` or `reversed` |
| `affiliate_payouts` | batch of approved commissions in one currency: total, method, reference, paid_at |

## Flow

1. `https://<hosted>/r/<code>` (or `?ref=<code>`) sets a first-party cookie for the attribution window and counts a click; the hosted checkout and sign-up read it. A typed promotion code that belongs to an affiliate attributes the same way.
2. When the customer is created or first pays, the attribution is written once (self-referral blocked: same e-mail, same domain as the affiliate, or same payment-method fingerprint ⇒ no attribution, logged).
3. On `invoice.paid` (outbox event, consumed idempotently) a commission is computed on the **net, pre-tax** amount actually paid, in the program's rules. Commission stays `pending` for `hold_days`, then `approved` by a daily job.
4. Refund, void, credit note or chargeback on the invoice **reverses** the commission (negative row if already paid, to be netted from the next payout).
5. The owner creates a payout from approved commissions ≥ `min_payout_minor`; marking it paid posts to accounting: commission expense and payable → cash. Payout is manual or bank transfer in v1 (no automatic money movement).

## Surfaces

- **Owner console** (Growth → Affiliates): program settings, affiliates list and approval, commissions with filters, payouts, a funnel (clicks → customers → paid → commission).
- **Affiliate portal**: an affiliate signs in with the normal methods (phase 42) and sees only their own link, clicks, referrals (customer shown as a masked e-mail), earnings by status, payouts. A `affiliate` membership role has no access to anything else.
- API: gateway routes under `/v1/affiliates/*` for the owner (key-scoped) and `/auth`-session routes for the portal.

## Rules and limits

- No multi-level (MLM) commissions, ever; one level only.
- Commission currency = invoice currency; a payout is per currency, no conversion.
- Sandbox payouts are fake and clearly labelled; live payouts follow phase 45's gate.
- Abuse: per-affiliate daily click cap, suspended affiliates earn nothing new but keep approved balance until review.
- Privacy: no cross-site tracking; the cookie holds only the affiliate code and timestamp; the privacy page text is added.

## Acceptance

- Idempotent: replaying `invoice.paid` creates one commission. Reversal after payout nets correctly.
- First touch is immutable; self-referral rejected; hold period respected.
- Accounting entries balance (debits = credits) per currency in a test.
- Error keys, six locales, RTL check of the portal.

## Out of scope
Automatic payouts through a provider, tax forms for affiliates, multi-level, cross-tenant (platform-wide) referral program: the engine is tenant-scoped so a platform program can later be one more tenant.
