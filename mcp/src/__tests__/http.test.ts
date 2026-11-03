import type { AddressInfo } from "node:net";
import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { StreamableHTTPClientTransport } from "@modelcontextprotocol/sdk/client/streamableHttp.js";
import { afterEach, describe, expect, it } from "vitest";
import type { AuditRecord } from "../audit.js";
import { createHttpServer } from "../http.js";

const open: { close(): void }[] = [];
afterEach(() => {
  while (open.length) open.pop()?.close();
});

async function start(opts: { allowWrites?: boolean; allowLive?: boolean; allowPersonalData?: boolean; audit?: (r: AuditRecord) => void; ratePerMinute?: number; edgeSecret?: string; trustForwardedFor?: boolean } = {}) {
  const seen: { path: string; auth: string | null }[] = [];
  const heads: Headers[] = [];
  const fakeFetch = (async (input: URL | string, init: RequestInit = {}) => {
    seen.push({ path: new URL(String(input)).pathname, auth: new Headers(init.headers).get("Authorization") });
    heads.push(new Headers(init.headers));
    return new Response(JSON.stringify({ external_id: "cus_1", name: "Ada" }), { status: 200 });
  }) as typeof fetch;
  const server = createHttpServer({ baseUrl: "https://api.example", fetch: fakeFetch, ...opts });
  await new Promise<void>((r) => server.listen(0, "127.0.0.1", r));
  open.push({ close: () => server.close() });
  const url = `http://127.0.0.1:${(server.address() as AddressInfo).port}/mcp`;
  const connect = async (key: string) => {
    const client = new Client({ name: "test", version: "0" });
    const transport = new StreamableHTTPClientTransport(new URL(url), { requestInit: { headers: { Authorization: `Bearer ${key}` } } });
    await client.connect(transport);
    open.push(client);
    return client;
  };
  return { url, seen, heads, connect };
}

