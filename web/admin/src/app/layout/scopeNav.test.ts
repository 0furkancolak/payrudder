import { describe, expect, it } from "vitest";
import { envFor, envTarget, scopeTarget } from "./scopeNav";

const tenants = (live?: string, sandbox?: string) => [
  ...(live ? [{ id: live, environment: "ENVIRONMENT_LIVE", status: "TENANT_STATUS_ACTIVE" }] : []),
  ...(sandbox ? [{ id: sandbox, environment: "ENVIRONMENT_SANDBOX" }] : []),
];
const apps = [
  { id: "A", tenants: tenants("AL", "AS") },
  { id: "B", tenants: tenants(undefined, "BS") },
];

describe("envFor", () => {
  it("keeps the wanted environment when the app has it, else the one it has", () => {
    expect(envFor(apps[0], "live")).toBe("live");
    expect(envFor(apps[1], "live")).toBe("sandbox");
    expect(envFor(undefined, "sandbox")).toBe("sandbox");
  });
});

describe("scopeTarget", () => {
  it("stays on the same section of a tenant page in the other app", () => {
    expect(scopeTarget("/app/t/AL/customers", "B", apps, "live")).toEqual({ to: "/app/t/BS/customers", search: { app: "B" } });
    expect(scopeTarget("/app/t/AL/invoices/42", "A", apps, "live")).toEqual({ to: "/app/t/AL/invoices", search: { app: "A" } });
  });
  it("goes to every app under /app/t/all, and back to one", () => {
    expect(scopeTarget("/app/t/AL/customers", "all", apps, "live")).toEqual({ to: "/app/t/all/customers", search: { app: "all", env: "live" } });
    expect(scopeTarget("/app/t/all/customers", "A", apps, "live")).toEqual({ to: "/app/t/AL/customers", search: { app: "A" } });
  });
  it("keeps an app's own page for another app, and has no all-apps version of it", () => {
    expect(scopeTarget("/app/apps/A/keys", "B", apps, "live")).toEqual({ to: "/app/apps/B/keys", search: { app: "B", env: "sandbox" } });
    expect(scopeTarget("/app/apps/A/keys", "all", apps, "live")).toEqual({ to: "/app", search: { app: "all", env: "live" } });
  });
  it("the home is the dashboard of the app, or of all apps", () => {
    expect(scopeTarget("/app", "B", apps, "live")).toEqual({ to: "/app/apps/B", search: { app: "B", env: "sandbox" } });
    expect(scopeTarget("/app", "all", apps, "sandbox")).toEqual({ to: "/app", search: { app: "all", env: "sandbox" } });
  });
  it("the organisation's pages stay and only the scope changes", () => {
    expect(scopeTarget("/app/members", "B", apps, "live")).toEqual({ to: "/app/members", search: { app: "B", env: "sandbox" } });
  });
});

describe("envTarget", () => {
  it("moves a tenant page to the other tenant of the same app", () => {
    expect(envTarget("/app/t/AL/payments", "A", apps[0], "sandbox")).toEqual({ to: "/app/t/AS/payments", search: { app: "A" } });
  });
  it("is nothing where the app has no such environment", () => {
    expect(envTarget("/app/t/BS/payments", "B", apps[1], "live")).toBeUndefined();
  });
  it("changes only the environment of the other pages", () => {
    expect(envTarget("/app/apps/A", "A", apps[0], "sandbox")).toEqual({ to: "/app/apps/A", search: { app: "A", env: "sandbox" } });
    expect(envTarget("/app/t/all/customers", "all", undefined, "sandbox")).toEqual({ to: "/app/t/all/customers", search: { app: "all", env: "sandbox" } });
  });
});
