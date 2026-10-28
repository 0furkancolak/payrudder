import { describe, expect, it } from "vitest";
import { createRoute, type AnyRoute } from "@tanstack/vue-router";
import { affiliateRoutes } from "@/features/affiliates/routes";
import { pricingRoutes } from "@/features/pricing/routes";
import { usageRoutes } from "@/features/usage/routes";
import { PERSON_PAGES, activeItem, buildNav, type ShellContext } from "./nav";

const keys = (sections: ReturnType<typeof buildNav>) => sections.flatMap((s) => s.items.map((i) => i.key));
const flat = (sections: ReturnType<typeof buildNav>) => sections.flatMap((s) => s.items);
const app = (extra: Partial<ShellContext> = {}): ShellContext => ({ level: "app", appId: "A", scope: "A", tenantId: "T", env: "live", ...extra });

describe("buildNav", () => {
  it("an app is always selected: the organisation's pages have the same sidebar, with the organisation group at the bottom", () => {
    const org = buildNav({ level: "org", appId: "A", scope: "A", tenantId: "T", env: "live" });
    const inApp = buildNav(app());
    expect(keys(org)).toEqual(keys(inApp));
    expect(org.at(-1)?.key).toBe("account");
    expect(org.at(-1)?.items.map((i) => i.key)).toEqual(["members", "orgIntegrations", "support", "settings"]);
  });

  it("Plan and billing is in the organisation group only where SaaS runs, and Settings does not own its page", () => {
    expect(keys(buildNav(app({ saas: false })))).not.toContain("plan");
    const nav = buildNav(app({ saas: true }));
    expect(nav.at(-1)?.items.map((i) => i.key)).toEqual(["members", "orgIntegrations", "support", "settings", "plan"]);
    expect(activeItem(nav, "/app/settings/plan")?.key).toBe("plan");
    expect(activeItem(nav, "/app/settings")?.key).toBe("settings");
  });

  it("the platform has the organisations, and the platform plans once SaaS runs", () => {
    const flat = buildNav({ level: "platform", platformTenant: "P" }).flatMap((s) => s.items);
    expect(flat.find((i) => i.key === "platformPlans")?.to).toBe("/tenants/P/packages");
    expect(flat.find((i) => i.key === "organisations")?.to).toBe("/organisations");
  });

  it("groups the pages of the selected app in categories, in order", () => {
    const nav = buildNav(app());
    expect(nav.map((s) => s.key)).toEqual(["overview", "customersBilling", "usageProducts", "money", "developers", "settings", "account"]);
    const section = (key: string) => nav.find((s) => s.key === key)?.items.map((i) => i.key);
    expect(section("overview")).toEqual(["dashboard", "goLive"]);
    expect(section("customersBilling")).toEqual(["customers", "subscriptions", "invoices", "payments", "paymentLinks", "credits"]);
    expect(section("usageProducts")).toEqual(["usage", "meters", "packages", "pricing", "coupons"]);
    expect(section("money")).toEqual(["accounting", "reports", "affiliates"]);
    expect(section("developers")).toEqual(["apiKeys", "webhooks", "sandbox"]);
    expect(section("settings")).toEqual(["appSettings", "integrations", "mail"]);
  });

  it("links the dashboard and the app's own pages under the app and the rest under the tenant, every link carrying the scope", () => {
    const nav = buildNav(app({ env: "sandbox" }));
    const items = flat(nav);
    const to = (k: string) => items.find((i) => i.key === k)?.to;
    expect(to("dashboard")).toBe("/app/apps/A");
    expect(items.find((i) => i.key === "dashboard")?.search).toEqual({ app: "A", env: "sandbox" });
    expect(to("customers")).toBe("/app/t/T/customers");
    expect(items.find((i) => i.key === "customers")?.search).toEqual({ app: "A" });
    expect(to("invoices")).toBe("/app/t/T/invoices");
    expect(to("payments")).toBe("/app/t/T/payments");
    expect(to("apiKeys")).toBe("/app/apps/A/keys");
    expect(items.find((i) => i.key === "apiKeys")?.search).toEqual({ app: "A", env: "sandbox" });
    expect(to("goLive")).toBe("/app/apps/A/go-live");
    expect(to("sandbox")).toBe("/app/apps/A/sandbox");
    expect(to("appSettings")).toBe("/app/apps/A/settings");
    expect(to("integrations")).toBe("/app/apps/A/integrations");
    expect(items.find((i) => i.key === "settings")?.search).toEqual({ app: "A", env: "sandbox" });
    // Operator-only pages are not offered to a person.
    for (const k of ["providers", "einvoice", "address", "apps"]) expect(keys(nav)).not.toContain(k);
  });

  it("every page of a person is in the sidebar and nothing else is", () => {
    const nav = buildNav(app());
    for (const p of PERSON_PAGES.filter((k) => k !== "members")) expect(keys(nav), p).toContain(p);
  });

  it("a page that has no route is not listed", () => {
    const nav = buildNav(app(), new Set(["customers"]));
    expect(keys(nav)).toEqual(["dashboard", "goLive", "customers", "apiKeys", "sandbox", "appSettings", "integrations", "orgIntegrations", "support", "settings"]);
  });

  it("all apps: the same pages over every app under /app/t/all, without the pages of one app", () => {
    const nav = buildNav({ level: "app", scope: "all", env: "live" });
    const items = flat(nav);
    expect(items.find((i) => i.key === "dashboard")?.to).toBe("/app");
    expect(items.find((i) => i.key === "dashboard")?.search).toEqual({ app: "all", env: "live" });
    expect(items.find((i) => i.key === "customers")?.to).toBe("/app/t/all/customers");
    expect(items.find((i) => i.key === "customers")?.search).toEqual({ app: "all", env: "live" });
    for (const k of ["apiKeys", "sandbox", "appSettings", "goLive", "integrations"]) expect(keys(nav), k).not.toContain(k);
    expect(keys(nav)).toContain("members");
    expect(activeItem(nav, "/app/t/all/invoices")?.key).toBe("invoices");
    expect(activeItem(nav, "/app")?.key).toBe("dashboard");
  });

  it("before the apps have arrived there is a tenant-less sidebar: the organisation group alone", () => {
    expect(keys(buildNav({ level: "org" }))).toEqual(["members", "orgIntegrations", "support", "settings"]);
  });

  it("the operator sees the platform, and a tenant with the operator-only pages too", () => {
    expect(keys(buildNav({ level: "platform" }))).toEqual(["tenants", "organisations", "playground", "deadLetters", "supportInbox", "myApps"]);
    const tenant = keys(buildNav({ level: "tenant", tenantId: "T" }));
    for (const k of ["dashboard", "customers", "invoices", "payments", "providers", "einvoice", "address", "mail", "apps", "myApps"]) expect(tenant).toContain(k);
  });

  it("only an operator has the door to the operator console, and it is the last group; the console leads back to the person's own apps", () => {
    expect(keys(buildNav(app()))).not.toContain("operator");
    expect(keys(buildNav(app({ operator: false })))).not.toContain("operator");
    const nav = buildNav(app({ operator: true }));
    expect(nav.at(-1)?.key).toBe("platform");
    expect(flat(nav).find((i) => i.key === "operator")?.to).toBe("/operator");
    const back = flat(buildNav({ level: "platform" })).find((i) => i.key === "myApps");
    expect(back?.to).toBe("/app");
  });

  it("the partner portal is a door of the organisation group for a partner", () => {
    expect(buildNav(app({ partner: true })).at(-1)?.items.map((i) => i.key)).toEqual(["members", "partner", "orgIntegrations", "support", "settings"]);
  });
});

