import { expect, test } from "@playwright/test";
import { baseTenant, MockGateway, openTenantPage, TENANT } from "./gateway";
import { pickFilter } from "./ui";

// The packages form reads the tenant's currency from the tenant list.
const tenantList = (c: { method: string; path: string }) =>
  c.method === "GET" && c.path === "/admin/v1/tenants" ? { body: { tenants: [{ id: TENANT, name: "Acme", environment: "ENVIRONMENT_LIVE", currency: "USD", timezone: "Europe/Istanbul" }] } } : undefined;

const meters = [
  { code: "api_calls", aggregation: "AGGREGATION_COUNT", unit_label: "calls", quantity_scale: 0, version: 1, category: "API" },
  { code: "gb_hours", aggregation: "AGGREGATION_SUM", value_property: "gb_h", unit_label: "GB-h", quantity_scale: 3, version: 2, category: "Storage", filter: { region: { values: ["eu"] } } },
  { code: "tokens", aggregation: "AGGREGATION_SUM", value_property: "n", unit_label: "jeton", quantity_scale: 0, version: 1 },
];
const metersMock = (c: { method: string; path: string }) => (c.method === "GET" && c.path === "/v1/meters" ? { body: { meters } } : undefined);

test.describe("meters", () => {
  test("meters are listed and filtered by a category of the tenant's own", async ({ page }) => {
    const gw = baseTenant(new MockGateway()).on(metersMock);
    await gw.install(page);
    await openTenantPage(page, "/meters");
    await expect(page.getByRole("cell", { name: "gb_hours", exact: true })).toBeVisible();
    await expect(page.getByRole("row", { name: /gb_hours/ })).toContainText("Storage");
    await pickFilter(page, "Category", "API");
    await expect(page.getByRole("cell", { name: "api_calls", exact: true })).toBeVisible();
    await expect(page.getByRole("cell", { name: "gb_hours", exact: true })).toHaveCount(0);
  });

  test("a meter is created with a free unit, a category and a filter", async ({ page }) => {
    const gw = baseTenant(new MockGateway()).on(metersMock).on((c) => (c.method === "POST" && c.path === "/v1/meters" ? { status: 201, body: { code: "images", version: 1 } } : undefined));
    await gw.install(page);
    await openTenantPage(page, "/meters");
    await page.getByTestId("meters-new").click();
    const form = page.getByRole("form", { name: "new meter" });
    await form.getByLabel("Code").fill("images");
    await form.getByLabel("Category").fill("Yapay zekâ");
    await form.getByLabel("Aggregation").selectOption("AGGREGATION_MAX");
    await form.getByLabel("Quantity property").fill("count");
    await form.getByLabel("Unit").fill("görsel");
    await form.getByLabel("Decimal places").selectOption("2");
    await form.getByRole("button", { name: "Add a condition" }).click();
    await form.getByLabel("Filter property 1").fill("model");
    await form.getByLabel("Accepted values 1").fill("sdxl, flux ,");
    await page.getByRole("dialog").getByRole("button", { name: "Create meter" }).click();
    await expect.poll(() => gw.called("POST", "/v1/meters").length).toBe(1);
    expect(gw.called("POST", "/v1/meters")[0]?.body).toEqual({
      code: "images",
      aggregation: "AGGREGATION_MAX",
      value_property: "count",
      unit_label: "görsel",
      quantity_scale: 2,
      category: "Yapay zekâ",
      filter: { model: { values: ["sdxl", "flux"] } },
    });
  });

  test("a meter of distinct values has no decimal places and counts the property it names", async ({ page }) => {
    const gw = baseTenant(new MockGateway()).on(metersMock).on((c) => (c.method === "POST" && c.path === "/v1/meters" ? { status: 201, body: { code: "active_users", version: 1 } } : undefined));
    await gw.install(page);
    await openTenantPage(page, "/meters");
    await page.getByTestId("meters-new").click();
    const form = page.getByRole("form", { name: "new meter" });
    await form.getByLabel("Code").fill("active_users");
    await form.getByLabel("Unit").fill("users");
    await form.getByLabel("Decimal places").selectOption("3");
    await form.getByLabel("Aggregation").selectOption("AGGREGATION_UNIQUE");
    await expect(form.getByLabel("Decimal places")).toBeDisabled();
    await expect(form.getByLabel("Decimal places")).toHaveValue("0");
    await expect(form.getByText("Counts how many different values of one event property appeared")).toBeVisible();
    await form.getByLabel("Property to count").fill("user_id");
    await page.getByRole("dialog").getByRole("button", { name: "Create meter" }).click();
    await expect.poll(() => gw.called("POST", "/v1/meters").length).toBe(1);
    expect(gw.called("POST", "/v1/meters")[0]?.body).toEqual({ code: "active_users", aggregation: "AGGREGATION_UNIQUE", value_property: "user_id", unit_label: "users", quantity_scale: 0 });

    await page.getByTestId("meters-new").click();
    const last = page.getByRole("form", { name: "new meter" });
    await last.getByLabel("Code").fill("seats_now");
    await last.getByLabel("Unit").fill("seats");
    await last.getByLabel("Aggregation").selectOption("AGGREGATION_LAST");
    await expect(last.getByText("Keeps the quantity of the most recent event")).toBeVisible();
    await last.getByLabel("Quantity property").fill("seats");
    await page.getByRole("dialog").getByRole("button", { name: "Create meter" }).click();
    await expect.poll(() => gw.called("POST", "/v1/meters").length).toBe(2);
    expect(gw.called("POST", "/v1/meters")[1]?.body).toEqual({ code: "seats_now", aggregation: "AGGREGATION_LAST", value_property: "seats", unit_label: "seats", quantity_scale: 0 });
  });

  test("a new version of a distinct-values meter keeps its aggregation, and a sum meter cannot become one", async ({ page }) => {
    const rowShaped = { code: "active_users", aggregation: "AGGREGATION_UNIQUE", value_property: "user_id", unit_label: "users", quantity_scale: 0, version: 1 };
    const gw = baseTenant(new MockGateway()).on((c) => (c.method === "GET" && c.path === "/v1/meters" ? { body: { meters: [...meters, rowShaped] } } : undefined));
    await gw.install(page);
    await openTenantPage(page, "/meters");
    await page.getByRole("row", { name: /active_users/ }).getByTestId("row-actions").click();
    await page.getByTestId("row-action-version").click();
    const unique = page.getByRole("form", { name: "new meter" });
    await expect(unique.getByLabel("Aggregation").locator("option")).toHaveText(["Distinct values"]);
    await expect(unique.getByTestId("aggregation-locked")).toContainText("Fixed for this code");
    await page.getByRole("dialog").getByRole("button", { name: "Cancel" }).click();

    await page.getByRole("row", { name: /gb_hours/ }).getByTestId("row-actions").click();
    await page.getByTestId("row-action-version").click();
    const sum = page.getByRole("form", { name: "new meter" });
    await expect(sum.getByLabel("Aggregation").locator("option")).toHaveText(["Sum", "Count", "Maximum"]);
  });

  test("a new version keeps the code and the decimal places", async ({ page }) => {
    const gw = baseTenant(new MockGateway()).on(metersMock).on((c) => (c.method === "POST" && c.path === "/v1/meters" ? { status: 201, body: { code: "gb_hours", version: 3 } } : undefined));
    await gw.install(page);
    await openTenantPage(page, "/meters");
    await page.getByRole("row", { name: /gb_hours/ }).getByTestId("row-actions").click();
    await page.getByTestId("row-action-version").click();
    const form = page.getByRole("form", { name: "new meter" });
    await expect(form.getByLabel("Code")).toBeDisabled();
    await expect(form.getByLabel("Decimal places")).toBeDisabled();
    await form.getByLabel("Category").fill("Compute");
    await page.getByRole("dialog").getByRole("button", { name: "Publish version" }).click();
    await expect.poll(() => gw.called("POST", "/v1/meters").length).toBe(1);
    expect(gw.called("POST", "/v1/meters")[0]?.body).toMatchObject({ code: "gb_hours", quantity_scale: 3, category: "Compute", value_property: "gb_h", filter: { region: { values: ["eu"] } } });
  });
});

