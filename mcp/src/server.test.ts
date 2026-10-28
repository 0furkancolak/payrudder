import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { InMemoryTransport } from "@modelcontextprotocol/sdk/inMemory.js";
import { describe, expect, it } from "vitest";
import { createApi } from "./api.js";
import { MAX_REPLY, createServer } from "./server.js";

interface Seen {
  method: string;
  url: string;
  auth: string | null;
  body: string | null;
}

async function connect(opts: { allowWrites?: boolean; answer?: (u: URL, init: RequestInit) => Response }) {
  const seen: Seen[] = [];
  const fakeFetch = (async (input: URL | string, init: RequestInit = {}) => {
    const url = new URL(String(input));
    seen.push({ method: init.method ?? "GET", url: url.pathname + url.search, auth: new Headers(init.headers).get("Authorization"), body: (init.body as string | undefined) ?? null });
    return opts.answer ? opts.answer(url, init) : new Response(JSON.stringify({ ok: true }), { status: 200 });
  }) as typeof fetch;
  const server = createServer({ api: createApi({ baseUrl: "https://api.example/", apiKey: "qk_test_SECRET", fetch: fakeFetch }), allowWrites: opts.allowWrites });
  const [a, b] = InMemoryTransport.createLinkedPair();
  const client = new Client({ name: "test", version: "0" });
  await Promise.all([server.connect(a), client.connect(b)]);
  return { client, seen };
}
const text = (r: { content?: unknown }) => ((r.content as { type: string; text: string }[])[0] as { text: string }).text;

describe("tools offered", () => {
  it("offers only the read tools by default, each marked read-only", async () => {
    const { client } = await connect({});
    const { tools } = await client.listTools();
    expect(tools.map((t) => t.name).sort()).toEqual(["get_customer", "get_customer_usage", "get_invoice", "list_customers", "list_invoices", "list_meters", "list_payments", "list_plans", "list_subscriptions"]);
    expect(tools.every((t) => t.annotations?.readOnlyHint === true)).toBe(true);
  });

  it("offers the write tools only when writes are turned on, and says they write", async () => {
    const { client } = await connect({ allowWrites: true });
    const { tools } = await client.listTools();
    const writes = tools.filter((t) => t.annotations?.readOnlyHint === false);
    expect(writes.map((t) => t.name).sort()).toEqual(["create_customer", "ingest_usage_events"]);
    expect(writes.every((t) => t.description?.startsWith("WRITES."))).toBe(true);
  });

  it("refuses a write tool that was not offered", async () => {
    const { client, seen } = await connect({});
    const r = await client.callTool({ name: "create_customer", arguments: { external_id: "x", name: "X", email: "x@example.com" } });
    expect(r.isError).toBe(true);
    expect(seen).toHaveLength(0);
  });
});

describe("calls", () => {
  it("sends the key as a bearer token and the arguments as the API's query", async () => {
    const { client, seen } = await connect({});
    await client.callTool({ name: "list_invoices", arguments: { customer_external_id: "cus_42", status: "paid", page_size: 10 } });
    expect(seen[0]).toMatchObject({ method: "GET", auth: "Bearer qk_test_SECRET" });
    expect(seen[0]?.url).toBe("/v1/invoices?customer=cus_42&status=paid&page_size=10");
  });

  it("encodes a customer id, so it cannot reach another route", async () => {
    const { client, seen } = await connect({});
    await client.callTool({ name: "get_customer", arguments: { customer_external_id: "../admin/v1/tenants?x=1" } });
    expect(seen[0]?.url).toBe("/v1/customers/..%2Fadmin%2Fv1%2Ftenants%3Fx%3D1");
  });

  it("validates arguments before any request: a bad month or status is refused", async () => {
    const { client, seen } = await connect({});
    expect((await client.callTool({ name: "get_customer_usage", arguments: { customer_external_id: "c", period: "2026-13" } })).isError).toBe(true);
    expect((await client.callTool({ name: "list_invoices", arguments: { status: "nope" } })).isError).toBe(true);
    expect(seen).toHaveLength(0);
  });

  it("posts a created customer and validated usage events when writes are on", async () => {
    const { client, seen } = await connect({ allowWrites: true });
    await client.callTool({ name: "create_customer", arguments: { external_id: "cus_1", name: "Ada", email: "ada@example.com" } });
    expect(seen[0]).toMatchObject({ method: "POST" });
    expect(seen[0]?.url).toBe("/v1/customers");
    expect(JSON.parse(seen[0]?.body ?? "{}")).toEqual({ external_id: "cus_1", name: "Ada", email: "ada@example.com" });
    const bad = await client.callTool({ name: "ingest_usage_events", arguments: { events: [{ customer_external_id: "c", meter_code: "m", quantity: "-1", event_time: "2026-11-03T10:15:00Z" }] } });
    expect(bad.isError).toBe(true);
    const ok = await client.callTool({ name: "ingest_usage_events", arguments: { events: [{ customer_external_id: "c", meter_code: "m", quantity: "1.5", event_time: "2026-11-03T10:15:00Z", idempotency_key: "k1" }] } });
    expect(ok.isError).toBeFalsy();
    expect(seen.at(-1)?.url).toBe("/v1/events");
  });
});

describe("answers", () => {
  it("returns the API's error key and message as an error result", async () => {
    const { client } = await connect({ answer: () => new Response(JSON.stringify({ error: { key: "plans.customer.not_found", message: "customer not found" } }), { status: 404 }) });
    const r = await client.callTool({ name: "get_customer", arguments: { customer_external_id: "nobody" } });
    expect(r.isError).toBe(true);
    expect(text(r)).toBe("plans.customer.not_found: customer not found (HTTP 404)");
  });

  it("explains a network failure without the key", async () => {
    const { client } = await connect({ answer: () => { throw new TypeError("fetch failed"); } });
    const r = await client.callTool({ name: "list_meters", arguments: {} });
    expect(r.isError).toBe(true);
    expect(text(r)).not.toContain("qk_test_SECRET");
  });

  it("cuts a very long answer and says so", async () => {
    const big = { items: "x".repeat(MAX_REPLY * 2) };
    const { client } = await connect({ answer: () => new Response(JSON.stringify(big), { status: 200 }) });
    const r = await client.callTool({ name: "list_plans", arguments: {} });
    expect(text(r).length).toBeLessThan(MAX_REPLY + 300);
    expect(text(r)).toContain("cut:");
  });
});
