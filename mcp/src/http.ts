import { createServer as listen, type IncomingMessage, type Server, type ServerResponse } from "node:http";
import { StreamableHTTPServerTransport } from "@modelcontextprotocol/sdk/server/streamableHttp.js";
import { createApi } from "./api.js";
import { keyPrefix, type Audit } from "./audit.js";
import { liveRefusal } from "./privacy.js";
import { createRateLimit, limitKeyOf } from "./ratelimit.js";
import { createServer } from "./server.js";

/**
 * Streamable HTTP (MCP spec 2025-03-26), for an assistant that is not on the same machine as the server. It is stateless: every POST to /mcp carries the caller's own API
 * key as a bearer token and is answered on its own, with that key and no other (nothing is shared between callers, no session is kept). The API address is the server's
 * (never taken from a request), so a caller cannot point it somewhere else. The key's role is what limits the caller; the server does not check the key, the API does on
 * the first call. This is key authentication only: there is no OAuth. The API's limits are per tenant and shared by every key; the server adds a limit per key when `ratePerMinute` is set (ratelimit.ts). Every tool call is written to the audit log (audit.ts) with the caller's key prefix (docs/plans/20-mcp.md).
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

export function createHttpServer(o: HttpOptions): Server {
  const limit = createRateLimit(o.ratePerMinute ?? 0);
  return listen(async (req, res) => {
    try {
      if (new URL(req.url ?? "/", "http://x").pathname !== "/mcp") return reply(res, 404, "The MCP endpoint is /mcp.");
      // Stateless: no GET stream to open and no session to delete.
      if (req.method !== "POST") return reply(res, 405, "POST JSON-RPC messages to /mcp.", { Allow: "POST" });
      const key = /^Bearer (.+)$/i.exec(req.headers.authorization ?? "")?.[1]?.trim();
      if (!key || !KEY.test(key)) return reply(res, 401, "Send your Payrudder API key (qk_test_... or qk_live_...) as a bearer token.", { "WWW-Authenticate": 'Bearer realm="payrudder-mcp"' });
      const refusal = liveRefusal(key, o.allowLive ?? false);
      if (refusal) return reply(res, 403, refusal);
      const body = await readBody(req);
      const server = createServer({ api: createApi({ baseUrl: o.baseUrl, apiKey: key, ...(o.fetch ? { fetch: o.fetch } : {}) }), allowWrites: o.allowWrites ?? false, allowPersonalData: o.allowPersonalData ?? false, transport: "http", keyHint: keyPrefix(key), ...(o.audit ? { audit: o.audit } : {}), ...(limit ? { rateLimit: () => limit.take(limitKeyOf(key)) } : {}) });
      const transport = new StreamableHTTPServerTransport({ sessionIdGenerator: undefined, enableJsonResponse: true });
      res.on("close", () => {
        void transport.close();
        void server.close();
      });
      await server.connect(transport);
      await transport.handleRequest(req, res, body);
    } catch (e) {
      if (!res.headersSent) reply(res, (e as { status?: number }).status ?? 500, (e as { status?: number }).status ? (e as Error).message : "The server failed.");
      else res.end();
    }
  });
}