test.describe("package usage prices and quotas", () => {
  const empty = (c: { path: string }) => (c.path === "/v1/plans" ? { body: { plans: [] } } : undefined);

  test("a version is published with an included quantity, a unit price in major units and a daily soft quota", async ({ page }) => {
    const gw = baseTenant(new MockGateway())
      .on(tenantList)
      .on(metersMock)
      .on(empty)
      .on((c) => {
        if (c.method === "POST" && c.path === "/v1/plans") return { status: 201, body: { code: "ai", name: "AI" } };
        if (c.method === "POST" && c.path === "/v1/plans/ai/versions") return { status: 201, body: { version: 1 } };
        return undefined;
      });
    await gw.install(page);
    await openTenantPage(page, "/packages");
    await page.getByTestId("packages-new").click();
    const form = page.getByRole("form", { name: "new package" });
    await form.getByLabel("Code").fill("ai");
    await form.getByLabel("Name", { exact: true }).fill("AI");
    await form.getByLabel("Fixed fee per month (USD)").fill("10");
    await form.getByRole("button", { name: "Add a usage price" }).click();
    await form.getByLabel("Meter of price 1").selectOption("tokens");
    await form.getByLabel("Included quantity of price 1").fill("1000");
    await form.getByLabel("Unit price of price 1").fill("0.0002");
    await form.getByRole("button", { name: "Add a usage price" }).click();
    await form.getByLabel("Meter of price 2").selectOption("gb_hours");
    await form.getByLabel("Pricing model of price 2").selectOption("tiered");
    await form.getByLabel("Price 2, tier 1 up to").fill("10.5");
    await form.getByLabel("Price 2, tier 1 unit price").fill("0");
    await form.getByLabel("Price 2, tier 2 unit price").fill("0.25");
    await form.getByRole("button", { name: "Add a quota" }).click();
    await form.getByLabel("Meter of quota 1").selectOption("tokens");
    await form.getByLabel("Limit of quota 1").fill("5000");
    await form.getByLabel("Window of quota 1").selectOption("QUOTA_WINDOW_DAY");
    await form.getByLabel("When quota 1 is reached").selectOption("QUOTA_MODE_SOFT");
    await form.getByLabel("Alerts of quota 1").fill("50, 80, 100");
    await page.getByRole("dialog").getByRole("button", { name: "Publish version" }).click();
    await expect.poll(() => gw.called("POST", "/v1/plans/ai/versions").length).toBe(1);
    expect(gw.called("POST", "/v1/plans/ai/versions")[0]?.body).toEqual({
      base_fee_minor: "1000",
      fees: [],
      prices: [
        { meter_code: "tokens", included: "1000", per_unit: { unit_price: "0.02" } },
        { meter_code: "gb_hours", tiered: { tiers: [{ up_to: "10.5", unit_price: "0" }, { unit_price: "25" }] } },
      ],
      entitlements: [{ meter_code: "tokens", limit: "5000", window: "QUOTA_WINDOW_DAY", mode: "QUOTA_MODE_SOFT", alert_thresholds_pct: [50, 80, 100] }],
    });
  });

  test("a yearly and a lifetime quota are offered, explained and sent", async ({ page }) => {
    const gw = baseTenant(new MockGateway())
      .on(tenantList)
      .on(metersMock)
      .on(empty)
      .on((c) => {
        if (c.method === "POST" && c.path === "/v1/plans") return { status: 201, body: { code: "ai", name: "AI" } };
        if (c.method === "POST" && c.path === "/v1/plans/ai/versions") return { status: 201, body: { version: 1 } };
        return undefined;
      });
    await gw.install(page);
    await openTenantPage(page, "/packages");
    await page.getByTestId("packages-new").click();
    const form = page.getByRole("form", { name: "new package" });
    await form.getByLabel("Code").fill("ai");
    await form.getByLabel("Name", { exact: true }).fill("AI");
    await form.getByRole("button", { name: "Add a quota" }).click();
    await form.getByLabel("Meter of quota 1").selectOption("tokens");
    await form.getByLabel("Limit of quota 1").fill("100000");
    await form.getByLabel("Window of quota 1").selectOption({ label: "Per calendar year" });
    await expect(form).toContainText("Counts each calendar year in the app's time zone.");
    await form.getByRole("button", { name: "Add a quota" }).click();
    await form.getByLabel("Meter of quota 2").selectOption("gb_hours");
    await form.getByLabel("Limit of quota 2").fill("2000");
    await form.getByLabel("Window of quota 2").selectOption("QUOTA_WINDOW_LIFETIME");
    await form.getByLabel("When quota 2 is reached").selectOption("QUOTA_MODE_NOTIFY");
    await expect(form).toContainText("Counts everything the customer has used and never resets.");
    await page.getByRole("dialog").getByRole("button", { name: "Publish version" }).click();
    await expect.poll(() => gw.called("POST", "/v1/plans/ai/versions").length).toBe(1);
    expect(gw.called("POST", "/v1/plans/ai/versions")[0]?.body).toMatchObject({
      entitlements: [
        { meter_code: "tokens", limit: "100000", window: "QUOTA_WINDOW_YEAR", mode: "QUOTA_MODE_HARD" },
        { meter_code: "gb_hours", limit: "2000", window: "QUOTA_WINDOW_LIFETIME", mode: "QUOTA_MODE_NOTIFY" },
      ],
    });
  });

  test("a soft quota on a meter without a price is refused before anything is sent", async ({ page }) => {
    const gw = baseTenant(new MockGateway()).on(tenantList).on(metersMock).on(empty);
    await gw.install(page);
    await openTenantPage(page, "/packages");
    await page.getByTestId("packages-new").click();
    const form = page.getByRole("form", { name: "new package" });
    await form.getByLabel("Code").fill("ai");
    await form.getByLabel("Name", { exact: true }).fill("AI");
    await form.getByRole("button", { name: "Add a quota" }).click();
    await form.getByLabel("Meter of quota 1").selectOption("api_calls");
    await form.getByLabel("Limit of quota 1").fill("100");
    await form.getByLabel("When quota 1 is reached").selectOption("QUOTA_MODE_SOFT");
    await page.getByRole("dialog").getByRole("button", { name: "Publish version" }).click();
    await expect(page.getByRole("alert")).toContainText("api_calls needs a usage price");
    expect(gw.called("POST", "/v1/plans")).toHaveLength(0);
  });

  test("a version's detail shows what is included and how each quota counts", async ({ page }) => {
    const gw = baseTenant(new MockGateway())
      .on(tenantList)
      .on(metersMock)
      .on((c) => {
        if (c.path === "/v1/plans") return { body: { plans: [{ id: "p1", code: "ai", name: "AI", latest_version: 1 }] } };
        if (c.path === "/v1/plans/ai/versions/latest") {
          return {
            body: {
              version: 1,
              base_fee_minor: "1000",
              prices: [{ meter_code: "tokens", included: "1000", per_unit: { unit_price: "0.02" } }],
              entitlements: [{ meter_code: "tokens", limit: "5000", window: "QUOTA_WINDOW_WEEK", mode: "QUOTA_MODE_NOTIFY", alert_thresholds_pct: [50, 100] }],
            },
          };
        }
        return undefined;
      });
    await gw.install(page);
    await openTenantPage(page, "/packages");
    await page.getByRole("cell", { name: "ai", exact: true }).click();
    const detail = page.getByTestId("package-detail");
    await expect(detail.getByRole("row", { name: /tokens/ }).first()).toContainText("1000");
    await expect(detail.getByRole("row", { name: /tokens/ }).first()).toContainText("0.0002 USD per unit");
    const quota = detail.getByRole("region", { name: "Limits" }).getByRole("row", { name: /tokens/ });
    await expect(quota).toContainText("Per week");
    await expect(quota).toContainText("Notify only");
    await expect(quota).toContainText("50% · 100%");
  });
});