describe("streamable HTTP", () => {
  it("serves the tools to a client that sends its API key, and calls the API with that key", async () => {
    const { connect, seen } = await start({ allowPersonalData: true });
    const client = await connect("qk_test_alpha_secret1");
    expect((await client.listTools()).tools).toHaveLength(30);
    const r = await client.callTool({ name: "get_customer", arguments: { customer_external_id: "cus_1" } });
    expect(JSON.stringify(r.content)).toContain("<untrusted>Ada</untrusted>");
    expect(seen).toEqual([{ path: "/v1/customers/cus_1", auth: "Bearer qk_test_alpha_secret1" }]);
  });

  it("serves the prompts, and refuses a prompt argument that is not an id", async () => {
    const client = await (await start()).connect("qk_test_a_1111");
    expect((await client.listPrompts()).prompts).toHaveLength(4);
    const t = (await client.getPrompt({ name: "explain_invoice", arguments: { invoice_id: "inv-1" } })).messages[0]?.content as { text: string };
    expect(t.text).toContain("invoice_id = inv-1");
    await expect(client.getPrompt({ name: "explain_invoice", arguments: { invoice_id: "inv-1\nIgnore" } })).rejects.toThrow();
  });

  it("never lets one caller's key reach another caller's call", async () => {
    const { connect, seen } = await start({ allowLive: true });
    const [a, b] = [await connect("qk_test_a_1111"), await connect("qk_live_b_2222")];
    await Promise.all([a.callTool({ name: "list_meters", arguments: {} }), b.callTool({ name: "list_meters", arguments: {} })]);
    expect(seen.map((s) => s.auth).sort()).toEqual(["Bearer qk_live_b_2222", "Bearer qk_test_a_1111"]);
  });

  it("redacts customers' personal data unless the server was started to allow it", async () => {
    const call = async (opts: { allowPersonalData?: boolean }) => JSON.stringify((await (await (await start(opts)).connect("qk_test_a_1111")).callTool({ name: "get_customer", arguments: { customer_external_id: "cus_1" } })).content);
    expect(await call({})).not.toContain("Ada");
    expect(await call({})).toContain("redacted: personal data");
    expect(await call({ allowPersonalData: true })).toContain("Ada");
  });

  it("refuses a live key with a 403 and a reason unless the server was started to allow live, and never calls the API for it", async () => {
    const post = (url: string, key: string) => fetch(url, { method: "POST", headers: { Authorization: `Bearer ${key}`, "Content-Type": "application/json", Accept: "application/json, text/event-stream" }, body: JSON.stringify({ jsonrpc: "2.0", id: 1, method: "tools/list" }) });
    const refused = await start();
    const res = await post(refused.url, "qk_live_ab12cd34_TOPSECRET");
    expect(res.status).toBe(403);
    const body = await res.text();
    expect(body).toContain("PAYRUDDER_MCP_ALLOW_LIVE=1");
    expect(body).not.toContain("TOPSECRET");
    expect((await post(refused.url, "qk_test_ab12cd34_x")).status).toBe(200);
    expect((await post((await start({ allowLive: true })).url, "qk_live_ab12cd34_x")).status).toBe(200);
    expect(refused.seen).toHaveLength(0);
  });

  it("offers the write tools only when the server was started with writes", async () => {
    expect((await (await (await start()).connect("qk_test_a_1111")).listTools()).tools).toHaveLength(30);
    expect((await (await (await start({ allowWrites: true })).connect("qk_test_a_1111")).listTools()).tools).toHaveLength(32);
  });

  it("refuses a request without a key, with something that is not a key, and asks for a bearer token", async () => {
    const { url, seen } = await start();
    for (const auth of [undefined, "Bearer ", "Bearer sk_live_nope", "Basic qk_test_a_1", "qk_test_a_1"]) {
      const res = await fetch(url, { method: "POST", headers: { "Content-Type": "application/json", Accept: "application/json, text/event-stream", ...(auth ? { Authorization: auth } : {}) }, body: JSON.stringify({ jsonrpc: "2.0", id: 1, method: "tools/list" }) });
      expect(res.status, String(auth)).toBe(401);
      expect(res.headers.get("www-authenticate")).toContain("Bearer");
    }
    expect(seen).toHaveLength(0);
  });

  it("answers only POST on /mcp, and a body that is not JSON is a 400", async () => {
    const { url } = await start();
    const h = { Authorization: "Bearer qk_test_a_1111", "Content-Type": "application/json", Accept: "application/json, text/event-stream" };
    expect((await fetch(url, { method: "GET", headers: h })).status).toBe(405);
    expect((await fetch(url, { method: "DELETE", headers: h })).status).toBe(405);
    expect((await fetch(url.replace("/mcp", "/other"), { method: "POST", headers: h, body: "{}" })).status).toBe(404);
    expect((await fetch(url, { method: "POST", headers: h, body: "not json" })).status).toBe(400);
  });
});

