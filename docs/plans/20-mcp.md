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
