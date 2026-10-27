import { afterEach, describe, expect, it } from "vitest";
import { clientFor, json, startServer, type FakeServer } from "./helpers";

let server: FakeServer | undefined;
afterEach(async () => {
  await server?.close();
  server = undefined;
});

async function collect<T>(it: AsyncIterable<T>): Promise<T[]> {
  const out: T[] = [];
  for await (const x of it) out.push(x);
  return out;
}

describe("pagination", () => {
  it("walks every page and yields items", async () => {
    server = await startServer((req, res) => {
      const token = new URL(req.url, "http://x").searchParams.get("page_token");
      if (!token) json(res, 200, { customers: [{ external_id: "a" }, { external_id: "b" }], next_page_token: "p2" });
      else if (token === "p2") json(res, 200, { customers: [{ external_id: "c" }], next_page_token: "p3" });
      else json(res, 200, { customers: [{ external_id: "d" }] });
    });
    const { client } = clientFor(server);
    const all = await collect(client.paginate.listCustomers({ page_size: 2 }));
    expect(all.map((c) => c.external_id)).toEqual(["a", "b", "c", "d"]);
    expect(server.requests.map((r) => r.url)).toEqual([
      "/v1/customers?page_size=2",
      "/v1/customers?page_size=2&page_token=p2",
      "/v1/customers?page_size=2&page_token=p3",
    ]);
  });

  it("starts from a given page token and supports early exit", async () => {
    server = await startServer((_req, res) => json(res, 200, { customers: [{ external_id: "a" }, { external_id: "b" }], next_page_token: "more" }));
    const { client } = clientFor(server);
    for await (const c of client.paginate.listCustomers({ page_token: "start" })) {
      expect(c.external_id).toBe("a");
      break;
    }
    expect(server.requests).toHaveLength(1);
    expect(server.requests[0]?.url).toBe("/v1/customers?page_token=start");
  });

  it("stops if the server repeats the token it was given", async () => {
    server = await startServer((_req, res) => json(res, 200, { customers: [{ external_id: "a" }], next_page_token: "same" }));
    const { client } = clientFor(server);
    const all = await collect(client.paginate.listCustomers({ page_token: "same" }));
    expect(all).toHaveLength(1);
  });

  it("handles operations with path parameters and a differently named array", async () => {
    server = await startServer((req, res) => {
      const token = new URL(req.url, "http://x").searchParams.get("page_token");
      if (!token) json(res, 200, { entries: [{ id: "1" }], next_page_token: "n" });
      else json(res, 200, { entries: [{ id: "2" }] });
    });
    const { client } = clientFor(server);
    const all = await collect(client.paginate.listCreditLedger("cus 1"));
    expect(all.map((e) => e.id)).toEqual(["1", "2"]);
    expect(server.requests[0]?.url).toBe("/v1/customers/cus%201/credits/ledger");
  });

  it("tolerates a page without the items array", async () => {
    server = await startServer((_req, res) => json(res, 200, {}));
    const { client } = clientFor(server);
    expect(await collect(client.paginate.listCustomers())).toEqual([]);
  });
});
