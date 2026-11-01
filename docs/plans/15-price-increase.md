# 15 — Price increases and notices

## The request
List 2 (8): the app must support **making a price increase** and **notifying about it**.

## Where we are
#253: `POST /v1/plans/{code}/price-changes` moves every Payrudder-run subscription on one plan version to another **at its next renewal** if that renewal is at least `notice_days` away (default 30, min 14), reusing the existing scheduled plan change; mails each customer (`price_change.v1`, six languages) through a Kafka fact `subscription.price-change.v1` and a notifier consumer; names the skipped ones (`renews_soon`, `has_pending_change`, `run_by_provider`, `not_changeable`); a dry run; a package row action with check/confirm in the console.

**Update 2026-11-02 (this PR):** the run is now a first-class record with per-subscription items (`price_changes`, `price_change_items`; `GET /v1/price-changes`, `GET /v1/price-changes/{id}`), a rerun continues the open one instead of reporting its own schedules as `has_pending_change` (they are `already_scheduled`), skipped subscriptions are retried (`POST /v1/price-changes/{id}/retry`), a whole price change is withdrawn in one call (`POST /v1/price-changes/{id}/withdraw`), the tenant's webhooks get `price_change.scheduled`, the console lists the price changes of a package with who/when/what became of each subscription and the retry and withdraw actions, and the notifier consumer has an integration test (Kafka + Mailpit). **That test found that no price-change mail was ever sent:** the notifier's `sent_notifications.kind` check allowed `command`, `limit` and `trial` only, so the consumer's first insert failed and it retried for ever; migration `00002_price_change_kind.sql` of the notifier fixes it. Design in [plans-svc](../03-services/plans-svc.md#as-built-price-changes-plan-15).

## Gaps
- *(Still open.)* The date cannot be chosen: the change happens at the renewal, so a customer renewing in 20 days with a 30-day notice is skipped, and the seller must rerun. For **yearly** plans the next renewal may be 11 months away, so an increase takes a year; no "effective date" model.
- *(Partly closed.)* **The mail path was never run end to end** (outbox → relay → Kafka → consumer → Nafru/SMTP). Now run from the Kafka record to Mailpit by the notifier's integration test (`TestNotifier/PriceChangeDeliveredTwice…`: one mail in the customer's language for a fact delivered twice, an undecodable record goes to the DLQ). **Still not run:** plans-svc's outbox relay publishing to a real broker for this topic, and delivery through Nafru or a seller's own provider (needs the whole compose stack, plan 11).
- *(Closed, except the schedule.)* Skipped subscriptions have no follow-up: the retry is a button and an endpoint, there is no timer that retries by itself. The list of "customers not yet notified" is the price change's skipped items.
- *(Closed.)* No record of **who was notified when** visible to the seller, no webhook specific to the price change, no way to withdraw a whole price change. Left: the mail's **delivery state** from the provider (plan 11), and telling customers that a price change was withdrawn (a mail that was sent is not taken back).
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

## Review addendum (2026-11-01)
- The notifier consumer `services/notifier/internal/pricechange` has no test; copy the trial-will-end integration test shape. Re-running a price change reports its own earlier schedules as `has_pending_change`. There is no webhook type and no per-country notice table.
- Default (not agreed): notice period 30 days (14 minimum, as coded), configurable per country later; a first-class `price_changes` record with `effective_at`, retry of skipped subscriptions and a withdraw-all action.

## Defaults chosen (2026-11-02, not agreed)
- **Date model:** unchanged, the new price starts at each subscription's **next renewal** that is at least the notice away. `effective_at` of a price change is the first such renewal (until one is scheduled, now plus the notice). A "from date X" model with proration stays a decision for the owner.
- **Notice:** default 30 days, minimum 14 (as coded), maximum 365, **one floor for every country**. A per-country table is not built: the legal minimums (EU consumer rules, Turkey, UK, US state auto-renewal laws) need a reviewer, and a table nobody has checked would look like advice. The floor is a guess, as before.
- **A rerun continues the open price change** of the same plan and pair of versions (the notice of the latest run is kept); there is at most one open at a time. A withdrawn price change is closed: a new run makes a new one. A subscription whose change was withdrawn (by "withdraw all", by hand, or because the customer or the seller changed the subscription) is **not scheduled again** by a retry.
- **Retry** needs no confirmation (it only does what the seller already decided, to the subscriptions that were left out); **withdraw all** asks first. Withdrawing does not mail the customers who were told.
- **Webhook:** `price_change.scheduled`, once per run that scheduled at least one subscription (not for a dry run), carrying the price change with its counts.
- **Permissions:** the new routes need the same API-key permission as the plans (`catalog:read` / `catalog:write`), like `POST /v1/plans/{code}/price-changes`.
- **Items are capped** at 500 per call (no paging yet) and a list at the latest 100 price changes.

## Verified in this PR
Run: unit tests (notifier, gateway incl. the OpenAPI route check, plans-svc, the four SDKs), `TestPlansSvc/PriceChange` (record, dry run, rerun, retry, withdraw with an applied change, the seller's own withdrawal stays, the webhook delivery), `TestPlansSvc/RLSIsolationEveryTable`, `TestTenantSchemaMatchesPublic`, `TestPlansSvc/SandboxReset`, both baseline migration cycles, `TestNotifier/PriceChange…` (Kafka + Mailpit), console lint, `vue-tsc`, vitest and the mock e2e of the packages page. **Not verified:** the whole path through a running compose stack (relay to the real broker, Nafru, a seller's provider), the console in a real browser against the real API, the Python SDK's ruff and mypy (not installed here), the legal sufficiency of the notice and of the mail's wording, a price change of thousands of subscriptions (each is its own transaction; the run has no kill switch besides withdrawing).
