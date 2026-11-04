import { readFile } from "node:fs/promises";
import { join } from "node:path";
import type { AddressInfo } from "node:net";
import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { StreamableHTTPClientTransport } from "@modelcontextprotocol/sdk/client/streamableHttp.js";
import { afterEach, describe, expect, it } from "vitest";
import { parse } from "yaml";
import { repoRoot } from "../../scripts/generate.mjs";
import type { AuditRecord } from "../audit.js";
import { createHttpServer } from "../http.js";
import { createOAuth } from "../oauth.js";
import { TOOLS } from "../tools.js";

// The server in OAuth mode (PAYRUDDER_MCP_AUTH=oauth): the metadata, the 401 challenge, the audience and expiry checks, 403 insufficient_scope, and the API called with the
// credential minted for it, never with the assistant's token. The authorization server and the API are fakes.

const RESOURCE = "https://mcp.example.com/mcp";
const ISSUER = "https://api.example.com";
const SECRET = "s".repeat(40);
const GOOD = "qo_" + "A".repeat(43);
const READER = "qo_" + "R".repeat(43);
const LIVE = "qo_" + "L".repeat(43);
const OTHER = "qo_" + "O".repeat(43);

const open: { close(): void }[] = [];
afterEach(() => {
  while (open.length) open.pop()?.close();
});

const info = (over: Record<string, unknown> = {}) => ({
  active: true, scope: "customers:read customers:write events:write catalog:read", client_id: "https://client.example/m.json", client_name: "Example", sub: "person-1", aud: RESOURCE, exp: 4_000_000_000,
  token_type: "Bearer", tenant_id: "tenant-1", environment: "sandbox", member_role: "owner", ...over,
});

async function start(opts: { allowWrites?: boolean; allowLive?: boolean; ratePerMinute?: number; audit?: (r: AuditRecord) => void; down?: boolean } = {}) {
  const apiCalls: { path: string; auth: string | null }[] = [];
  const asCalls: string[] = [];
  const tokens = new Map<string, Record<string, unknown>>([
    [GOOD, info()],
    [READER, info({ scope: "customers:read catalog:read" })],
    [LIVE, info({ environment: "live", tenant_id: "tenant-live" })],
    [OTHER, info({ aud: "https://other.example/mcp" })],
  ]);
  let down = opts.down ?? false;
  const asFetch = (async (url: URL | string, init: RequestInit = {}) => {
    if (down) throw new Error("refused");
    const path = new URL(String(url)).pathname;
    asCalls.push(path);
    const form = new URLSearchParams(String(init.body));
    const t = tokens.get(form.get("token") ?? form.get("subject_token") ?? "");
    if (path === "/oauth/introspect") return new Response(JSON.stringify(t && t.aud === form.get("resource") ? t : { active: false }), { status: 200 });
    if (!t) return new Response(JSON.stringify({ error: "invalid_grant" }), { status: 401 });
    return new Response(JSON.stringify({ access_token: "qm_" + "M".repeat(43), expires_in: 300, token_type: "Bearer" }), { status: 200 });
  }) as typeof fetch;
  const apiFetch = (async (url: URL | string, init: RequestInit = {}) => {
    apiCalls.push({ path: new URL(String(url)).pathname, auth: new Headers(init.headers).get("Authorization") });
    return new Response(JSON.stringify({ external_id: "cus_1", name: "Ada" }), { status: 200 });
  }) as typeof fetch;
  const oauth = createOAuth({ publicUrl: RESOURCE, issuer: ISSUER, secret: SECRET, fetch: asFetch });
  const server = createHttpServer({ baseUrl: "https://api.example", fetch: apiFetch, oauth, allowWrites: opts.allowWrites ?? false, ...(opts.allowLive ? { allowLive: true } : {}), ...(opts.ratePerMinute !== undefined ? { ratePerMinute: opts.ratePerMinute } : {}), ...(opts.audit ? { audit: opts.audit } : {}) });
  await new Promise<void>((r) => server.listen(0, "127.0.0.1", r));
  open.push({ close: () => server.close() });
  const origin = `http://127.0.0.1:${(server.address() as AddressInfo).port}`;
  const post = (token: string | undefined, body: unknown) =>
    fetch(`${origin}/mcp`, { method: "POST", headers: { "Content-Type": "application/json", Accept: "application/json, text/event-stream", ...(token ? { Authorization: `Bearer ${token}` } : {}) }, body: JSON.stringify(body) });
  const call = (name: string, id = 1, args: Record<string, unknown> = {}) => ({ jsonrpc: "2.0", id, method: "tools/call", params: { name, arguments: args } });
  const connect = async (token: string) => {
    const client = new Client({ name: "test", version: "0" });
    await client.connect(new StreamableHTTPClientTransport(new URL(`${origin}/mcp`), { requestInit: { headers: { Authorization: `Bearer ${token}` } } }));
    open.push(client);
    return client;
  };
  return { origin, post, call, connect, apiCalls, asCalls, tokens, setDown: (v: boolean) => (down = v) };
}

