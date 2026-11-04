import { createServer as listen, type IncomingMessage, type Server, type ServerResponse } from "node:http";
import { StreamableHTTPServerTransport } from "@modelcontextprotocol/sdk/server/streamableHttp.js";
import { createApi } from "./api.js";
import { keyPrefix, type Audit } from "./audit.js";
import { requiredScopes, type OAuth } from "./oauth.js";
import { liveRefusal } from "./privacy.js";
import { createRateLimit, limitKeyOf } from "./ratelimit.js";
import { createServer } from "./server.js";
import { TOOLS } from "./tools.js";

/**
 * Streamable HTTP (MCP spec 2025-03-26), for an assistant that is not on the same machine as the server. It is stateless: every POST to /mcp carries the caller's own
 * credential as a bearer token and is answered on its own, with that credential and no other (nothing is shared between callers, no session is kept). The API address is the
 * server's (never taken from a request), so a caller cannot point it somewhere else.
 *
 * Two ways to authenticate (PAYRUDDER_MCP_AUTH). The default is an API key: the caller sends it and its role is what limits the caller; the server does not check the key, the
 * API does on the first call. With `oauth` set the server is an OAuth 2.1 resource server (oauth.ts): the caller signs in through Payrudder's authorization server, the server
 * checks the token on every request and calls the API with a credential of its own that the authorization server minted for it. The API's limits are per tenant and shared
 * by every key; the server adds a limit per key (per person and tenant in OAuth mode) when `ratePerMinute` is set (ratelimit.ts). Every tool call is written to the audit
 * log (audit.ts) with the caller's key prefix, or the person (docs/plans/20-mcp.md).
 */
export interface HttpOptions {
  baseUrl: string;
  allowWrites?: boolean;
  /** Accept live keys (PAYRUDDER_MCP_ALLOW_LIVE=1); a request with one is refused otherwise. */
  allowLive?: boolean;
  /** Let customers' personal data reach the model (PAYRUDDER_MCP_ALLOW_PERSONAL_DATA=1); redacted otherwise. */
  allowPersonalData?: boolean;
  /** One record per tool call, with the caller's key prefix (audit.ts). */
  audit?: Audit;
  /** Tool calls a minute for each key (ratelimit.ts); 0 or unset is no limit. */
  ratePerMinute?: number;
  /**
   * PAYRUDDER_MCP_EDGE_SECRET, the gateway's PAYRUDDER_TRUSTED_PROXY_SECRET. The gateway counts the keys it refuses by the address they come from, and every caller of this server
   * comes from the server's: one flood of junk keys would lock all of them out. With the secret the caller's address goes along (X-Forwarded-For, X-Edge-Auth) and is the one counted.
   */
  edgeSecret?: string;
  /** PAYRUDDER_MCP_TRUST_FORWARDED_FOR=1: a proxy in front of this server writes the caller's address as the last X-Forwarded-For entry; otherwise the connection's address is used. */
  trustForwardedFor?: boolean;
  /** Set: OAuth mode (PAYRUDDER_MCP_AUTH=oauth), and API keys are not accepted. */
  oauth?: OAuth;
  /** Injected in tests. */
  fetch?: typeof fetch;
}

const MAX_BODY = 1 << 20;
/** The shape of an API key (qk_test_... or qk_live_...). Anything else is refused before the API is asked. */
const KEY = /^qk_(test|live)_\S+$/;

function reply(res: ServerResponse, status: number, message: string, headers: Record<string, string> = {}) {
  res.writeHead(status, { "Content-Type": "application/json", ...headers });
  res.end(JSON.stringify({ jsonrpc: "2.0", error: { code: status === 401 ? -32001 : -32000, message }, id: null }));
}

function readBody(req: IncomingMessage): Promise<unknown> {
  return new Promise((resolve, reject) => {
    const chunks: Buffer[] = [];
    let size = 0;
    req.on("data", (c: Buffer) => {
      size += c.length;
      if (size > MAX_BODY) {
        reject(Object.assign(new Error("too large"), { status: 413 }));
        req.destroy();
      } else chunks.push(c);
    });
    req.on("end", () => {
      try {
        resolve(JSON.parse(Buffer.concat(chunks).toString("utf8")));
      } catch {
        reject(Object.assign(new Error("not JSON"), { status: 400 }));
      }
    });
    req.on("error", reject);
  });
}

/** The address of whoever made the request: the connection's, or with a proxy in front the last entry the proxy wrote in X-Forwarded-For (an earlier one is the client's own claim). */
function callerAddress(req: IncomingMessage, behindProxy: boolean): string {
  const forwarded = behindProxy ? String(req.headers["x-forwarded-for"] ?? "").split(",").pop()?.trim() : "";
  return forwarded || req.socket.remoteAddress || "";
}

/** Who a request is for the work that follows: the credential the API is called with, what the audit log and the limiter call the caller. */
interface Caller {
  apiKey: string;
  keyHint: string;
  clientHint?: string;
  limitKey: string;
}

const LIVE_REFUSED = "This token is for a live environment and this server refuses live environments. Start the server with PAYRUDDER_MCP_ALLOW_LIVE=1 to let an assistant act on live data, or connect to a sandbox.";