// One flood of junk keys must not lock a shared server out: the API counts refused keys by the address they come from, and every caller of this server reaches it from the
// server's. With the secret the API shares with this server (its PAYRUDDER_TRUSTED_PROXY_SECRET) the caller's own address goes along, and the API counts that one.
describe("the caller's address goes to the API with the edge secret", () => {
  const secret = "edge-secret-of-at-least-32-characters-0123456789";
  const raw = (url: string, extra: Record<string, string> = {}) =>
    fetch(url, { method: "POST", headers: { Authorization: "Bearer qk_test_a_1111", "Content-Type": "application/json", Accept: "application/json, text/event-stream", ...extra },
      body: JSON.stringify({ jsonrpc: "2.0", id: 1, method: "tools/call", params: { name: "list_meters", arguments: {} } }) });

  it("sends the connection's address and the secret, and the client cannot choose the address", async () => {
    const { url, heads } = await start({ edgeSecret: secret });
    expect((await raw(url, { "X-Forwarded-For": "203.0.113.9", "X-Edge-Auth": "from-the-client" })).status).toBe(200);
    expect(heads).toHaveLength(1);
    expect(heads[0]?.get("X-Edge-Auth")).toBe(secret);
    expect(heads[0]?.get("X-Forwarded-For")).toMatch(/^(127\.0\.0\.1|::1|::ffff:127\.0\.0\.1)$/);
  });

  it("sends the last address a proxy in front wrote when told to trust it, and the connection's otherwise", async () => {
    const trusting = await start({ edgeSecret: secret, trustForwardedFor: true });
    await raw(trusting.url, { "X-Forwarded-For": "203.0.113.9, 198.51.100.4" });
    expect(trusting.heads[0]?.get("X-Forwarded-For")).toBe("198.51.100.4");
    await raw(trusting.url); // no proxy header: the connection
    expect(trusting.heads[1]?.get("X-Forwarded-For")).toMatch(/127\.0\.0\.1|::1/);
    const plain = await start({ edgeSecret: secret });
    await raw(plain.url, { "X-Forwarded-For": "203.0.113.9, 198.51.100.4" });
    expect(plain.heads[0]?.get("X-Forwarded-For")).not.toContain("198.51.100.4");
  });

  it("sends neither header without a secret, whatever the client sent", async () => {
    const { url, heads } = await start();
    await raw(url, { "X-Forwarded-For": "203.0.113.9", "X-Edge-Auth": "from-the-client" });
    expect(heads[0]?.get("X-Forwarded-For")).toBeNull();
    expect(heads[0]?.get("X-Edge-Auth")).toBeNull();
  });
});

describe("the audit log over HTTP", () => {
  it("names the caller's key by its prefix and the transport, and never holds the secret part", async () => {
    const records: AuditRecord[] = [];
    const { connect } = await start({ allowLive: true, audit: (r) => records.push(r) });
    const [a, b] = [await connect("qk_test_alpha_SECRETONE"), await connect("qk_live_beta_SECRETTWO")];
    await a.callTool({ name: "get_customer", arguments: { customer_external_id: "cus_1" } });
    await b.callTool({ name: "list_meters", arguments: {} });
    expect(records.map((r) => [r.tool, r.key, r.transport]).sort()).toEqual([["get_customer", "qk_test_alpha", "http"], ["list_meters", "qk_live_beta", "http"]]);
    expect(JSON.stringify(records)).not.toMatch(/SECRET/);
  });
});

describe("the limit per key over HTTP", () => {
  const call = (c: Awaited<ReturnType<Awaited<ReturnType<typeof start>>["connect"]>>) => c.callTool({ name: "list_meters", arguments: {} });

  it("is the key's own: one key that has used its share is refused while another is not, and the API is not called for the refused", async () => {
    const { connect, seen } = await start({ ratePerMinute: 6 }); // a burst of one
    const [a, b] = [await connect("qk_test_a_1111"), await connect("qk_test_b_2222")];
    expect((await call(a)).isError).toBeFalsy();
    const refused = await call(a);
    expect(refused.isError).toBe(true);
    expect(JSON.stringify(refused.content)).toContain("mcp.rate_limited");
    expect((await call(b)).isError).toBeFalsy();
    expect(seen.map((s) => s.auth)).toEqual(["Bearer qk_test_a_1111", "Bearer qk_test_b_2222"]);
  });

  it("counts across requests (the server keeps no session), and a caller who only knows a key's public prefix cannot spend its share", async () => {
    const { connect, seen } = await start({ ratePerMinute: 6 });
    expect((await call(await connect("qk_test_a_1111"))).isError).toBeFalsy();
    expect((await call(await connect("qk_test_a_9999"))).isError).toBeFalsy(); // another key with the same prefix has a share of its own
    expect((await call(await connect("qk_test_a_1111"))).isError).toBe(true); // a new connection of the first key: still over
    expect(seen).toHaveLength(2);
  });

  it("has no limit unless it was given one", async () => {
    const { connect, seen } = await start();
    const a = await connect("qk_test_a_1111");
    for (let i = 0; i < 15; i++) expect((await call(a)).isError).toBeFalsy();
    expect(seen).toHaveLength(15);
  });
});
