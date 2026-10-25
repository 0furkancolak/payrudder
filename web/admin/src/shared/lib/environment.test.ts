import { describe, expect, it, vi } from "vitest";
import { defaultEnv, defaultOrgEnv, envEnum, envOf, findTenant, isEnvironment, lastEnv, liveState, rememberEnv, tenantOf } from "./environment";

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

  it("tells the state of the live environment", () => {
    const t = (status: string) => ({ tenants: [{ environment: "ENVIRONMENT_LIVE", status }] });
    expect(liveState(t("TENANT_STATUS_ACTIVE"))).toBe("active");
    expect(liveState(t("TENANT_STATUS_INACTIVE"))).toBe("inactive");
    expect(liveState(t("TENANT_STATUS_SUSPENDED"))).toBe("suspended");
    expect(liveState(t(""))).toBe("inactive");
    expect(liveState({ tenants: [{ environment: "ENVIRONMENT_SANDBOX" }] })).toBe("none");
    expect(liveState(undefined)).toBe("none");
  });

  it("opens an app in the environment last used, else live when active, else sandbox", () => {
    const store = new Map<string, string>();
    vi.stubGlobal("window", { localStorage: { getItem: (k: string) => store.get(k) ?? null, setItem: (k: string, v: string) => void store.set(k, v), removeItem: (k: string) => void store.delete(k) } });
    const mk = (id: string, status: string) => ({ id, tenants: [{ id: `${id}-l`, environment: "ENVIRONMENT_LIVE", status }, { id: `${id}-s`, environment: "ENVIRONMENT_SANDBOX", status: "TENANT_STATUS_ACTIVE" }] });
    expect(defaultEnv(mk("a", "TENANT_STATUS_ACTIVE"))).toBe("live");
    expect(defaultEnv(mk("a", "TENANT_STATUS_INACTIVE"))).toBe("sandbox");
    rememberEnv("a", "live");
    expect(lastEnv("a")).toBe("live");
    expect(defaultEnv(mk("a", "TENANT_STATUS_INACTIVE"))).toBe("live");
    expect(defaultEnv(mk("b", "TENANT_STATUS_INACTIVE"))).toBe("sandbox");
    // A remembered environment the app does not have is ignored.
    rememberEnv("c", "sandbox");
    expect(defaultEnv({ id: "c", tenants: [{ id: "c-l", environment: "ENVIRONMENT_LIVE", status: "TENANT_STATUS_ACTIVE" }] })).toBe("live");
    store.set("payrudder.env.d", "prod");
    expect(lastEnv("d")).toBeNull();
    vi.unstubAllGlobals();
  });

  it("opens the organisation in live only when some app is active there", () => {
    const app = (status: string) => ({ tenants: [{ id: "x", environment: "ENVIRONMENT_LIVE", status }, { id: "y", environment: "ENVIRONMENT_SANDBOX" }] });
    expect(defaultOrgEnv([app("TENANT_STATUS_INACTIVE"), app("TENANT_STATUS_ACTIVE")])).toBe("live");
    expect(defaultOrgEnv([app("TENANT_STATUS_INACTIVE")])).toBe("sandbox");
    expect(defaultOrgEnv([])).toBe("live");
  });

  it("finds a tenant among the apps, with its app and environment", () => {
    const hit = findTenant(apps, "t-sbx");
    expect(hit?.app.id).toBe("a1");
    expect(hit?.env).toBe("sandbox");
    expect(findTenant(apps, "nope")).toBeNull();
    expect(findTenant(undefined, "t-live")).toBeNull();
  });
});
