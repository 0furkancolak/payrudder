import { describe, expect, it } from "vitest";
import { ACCOUNT_LABEL, accountKey, accountRows, appRows, connectionOf, helpKey, orgSummary, splitAccountKey, summarize, targetsOf, type AppRow } from "./connections";

const live = (id: string): AppRow => ({ id, name: `App ${id}`, hasLive: true });
const sandboxOnly: AppRow = { id: "s", name: "Sandbox only", hasLive: false };

describe("connectionOf", () => {
  it("reads a provider that is not among the accounts as not connected", () => {
    expect(connectionOf([], "paddle").state).toBe("none");
    expect(connectionOf(undefined, "paddle").state).toBe("none");
  });
  it("tells a working account from one with a problem, and words the problem", () => {
    expect(connectionOf([{ provider: "paddle", working: true, checked: true, default: true }], "paddle")).toEqual({ state: "working", problem: null, isDefault: true, checked: true });
    expect(connectionOf([{ provider: "iyzico", working: false, problem: "unreachable" }], "iyzico").problem).toBe("unreachable");
    // A problem the console does not know reads as the provider refusing, and a missing one on a broken account too.
    expect(connectionOf([{ provider: "iyzico", working: false, problem: "weird" }], "iyzico").problem).toBe("refused");
    expect(connectionOf([{ provider: "iyzico", working: false }], "iyzico").problem).toBe("refused");
  });
});

describe("appRows and targetsOf", () => {
  it("knows which apps have a live environment", () => {
    const rows = appRows([{ id: "a", name: "A", tenants: [{ id: "t1", environment: "ENVIRONMENT_LIVE" }] }, { id: "b", name: "B", tenants: [{ id: "t2", environment: "ENVIRONMENT_SANDBOX" }] }, { name: "no id" }]);
    expect(rows).toEqual([{ id: "a", name: "A", hasLive: true }, { id: "b", name: "B", hasLive: false }]);
  });
  it("reaches one app, or every app with a live environment", () => {
    const apps = [live("a"), live("b"), sandboxOnly];
    expect(targetsOf("app", "b", apps).map((a) => a.id)).toEqual(["b"]);
    expect(targetsOf("org", "b", apps).map((a) => a.id)).toEqual(["a", "b"]);
    expect(targetsOf("app", "zzz", apps)).toEqual([]);
  });
});

describe("orgSummary", () => {
  const rows = [
    { app: live("a"), accounts: [{ provider: "paddle", working: true }] },
    { app: live("b"), accounts: [{ provider: "paddle", working: false, problem: "refused" }] },
    { app: live("c"), accounts: [] },
    { app: sandboxOnly, accounts: [{ provider: "paddle", working: true }] },
  ];
  it("counts only apps with a live environment, and a problem anywhere is a problem", () => {
    expect(orgSummary(rows, "paddle")).toEqual({ apps: 3, connected: 2, working: 1, problems: 1, state: "problem" });
  });
  it("is working when every connected account works and none when nothing is connected", () => {
    expect(orgSummary(rows.slice(0, 1), "paddle").state).toBe("working");
    expect(orgSummary(rows, "iyzico")).toEqual({ apps: 3, connected: 0, working: 0, problems: 0, state: "none" });
  });
});

describe("summarize", () => {
  it("names the apps that failed", () => {
    expect(summarize([{ appId: "a", name: "A", ok: true }, { appId: "b", name: "B", ok: false, error: new Error("x") }])).toEqual({ ok: 1, failed: 1, failedApps: ["B"] });
  });
});

describe("helpKey", () => {
  it("gives help only for the fields that need it", () => {
    expect(helpKey("iyzico", "secret_key")).toBe("product.providers.help.iyzico.secret_key");
    expect(helpKey("iyzico", "buyer.ip")).toBe("product.providers.help.iyzico.buyer_ip");
    expect(helpKey("iyzico", "buyer.city")).toBeNull();
  });
});

describe("accounts of one provider", () => {
  it("joins and splits the key", () => {
    expect(accountKey("paddle", "")).toBe("paddle");
    expect(accountKey("paddle", "eu")).toBe("paddle~eu");
    expect(splitAccountKey("paddle~eu")).toEqual({ provider: "paddle", label: "eu" });
    expect(splitAccountKey("iyzico")).toEqual({ provider: "iyzico", label: "" });
  });

  it("accepts the labels the server accepts", () => {
    for (const ok of ["eu", "a", "tr-istanbul", "2024"]) expect(ACCOUNT_LABEL.test(ok)).toBe(true);
    for (const bad of ["", "EU", "-eu", "eu west", "a~b", "x".repeat(32)]) expect(ACCOUNT_LABEL.test(bad)).toBe(false);
  });

  it("lists the primary account before the further ones and keeps the default", () => {
    const rows = accountRows([
      { provider: "paddle~uk", working: true },
      { provider: "paddle", working: true, default: true },
      { provider: "iyzico~ist", working: false, problem: "refused" },
    ]);
    expect(rows.map((r) => r.key)).toEqual(["iyzico~ist", "paddle", "paddle~uk"]);
    expect(rows.find((r) => r.key === "paddle")?.isDefault).toBe(true);
    expect(rows.find((r) => r.key === "iyzico~ist")?.state).toBe("problem");
  });
});
