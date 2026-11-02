import { describe, expect, it } from "vitest";
import { heldApps, holdOf, scheduledCandidate } from "./hold";

const app = (id: string, name: string, live: string, sandbox = "TENANT_STATUS_ACTIVE") => ({
  id,
  name,
  tenants: [
    { environment: "ENVIRONMENT_LIVE", status: live },
    { environment: "ENVIRONMENT_SANDBOX", status: sandbox },
  ],
});
const apps = [app("a", "Web shop", "TENANT_STATUS_SUSPENDED"), app("b", "Blog", "TENANT_STATUS_ACTIVE"), app("c", "Games", "TENANT_STATUS_BANNED"), app("d", "New", "TENANT_STATUS_INACTIVE"), app("e", "Mine", "TENANT_STATUS_FROZEN")];

describe("holdOf", () => {
  it("knows the three holds and nothing else", () => {
    expect(holdOf("TENANT_STATUS_SUSPENDED")).toBe("suspended");
    expect(holdOf("TENANT_STATUS_FROZEN")).toBe("frozen");
    expect(holdOf("TENANT_STATUS_BANNED")).toBe("banned");
    for (const s of ["TENANT_STATUS_ACTIVE", "TENANT_STATUS_INACTIVE", "TENANT_STATUS_UNSPECIFIED", "", undefined, "constructor"]) expect(holdOf(s)).toBeUndefined();
  });
});

describe("heldApps", () => {
  it("lists every held app for the scope all, or when the scope is not known yet", () => {
    expect(heldApps(apps, "all").map((a) => [a.name, a.hold])).toEqual([["Web shop", "suspended"], ["Games", "banned"], ["Mine", "frozen"]]);
    expect(heldApps(apps, null)).toHaveLength(3);
  });
  it("narrows to the app of the page", () => {
    expect(heldApps(apps, "a")).toEqual([{ id: "a", name: "Web shop", hold: "suspended" }]);
    expect(heldApps(apps, "b")).toEqual([]);
  });
  it("reads the live environment only", () => {
    expect(heldApps([app("x", "Sandbox only", "TENANT_STATUS_ACTIVE", "TENANT_STATUS_SUSPENDED")], "all")).toEqual([]);
    expect(heldApps([{ id: "y", name: "No tenants" }], "all")).toEqual([]);
    expect(heldApps(undefined, "all")).toEqual([]);
  });
});

describe("scheduledCandidate", () => {
  it("is the one app of the page when it is in service, and nothing for a held app, an inactive one, the scope all or an unknown app", () => {
    expect(scheduledCandidate(apps, "b")).toEqual({ id: "b", name: "Blog" });
    for (const scope of ["a", "d", "all", null, "nobody"]) expect(scheduledCandidate(apps, scope)).toBeNull();
    expect(scheduledCandidate(undefined, "b")).toBeNull();
  });
});
