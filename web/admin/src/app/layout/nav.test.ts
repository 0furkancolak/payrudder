import { describe, expect, it } from "vitest";
import { createRoute, type AnyRoute } from "@tanstack/vue-router";
import { affiliateRoutes } from "@/features/affiliates/routes";
import { pricingRoutes } from "@/features/pricing/routes";
import { usageRoutes } from "@/features/usage/routes";
import { PERSON_PAGES, activeItem, buildNav } from "./nav";

const keys = (sections: ReturnType<typeof buildNav>) => sections.flatMap((s) => s.items.map((i) => i.key));

describe("buildNav", () => {
  it("the organisation level has the apps, the members and the settings, nothing of an app", () => {
    const nav = buildNav({ level: "org" });
    expect(keys(nav)).toEqual(["overview", "members", "paymentProviders", "settings"]);
  });

  it("an app level has the app's pages under the tenant, and the dashboard and keys under the app", () => {
    const nav = buildNav({ level: "app", appId: "A", tenantId: "T", env: "sandbox" });
    const flat = nav.flatMap((s) => s.items);
    const to = (k: string) => flat.find((i) => i.key === k)?.to;
    expect(to("dashboard")).toBe("/app/apps/A");
    expect(flat.find((i) => i.key === "dashboard")?.search).toEqual({ env: "sandbox" });
    expect(to("customers")).toBe("/app/t/T/customers");
    expect(to("invoices")).toBe("/app/t/T/invoices");
    expect(to("payments")).toBe("/app/t/T/payments");
    expect(to("apiKeys")).toBe("/app/apps/A/keys");
    // Going live and the sandbox tools are pages of the app; their links keep the environment the person is in.
    expect(to("goLive")).toBe("/app/apps/A/go-live");
    expect(to("sandbox")).toBe("/app/apps/A/sandbox");
    expect(flat.find((i) => i.key === "goLive")?.search).toEqual({ env: "sandbox" });
    expect(flat.find((i) => i.key === "sandbox")?.search).toEqual({ env: "sandbox" });
    expect(flat.find((i) => i.key === "apiKeys")?.search).toEqual({ env: "sandbox" });
    expect(to("appSettings")).toBe("/app/apps/A/settings");
    // Operator-only pages are not offered to a person.
    for (const k of ["providers", "einvoice", "address", "apps"]) expect(keys(nav)).not.toContain(k);
  });

  it("every page of a person is in the sidebar and nothing else is", () => {
    const nav = buildNav({ level: "app", appId: "A", tenantId: "T" });
    for (const p of PERSON_PAGES.filter((k) => k !== "members")) expect(keys(nav), p).toContain(p);
  });

  it("a page that has no route is not listed", () => {
    const nav = buildNav({ level: "app", appId: "A", tenantId: "T" }, new Set(["customers"]));
    expect(keys(nav)).toEqual(["dashboard", "goLive", "customers", "apiKeys", "sandbox", "appSettings", "paymentProviders"]);
  });

  it("the operator sees the platform, and a tenant with the operator-only pages too", () => {
    expect(keys(buildNav({ level: "platform" }))).toEqual(["tenants", "playground", "deadLetters", "myApps"]);
    const tenant = keys(buildNav({ level: "tenant", tenantId: "T" }));
    for (const k of ["dashboard", "customers", "invoices", "payments", "providers", "einvoice", "address", "mail", "apps", "myApps"]) expect(tenant).toContain(k);
  });

  it("only an operator has the door to the operator console, and the console leads back to the person's own apps", () => {
    expect(keys(buildNav({ level: "org" }))).not.toContain("operator");
    expect(keys(buildNav({ level: "org", operator: false }))).not.toContain("operator");
    const door = buildNav({ level: "org", operator: true }).flatMap((s) => s.items).find((i) => i.key === "operator");
    expect(door?.to).toBe("/operator");
    expect(keys(buildNav({ level: "app", appId: "A", tenantId: "T" }))).not.toContain("operator");
    const back = buildNav({ level: "platform" }).flatMap((s) => s.items).find((i) => i.key === "myApps");
    expect(back?.to).toBe("/app");
  });
});