test.describe("customer quota overrides", () => {
  test("a customer gets its own limit on a meter, sees it in force and loses it again", async ({ page }) => {
    let overrides: Record<string, unknown>[] = [{ customer_external_id: "cus_1", meter_code: "gb_hours", limit: "50.000", window: "QUOTA_WINDOW_MONTH", mode: "QUOTA_MODE_HARD", alert_thresholds_pct: [80, 100] }];
    const gw = baseTenant(new MockGateway())
      .on(metersMock)
      .on((c) => {
        if (c.path === "/v1/customers" && c.method === "GET") return { body: { customers: [{ id: "id-1", external_id: "cus_1", name: "Ada", email: "ada@example.com" }] } };
        if (c.path === "/v1/customers/cus_1/entitlement-overrides" && c.method === "GET") return { body: { overrides } };
        if (c.path === "/v1/customers/cus_1/entitlement-overrides/api_calls" && c.method === "PUT") {
          overrides = [...overrides, { customer_external_id: "cus_1", meter_code: "api_calls", ...c.body }];
          return { body: overrides.at(-1) };
        }
        if (c.path === "/v1/customers/cus_1/entitlement-overrides/gb_hours" && c.method === "DELETE") {
          overrides = overrides.filter((o) => o.meter_code !== "gb_hours");
          return { status: 204 };
        }
        if (c.path === "/v1/customers/cus_1/entitlements") {
          return {
            body: {
              items: [
                { meter_code: "api_calls", used: "1200", limit: "1000", unlimited: false, allowed: true, over_limit: true, window: "day", window_id: "2026-11-05", resets_at: "2026-11-05T21:00:00Z", mode: "soft", overridden: true },
                { meter_code: "gb_hours", used: "10.000", limit: "50.000", unlimited: false, allowed: true, over_limit: false, window: "month", mode: "hard", overridden: true },
              ],
            },
          };
        }
        return undefined;
      });
    await gw.install(page);
    await openTenantPage(page, "/customers");
    await page.getByRole("row", { name: /cus_1/ }).getByTestId("row-actions").click();
    await page.getByTestId("row-action-quotas").click();
    const sheet = page.getByRole("dialog", { name: "Quotas of cus_1" });
    await expect(sheet.getByRole("region", { name: "Own limits" }).getByRole("row", { name: /gb_hours/ })).toContainText("Per billing month");

    const form = sheet.getByRole("form", { name: "quota override" });
    await form.getByLabel("Meter").selectOption("api_calls");
    await form.getByLabel("Limit", { exact: true }).fill("1000");
    await form.getByLabel("Window").selectOption("QUOTA_WINDOW_DAY");
    await form.getByLabel("When reached").selectOption("QUOTA_MODE_NOTIFY");
    await form.getByLabel("Alerts").fill("90");
    await sheet.getByRole("button", { name: "Save override" }).click();
    await expect.poll(() => gw.called("PUT", "/v1/customers/cus_1/entitlement-overrides/api_calls").length).toBe(1);
    expect(gw.called("PUT", "/v1/customers/cus_1/entitlement-overrides/api_calls")[0]?.body).toEqual({
      limit: "1000",
      window: "QUOTA_WINDOW_DAY",
      mode: "QUOTA_MODE_NOTIFY",
      alert_thresholds_pct: [90],
    });

    // The usage in force: a soft daily limit over its limit is still allowed.
    const usage = sheet.getByRole("region", { name: "Usage this period" });
    const calls = usage.getByRole("row", { name: /api_calls/ });
    await expect(calls).toContainText("Per day");
    await expect(calls).toContainText("over the limit, still allowed");
    await expect(calls).toContainText("own limit");

    await sheet.getByRole("button", { name: "Remove the override on gb_hours" }).click();
    await expect.poll(() => gw.called("DELETE", "/v1/customers/cus_1/entitlement-overrides/gb_hours").length).toBe(1);
    await expect(sheet.getByRole("region", { name: "Own limits" }).getByRole("row", { name: /gb_hours/ })).toHaveCount(0);
  });

  test("a limit with more decimals than the meter has is refused before anything is sent", async ({ page }) => {
    const gw = baseTenant(new MockGateway())
      .on(metersMock)
      .on((c) => {
        if (c.path === "/v1/customers" && c.method === "GET") return { body: { customers: [{ id: "id-1", external_id: "cus_1", name: "Ada", email: "ada@example.com" }] } };
        if (c.path === "/v1/customers/cus_1/entitlement-overrides") return { body: { overrides: [] } };
        if (c.path === "/v1/customers/cus_1/entitlements") return { body: { items: [] } };
        return undefined;
      });
    await gw.install(page);
    await openTenantPage(page, "/customers");
    await page.getByRole("row", { name: /cus_1/ }).getByTestId("row-actions").click();
    await page.getByTestId("row-action-quotas").click();
    const sheet = page.getByRole("dialog", { name: "Quotas of cus_1" });
    await expect(sheet.getByText("No overrides: the package's limits apply.")).toBeVisible();
    const form = sheet.getByRole("form", { name: "quota override" });
    await form.getByLabel("Meter").selectOption("api_calls");
    await form.getByLabel("Limit", { exact: true }).fill("10.5");
    await sheet.getByRole("button", { name: "Save override" }).click();
    await expect(sheet.getByRole("alert")).toContainText("at most 0 decimals");
    expect(gw.called("PUT", /entitlement-overrides/)).toHaveLength(0);
  });
});
