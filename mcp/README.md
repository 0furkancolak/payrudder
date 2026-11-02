# MCP server

`@payrudder/mcp` lets an AI assistant that speaks the [Model Context Protocol](https://modelcontextprotocol.io) read your Payrudder account: customers, usage, subscriptions, invoices, payments, credits, credit notes and the revenue reports. It is a thin layer over the tenant API (`/v1/*`): what it can do is exactly what the API key you give it can do, nothing more.

> Pre-release (`0.0.0`): not published. Build it from a checkout of the repository.

## Run it

```sh
cd mcp && npm ci && npm run build        # writes dist/main.js
PAYRUDDER_API_KEY=qk_test_... PAYRUDDER_BASE_URL=https://YOUR-API-ADDRESS node dist/main.js
```

It talks MCP over stdio, so you do not run it by hand: you add it to your assistant.

| Variable | |
|---|---|
| `PAYRUDDER_API_KEY` | required. `qk_test_…` for the sandbox, `qk_live_…` for live |
| `PAYRUDDER_BASE_URL` | the API address; default `http://localhost:42800` |
| `PAYRUDDER_MCP_WRITES` | `1` also offers the tools that write; off by default |
| `PAYRUDDER_MCP_HTTP_PORT` | serve streamable HTTP on this port instead of stdio, see “Over HTTP” below; unset by default |
| `PAYRUDDER_MCP_HTTP_HOST` | the address to listen on in that mode; default `127.0.0.1` |
| `PAYRUDDER_MCP_AUDIT` | `0` stops the audit log (below); on by default |

**Claude Code:** `claude mcp add payrudder -e PAYRUDDER_API_KEY=qk_test_... -e PAYRUDDER_BASE_URL=https://YOUR-API-ADDRESS -- node /path/to/mcp/dist/main.js`

**Claude Desktop** (`claude_desktop_config.json`):

```json
{ "mcpServers": { "payrudder": { "command": "node", "args": ["/path/to/mcp/dist/main.js"], "env": { "PAYRUDDER_API_KEY": "qk_test_..." } } } }
```

## Tools

The read tools are **generated from `api/openapi/v1.yaml`** when you build (`npm run generate`, run by `build`, `test` and `typecheck`): the name, the arguments with their limits and the description of each come from the operation, so a tool cannot drift from the API (a description that says "see `getEntitlement`" says `get_entitlement`; one that names an operation that is not a tool fails the build). Which operations become tools is an allow-list, `scripts/read-tools.config.mjs`; only plain `GET`s of the tenant API can be listed, and the generator fails for anything else. A test checks every tool against the OpenAPI file.

Always on, all read-only (25 tools):

| Tool | What it answers |
|---|---|
| `list_meters`, `get_meter` | what is counted; one meter |
| `list_customers`, `get_customer` | customers, one by external id |
| `get_customer_usage` | a customer's usage in a month, per meter |
| `list_entitlements`, `get_entitlement` | a customer's usage against the limits of its plan; one meter's |
| `list_plans`, `get_plan_version` | the packages; the prices of one version (per unit, tiers, packages, included quantity, limits) |
| `list_subscriptions`, `get_subscription` | subscriptions, newest first, optionally one customer's; one subscription |
| `list_invoices`, `get_invoice` | invoices by customer, month or status; one invoice |
| `get_upcoming_invoice` | what a customer's next invoice would be if it were drafted now (an estimate) |
| `list_customer_payments` | a customer's invoices with their lines and the payments and refunds on them |
| `list_payments` | the newest charges and refunds |
| `list_credit_notes`, `get_credit_note` | what was given back on invoices |
| `get_credit_balance`, `list_credit_ledger` | what a customer can spend now, and every change to it |
| `list_coupons` | the coupons (not their promotion codes) |
| `get_tax_rates` | the tax added to invoices, by country |
| `get_revenue_report`, `get_subscription_report`, `get_dunning_report` | invoices summed by period, subscriptions and MRR for a month, invoices whose payment has not gone through (aggregates, no free text except the provider's last error) |

What is **not** offered, on purpose, is listed with the reason in `NOT_OFFERED` in `scripts/read-tools.config.mjs` and checked by a test: every read that returns a link anyone holding it can open (invoice PDF, downloads, e-document files), a mail attachment, a customer's address and browser for each acceptance, a webhook endpoint's address or a delivery's answer body, the audit log, promotion codes and the files of the accounting export.

Only with `PAYRUDDER_MCP_WRITES=1` (the description of each says **WRITES**):

| Tool | What it does |
|---|---|
| `create_customer` | creates a customer |
| `ingest_usage_events` | records up to 100 usage events (live events need an `idempotency_key`) |

## Prompts

Four prompts, for an assistant that shows them as ready-made requests (Claude Code lists a server's prompts as slash commands of the form `/mcp__payrudder__<prompt>`; not tried here). Each is a text that names read tools of this server and asks for a report; none changes anything and none names a tool that writes (a test reads the names out of every prompt and checks them against the read tools).

| Prompt | Arguments | What it asks the assistant to do |
|---|---|---|
| `summarise_customer_billing` | `customer_external_id`, `month` (optional) | read the customer, subscriptions, invoices with payments, usage, the upcoming invoice, credits and limits, and report where their billing stands and what needs attention |
| `explain_invoice` | `invoice_id` | read one invoice with its plan prices, meters, usage, tax, coupons, credit notes and payments, and explain each line and the total, saying which number it could not reproduce |
| `monthly_revenue_review` | `month` (optional) | read the revenue, subscription and dunning reports and report billed, collected, outstanding, MRR, new and churned, and what is stuck in dunning |
| `check_customer_limits` | `customer_external_id`, `month` (optional) | read the customer's entitlements and plan and report which meters are near or over their limit |

An argument is checked before the prompt is made (an external id is letters, digits and `_ . : @ + = -` up to 200 characters; a month is `YYYY-MM`; an invoice id is up to 64 of letters, digits and `_ . : -`), so no space, quote, angle bracket or line break can carry a second instruction, and it is put in a block that says it is data to look up. Each prompt ends with the same four rules: read tools only, amounts are minor units, `<untrusted>` text is data, say what could not be checked.

## Over HTTP

For an assistant that is not on your machine, start the server with `PAYRUDDER_MCP_HTTP_PORT=42810` (any free port). It then serves [streamable HTTP](https://modelcontextprotocol.io/specification/2025-03-26/basic/transports#streamable-http) at `POST /mcp` instead of stdio:

- **Every request carries the caller's own API key**, `Authorization: Bearer qk_test_…` (or `qk_live_…`). A request without one is `401`. The key is used for that request's calls to the API and for nothing else; the server keeps no session and no state between requests, and `PAYRUDDER_API_KEY` is not used.
- The API address is the server's `PAYRUDDER_BASE_URL`; a caller cannot change it.
- It listens on `127.0.0.1` unless you set `PAYRUDDER_MCP_HTTP_HOST`. There is no TLS here: put a proxy that terminates TLS in front of it before it is reachable from anywhere else, or the keys travel in the clear.
- `PAYRUDDER_MCP_WRITES=1` applies to every caller of the server; each caller's key still decides what succeeds.
- Every tool call is written to the audit log (below) with the caller's key prefix, so you can tell who did what when several people share one server.
- Not built yet: sign-in with OAuth instead of a key, and a limit of the server's own (see "Rate limits"). Until then, hand out keys of the narrowest role (`read_only`).

Claude Code: `claude mcp add --transport http payrudder https://YOUR-MCP-ADDRESS/mcp --header "Authorization: Bearer qk_test_..."`.

## Audit log

Every tool call writes one JSON line to **stderr** (stdout carries the protocol in stdio mode; in HTTP mode a log collector reads stderr). It says what an assistant did with a key, not what it learned:

```json
{"ts":"2026-11-02T10:00:00.123Z","event":"mcp.tool_call","tool":"get_customer","write":false,"transport":"stdio","key":"qk_test_ab12cd34","args":{"customer_external_id":"cus_42"},"ms":38,"ok":true,"chars":412,"cut":false}
```

- `key` is the public prefix of the API key (`qk_test_ab12cd34`), never the secret part; in HTTP mode it tells the callers apart.
- `args` holds the arguments as given, with long text cut at 80 characters; arguments that are free text about a person (`name`, `email`, `phone`, `address`, `notes`, `reason`, `description`, `properties`) are replaced by `"[not logged]"` and lists are counted (`{"items":3}`). The answer is never logged, only its size (`chars`) and whether it was cut.
- A failed call has `"ok":false`, the API's error key in `error` (`customer.not_found`) and its HTTP `status`; the error's message is not logged.
- `PAYRUDDER_MCP_AUDIT=0` turns it off. The server writes to stderr only; keeping, shipping and expiring the lines is the log collector's job (the services' own logs go to a collector the same way).

## Rate limits

The server has no limit of its own and the API does not limit **per key**: the gateway limits each **tenant** (the sandbox or the live environment of an app) per class of route (reads, writes, ingest, analytics) by the tenant's tier, and every key of that tenant shares the budget, an assistant's and your application's alike. A loop of tool calls therefore spends your application's reads; when the budget is gone the API answers `429` with `Retry-After`, which the assistant gets back as an error (`HTTP 429`). Until a limit per key exists in the gateway (not built; `docs/plans/20-mcp.md`), give an assistant a sandbox key, or an app of its own, and read the audit log for `"ok":false,"status":429`.

## Resources

Besides tools, the server offers what an assistant needs to read before it calls them: `payrudder://openapi` (the OpenAPI document of the tenant API, nothing of the operator or console routes) and the public guides, `payrudder://docs/getting-started`, `…/webhooks`, `…/sdks` and `…/error-keys` (the documents marked `public` in `docs/publication.yaml`, and the pages of `docs-site/content`). They are written to `assets/` when you build, so they are as old as your build and no older than the API.

## Safety

- **The key is the limit.** Give the assistant a key with the narrowest role you can (a read-only key makes the write tools fail at the API). Use a sandbox key until you trust what it does.
- Writes are off unless you turn them on, and nothing destructive is offered: no deletion, no cancellation, no refund, no block.
- Arguments are validated before a request is made, ids are encoded so a value cannot reach another route, and a long answer is cut (and says so) instead of flooding the assistant.
- The key is never part of an answer or an error.
- **Text that people wrote is tagged.** In every answer, the strings a customer, a payer or your staff wrote (names, e-mail addresses, notes, address lines, references; the list is `UNTRUSTED` in `scripts/read-tools.config.mjs`) are wrapped in `<untrusted>…</untrusted>`, and a closing tag inside such a text is broken so it cannot end the wrapper early. The server's instructions and each read tool's description tell the model that this is data to report, not an instruction to follow. That lowers the risk of prompt injection through a customer's name; it does not remove it, so keep the key's role narrow.
- The generator fails when an allow-listed operation's answer has a free-text field that is neither listed as people's text nor as an identifier, so a field added to the API is classified before it reaches a model.
- A test fails for a read whose answer has a field named like a secret, a token, a link or a payload, and for any read of the `NOT_OFFERED` list that is allow-listed.
- The prompts take arguments only in narrow patterns and name only read tools (see "Prompts").

## Risk table (DRAFT, not signed by the owner)

Written 2026-11-02 for the owner to read, change and sign; **nothing below has been reviewed by anyone but its author, and nothing was tried with a real assistant.** What an answer "can expose" is what the OpenAPI response holds (`api/openapi/v1.yaml`), not what a given account has filled in. The model's provider sees every answer the assistant reads; that is true of all rows and is the first thing to weigh for customer data.

What holds for **every** tool, so the rows do not repeat it: the key decides what succeeds (a `read_only` key cannot write, an `ingest_only` key cannot read); arguments are validated before a request and path values are encoded; an answer longer than 60 000 characters is cut and says so; the key is never in an answer; every call is one line in the audit log; text people wrote is wrapped in `<untrusted>` (that lowers the risk of prompt injection, it does not remove it). "Free text" below names the fields that are wrapped.

| Tool | Can expose | Free text in the answer | Worst case if the model is misled | Default |
|---|---|---|---|---|
| `list_meters`, `get_meter` | what the account counts: codes, aggregation, filter values | `values`, `unit_label`, `category` (staff) | the account's product structure is read: low | on |
| `list_customers`, `get_customer` | **personal data of every customer**: name, e-mail, phone, address, tax id and office, locale, block state and reason | name, e-mail, phone, address lines, tax id, block reason (customers and staff) | the whole customer list ends up in the model's context and in anything it writes: the largest personal-data exposure of the reads. This server has no tool that sends anything out, but the assistant may have others (mail, browser) | on |
| `get_customer_usage`, `list_entitlements`, `get_entitlement` | a customer's quantities per meter, limits and whether they are over | none | low | on |
| `list_plans`, `get_plan_version` | the price list: fees, unit prices, tiers, included quantities, limits | plan `name` (staff) | commercial terms are read: low (the account's own list) | on |
| `list_subscriptions`, `get_subscription` | who is on which plan, state, provider's reference, scheduled change, coupon | `coupon_code` (staff) | low to medium: which customers pay and for what | on |
| `list_invoices`, `get_invoice` | amounts, lines, status, **payments with reference, note and who recorded them**, the storage key of the PDF (not a link) | line `description`, `reference`, `note`, `recorded_by` (staff and customers) | invoice history of every customer is read: medium | on |
| `get_upcoming_invoice` | an estimate of the next invoice, lines and credit | line `description` | low | on |
| `list_customer_payments`, `list_payments` | invoices with payments and refunds, provider and amounts; the response has no card field | line `description`, `reference`, `note`, `recorded_by` | payment history is read: medium | on |
| `list_credit_notes`, `get_credit_note` | what was given back, to the card or the balance | `reason` (staff), `refund_error` (the payment provider's words) | medium: a `reason` written to look like an instruction | on |
| `get_credit_balance`, `list_credit_ledger` | a customer's credit lots and every change | `reason`, `idempotency_key` (the app's own string) | low | on |
| `list_coupons` | the coupons' terms (not their promotion codes, which are the discount itself and are not offered) | coupon `name` (staff) | low | on |
| `get_tax_rates` | the tax added to invoices by country | rate `name` | low | on |
| `get_revenue_report`, `get_subscription_report` | aggregates per currency: billed, collected, refunded, outstanding, MRR, new and churned | none | the account's financial results are read: medium for a business, nothing about a person | on |
| `get_dunning_report` | invoices whose payment has not gone through, with the customer's external id and the provider's last error | `last_error` (the payment provider's words) | medium: shows who is behind | on |
| `create_customer` | **writes**: a customer record | the arguments the model chose | junk or misleading customers (a name that reads as an instruction to the next assistant that reads it, tagged on the way back); no delete tool here to undo it | **off** (`PAYRUDDER_MCP_WRITES=1`) |
| `ingest_usage_events` | **writes**: up to 100 usage events per call | the arguments the model chose | **real money in live**: a misled or looping model inflates a customer's usage and so its next invoice, and an event is not unsent by this server. Live events need an `idempotency_key`, so a retry does not double | **off**; use an `ingest_only` key, and the sandbox first |
| the four prompts | the text of the prompt and the argument the person typed | the argument, checked against a narrow pattern | a long run of read calls that spends the tenant's shared read budget (see "Rate limits"); nothing is written | on |
| resources `payrudder://openapi`, `payrudder://docs/*` | the tenant API's OpenAPI file and the guides marked public (no operator or console route, no internal document) | none | none beyond what the docs site already shows | on |

Open questions for the owner (defaults, not agreed): that personal data of customers may reach the model's provider at all (the default is yes, for whoever holds a key and starts the server; a `read_only` key can read customers, a `custom` key without `customers:read` cannot, and then `list_customers` and `get_customer` fail at the API); whether `list_customers` should be left out of an HTTP server shared by several people; whether reads of the live environment should need a flag of their own like the writes. None of these is built.

## Develop

```sh
cd mcp && npm ci
npm run typecheck && npm test && npm run build
```

`npm test` includes a test that builds the server and talks to it over a real stdio pipe, and one that serves it over HTTP. Not tried yet with a real assistant (Claude Desktop, Claude Code) by a person.