export function createHttpServer(o: HttpOptions): Server {
  const limit = createRateLimit(o.ratePerMinute ?? 0);
  const scopeOf = new Map(TOOLS.map((t) => [t.name, t.scope]));

  /** Answers one request of an authenticated caller on a server of its own, and closes it when the answer is done. */
  async function run(req: IncomingMessage, res: ServerResponse, body: unknown, c: Caller): Promise<void> {
    const server = createServer({
      api: createApi({ baseUrl: o.baseUrl, apiKey: c.apiKey, ...(o.fetch ? { fetch: o.fetch } : {}), ...(o.edgeSecret ? { forward: { address: callerAddress(req, o.trustForwardedFor ?? false), secret: o.edgeSecret } } : {}) }),
      allowWrites: o.allowWrites ?? false,
      allowPersonalData: o.allowPersonalData ?? false,
      transport: "http",
      keyHint: c.keyHint,
      ...(c.clientHint ? { clientHint: c.clientHint } : {}),
      ...(o.audit ? { audit: o.audit } : {}),
      ...(limit ? { rateLimit: () => limit.take(c.limitKey) } : {}),
    });
    const transport = new StreamableHTTPServerTransport({ sessionIdGenerator: undefined, enableJsonResponse: true });
    res.on("close", () => {
      void transport.close();
      void server.close();
    });
    await server.connect(transport);
    await transport.handleRequest(req, res, body);
  }

  /** OAuth mode: the token is checked on every request (active, for this server, not expired), then the scopes the calls in the body need. */
  async function runOAuth(oauth: OAuth, req: IncomingMessage, res: ServerResponse): Promise<void> {
    const v = await oauth.authenticate(req.headers.authorization);
    if (!v.ok) return reply(res, v.status, v.message, { ...(v.challenge ? { "WWW-Authenticate": v.challenge } : {}), ...(v.retryAfter ? { "Retry-After": String(v.retryAfter) } : {}) });
    const p = v.principal;
    // Stateless: no GET stream to open and no session to delete.
    if (req.method !== "POST") return reply(res, 405, "POST JSON-RPC messages to /mcp.", { Allow: "POST" });
    if (p.environment === "live" && !o.allowLive) return reply(res, 403, LIVE_REFUSED);
    const body = await readBody(req);
    const missing = requiredScopes(body, (tool) => scopeOf.get(tool)).filter((s) => !p.scopes.includes(s));
    if (missing.length > 0) {
      const scope = missing.join(" ");
      return reply(res, 403, `This token does not carry the scope ${scope} that this call needs. Authorize again with it.`, { "WWW-Authenticate": oauth.challenge({ error: "insufficient_scope", scope, description: `The scope ${scope} is required for this call` }) });
    }
    // The limit is per person and tenant, not per token: a new sign-in (a new token, another assistant) does not give a new budget.
    await run(req, res, body, { apiKey: p.credential, keyHint: `oauth:${p.sub}`, clientHint: p.clientId, limitKey: limitKeyOf(`oauth:${p.sub}:${p.tenantId}`) });
  }

  return listen(async (req, res) => {
    try {
      const path = new URL(req.url ?? "/", "http://x").pathname;
      if (o.oauth?.metadataPaths.includes(path)) {
        // Protected Resource Metadata (RFC 9728): public, readable by a browser-based client too.
        const cors = { "Access-Control-Allow-Origin": "*", "Access-Control-Allow-Methods": "GET, OPTIONS" };
        if (req.method === "OPTIONS") {
          res.writeHead(204, cors).end();
          return;
        }
        if (req.method !== "GET" && req.method !== "HEAD") return reply(res, 405, "GET the metadata.", { Allow: "GET, HEAD, OPTIONS" });
        res.writeHead(200, { "Content-Type": "application/json", "Cache-Control": "public, max-age=3600", ...cors });
        res.end(req.method === "HEAD" ? undefined : JSON.stringify(o.oauth.metadata()));
        return;
      }
      if (path !== "/mcp") return reply(res, 404, "The MCP endpoint is /mcp.");
      if (o.oauth) return await runOAuth(o.oauth, req, res);
      // Stateless: no GET stream to open and no session to delete.
      if (req.method !== "POST") return reply(res, 405, "POST JSON-RPC messages to /mcp.", { Allow: "POST" });
      const key = /^Bearer (.+)$/i.exec(req.headers.authorization ?? "")?.[1]?.trim();
      if (!key || !KEY.test(key)) return reply(res, 401, "Send your Payrudder API key (qk_test_... or qk_live_...) as a bearer token.", { "WWW-Authenticate": 'Bearer realm="payrudder-mcp"' });
      const refusal = liveRefusal(key, o.allowLive ?? false);
      if (refusal) return reply(res, 403, refusal);
      const body = await readBody(req);
      await run(req, res, body, { apiKey: key, keyHint: keyPrefix(key), limitKey: limitKeyOf(key) });
    } catch (e) {
      if (!res.headersSent) reply(res, (e as { status?: number }).status ?? 500, (e as { status?: number }).status ? (e as Error).message : "The server failed.");
      else res.end();
    }
  });
}
