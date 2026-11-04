#!/usr/bin/env node
import type { AddressInfo } from "node:net";
import { StdioServerTransport } from "@modelcontextprotocol/sdk/server/stdio.js";
import { createApi } from "./api.js";
import { defaultAudit, keyPrefix } from "./audit.js";
import { createHttpServer } from "./http.js";
import { canonicalResource, createOAuth } from "./oauth.js";
import { liveRefusal } from "./privacy.js";
import { createRateLimit } from "./ratelimit.js";
import { createServer } from "./server.js";

// PAYRUDDER_API_KEY        the key the tools act with (qk_test_... for the sandbox, qk_live_... for live); its permissions are the tools' permissions (stdio)
// PAYRUDDER_BASE_URL       the API address (default http://localhost:42800)
// PAYRUDDER_MCP_WRITES     1 to also offer the tools that write (create a customer, send usage events)
// PAYRUDDER_MCP_ALLOW_PERSONAL_DATA  1 to let customers' names, e-mail addresses, phone numbers, tax ids and addresses reach the model (redacted in every answer otherwise)
// PAYRUDDER_MCP_ALLOW_LIVE           1 to accept a live key; otherwise a qk_live_ key is refused (stdio: at start; HTTP: the request gets a 403)
// PAYRUDDER_MCP_HTTP_PORT  set it to serve streamable HTTP on /mcp instead of stdio; each caller sends its own key as a bearer token, so PAYRUDDER_API_KEY is not used
// PAYRUDDER_MCP_HTTP_HOST  the address to listen on in that mode (default 127.0.0.1: put a TLS-terminating proxy in front before listening on anything else)
// PAYRUDDER_MCP_AUDIT      0 to stop the audit log (one JSON line per tool call on stderr)
// PAYRUDDER_MCP_EDGE_SECRET   (HTTP) the gateway's PAYRUDDER_TRUSTED_PROXY_SECRET, at least 32 characters: the caller's address goes to the API, which counts a refused key against it and not against this server
// PAYRUDDER_MCP_TRUST_FORWARDED_FOR  (HTTP) 1 when a proxy in front writes the caller's address as the last X-Forwarded-For entry; otherwise the connection's address is sent
// PAYRUDDER_MCP_AUTH       key (the default) or oauth: sign-in through Payrudder's authorization server instead of an API key (HTTP only; docs/plans/20-mcp.md, "OAuth design", default, not agreed)
// PAYRUDDER_MCP_PUBLIC_URL (oauth) the address clients reach this endpoint at, https://mcp.example.com/mcp: the audience of the tokens; the gateway lists it in PAYRUDDER_OAUTH_RESOURCES
// PAYRUDDER_MCP_ISSUER     (oauth) the authorization server, the gateway's PAYRUDDER_PUBLIC_URL (default PAYRUDDER_BASE_URL)
// PAYRUDDER_MCP_OAUTH_SECRET (oauth) the gateway's PAYRUDDER_OAUTH_RESOURCE_SECRET, at least 32 characters: introspects tokens and trades them for the API credential
// PAYRUDDER_MCP_RATE_PER_MINUTE  tool calls a minute for each key (default 60, a burst of 10; 0 for no limit): the API limits per tenant, so this is the key's own share
const baseUrl = process.env.PAYRUDDER_BASE_URL ?? "http://localhost:42800";
const writes = process.env.PAYRUDDER_MCP_WRITES === "1";
const allowPersonalData = process.env.PAYRUDDER_MCP_ALLOW_PERSONAL_DATA === "1";
const allowLive = process.env.PAYRUDDER_MCP_ALLOW_LIVE === "1";
const httpPort = process.env.PAYRUDDER_MCP_HTTP_PORT;
const audit = defaultAudit();
const ratePerMinute = Number(process.env.PAYRUDDER_MCP_RATE_PER_MINUTE || 60);
if (!Number.isInteger(ratePerMinute) || ratePerMinute < 0) {
  console.error("payrudder-mcp: PAYRUDDER_MCP_RATE_PER_MINUTE must be a whole number of calls a minute (0 for no limit).");
  process.exit(1);
}

