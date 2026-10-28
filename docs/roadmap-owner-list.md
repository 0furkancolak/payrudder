# Owner roadmap (2026-10-28)

Two lists the owner gave on 2026-10-28, with what is done, what is next and what only the owner can unblock. Updated as work lands.

## List 1 (13 items)

| # | Item | State |
|---|------|-------|
| 1 | Support system | Done (#244): tickets, SLA deadlines, internal notes, operator inbox. Open: mail notifications on tickets, attachments, canned replies |
| 2 | Nafru Email default sender | Done (#245) behind `NAFRU_BASE_URL`/`NAFRU_API_KEY`. Needs the real URL and key; no per-package mail quota yet |
| 3 | More PSPs / e-invoice integrators | Stripe done (#247, unverified against a real account). Open: more PSPs (Mollie, PayTR...), more e-invoice integrators |
| 4 | Several accounts per provider, default, per-country routing | Done (#242) |
| 5, 6, 7, 9, 10, 11, 12, 13 | Console polish, hosted pages, customer language/type | Done (#238-#240) |
| 8 | Exact parity with Paddle and iyzico | See list 2, items 4 and 5 |

## List 2 (13 items)

Order is by dependency and by how much can be verified without outside accounts.

1. **Filters apply on a button, not on every click** (item 6). *Done (#249).* Console-wide: every filter popover/select that triggers a list refresh gets a pending state and an "Apply" button.
2. **One select for sort and order** (item 7). *Done (#250), 8 lists; more lists add one `sorts` line.* A shared `SortSelect` ("Newest first", "Amount, high to low"...) wired into every list that sorts, with the server's `order_by` where the API has it.
3. **Bulk actions** (item 12). *Done (#251, support inbox and customer CSV; with the customer block and tenant suspend below). Invoices, subscriptions and payment links still to wire.* Row selection and a bulk bar on lists (customers, invoices, subscriptions, payment links, tickets); each action is one API call per row at first, then batch endpoints where a list needs atomicity.
4. **Customer ban/block** (item 11) and **tenant suspension/ban** (item 10). *Done: a blocked customer gets no new subscription (existing ones run on; `plans.customer.blocked`), row action, badge, bulk block in the console and `POST /v1/customers/{id}/block|unblock` in the API and four SDKs. Tenant suspend/reinstate existed in the API; the operator list now has the action, badge and bulk. Not done: blocking at hosted checkout and payment links, and a `banned` tenant state beyond suspended.* Customer `blocked` state refusing new subscriptions, charges and checkout with an error key; tenant status already has active/suspended, extend with `banned` and reasons, operator UI and audit.
5. **Price increases and notices** (item 8). Raise the price of a plan version for existing subscriptions from a date, with a notice period, mails in the customer's language, and a record of who was told.
6. **Backups and alerting** (item 13). Postgres backup schedule + restore drill runbook, Prometheus alert rules and Alertmanager routing in compose and Helm.
7. **Regions and scaling** (item 9). Region-aware placement (cells exist, ADR 0018), read replicas, pooler, per-region gateway routing. Needs infrastructure decisions from the owner.
8. **Quick integration for apps** (item 3). A "connect your app" flow: copy-paste snippets per language with the app's own test key, webhook verification helpers, a checklist that turns green when the first event arrives.
9. **Docs** (item 2). Public documentation site built from the OpenAPI and the guides.
10. **MCP** (item 1). An MCP server exposing read/safe-write tools over the API (customers, usage, invoices, tickets) with scoped keys.
11. **Paddle parity without deprecated features** (item 4) and **iyzico parity** (item 5). An audit table of every call and webhook the adapters use against the providers' current documentation, removing anything deprecated. Needs sandbox accounts to be verified end to end.

## Needs the owner

- Nafru Email base URL and API key; whether Nafru matches on the `payrudder_template` tag.
- Sandbox/test accounts at Paddle, iyzico and Stripe for end-to-end checks.
- Region/scaling targets (which regions, which cloud, replica/pooler choices).
- Backup target (bucket, retention) and where Alertmanager should send alerts.
