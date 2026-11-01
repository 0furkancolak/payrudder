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
| `PAYRUDDER_MCP_HTTP_PORT` | serve streamable HTTP on this port instead of stdio, see “Over HTTP” below; unset by default |
| `PAYRUDDER_MCP_HTTP_HOST` | the address to listen on in that mode; default `127.0.0.1` |

**Claude Code:** `claude mcp add payrudder -e PAYRUDDER_API_KEY=qk_test_... -e PAYRUDDER_BASE_URL=https://YOUR-API-ADDRESS -- node /path/to/mcp/dist/main.js`

**Claude Desktop** (`claude_desktop_config.json`):

```json
{ "mcpServers": { "payrudder": { "command": "node", "args": ["/path/to/mcp/dist/main.js"], "env": { "PAYRUDDER_API_KEY": "qk_test_..." } } } }
```

## Tools

The read tools are **generated from `api/openapi/v1.yaml`** when you build (`npm run generate`, run by `build`, `test` and `typecheck`): the name, the arguments with their limits and the description of each come from the operation, so a tool cannot drift from the API. Which operations become tools is an allow-list, `scripts/read-tools.config.mjs`; only plain `GET`s of the tenant API can be listed, and the generator fails for anything else. A test checks every tool against the OpenAPI file.

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

## Over HTTP

For an assistant that is not on your machine, start the server with `PAYRUDDER_MCP_HTTP_PORT=42810` (any free port). It then serves [streamable HTTP](https://modelcontextprotocol.io/specification/2025-03-26/basic/transports#streamable-http) at `POST /mcp` instead of stdio:

- **Every request carries the caller's own API key**, `Authorization: Bearer qk_test_…` (or `qk_live_…`). A request without one is `401`. The key is used for that request's calls to the API and for nothing else; the server keeps no session and no state between requests, and `PAYRUDDER_API_KEY` is not used.
- The API address is the server's `PAYRUDDER_BASE_URL`; a caller cannot change it.
- It listens on `127.0.0.1` unless you set `PAYRUDDER_MCP_HTTP_HOST`. There is no TLS here: put a proxy that terminates TLS in front of it before it is reachable from anywhere else, or the keys travel in the clear.
- `PAYRUDDER_MCP_WRITES=1` applies to every caller of the server; each caller's key still decides what succeeds.
- Not built yet: sign-in with OAuth instead of a key, a rate limit per key, and a log of what an assistant did. Until then, hand out keys of the narrowest role (`read_only`).

Claude Code: `claude mcp add --transport http payrudder https://YOUR-MCP-ADDRESS/mcp --header "Authorization: Bearer qk_test_..."`.

## Resources

Besides tools, the server offers what an assistant needs to read before it calls them: `payrudder://openapi` (the OpenAPI document of the tenant API, nothing of the operator or console routes) and the public guides, `payrudder://docs/getting-started`, `…/webhooks`, `…/sdks` and `…/error-keys` (the documents marked `public` in `docs/publication.yaml`, and the pages of `docs-site/content`). They are written to `assets/` when you build, so they are as old as your build and no older than the API.

## Safety

- **The key is the limit.** Give the assistant a key with the narrowest role you can (a read-only key makes the write tools fail at the API). Use a sandbox key until you trust what it does.
- Writes are off unless you turn them on, and nothing destructive is offered: no deletion, no cancellation, no refund, no block.
- Arguments are validated before a request is made, ids are encoded so a value cannot reach another route, and a long answer is cut (and says so) instead of flooding the assistant.
- The key is never part of an answer or an error.
- **Text that people wrote is tagged.** In every answer, the strings a customer, a payer or your staff wrote (names, e-mail addresses, notes, address lines, references; the list is `UNTRUSTED` in `scripts/read-tools.config.mjs`) are wrapped in `<untrusted>…</untrusted>`, and a closing tag inside such a text is broken so it cannot end the wrapper early. The server's instructions and each read tool's description tell the model that this is data to report, not an instruction to follow. That lowers the risk of prompt injection through a customer's name; it does not remove it, so keep the key's role narrow.
- The generator fails when an allow-listed operation's answer has a free-text field that is neither listed as people's text nor as an identifier, so a field added to the API is classified before it reaches a model.

## Develop

```sh
cd mcp && npm ci
npm run typecheck && npm test && npm run build
```

`npm test` includes a test that builds the server and talks to it over a real stdio pipe, and one that serves it over HTTP. Not tried yet with a real assistant (Claude Desktop, Claude Code) by a person.