describe("activeItem", () => {
  const nav = buildNav(app());
  it("follows the path, deeper pages included", () => {
    expect(activeItem(nav, "/app/apps/A")?.key).toBe("dashboard");
    expect(activeItem(nav, "/app/t/T/customers")?.key).toBe("customers");
    expect(activeItem(nav, "/app/t/T/invoices/123")?.key).toBe("invoices");
    expect(activeItem(nav, "/app/apps/A/keys")?.key).toBe("apiKeys");
    expect(activeItem(nav, "/app/apps/A/settings")?.key).toBe("appSettings");
    expect(activeItem(nav, "/app/members")?.key).toBe("members");
    expect(activeItem(nav, "/app/settings")?.key).toBe("settings");
  });
  it("is nothing for a path that is not in the navigation", () => {
    expect(activeItem(nav, "/app/t/T/unknown")).toBeUndefined();
    expect(activeItem(nav, "/app/t/OTHER/customers")).toBeUndefined();
  });
  it("does not confuse a prefix with a page", () => {
    expect(activeItem(nav, "/app/t/T/customers-old")).toBeUndefined();
  });
});

describe("Money > Affiliates", () => {
  const person = buildNav(app());

  it("is in the Money category after accounting and reports, with the affiliates page", () => {
    const money = person.find((s) => s.key === "money");
    expect(money?.items.map((i) => i.key)).toEqual(["accounting", "reports", "affiliates"]);
    expect(money?.items[2]?.to).toBe("/app/t/T/affiliates");
  });

  it("is a page of a person and of the operator, and is current under its own path", () => {
    expect(PERSON_PAGES).toContain("affiliates");
    expect(keys(buildNav({ level: "tenant", tenantId: "T" }))).toContain("affiliates");
    expect(activeItem(person, "/app/t/T/affiliates")?.key).toBe("affiliates");
  });

  it("is only listed when its route exists", () => {
    expect(keys(buildNav(app(), new Set(["customers"])))).not.toContain("affiliates");
  });

  it("has a route at the path the sidebar links to", () => {
    const parent = createRoute({ getParentRoute: () => ({}) as AnyRoute, path: "/app/t/$id" });
    const routes = affiliateRoutes(parent);
    expect(routes.map((r) => (r.options as { path?: string }).path)).toEqual(["affiliates"]);
  });
});

