import { afterEach, describe, expect, it, vi } from "vitest";
import { Payrudder, VERSION } from "../src/index";
import { clientFor, json, startServer, type FakeServer } from "./helpers";

let server: FakeServer | undefined;
afterEach(async () => {
  vi.unstubAllEnvs();
  await server?.close();
  server = undefined;
});

describe("configuration", () => {
  it("requires an api key", () => {
    vi.stubEnv("PAYRUDDER_API_KEY", "");
    expect(() => new Payrudder()).toThrow(/apiKey is required/);
  });

  it("reads the key and base url from the environment", async () => {
    server = await startServer((_req, res) => json(res, 200, { id: "c1" }));
    vi.stubEnv("PAYRUDDER_API_KEY", "qk_test_env");
    vi.stubEnv("PAYRUDDER_BASE_URL", server.url + "/");
    await new Payrudder().customers.getCustomer("c1");
    expect(server.requests[0]?.headers.authorization).toBe("Bearer qk_test_env");
    expect(server.requests[0]?.url).toBe("/v1/customers/c1");
  });

  it("prefers explicit options over the environment", async () => {
    server = await startServer((_req, res) => json(res, 200, {}));
    vi.stubEnv("PAYRUDDER_API_KEY", "qk_test_env");
    vi.stubEnv("PAYRUDDER_BASE_URL", "http://127.0.0.1:1");
    await new Payrudder({ apiKey: "qk_live_x", baseUrl: server.url }).customers.getCustomer("c1");
    expect(server.requests[0]?.headers.authorization).toBe("Bearer qk_live_x");
  });

  it("defaults the base url to the local gateway", async () => {
    const seen: string[] = [];
    const client = new Payrudder({
      apiKey: "k",
      fetch: (input) => {
        seen.push(typeof input === "string" ? input : input instanceof URL ? input.href : input.url);
        return Promise.resolve(new Response("{}", { status: 200 }));
      },
    });
    await client.customers.getCustomer("c1");
    expect(seen[0]).toBe("http://localhost:42800/v1/customers/c1");
  });

  it("accepts an injected fetch", async () => {
    const fetchMock = vi.fn(() => Promise.resolve(new Response(JSON.stringify({ id: "c1" }), { status: 200 })));
    const client = new Payrudder({ apiKey: "k", fetch: fetchMock });
    expect(await client.customers.getCustomer("c1")).toEqual({ id: "c1" });
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("rejects nonsense limits", () => {
    expect(() => new Payrudder({ apiKey: "k", maxRetries: -1 })).toThrow(/maxRetries/);
    expect(() => new Payrudder({ apiKey: "k", timeout: 0 })).toThrow(/timeout/);
  });
});

describe("requests", () => {
  it("sends auth, user agent and JSON headers", async () => {
    server = await startServer((_req, res) => json(res, 201, { id: "c1" }));
    const { client } = clientFor(server);
    await client.customers.createCustomer({ external_id: "cus_1", name: "Ada", email: "ada@example.com" });
    const req = server.requests[0];
    expect(req?.method).toBe("POST");
    expect(req?.url).toBe("/v1/customers");
    expect(req?.headers.authorization).toBe("Bearer qk_test_abcd1234_secret");
    expect(req?.headers["user-agent"]).toBe(`payrudder-typescript/${VERSION}`);
    expect(req?.headers["content-type"]).toBe("application/json");
    expect(JSON.parse(req?.body ?? "")).toEqual({ external_id: "cus_1", name: "Ada", email: "ada@example.com" });
  });

  it("encodes path segments and serialises queries (dates, arrays, undefined)", async () => {
    server = await startServer((_req, res) => json(res, 200, { entitlements: [] }));
    const { client } = clientFor(server);
    await client.customers.getEntitlement("a/b c", "tokens", { period: "2026-11" });
    await client.customers.listCustomers({ page_size: 2, page_token: undefined });
    expect(server.requests[0]?.url).toBe("/v1/customers/a%2Fb%20c/entitlements/tokens?period=2026-11");
    expect(server.requests[1]?.url).toBe("/v1/customers?page_size=2");
  });

  it("writes int64 bigints as JSON strings and never as numbers", async () => {
    server = await startServer((_req, res) => json(res, 200, { credits: "9007199254740993" }));
    const { client } = clientFor(server);
    const res = await client.customers.grantCredits("cus_1", { credits: 9007199254740993n, source: "manual" });
    expect(server.requests[0]?.body).toContain('"credits":"9007199254740993"');
    expect(res).toEqual({ credits: "9007199254740993" }); // string preserved, not a lossy number
  });

  it("returns undefined for 204 and text for CSV", async () => {
    server = await startServer((req, res) => {
      if (req.url.startsWith("/v1/expense-rules")) {
        res.writeHead(204);
        res.end();
      } else {
        res.writeHead(200, { "Content-Type": "text/csv" });
        res.end("a,b\n1,2\n");
      }
    });
    const { client } = clientFor(server);
    expect(await client.expenseRules.deleteExpenseRule("0192")).toBeUndefined();
    expect(await client.accounting.exportAccountingCsv({ from: "2026-01-01", to: "2026-02-01" })).toBe("a,b\n1,2\n");
  });

  it("returns bytes for binary downloads", async () => {
    server = await startServer((_req, res) => {
      res.writeHead(200, { "Content-Type": "application/octet-stream" });
      res.end(Buffer.from([1, 2, 3]));
    });
    const { client } = clientFor(server);
    expect(await client.expenseInbox.getInboxItemFile("0192")).toEqual(new Uint8Array([1, 2, 3]));
  });

  it("uploads multipart bodies without forcing a content type", async () => {
    server = await startServer((_req, res) => json(res, 200, { results: [] }));
    const { client } = clientFor(server);
    const form = new FormData();
    form.append("file", new Blob(["hello"]), "a.txt");
    await client.assets.uploadAssets(form);
    expect(server.requests[0]?.headers["content-type"]).toMatch(/^multipart\/form-data; boundary=/);
    expect(server.requests[0]?.body).toContain("hello");
  });

  it("passes extra headers and an idempotency key from the call options", async () => {
    server = await startServer((_req, res) => json(res, 201, {}));
    const { client } = clientFor(server);
    await client.customers.createCustomer({ external_id: "c", name: "C", email: "c@example.com" }, { idempotencyKey: "k-1", headers: { "X-Payrudder-Actor": "ops" } });
    expect(server.requests[0]?.headers["idempotency-key"]).toBe("k-1");
    expect(server.requests[0]?.headers["x-payrudder-actor"]).toBe("ops");
  });

  it("aborts with the caller's signal and does not retry", async () => {
    server = await startServer(() => undefined); // never answers
    const { client } = clientFor(server);
    const ac = new AbortController();
    const p = client.customers.getCustomer("c1", { signal: ac.signal });
    const srv = server;
    await vi.waitFor(() => expect(srv.requests).toHaveLength(1)); // abort only once the request is in flight
    ac.abort();
    await expect(p).rejects.toMatchObject({ name: "ConnectionError", message: "Request aborted" });
    expect(server.requests).toHaveLength(1);
  });
});
