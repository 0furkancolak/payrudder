import { describe, expect, it } from "vitest";
import { safeNext } from "../lib";
import { groupScopes, oauthParams } from "../oauth";

describe("the authorization request in the address", () => {
  it("reads the known parameters as text, so a numeric state stays what the client sent", () => {
    const p = oauthParams("?client_id=https%3A%2F%2Fa.example%2Fm.json&state=007&scope=customers%3Aread+invoices%3Aread&code_challenge_method=S256");
    expect(p).toEqual({ client_id: "https://a.example/m.json", state: "007", scope: "customers:read invoices:read", code_challenge_method: "S256" });
  });

  it("passes nothing else on", () => {
    expect(oauthParams("?client_id=x&next=/app&evil=1&app=all")).toEqual({ client_id: "x" });
  });
});

describe("the scopes shown", () => {
  it("groups reads and writes by resource, in the order of the API keys page, and leaves out what was not asked for", () => {
    expect(groupScopes(["invoices:read", "customers:write", "customers:read", "events:write"])).toEqual([
      { resource: "events", write: "events:write" },
      { resource: "customers", read: "customers:read", write: "customers:write" },
      { resource: "invoices", read: "invoices:read" },
    ]);
    expect(groupScopes([])).toEqual([]);
    expect(groupScopes(["not:ascope"])).toEqual([]);
  });
});

describe("returning to the consent page after signing in", () => {
  const long = "/oauth/authorize?" + "a=".padEnd(900, "x");
  it("keeps an authorization request longer than an ordinary return path", () => {
    expect(safeNext(long)).toBe(long);
    expect(safeNext("/app/" + "x".repeat(600))).toBe("");
  });

  it("still refuses what is not a path of this console", () => {
    expect(safeNext("//evil.example/oauth/authorize?x=1")).toBe("");
    expect(safeNext("/oauth/authorize?" + "x".repeat(4000))).toBe("");
  });
});
