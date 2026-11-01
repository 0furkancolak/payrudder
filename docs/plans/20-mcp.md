# 20 — MCP server

## The request
List 2 (1): **MCP**.

## Where we are
#257: `mcp/`, a stdio MCP server using the official SDK: nine read-only tools (meters, customers, a customer's usage, plans, subscriptions, invoices, payments), two write tools (create customer, send usage events) only when `PAYRUDDER_MCP_WRITES=1`, the API key is the limit; validation before requests, encoded ids, truncated long answers, errors with API keys. Tested with an in-memory MCP client and over real stdio against a local HTTP server; README, CI job.

## Gaps
- **Which MCP is meant was not asked.** There are at least three: (a) an MCP server for the *tenant API* (built), (b) an MCP for the **operator** (support, tenants), (c) an MCP that exposes **Payrudder's documentation** to assistants, and (d) the console/app *being* an MCP client. Only (a) exists.
- Stdio only: a hosted (HTTP/SSE "streamable HTTP") server with OAuth is what remote assistants need; no auth model beyond one key; no per-tool permissions or rate limits; no audit of what an assistant did; no resources (OpenAPI, docs) or prompts; tools not generated from the OpenAPI (hand-picked, so they can drift).
- Prompt-injection exposure: answers contain customer-written text; the README warns but nothing sanitises or tags untrusted fields.
- Not tried with a real assistant (Claude Desktop/Code) by a person.
- No coverage of billing actions (refunds, credit notes, cancel) by design; unspecified whether the owner wants any.

## Research first
1. Ask the owner which MCP(s) and for whom (internal operators, customers' developers, both).
2. Read the current MCP spec (authorization, streamable HTTP, structured output, elicitation for confirmations) and the SDK release notes.
3. Decide the write-safety model: confirmation via elicitation, per-tool scopes, dry-run.

## Decisions needed
Audience, hosting, authentication (API key vs OAuth with the console's sign-in), which actions an assistant may take.

## Work
1. Spec of audiences and tool sets, with a risk table per tool.
2. Generate read tools from the OpenAPI (with an allow-list) so tools and API never drift; test the mapping.
3. Streamable HTTP transport + OAuth (if hosted); per-key rate limits and audit log.
4. Resources: OpenAPI and docs as MCP resources; a few prompts (monthly revenue review, find the customer over limit).
5. Operator MCP in a separate server with its own auth and an approval step for any write.
6. Evaluate with real assistants on scripted tasks; record results.

## Done means
A person completes five scripted tasks through a real assistant; every tool has a test; the risk table is signed off by the owner.

## Not verifiable here
Assistant behaviour (needs a person and a client).

## Risks
Giving an assistant write access to billing: default to read-only and confirmations.

## Review addendum (2026-11-01)
- Tests use `InMemoryTransport` only (`mcp/src/server.test.ts`); no stdio spawn test, and `mcp.yml` runs typecheck, test and build. API-key roles already limit tools (`read_only`, `billing`, `ingest_only`, custom, enforced by the gateway); `main.ts` warns when writes are enabled with an admin key.
- Work: stdio spawn test; tag untrusted fields; generate read tools from OpenAPI with an allow-list and a mapping test; streamable HTTP with bearer API-key auth.

## Progress (2026-11-02, branch `feat/dx-docs-mcp`)

Done:
- **A real stdio test** (`mcp/src/stdio.test.ts`): it builds the server, starts `dist/main.js` as a child process the way an assistant does, and talks MCP over its stdin and stdout to a local HTTP server that plays the API (tools listed, a call made with the key from the environment, the answer tagged; writes only with `PAYRUDDER_MCP_WRITES=1`; no key means exit 1 with the message on stderr and nothing on stdout).
- **The nine read tools are generated from `api/openapi/v1.yaml`** by `mcp/scripts/generate.mjs` at build, test and typecheck time (the output is not committed, so it cannot be stale): name from the `operationId`, arguments with their limits from the path and query parameters, description from the summary. The allow-list is `mcp/scripts/read-tools.config.mjs`; only a plain `GET` of `/v1/*` can be listed and the generator fails for anything else. The mapping test checks every tool against the OpenAPI file (route, parameters, required, enum, minimum, maximum, pattern, rename) and that the generated file is what the generator makes now. The two write tools stay hand-written; the nine tools keep their names and arguments (the existing tests passed unchanged). `mcp.yml` now also runs when the OpenAPI file or the public docs change.
- **Untrusted fields are tagged:** strings under the keys the customers, payers and staff write (names, e-mail, notes, address lines, references, …; a closing tag inside such a text is broken) come back inside `<untrusted>…</untrusted>`, in reads and in the answers of writes; the server's instructions and every read tool's description say what the tag means. The generator fails when an allow-listed answer has a free-text field that is neither listed as people's text nor as an identifier the model must copy exactly, so a new field is classified before a model sees it.
- **Streamable HTTP** behind a flag (`PAYRUDDER_MCP_HTTP_PORT`): `POST /mcp`, stateless, JSON responses, **each request carries the caller's own API key as a bearer token** and that key alone is used for its calls to the API (`401` without one, nothing shared between callers, the API address is the server's), 127.0.0.1 unless told otherwise, a 1 MiB body limit. Tested over a real client and server (the keys of two concurrent callers never mix).
- **Resources:** `payrudder://openapi` (the tenant API only, with the components it reaches) and the public guides (`payrudder://docs/<page>`: the pages of `docs-site/content` and the documents `docs/publication.yaml` marks public), written to `mcp/assets/` at build.

Defaults (not agreed): API-key bearer authentication (no OAuth); the allow-list is exactly the nine tools that existed; the write tools stay off unless the person who starts the server turns them on, for every caller of an HTTP server alike; the HTTP mode listens on 127.0.0.1.

Not done: OAuth with the console's sign-in, a rate limit per key, an audit log of what an assistant did (all three are needed before this is hosted for others), prompts (monthly revenue review, customer over a limit), the operator MCP, the risk table per tool for the owner to sign, and any new read tool (add an operation to the allow-list and classify its fields).

Not verified: nothing here was tried with a real assistant (Claude Desktop, Claude Code) by a person; the HTTP mode was tested with the SDK's own client only; no TLS or proxy setup was tried.