/** PAYRUDDER_MCP_AUTH=oauth: the resource server of docs/plans/20-mcp.md ("OAuth design"). Exits with the reason when it is half set up; undefined in the default key mode. */
function oauthFromEnv() {
  const mode = process.env.PAYRUDDER_MCP_AUTH ?? "key";
  if (mode !== "key" && mode !== "oauth") return fail("PAYRUDDER_MCP_AUTH must be key (the default) or oauth.");
  if (mode === "key") return undefined;
  if (httpPort === undefined) return fail("PAYRUDDER_MCP_AUTH=oauth serves HTTP: set PAYRUDDER_MCP_HTTP_PORT (stdio reads its key from the environment).");
  const publicUrl = canonicalResource(process.env.PAYRUDDER_MCP_PUBLIC_URL ?? "");
  if (!publicUrl.ok) return fail(`PAYRUDDER_MCP_PUBLIC_URL must be the address clients reach this endpoint at, like https://mcp.example.com/mcp: ${publicUrl.why}.`);
  const secret = process.env.PAYRUDDER_MCP_OAUTH_SECRET ?? "";
  if (secret.length < 32) return fail("PAYRUDDER_MCP_OAUTH_SECRET must be the gateway's PAYRUDDER_OAUTH_RESOURCE_SECRET, at least 32 characters.");
  const issuer = canonicalResource(process.env.PAYRUDDER_MCP_ISSUER ?? baseUrl);
  if (!issuer.ok) return fail(`PAYRUDDER_MCP_ISSUER (default PAYRUDDER_BASE_URL) must be the gateway's PAYRUDDER_PUBLIC_URL: ${issuer.why}.`);
  return createOAuth({ publicUrl: publicUrl.url, issuer: issuer.url, secret });
}

function fail(message: string): never {
  console.error(`payrudder-mcp: ${message}`);
  process.exit(1);
}

if (httpPort !== undefined) {
  const port = Number(httpPort);
  if (!Number.isInteger(port) || port < 0 || port > 65535) {
    console.error("payrudder-mcp: PAYRUDDER_MCP_HTTP_PORT must be a port number.");
    process.exit(1);
  }
  const host = process.env.PAYRUDDER_MCP_HTTP_HOST ?? "127.0.0.1";
  const edgeSecret = process.env.PAYRUDDER_MCP_EDGE_SECRET;
  if (edgeSecret !== undefined && edgeSecret.length < 32) {
    console.error("payrudder-mcp: PAYRUDDER_MCP_EDGE_SECRET must be the gateway's PAYRUDDER_TRUSTED_PROXY_SECRET, at least 32 characters.");
    process.exit(1);
  }
  const oauth = oauthFromEnv();
  const http = createHttpServer({ baseUrl, allowWrites: writes, allowLive, allowPersonalData, ratePerMinute, ...(edgeSecret ? { edgeSecret } : {}), trustForwardedFor: process.env.PAYRUDDER_MCP_TRUST_FORWARDED_FOR === "1", ...(audit ? { audit } : {}), ...(oauth ? { oauth } : {}) });
  http.listen(port, host, () => {
    console.error(`payrudder-mcp: serving streamable HTTP on http://${host}:${(http.address() as AddressInfo).port}/mcp (${writes ? "reads and writes" : "read only"}, ${oauth ? `OAuth sign-in through ${oauth.resource}` : "the caller's key in every request"})`);
  });
} else {
  oauthFromEnv(); // PAYRUDDER_MCP_AUTH=oauth without a port exits here with the reason; in key mode this is nothing
  const apiKey = process.env.PAYRUDDER_API_KEY;
  if (!apiKey) {
    console.error("payrudder-mcp: set PAYRUDDER_API_KEY (a qk_test_ or qk_live_ key).");
    process.exit(1);
  }
  const refusal = liveRefusal(apiKey, allowLive);
  if (refusal) {
    console.error(`payrudder-mcp: ${refusal}`);
    process.exit(1);
  }
  const limit = createRateLimit(ratePerMinute);
  const server = createServer({ api: createApi({ baseUrl, apiKey }), allowWrites: writes, allowPersonalData, transport: "stdio", keyHint: keyPrefix(apiKey), ...(audit ? { audit } : {}), ...(limit ? { rateLimit: () => limit.take("stdio") } : {}) });
  await server.connect(new StdioServerTransport());
  console.error(`payrudder-mcp: ready (${writes ? "reads and writes" : "read only"}, ${apiKey.startsWith("qk_live_") ? "LIVE" : "sandbox"} key)`);
}
