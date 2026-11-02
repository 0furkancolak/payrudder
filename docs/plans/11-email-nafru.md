# 11 — Nafru Email as the default sender

## The request
List 1 (2): Nafru Email is the **default provider**; a user who wants custom templates adds **Payrudder in Nafru Email's "applications"** and edits them there; **packages include mail** and need no extra step.

## Where we are
**Leftovers built (2026-11-02, branch `feat/product-leftovers-2-p11`; defaults not agreed):**
- **Console views.** The tenant's mail page lists the suppression list (with why, and "Mail again" after asking) and the message log (recent sends with the state the provider reported: delivered, delayed, bounced, bounced for now, complaint); a price-change preview shows the notice in a chosen language (plan 15). The gateway reaches the notifier's signed listener (`PAYRUDDER_NOTIFIER_EVENTS_URL` and `PAYRUDDER_NOTIFIER_MAIL_EVENTS_SECRET`; both or neither), always naming the tenant itself; four routes in the tenant API, in the four SDKs and the OpenAPI file. The notifier keeps what the provider reports of each mail in `sent_notifications.delivery_state` (migration `00005`, with an index for the log). **Wired in compose, Helm and Railway since plan 16/17's leftovers branch `feat/deploy-ops-3`** (see [the runbook](../runbooks/mail-suppression.md)); without both variables the cards say "not connected".
- **Backfill of the `mails` allowance** on plan versions published before the meter existed: at every start of a SaaS plans-svc (`saas/command.BackfillMails`, best effort, idempotent), for Free, Pro and Scale only, the missing entitlement row is added to each version published **before the meter was made** and the entitlements of the subscriptions on it are published again so limits-svc learns the limit (default, not agreed: the seeded numbers above). A version published after the meter is left as its operator made it (leaving mails out is a choice: no limit). Test: `TestPlansSvc/Saas/TheMailsAllowanceIsBackfilledOnVersionsThatPredateTheMeter`.
- **The send rate limit is shared by the replicas** when `PAYRUDDER_REDIS_URL` is set on the notifier (compose, Helm and Railway set it): one bucket per tenant in Redis (the gateway's GCRA script, Redis' clock), and a Redis that cannot be reached falls back to buckets of the replica itself, never to refusing mail (`notifier_mail_rate_limit_fallback_total`). Test against a real Redis: `TestMailRateLimitIsSharedThroughRedis`.

**Status 2026-11-02 (PR "Mail: a `mails` meter, suppression list, send rate limit, Nafru adapter against a fake, template contract"): the platform side is built and tested against a fake; everything that needs the real Nafru is open and unverified.** Added on top of #245 (below):

