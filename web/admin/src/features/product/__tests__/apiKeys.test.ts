import { describe, expect, it } from "vitest";
import { ALL_PERMISSIONS, DEFAULT_KEY_ROLE, KEY_RESOURCES, KEY_ROLES, keyRequest, parseRatePercent, permissionCount, permissionsOfRole, ratePercentOf, roleOfKey, type KeyPermission } from "../apiKeys";

describe("key roles", () => {
  it("a new key starts on a role that can only read", () => {
    expect(DEFAULT_KEY_ROLE).not.toBe("admin");
    expect((permissionsOfRole(DEFAULT_KEY_ROLE) ?? []).every((p) => p.endsWith(":read"))).toBe(true);
  });

  it("the admin role holds every permission and read_only every read except the audit log's", () => {
    expect(permissionsOfRole("admin")).toEqual(ALL_PERMISSIONS);
    const ro = permissionsOfRole("read_only") ?? [];
    expect(ro.length).toBeGreaterThan(0);
    expect(ro.every((p) => p.endsWith(":read"))).toBe(true);
    expect(ALL_PERMISSIONS.filter((p) => p.endsWith(":read") && p !== "audit:read")).toEqual(ro);
  });

  it("audit:read is a permission of the admin role and of a custom key, and the read_only role (the default) does not hold it", () => {
    expect(ALL_PERMISSIONS).toContain("audit:read");
    expect(KEY_RESOURCES.find((r) => r.resource === "audit")).toEqual({ resource: "audit", read: "audit:read" });
    expect(permissionsOfRole("admin")).toContain("audit:read");
    expect(permissionsOfRole("read_only")).not.toContain("audit:read");
    expect(permissionsOfRole(DEFAULT_KEY_ROLE)).not.toContain("audit:read");
    for (const r of ["billing", "ingest_only"] as const) expect(permissionsOfRole(r)).not.toContain("audit:read");
    expect(keyRequest("custom", new Set<KeyPermission>(["audit:read"]))).toEqual({ role: "custom", permissions: ["audit:read"] });
  });

  it("ingest_only sends events and nothing else, and custom has no fixed set", () => {
    expect(permissionsOfRole("ingest_only")).toEqual(["events:write"]);
    expect(permissionsOfRole("custom")).toBeNull();
  });

  it("every fixed role is made of known permissions, and billing cannot edit the catalogue or webhooks", () => {
    for (const r of KEY_ROLES) for (const p of permissionsOfRole(r) ?? []) expect(ALL_PERMISSIONS).toContain(p);
    const billing = permissionsOfRole("billing") ?? [];
    expect(billing).toContain("invoices:write");
    expect(billing).not.toContain("catalog:write");
    expect(billing).not.toContain("webhooks:read");
  });

  it("the permissions are listed once each, in a stable order", () => {
    expect(new Set(ALL_PERMISSIONS).size).toBe(ALL_PERMISSIONS.length);
    expect(ALL_PERMISSIONS[0]).toBe("events:write");
  });
});

describe("keyRequest", () => {
  it("a fixed role names itself only", () => {
    expect(keyRequest("billing", new Set(["customers:read"] as KeyPermission[]))).toEqual({ role: "billing" });
  });
  it("a custom key lists its permissions in the gateway's order", () => {
    expect(keyRequest("custom", new Set<KeyPermission>(["invoices:read", "customers:read"]))).toEqual({ role: "custom", permissions: ["customers:read", "invoices:read"] });
  });
  it("a custom key with nothing chosen is not a request", () => {
    expect(keyRequest("custom", new Set())).toBeNull();
  });
});

describe("a key's own rate limit", () => {
  it("empty means none, 1 to 100 is a share of the plan", () => {
    expect(parseRatePercent("")).toEqual({ ok: true, value: undefined });
    expect(parseRatePercent("  ")).toEqual({ ok: true, value: undefined });
    expect(parseRatePercent(null)).toEqual({ ok: true, value: undefined });
    expect(parseRatePercent("1")).toEqual({ ok: true, value: 1 });
    expect(parseRatePercent(" 100 ")).toEqual({ ok: true, value: 100 });
    expect(parseRatePercent(25)).toEqual({ ok: true, value: 25 });
  });
  it("0, 101, fractions, signs and text are refused", () => {
    for (const bad of ["0", "101", "1000", "-5", "2.5", "1e2", "abc", "50%", 0, 101]) expect(parseRatePercent(bad), String(bad)).toEqual({ ok: false });
  });
  it("a key from the API without a percentage (0, null, absent) has none", () => {
    expect(ratePercentOf({})).toBe(0);
    expect(ratePercentOf({ rate_percent: null })).toBe(0);
    expect(ratePercentOf({ rate_percent: 0 })).toBe(0);
    expect(ratePercentOf({ rate_percent: 30 })).toBe(30);
  });
});

describe("reading a key", () => {
  it("falls back to the scope for a key from before roles", () => {
    expect(roleOfKey({ scope: "SCOPE_INGEST" })).toBe("ingest_only");
    expect(roleOfKey({ scope: "SCOPE_READ" })).toBe("read_only");
    expect(roleOfKey({ scope: "SCOPE_FULL" })).toBe("admin");
    expect(roleOfKey({ role: "billing", scope: "SCOPE_FULL" })).toBe("billing");
    expect(roleOfKey({ role: "nonsense" })).toBe("admin");
  });
  it("counts what a key holds", () => {
    expect(permissionCount({ role: "admin", permissions: [] })).toBe(ALL_PERMISSIONS.length);
    expect(permissionCount({ role: "custom", permissions: ["customers:read", "invoices:read"] })).toBe(2);
    expect(permissionCount({ role: "custom" })).toBe(0);
  });
});
