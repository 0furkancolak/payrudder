# 11 — Nafru Email as the default sender

## The request
List 1 (2): Nafru Email is the **default provider**; a user who wants custom templates adds **Payrudder in Nafru Email's "applications"** and edits them there; **packages include mail** and need no extra step.

## Where we are
#245: `NAFRU_BASE_URL` + `NAFRU_API_KEY` (plans-svc and the notifier, both or neither) make Nafru the sender of Payrudder's own mail and of every tenant's mail that has no provider of its own; otherwise SMTP (a dev mail catcher). A tenant that connects its own Nafru account sends through it. Each message carries `payrudder_template` as a Resend tag and `X-Payrudder-Template`. Console copy says mail is included and where templates are edited.

## Gaps
- **The Nafru contract was assumed.** Nafru Email is described as Resend-compatible; whether its "applications" match on a tag or header, whether it has template ids and variables (so Payrudder would send data and not HTML), what its sending limits, domain verification and webhooks (delivery, bounce, complaint) are — none was read from Nafru's documentation or tried.
- "Packages include mail": **no quota or plan field exists** (SaaS plans have no mail allowance); no metering of mail sent per tenant; no abuse limit.
- Deliverability: SPF/DKIM/DMARC for the sending domain, bounce and complaint handling (suppression list), unsubscribe for non-transactional mail, per-tenant From (a customer's mail should come from the seller's name/domain where configured).
- Templates: Payrudder renders its own six-language HTML; the "edit in Nafru" path (send variables, let Nafru render) is not implemented, only labelled.
- Not tested against a real Nafru.

## Research first
1. Read Nafru Email's API docs (and ask the owner, who owns it): send endpoint shape, templates/applications model, tags/headers/metadata, webhooks, limits, idempotency, domain setup.
2. Decide the send mode: **Payrudder renders** (today) vs **Nafru renders from a template + data** (needed for "edit in Nafru"); likely both, selectable per tenant.
3. Inventory the mails Payrudder sends (invoice, receipt, payment failed, limit alert, trial ending, price change, support, auth) with their variables → this becomes the "template contract" a Nafru application would implement.

## Decisions needed
Nafru's URL, an API key for the platform, the contract above, and the **package mail allowance** (per plan monthly count, hard stop or soft).

## Work
1. Write the template contract doc (names, variables, languages) — the interface between Payrudder and Nafru.
2. Implement "send by template id with data" behind a per-tenant setting; keep render-in-Payrudder as default until Nafru's side is confirmed.
3. Mail metering and plan allowance (SaaS plans) with alerts at thresholds; limits to stop abuse.
4. Bounce/complaint webhooks → suppression list; surface delivery state in the console (a message log per tenant).
5. Domain verification flow for a tenant's own sending domain.
6. Tests with a fake Nafru derived from the real docs; a real run when the key exists.

## Done means
A real mail through Nafru for each template, in six languages, with the application path demonstrated by the owner; allowance enforced and visible.

## Not verifiable here
Everything against Nafru and real inboxes.

## Risks
Sending as many tenants' mail from one platform account puts the domain's reputation at risk: per-tenant limits and suppression are not optional.