- **A `mails` meter (ADR 0024).** A fifth resource of the platform tenant (`events`, `mails`, `apps`, `members`, `api_keys`): a SUM meter, an entitlement per plan version, overridable per organisation, shown on the plan page (usage filled by the gateway from limits-svc like the events). The notifier asks the organisation's allowance before a live send and ingests one event (`saas-mails:<dedup key>`) after it; thresholds and alerts are the quota engine's, no new table. Fails open (ADR 0006). **Default allowance (not agreed): Free 100 a month, hard; Pro 5,000, notify; Scale 50,000, notify.** Only mail the platform's own sender sends for a live tenant of an organisation counts: sandbox mail, the platform's own mail and mail through a tenant's own Nafru account do not (a tenant that brings its own provider does not use the allowance). Existing deployments get the meter at the next start, and the plan versions already published get the entitlement then (the backfill, below).
- **Suppression list.** `mail_suppressions` per tenant in `notifier_db` (migration 00003; `sent_notifications` now records the tenant so a bounce, which names only the provider's message id, can be traced to it). A hard bounce or a complaint suppresses the address the notifier recorded for the message; a soft bounce, a delay and a delivery only count. The mail to a suppressed address is dropped as done (a billing command is answered `suppressed`). Fed by `POST /v1/mail-events` on a separate listener (`NOTIFIER_EVENTS_ADDR`, signed with `NOTIFIER_MAIL_EVENTS_SECRET`), with `GET`, `PUT` and `DELETE /v1/mail-suppressions` for the operator. [Runbook](../runbooks/mail-suppression.md). **The event is Payrudder's neutral one, modelled on Resend's public event names; no provider's webhook is accepted as it is** (Resend signs with Svix; Nafru's format and signature are UNVERIFIED), so a translating layer is still to be written.
- **Send rate limit.** A token bucket per tenant on the platform's sender (`NOTIFIER_MAIL_RATE_PER_MINUTE` 600, `NOTIFIER_MAIL_RATE_BURST` 200, per replica; 0 is off). An over-limit mail is retried, not dropped (see Risks for what the retry machinery allows).
- **Nafru adapter.** `pkg/mailx` is split: `Resend` is the Resend-compatible wire shape (`POST /emails`, Bearer, `Idempotency-Key`, `GET /domains`; Resend's public API reference read 2026-11-02), `Nafru` adds what Payrudder assumes about Nafru: the `payrudder_template` tag and the `X-Payrudder-Template` header. This also fixes a bug of #245: the tag value was the template name with its dot (`invoice_paid.v1`), which Resend-style providers refuse in a tag, so the tag was never sent for any real template; the dot is now an underscore in the tag and stays in the header. `pkg/mailx/fakenafru` is a Resend-compatible fake server (records requests, answers a repeated `Idempotency-Key` with the first id, can post bounce events to the notifier) used by the notifier's tests, including an integration test (Postgres): send, bounce, suppression, next mail dropped, another tenant unaffected, operator lifts it.
- **Template contract.** [`../email-template-contract.md`](../email-template-contract.md): the six tenant templates with category, trigger, variables and languages, the message fields (tag, header, dedup key as `Idempotency-Key`), what a rendering provider would have to implement, and plans-svc's eight mails to people. A test fails when the table and the template files drift.

**Before this status:** #245: `NAFRU_BASE_URL` + `NAFRU_API_KEY` (plans-svc and the notifier, both or neither) make Nafru the sender of Payrudder's own mail and of every tenant's mail that has no provider of its own; otherwise SMTP (a dev mail catcher). A tenant that connects its own Nafru account sends through it. Each message carries `payrudder_template` as a Resend tag and `X-Payrudder-Template`. Console copy says mail is included and where templates are edited.

## Gaps
Closed by the status above: the allowance and metering ("packages include mail" now has a number and a meter), bounce and complaint handling as a suppression list (fed from fixtures, not from Nafru), an abuse limit per tenant, the template contract document. Still open, in the order of the original gaps:
- **The Nafru contract was assumed.** Nafru Email is described as Resend-compatible; whether its "applications" match on a tag or header, whether it has template ids and variables (so Payrudder would send data and not HTML), what its sending limits, domain verification and webhooks (delivery, bounce, complaint) are — none was read from Nafru's documentation or tried.
- "Packages include mail": closed (the `mails` meter) for the platform's own sender. Not covered: **a tenant's customer-facing allowance on a plan the tenant sells** (this meter is Payrudder's plans for organisations, not the packages a seller sells), a console view of the allowance on the mail page (the plan page shows it). (The message log per tenant is built: see above.)
- Deliverability: SPF/DKIM/DMARC for the sending domain (nothing checks them), the translating layer from a provider's webhooks to the suppression endpoint, unsubscribe for non-transactional mail (Payrudder sends only transactional mail), per-tenant From (a customer's mail should come from the seller's name/domain where configured).
- Templates: Payrudder renders its own six-language HTML; the "edit in Nafru" path (send variables, let Nafru render) is not implemented, only labelled.
- Not tested against a real Nafru.

## Research first
1. Read Nafru Email's API docs (and ask the owner, who owns it): send endpoint shape, templates/applications model, tags/headers/metadata, webhooks, limits, idempotency, domain setup.
2. Decide the send mode: **Payrudder renders** (today) vs **Nafru renders from a template + data** (needed for "edit in Nafru"); likely both, selectable per tenant.
3. Inventory the mails Payrudder sends (invoice, receipt, payment failed, limit alert, trial ending, price change, support, auth) with their variables → this becomes the "template contract" a Nafru application would implement.

## Decisions needed
Nafru's URL, an API key for the platform, the contract above, and the **package mail allowance** (per plan monthly count, hard stop or soft).

**Defaults chosen (not agreed), 2026-11-02:**

| Question | Default | Why |
|---|---|---|
| Allowance | Free 100 a month hard, Pro 5,000 notify, Scale 50,000 notify | Free is where abuse starts, so it stops; a paying organisation's customers are never left without a receipt (it is alerted, not cut off). Edit the numbers as the platform tenant's packages |
| What counts | mail sent by the platform's sender for a live tenant of an organisation | a tenant's own provider uses the tenant's own sending reputation and quota; sandbox mail goes to a sink |
| A hard limit reached | the mail is dropped as done (counted `notifier_mail_dropped_total{category="quota"}`), the billing saga goes on | a retry cannot change a monthly limit; the organisation sees the usage on its plan page and gets the quota engine's threshold alerts first |
| Suppression scope | per tenant, not platform-wide | a complaint is about one seller's mail; the bounce of an address that does not exist is learned again per tenant at the cost of one bounce each |
| Suppression feed | Payrudder's neutral event, signed with a shared secret, on its own listener (off by default) | no provider contract is assumed; a translating layer adapts the real one |
| Rate limit | 600 a minute per tenant per replica, burst 200 | catches a loop or abuse without touching a month close of a normal tenant; raise it per deployment |
| Soft bounces | counted, never suppress | an unproven address is never put on the list for a mailbox that was full once |
| Render mode | Payrudder renders (unchanged); "Nafru renders" is not built | needs Nafru's template contract |

## Work
1. ~~Write the template contract doc (names, variables, languages).~~ Done: [`email-template-contract.md`](../email-template-contract.md), drift-tested.
2. Implement "send by template id with data" behind a per-tenant setting; keep render-in-Payrudder as default until Nafru's side is confirmed. **Open: needs Nafru's contract.**
3. ~~Mail metering and plan allowance (SaaS plans) with alerts at thresholds; limits to stop abuse.~~ Done: the `mails` meter and the rate limit (above).
4. Bounce/complaint webhooks → suppression list: the list and the neutral endpoint are done; **open:** the translating layer for Nafru's real webhooks (the console view of the list and the message log are built).
5. Domain verification flow for a tenant's own sending domain. **Open, not started.**
6. Tests with a fake Nafru derived from the real docs; a real run when the key exists. The fake (`pkg/mailx/fakenafru`) is derived from **Resend's** public docs, not Nafru's; the real run is open.

## Done means
A real mail through Nafru for each template, in six languages, with the application path demonstrated by the owner; allowance enforced and visible.

## Not verifiable here
Everything against Nafru and real inboxes: that Nafru accepts the Resend shape for every field Payrudder sends, whether its applications match on the tag or header, its webhook payload and signature, its sending limits and idempotency window; SPF, DKIM and DMARC of the sending domain; that a bounce reaches the endpoint through a real layer. Also not run: the `mails` meter end to end across notifier, ingest-svc and limits-svc (the pieces are tested against fakes of each other; the compose stack was not started), and the helm/kind deployment of the new addresses (the chart renders and its render tests pass).

## Risks
Sending as many tenants' mail from one platform account puts the domain's reputation at risk: per-tenant limits and suppression are not optional.

- **The retry window of a rate-limited command is short.** A `SendNotification` command is retried three times (5 s, 30 s, 2 min) and then dead-lettered; a tenant that sends more than the burst plus ~2.5 minutes of the sustained rate at once (a very large month close) loses the rest to the dead-letter queue, from where they can be replayed. The default (600 a minute) is meant to be above any normal close; raise `NOTIFIER_MAIL_RATE_PER_MINUTE` for a big tenant. The Kafka consumers (limit alerts, trial and price-change notices) do not lose anything: they hold the partition and retry the batch, which also delays mail of other tenants behind a flooding one on that partition.
- **The limit is per replica unless `PAYRUDDER_REDIS_URL` is set** (then it is shared; see above); the local buckets are in memory (a restart refills them).
- **A lost count is a free mail.** Metering fails open and one batch can overshoot, as ADR 0006 says of events. The `mails` meter is an allowance, not an invoice line.
- **Plan versions published before this meter** get the seeded `mails` limit at the next start of plans-svc (the backfill above); one an operator published after the meter without it stays unlimited.
- **A suppression is permanent** until an operator lifts it: no expiry, no automatic lift when the customer changes their address (a new address is a new row).

## Review addendum (2026-11-01)
- No bounce, complaint or suppression code exists. Tenants already switch mail off by category (`pkg/mailcategory`); the six templates (a seventh, `contract_accepted.v1`, arrived with plan 21) are the template-contract inventory.
- Replace Work 3 (allowance) with: a **`mails` meter per ADR 0024** (the existing `events`, `apps`, `members`, `api_keys` meters give quota, thresholds and alerts without a new table).
- Default (not agreed): build against a fake with a Resend-compatible shape; the real Nafru tag and application contract stay UNVERIFIED. Add a suppression list fed by a bounce endpoint from fixtures and a per-tenant send rate limit.
