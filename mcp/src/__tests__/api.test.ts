import { describe, expect, it } from "vitest";
import { createApi, seg } from "../api.js";
import { toolOf } from "../readTools.js";

describe("a path segment from a value the model supplied", () => {
  it("is encoded, so a slash or a question mark cannot reach another route", () => {
    expect(seg("cus_1")).toBe("cus_1");
    expect(seg("a/../b?x=1#y")).toBe("a%2F..%2Fb%3Fx%3D1%23y");
    expect(seg("%2e%2e")).toBe("%252e%252e");
  });

  it("is never a dot segment: encodeURIComponent leaves . and .. alone and a URL collapses them, so '..' would read the parent route", () => {
    expect(() => seg("..")).toThrow(/not an id/);
    expect(() => seg(".")).toThrow(/not an id/);
    expect(seg("...")).toBe("..."); // only a segment that is exactly . or .. moves a request
    expect(seg("a.b")).toBe("a.b");
  });

  it("stops a read tool before any request is made", async () => {
    const calls: string[] = [];
    const api = createApi({ baseUrl: "https://api.example.test", apiKey: "qk_test_x", fetch: (async (u: URL) => { calls.push(String(u)); return new Response("{}"); }) as typeof fetch });
    const tool = toolOf({ name: "get_customer", operationId: "getCustomer", path: "/v1/customers/{id}/invoices", title: "t", description: "d", params: [{ arg: "id", name: "id", in: "path", required: true, type: "string" }] });
    await expect((async () => tool.run({ id: ".." }, api))()).rejects.toThrow(/not an id/);
    expect(calls).toEqual([]);
    await tool.run({ id: "cus_1" }, api);
    expect(calls).toEqual(["https://api.example.test/v1/customers/cus_1/invoices"]);
  });
});
