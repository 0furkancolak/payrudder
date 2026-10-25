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

## Tasks

- [x] Schema and program/affiliate CRUD, platform-wide codes, per-day click counts without a visitor, first-touch attribution that a trigger keeps immutable, self-referral blocking (same mailbox incl. `+tag`, same company domain but not a shared mail provider, same payment method), logged and kept in `affiliate_attribution_rejections` (`00047`, PR #117)
- [x] Commissions on `invoice.paid` (one per invoice, net pre-tax base), hold job (pending to approved), reversal on void / credit note / chargeback / by hand, a negative row after payout (`00048`, PR #120)
- [x] Payouts per affiliate and currency, netting, mark paid, accounting postings balanced per currency by a deferred trigger, sandbox payouts fake (`00050`, PRs #122 and #129)
- [x] Hosted `/r/<code>` and `?ref=` cookie, checkout and sign-up read it, `/privacy` (PR #129)
- [x] Owner console Growth, Affiliates (PR #126) and partner portal (API PR #132, pages: see the PR list of the phase)
- [x] Docs, privacy text, this file

## As built, and decisions

- **No new service.** Module `affiliates` in plans-svc (model / command / query / portal), `AffiliateService` (key-scoped), `AffiliatePublicService` (the public link, system pool, no tenant), `AffiliatePortalService` (a signed-in person). billing-svc already hands every invoice and credit note event to plans-svc (`RecordTenantEvent`, from its `tenant_event_outbox`); plans-svc reads them in the transaction that records them (`EventHook`), idempotently: `invoice_id` is unique for commissions and every reversal has a key (`void:<invoice>`, `credit_note:<id>`, `chargeback:<invoice>:<ref>`, `manual:...`). `invoice.paid` now also carries `environment`, `tax_minor` and `net_minor`; an event whose environment is not its tenant's is refused (ADR 0022).
- **Codes are unique across the platform**, not per tenant: `/r/<code>` carries no tenant. Sandbox and live programs therefore use different codes.
- **Commission rules.** Percent (basis points, half up) of the net, pre-tax amount actually paid, in the invoice's currency; fixed only on invoices in its own currency (no conversion exists). Term: first N invoices, N months from the first commission, lifetime. A suspended affiliate or a paused program earns nothing new; approved commissions are kept. Credit notes reverse in proportion to the cumulative credited share of the invoice.
- **Payouts.** One open payout per affiliate and currency; later approvals join it. `min_payout_minor` is one number compared with each currency's total in that currency's minor units. A payout whose refunds left nothing is cancelled, not paid; a negative balance rolls forward. Payrudder moves no money; marking paid posts Dr `commission_expense` / Cr `commissions_payable`, then Dr `commissions_payable` / Cr `cash`.
- **Role `affiliate`.** A partner who claims their record becomes a member of the seller's account with this role; every account-wide read in identity leaves it out (tested), so it sees only the partner portal.
- **Cookie.** `payrudder_ref` = code + time, first-party, HttpOnly, Lax, window-long. First touch keeps the cookie; a reload is not a second click. A guest payment has no customer to attribute.
- **Sandbox/live.** Tenant-scoped tables; sandbox payouts and postings are `fake` and labelled; a live tenant that is not `active` (phase 45 gate) can neither create nor pay a payout; a sandbox reset empties commissions, payouts and postings and keeps the program and partners; a live journal is append-only (trigger).
- **Setting.** `PAYRUDDER_AFFILIATE_APPROVE_EVERY` (plans-svc, default `1h`): how often the hold job looks.

## Limits (not done)

- The journal is plans-svc's own double-entry record and `GET /v1/affiliates/journal(.csv)`; it is not yet folded into billing-svc's Accounting summary (which reads the `expenses` table).
- No automatic chargeback source: payments-worker records disputes in its ledger but billing has no event for them; use `POST /v1/affiliates/commissions/reverse`.
- Real stack end to end (compose with the gateway, a provider checkout with a referral cookie, the console against plans-svc) was not run; the pieces are tested against real Postgres and fakes of their neighbours.
- Applications are by tenant id (`/app/partner/apply?tenant=<id>`); there is no public directory of programs.