describe("Protected Resource Metadata", () => {
  it("is served without a token at the address built from the resource, and at the root", async () => {
    const { origin } = await start();
    for (const p of ["/.well-known/oauth-protected-resource/mcp", "/.well-known/oauth-protected-resource"]) {
      const r = await fetch(origin + p);
      expect(r.status).toBe(200);
      expect(r.headers.get("access-control-allow-origin")).toBe("*");
      expect(await r.json()).toMatchObject({ resource: RESOURCE, authorization_servers: [ISSUER], bearer_methods_supported: ["header"] });
    }
    expect((await fetch(origin + "/.well-known/oauth-protected-resource/mcp", { method: "OPTIONS" })).status).toBe(204);
    expect((await fetch(origin + "/.well-known/oauth-protected-resource/mcp", { method: "POST" })).status).toBe(405);
  });

  it("is not served in the default key mode", async () => {
    const server = createHttpServer({ baseUrl: "https://api.example" });
    await new Promise<void>((r) => server.listen(0, "127.0.0.1", r));
    open.push({ close: () => server.close() });
    expect((await fetch(`http://127.0.0.1:${(server.address() as AddressInfo).port}/.well-known/oauth-protected-resource`)).status).toBe(404);
  });
});

describe("the challenge", () => {
  it("answers a request without a token with 401 and a WWW-Authenticate that points at the metadata", async () => {
    const { post, call, asCalls } = await start();
    const r = await post(undefined, call("list_meters"));
    expect(r.status).toBe(401);
    const h = r.headers.get("www-authenticate") ?? "";
    expect(h).toMatch(/^Bearer /);
    expect(h).toContain('resource_metadata="https://mcp.example.com/.well-known/oauth-protected-resource/mcp"');
    expect(h).toContain('scope="read_only"');
    expect(asCalls).toHaveLength(0);
  });

  it("answers GET without a token with 401 too (the specification asks for it on every request), and GET with one with 405", async () => {
    const { origin } = await start();
    expect((await fetch(`${origin}/mcp`)).status).toBe(401);
    expect((await fetch(`${origin}/mcp`, { headers: { Authorization: `Bearer ${GOOD}` } })).status).toBe(405);
  });

  it("refuses an API key: this mode accepts tokens of the authorization server only", async () => {
    const { post, call, apiCalls } = await start();
    const r = await post("qk_test_ab12cd34_" + "x".repeat(40), call("list_meters"));
    expect(r.status).toBe(401);
    expect(r.headers.get("www-authenticate")).toContain('error="invalid_token"');
    expect(apiCalls).toHaveLength(0);
  });
});

describe("a valid token", () => {
  it("serves the tools and calls the API with the minted credential, never with the assistant's token", async () => {
    const { connect, apiCalls } = await start();
    const client = await connect(GOOD);
    expect((await client.listTools()).tools).toHaveLength(30);
    await client.callTool({ name: "get_customer", arguments: { customer_external_id: "cus_1" } });
    expect(apiCalls).toEqual([{ path: "/v1/customers/cus_1", auth: `Bearer qm_${"M".repeat(43)}` }]);
    expect(JSON.stringify(apiCalls)).not.toContain(GOOD);
  });

  it("refuses a token issued for another server, and a token the authorization server does not know, without calling the API", async () => {
    const { post, call, apiCalls } = await start();
    for (const t of [OTHER, "qo_" + "Z".repeat(43)]) {
      const r = await post(t, call("list_meters"));
      expect(r.status).toBe(401);
      expect(r.headers.get("www-authenticate")).toContain('error="invalid_token"');
    }
    expect(apiCalls).toHaveLength(0);
  });

  it("is a 503 with Retry-After while the authorization server is unreachable, and recovers", async () => {
    const { post, call, setDown } = await start({ down: true });
    const r = await post(GOOD, call("list_meters"));
    expect(r.status).toBe(503);
    expect(r.headers.get("retry-after")).toBe("5");
    setDown(false);
    expect((await post(GOOD, call("list_meters"))).status).toBe(200);
  });

  it("refuses a live environment unless the server was started to allow live", async () => {
    const refused = await start();
    const r = await refused.post(LIVE, refused.call("list_meters"));
    expect(r.status).toBe(403);
    expect(await r.text()).toContain("PAYRUDDER_MCP_ALLOW_LIVE=1");
    expect(r.headers.get("www-authenticate")).toBeNull();
    expect(refused.apiCalls).toHaveLength(0);
    const allowed = await start({ allowLive: true });
    expect((await allowed.post(LIVE, allowed.call("list_meters"))).status).toBe(200);
  });
});