describe("activeItem", () => {
  const nav = buildNav({ level: "app", appId: "A", tenantId: "T" });
  it("follows the path, deeper pages included", () => {
    expect(activeItem(nav, "/app/apps/A")?.key).toBe("dashboard");
    expect(activeItem(nav, "/app/t/T/customers")?.key).toBe("customers");
    expect(activeItem(nav, "/app/t/T/invoices/123")?.key).toBe("invoices");
    expect(activeItem(nav, "/app/apps/A/keys")?.key).toBe("apiKeys");
    expect(activeItem(nav, "/app/apps/A/settings")?.key).toBe("appSettings");
  });
  it("is nothing for a path that is not in the navigation", () => {
    expect(activeItem(nav, "/app/t/T/unknown")).toBeUndefined();
    expect(activeItem(nav, "/app/t/OTHER/customers")).toBeUndefined();
  });
  it("does not confuse a prefix with a page", () => {
    expect(activeItem(buildNav({ level: "app", appId: "A", tenantId: "T" }), "/app/t/T/customers-old")).toBeUndefined();
  });
});

describe("Growth > Affiliates", () => {
  const person = buildNav({ level: "app", appId: "A", tenantId: "T" });

  it("is a Growth section between Billing and Developers with the affiliates page", () => {
    expect(person.map((s) => s.key)).toEqual(["overview", "customers", "billing", "growth", "developers", "settings"]);
    const growth = person.find((s) => s.key === "growth");
    expect(growth?.items.map((i) => i.key)).toEqual(["affiliates"]);
    expect(growth?.items[0]?.to).toBe("/app/t/T/affiliates");
  });

  it("is a page of a person and of the operator, and is current under its own path", () => {
    expect(PERSON_PAGES).toContain("affiliates");
    expect(keys(buildNav({ level: "tenant", tenantId: "T" }))).toContain("affiliates");
    expect(activeItem(person, "/app/t/T/affiliates")?.key).toBe("affiliates");
  });

  it("is only listed when its route exists", () => {
    expect(keys(buildNav({ level: "app", appId: "A", tenantId: "T" }, new Set(["customers"])))).not.toContain("affiliates");
  });

  it("has a route at the path the sidebar links to", () => {
    const parent = createRoute({ getParentRoute: () => ({}) as AnyRoute, path: "/app/t/$id" });
    const routes = affiliateRoutes(parent);
    expect(routes.map((r) => (r.options as { path?: string }).path)).toEqual(["affiliates"]);
  });
});

describe("Usage and Coupons", () => {
  const person = buildNav({ level: "app", appId: "A", tenantId: "T" });
  const section = (key: string) => person.find((s) => s.key === key)?.items.map((i) => i.key);
  const parent = createRoute({ getParentRoute: () => ({}) as AnyRoute, path: "/app/t/$id" });
  const pathsOf = (routes: AnyRoute[]) => routes.map((r) => (r.options as { path?: string }).path);

  it("puts usage with the customers and coupons with the billing pages, after pricing", () => {
    expect(section("customers")).toEqual(["customers", "subscriptions", "usage", "credits"]);
    expect(section("billing")?.slice(0, 4)).toEqual(["packages", "pricing", "coupons", "invoices"]);
    expect(person.flatMap((s) => s.items).find((i) => i.key === "usage")?.to).toBe("/app/t/T/usage");
    expect(person.flatMap((s) => s.items).find((i) => i.key === "coupons")?.to).toBe("/app/t/T/coupons");
  });

  it("is in the operator's tenant console too, at the same paths under the tenant", () => {
    const flat = buildNav({ level: "tenant", tenantId: "T" }).flatMap((s) => s.items);
    expect(flat.find((i) => i.key === "usage")?.to).toBe("/tenants/T/usage");
    expect(flat.find((i) => i.key === "coupons")?.to).toBe("/tenants/T/coupons");
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
    const keysOf = keys(buildNav({ level: "app", appId: "A", tenantId: "T" }, new Set(["customers"])));
    expect(keysOf).not.toContain("usage");
    expect(keysOf).not.toContain("coupons");
  });
});
