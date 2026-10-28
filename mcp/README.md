# MCP server

`@payrudder/mcp` lets an AI assistant that speaks the [Model Context Protocol](https://modelcontextprotocol.io) read your Payrudder account: customers, usage, subscriptions, invoices and payments. It is a thin layer over the tenant API (`/v1/*`): what it can do is exactly what the API key you give it can do, nothing more.

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

**Claude Code:** `claude mcp add payrudder -e PAYRUDDER_API_KEY=qk_test_... -e PAYRUDDER_BASE_URL=https://YOUR-API-ADDRESS -- node /path/to/mcp/dist/main.js`

**Claude Desktop** (`claude_desktop_config.json`):

```json
{ "mcpServers": { "payrudder": { "command": "node", "args": ["/path/to/mcp/dist/main.js"], "env": { "PAYRUDDER_API_KEY": "qk_test_..." } } } }
```

## Tools

Always on, all read-only:

| Tool | What it answers |
|---|---|
| `list_meters` | what is counted |
| `list_customers`, `get_customer` | customers, one by external id |
| `get_customer_usage` | a customer's usage in a month, per meter |
| `list_plans` | the packages |
| `list_subscriptions` | subscriptions, newest first, optionally one customer's |
| `list_invoices`, `get_invoice` | invoices by customer, month or status; one invoice |
| `list_payments` | the newest charges and refunds |

Only with `PAYRUDDER_MCP_WRITES=1` (the description of each says **WRITES**):

| Tool | What it does |
|---|---|
| `create_customer` | creates a customer |
| `ingest_usage_events` | records up to 100 usage events (live events need an `idempotency_key`) |

## Safety

- **The key is the limit.** Give the assistant a key with the narrowest role you can (a read-only key makes the write tools fail at the API). Use a sandbox key until you trust what it does.
- Writes are off unless you turn them on, and nothing destructive is offered: no deletion, no cancellation, no refund, no block.
- Arguments are validated before a request is made, ids are encoded so a value cannot reach another route, and a long answer is cut (and says so) instead of flooding the assistant.
- The key is never part of an answer or an error.
- Treat what the tools return as data: a customer's name or a note in an invoice is text from your customers, not an instruction.