describe("403 insufficient_scope", () => {
  it("names the scope a call needs, with the metadata address, and does not run the tool", async () => {
    const { post, call, apiCalls } = await start({ allowWrites: true });
    const r = await post(READER, call("create_customer", 1, { external_id: "c", name: "C", email: "c@example.com" }));
    expect(r.status).toBe(403);
    const h = r.headers.get("www-authenticate") ?? "";
    expect(h).toContain('error="insufficient_scope"');
    expect(h).toContain('scope="customers:write"');
    expect(h).toContain("resource_metadata=");
    expect(apiCalls).toHaveLength(0);
  });

  it("asks for every missing scope of a batch in one challenge, and lets a call the token covers through", async () => {
    const { post, call, apiCalls } = await start({ allowWrites: true });
    const batch = await post(READER, [call("create_customer", 1), call("ingest_usage_events", 2), call("get_customer", 3, { customer_external_id: "cus_1" })]);
    expect(batch.status).toBe(403);
    expect(batch.headers.get("www-authenticate")).toContain('scope="customers:write events:write"');
    const ok = await post(READER, call("get_customer", 1, { customer_external_id: "cus_1" }));
    expect(ok.status).toBe(200);
    expect(apiCalls).toHaveLength(1);
    // A read of something the token has no scope for is refused before the tool runs, too.
    const invoices = await post(READER, call("list_invoices", 4));
    expect(invoices.status).toBe(403);
    expect(invoices.headers.get("www-authenticate")).toContain('scope="invoices:read"');
  });

  it("does not offer the write tools unless the server was started with writes, whatever the token carries", async () => {
    const { connect } = await start();
    expect((await (await connect(GOOD)).listTools()).tools.map((t) => t.name)).not.toContain("create_customer");
  });
});

describe("the limit and the audit log", () => {
  it("counts calls per person and tenant: a second token of the same person shares the budget", async () => {
    const { post, call, tokens } = await start({ ratePerMinute: 6 }); // a burst of one
    tokens.set("qo_" + "B".repeat(43), info());
    const first = await (await post(GOOD, call("list_meters", 1))).json();
    expect(JSON.stringify(first)).not.toContain("rate_limited");
    const second = await (await post("qo_" + "B".repeat(43), call("list_meters", 2))).json();
    expect(JSON.stringify(second)).toContain("mcp.rate_limited");
  });

  it("gives another person, and the same person in another tenant, a budget of their own", async () => {
    const { post, call, tokens } = await start({ ratePerMinute: 6 });
    tokens.set("qo_" + "C".repeat(43), info({ sub: "person-2" }));
    tokens.set("qo_" + "D".repeat(43), info({ tenant_id: "tenant-2" }));
    await post(GOOD, call("list_meters", 1));
    for (const t of ["qo_" + "C".repeat(43), "qo_" + "D".repeat(43)]) {
      expect(JSON.stringify(await (await post(t, call("list_meters", 2))).json())).not.toContain("rate_limited");
    }
  });

  it("writes the person and the client to the audit log, never a token", async () => {
    const records: AuditRecord[] = [];
    const { post, call } = await start({ audit: (r) => records.push(r) });
    await post(GOOD, call("list_meters"));
    expect(records).toHaveLength(1);
    expect(records[0]).toMatchObject({ key: "oauth:person-1", client: "https://client.example/m.json", tool: "list_meters", ok: true });
    expect(JSON.stringify(records)).not.toContain(GOOD);
  });
});

describe("the scopes the tools name", () => {
  it("are permissions the API knows", async () => {
    const doc = parse(await readFile(join(repoRoot, "api/openapi/v1.yaml"), "utf8"));
    const known: string[] = doc.components.schemas.ApiKeyPermission.enum;
    for (const t of TOOLS) {
      if (t.scope) expect(known, `${t.name}: ${t.scope}`).toContain(t.scope);
      if (t.write) expect(t.scope, `${t.name} writes and must name its scope`).toBeDefined();
    }
    expect(TOOLS.filter((t) => !t.scope).map((t) => t.name)).toEqual([]);
  });
});
