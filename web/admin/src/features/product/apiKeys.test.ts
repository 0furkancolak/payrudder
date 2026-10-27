import { describe, expect, it } from "vitest";
import { ALL_PERMISSIONS, KEY_ROLES, keyRequest, permissionCount, permissionsOfRole, roleOfKey, type KeyPermission } from "./apiKeys";

describe("key roles", () => {
  it("the admin role holds every permission and read_only every read", () => {
    expect(permissionsOfRole("admin")).toEqual(ALL_PERMISSIONS);
    const ro = permissionsOfRole("read_only") ?? [];
    expect(ro.length).toBeGreaterThan(0);
    expect(ro.every((p) => p.endsWith(":read"))).toBe(true);
    expect(ALL_PERMISSIONS.filter((p) => p.endsWith(":read"))).toEqual(ro);
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
