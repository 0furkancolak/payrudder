import { QueryClient } from "@tanstack/vue-query";
import { afterEach, describe, expect, it, vi } from "vitest";

afterEach(() => vi.unstubAllGlobals());

describe("leaving an account", () => {
  it("forgets the chosen account and marks what was cached for the old one as stale", async () => {
    const store = new Map<string, string>([["payrudder.account", "acc-A"]]);
    vi.stubGlobal("window", {
      location: { pathname: "/app/members" },
      sessionStorage: { getItem: (k: string) => store.get(k) ?? null, setItem: (k: string, v: string) => void store.set(k, v), removeItem: (k: string) => void store.delete(k) },
    });
    const { leaveAccount, productKeys } = await import("./api");
    const { activeAccount } = await import("@/shared/lib/session");
    const queries = new QueryClient();
    queries.setQueryData(productKeys.apps, { apps: [{ id: "app-of-A" }] });
    queries.setQueryData(["tenants", "t1"], { name: "not an app query" });

    leaveAccount(queries);

    expect(activeAccount.value).toBeNull();
    expect(queries.getQueryState(productKeys.apps)?.isInvalidated).toBe(true);
    expect(queries.getQueryState(["tenants", "t1"])?.isInvalidated).toBe(false);
  });
});