describe("Usage and Coupons", () => {
  const person = buildNav(app());
  const parent = createRoute({ getParentRoute: () => ({}) as AnyRoute, path: "/app/t/$id" });
  const pathsOf = (routes: AnyRoute[]) => routes.map((r) => (r.options as { path?: string }).path);

  it("puts usage, meters, packages, pricing and coupons together as Usage and products", () => {
    expect(person.find((s) => s.key === "usageProducts")?.items.map((i) => i.key)).toEqual(["usage", "meters", "packages", "pricing", "coupons"]);
    expect(flat(person).find((i) => i.key === "usage")?.to).toBe("/app/t/T/usage");
    expect(flat(person).find((i) => i.key === "coupons")?.to).toBe("/app/t/T/coupons");
  });

  it("is in the operator's tenant console too, at the same paths under the tenant", () => {
    const items = flat(buildNav({ level: "tenant", tenantId: "T" }));
    expect(items.find((i) => i.key === "usage")?.to).toBe("/tenants/T/usage");
    expect(items.find((i) => i.key === "coupons")?.to).toBe("/tenants/T/coupons");
  });

  it("is current under its own path, and pricing is not mistaken for coupons", () => {
    expect(activeItem(person, "/app/t/T/usage")?.key).toBe("usage");
    expect(activeItem(person, "/app/t/T/coupons")?.key).toBe("coupons");
    expect(activeItem(person, "/app/t/T/pricing")?.key).toBe("pricing");
  });

  it("has a route at the path the sidebar links to", () => {
    expect(pathsOf(usageRoutes(parent))).toEqual(["usage"]);
    expect(pathsOf(pricingRoutes(parent))).toEqual(["pricing", "coupons"]);
  });

  it("is only listed when its route exists", () => {
    const keysOf = keys(buildNav(app(), new Set(["customers"])));
    expect(keysOf).not.toContain("usage");
    expect(keysOf).not.toContain("coupons");
  });
});
