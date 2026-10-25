import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

// The session module reads the tab's storage when it loads, so each test gives it a fresh fake window and a fresh copy of the module.
function fakeWindow(pathname = "/app") {
  const store = new Map<string, string>();
  vi.stubGlobal("window", {
    location: { pathname },
    sessionStorage: {
      getItem: (k: string) => store.get(k) ?? null,
      setItem: (k: string, v: string) => void store.set(k, v),
      removeItem: (k: string) => void store.delete(k),
    },
  });
  return store;
}

beforeEach(() => vi.resetModules());
afterEach(() => vi.unstubAllGlobals());

describe("the session of this tab", () => {
  it("is an operator only while signed in and only when the server said so", async () => {
    fakeWindow();
    const s = await import("./session");
    expect(s.isPerson.value).toBe(false);
    expect(s.isOperator.value).toBe(false);
    s.setOperatorFlag(true); // a flag without a session opens nothing
    expect(s.isOperator.value).toBe(false);
    s.setPersonToken("qs_abc", true);
    expect(s.isPerson.value).toBe(true);
    expect(s.isOperator.value).toBe(true);
    expect(s.currentToken()).toBe("qs_abc");
  });

  it("forgets the operator flag at sign-out and at a lost session", async () => {
    const store = fakeWindow();
    const s = await import("./session");
    s.setPersonToken("qs_abc", true);
    s.sessionLost();
    expect(s.isOperator.value).toBe(false);
    expect(s.currentToken()).toBeNull();
    expect(store.has("payrudder.sessionToken")).toBe(false);
    expect(store.has("payrudder.operator")).toBe(false);
  });

  it("starts as a plain person unless told otherwise, and follows the server's later verdict", async () => {
    fakeWindow();
    const s = await import("./session");
    s.setPersonToken("qs_abc");
    expect(s.isOperator.value).toBe(false);
    s.setOperatorFlag(true);
    expect(s.isOperator.value).toBe(true);
    s.setOperatorFlag(false);
    expect(s.isOperator.value).toBe(false);
  });

  it("keeps no operator token at all: the only credential is the session token", async () => {
    fakeWindow();
    const s = await import("./session");
    expect(Object.keys(s)).not.toContain("operatorToken");
    expect(Object.keys(s)).not.toContain("setOperatorToken");
  });

  it("puts the tenant pages under /app/t inside the product and under /tenants in the operator console", async () => {
    fakeWindow("/app/t/abc/customers");
    expect((await import("./session")).tenantBase()).toBe("/app/t");
    vi.resetModules();
    fakeWindow("/tenants/abc/customers");
    expect((await import("./session")).tenantBase()).toBe("/tenants");
  });
});
