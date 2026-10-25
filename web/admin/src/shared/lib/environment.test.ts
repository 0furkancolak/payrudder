import { describe, expect, it } from "vitest";
import { envEnum, envOf, findTenant, isEnvironment, tenantOf } from "./environment";

const apps = [
  { id: "a1", tenants: [{ id: "t-live", environment: "ENVIRONMENT_LIVE" }, { id: "t-sbx", environment: "ENVIRONMENT_SANDBOX" }] },
  { id: "a2", tenants: [{ id: "t2-live", environment: "ENVIRONMENT_LIVE" }] },
];

describe("environments", () => {
  it("maps the API's words both ways", () => {
    expect(envEnum("live")).toBe("ENVIRONMENT_LIVE");
    expect(envEnum("sandbox")).toBe("ENVIRONMENT_SANDBOX");
    expect(envOf("ENVIRONMENT_SANDBOX")).toBe("sandbox");
    expect(envOf("ENVIRONMENT_LIVE")).toBe("live");
    expect(envOf(undefined)).toBe("live");
    expect(isEnvironment("live") && isEnvironment("sandbox") && !isEnvironment("prod")).toBe(true);
  });

  it("finds the tenant of an environment in an app", () => {
    expect(tenantOf(apps[0], "live")).toBe("t-live");
    expect(tenantOf(apps[0], "sandbox")).toBe("t-sbx");
    expect(tenantOf(apps[1], "sandbox")).toBe("");
    expect(tenantOf(undefined, "live")).toBe("");
  });

  it("finds a tenant among the apps, with its app and environment", () => {
    const hit = findTenant(apps, "t-sbx");
    expect(hit?.app.id).toBe("a1");
    expect(hit?.env).toBe("sandbox");
    expect(findTenant(apps, "nope")).toBeNull();
    expect(findTenant(undefined, "t-live")).toBeNull();
  });
});
