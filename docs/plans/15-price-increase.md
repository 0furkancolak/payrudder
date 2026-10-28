# 15 — Price increases and notices

## The request
List 2 (8): the app must support **making a price increase** and **notifying about it**.

## Where we are
#253: `POST /v1/plans/{code}/price-changes` moves every Payrudder-run subscription on one plan version to another **at its next renewal** if that renewal is at least `notice_days` away (default 30, min 14), reusing the existing scheduled plan change; mails each customer (`price_change.v1`, six languages) through a Kafka fact `subscription.price-change.v1` and a notifier consumer; names the skipped ones (`renews_soon`, `has_pending_change`, `run_by_provider`, `not_changeable`); a dry run; a package row action with check/confirm in the console.

## Gaps
- The date cannot be chosen: the change happens at the renewal, so a customer renewing in 20 days with a 30-day notice is skipped, and the seller must rerun. For **yearly** plans the next renewal may be 11 months away, so an increase takes a year; no "effective date" model.
- **The mail path was never run end to end** (outbox → relay → Kafka → consumer → Nafru/SMTP). Only the outbox row and the template rendering are tested.
- Skipped subscriptions have no follow-up: no schedule that retries, no list view of "customers not yet notified".
- No record of **who was notified when** visible to the seller (the change row is the only trace), no webhook specific to the price change (only `subscription.change_scheduled`), no way to withdraw a whole price change (each scheduled change can be withdrawn individually).
- Provider-run subscriptions (Paddle) are skipped entirely; Paddle has its own price change rules and notice requirements (`subscription update` with `proration_billing_mode`, and "scheduled change").
- Legal minimums differ by country (consumer protection: notice periods, consent for increases in some jurisdictions, right to cancel); the 14-day floor is a guess.
- Price **increase** vs decrease are not distinguished; no percentage/cap rules, no grandfathering option (keep old price for existing customers), no coupon interplay, tax display, or currency-specific price tables checked for completeness in the target version.
- The notice mail is in the customer's language but the amount is formatted without locale rules (see 05).

## Research first
1. Legal minimums for the owner's markets (EU consumer rules, Turkey, UK, US state laws on auto-renewal increases) — with a reviewer.
2. How Stripe Billing (price migration, "subscription schedules"), Chargebee and Paddle model scheduled price changes and notifications.
3. Decide the model: scheduled change at renewal (today) vs "from date X" with proration rules.

## Decisions needed
Effective-date model, minimum notice per country, grandfathering, whether customers must consent, who approves a mass change (two-person), message wording.

## Work
1. Spec including a state diagram per subscription (notified, scheduled, applied, withdrawn, failed).
2. A first-class `price_change` record (id, plan, from/to, effective rule, notice, created_by, state) with per-subscription items and notification status; list/detail in the console; withdraw all.
3. Retry job for skipped subscriptions; reports of who was notified and when; delivery state from the mail provider (11).
4. Provider-run subscriptions: per-provider implementation through 06–08.
5. Locale-aware amounts (05); preview of the customer's mail in the console.
6. End-to-end test of the full stack path (compose) with Mailpit.

## Done means
A price change on a test stack notifies every customer in their language, shows in the console who was told, applies on the date, and can be withdrawn; legal wording approved.

## Not verifiable here
Legal sufficiency; real delivery.

## Risks
Mass notifications and charges: dry run + approval + rate limiting + a kill switch.
