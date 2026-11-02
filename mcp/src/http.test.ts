import type { AddressInfo } from "node:net";
import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { StreamableHTTPClientTransport } from "@modelcontextprotocol/sdk/client/streamableHttp.js";
import { afterEach, describe, expect, it } from "vitest";
import type { AuditRecord } from "./audit.js";
import { createHttpServer } from "./http.js";

const open: { close(): void }[] = [];
afterEach(() => {
  while (open.length) open.pop()?.close();
});

async function start(opts: { allowWrites?: boolean; allowLive?: boolean; allowPersonalData?: boolean; audit?: (r: AuditRecord) => void } = {}) {
  const seen: { path: string; auth: string | null }[] = [];
  const fakeFetch = (async (input: URL | string, init: RequestInit = {}) => {
    seen.push({ path: new URL(String(input)).pathname, auth: new Headers(init.headers).get("Authorization") });
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
  return { url, seen, connect };
}

describe("streamable HTTP", () => {
  it("serves the tools to a client that sends its API key, and calls the API with that key", async () => {
    const { connect, seen } = await start({ allowPersonalData: true });
    const client = await connect("qk_test_alpha_secret1");
    expect((await client.listTools()).tools).toHaveLength(25);
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
    expect((await (await (await start()).connect("qk_test_a_1111")).listTools()).tools).toHaveLength(25);
    expect((await (await (await start({ allowWrites: true })).connect("qk_test_a_1111")).listTools()).tools).toHaveLength(27);
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
