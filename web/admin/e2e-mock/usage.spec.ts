import { expect, test, type Page } from "@playwright/test";
import { baseTenant, MockGateway, openTenantPage } from "./gateway";
import { METERS, usageReads } from "./usage";
import { pickFilter, pickRange, rowAction } from "./ui";

// 21 October 2026, local: the page opens on this month so far, 1 to 21 October; the tests pick the last 30 days (22 September to 21 October). Its billing period is 2026-10.
const NOW = new Date(2026, 9, 21, 12, 0, 0);

async function open(page: Page, gw: MockGateway) {
  await page.clock.setFixedTime(NOW);
  await gw.install(page);
  await openTenantPage(page, "/usage");
  // The page opens on this month so far; these figures are those of the last 30 days.
  await pickRange(page, "30d");
}
const gateway = () => baseTenant(new MockGateway()).on(usageReads);

// A tenant that also has a meter counting distinct values and one taking the latest value.
const ROW_SHAPED = [
  { code: "active_users", aggregation: "AGGREGATION_UNIQUE", value_property: "user_id", quantity_scale: 0, unit_label: "users", version: 1, create_time: "2026-09-12T09:00:00Z" },
  { code: "seats_now", aggregation: "AGGREGATION_LAST", value_property: "seats", quantity_scale: 0, unit_label: "seats", version: 1, create_time: "2026-09-13T09:00:00Z" },
];
const withRowShaped = (c: { method: string; path: string }) => (c.method === "GET" && c.path === "/v1/meters" ? { body: { meters: [...METERS, ...ROW_SHAPED] } } : undefined);

test.describe("usage overview", () => {
  test("a card per meter with its total and how it moved, the chart, the top customers and who is near or over a limit", async ({ page }) => {
    const gw = gateway();
    await open(page, gw);
    await expect(page.getByTestId("page-title")).toHaveText("Usage");
    // 9 days of 5 and 21 days of 10 against 30 days of 5 before: 255 calls, 70 % more. A meter that takes the highest value shows its highest day.
    await expect(page.getByTestId("meter-total-api_calls")).toHaveText("255");
    await expect(page.getByTestId("meter-delta-api_calls")).toContainText("+70%");
    await expect(page.getByTestId("meter-total-storage_gb")).toHaveText("10");
    await expect(page.getByTestId("usage-chart-total")).toContainText("Total 255 calls");
    await expect(page.getByTestId("usage-period")).toHaveText("Limits and rankings: 2026-10");
    // The series is asked by the day over the whole range, and the period before is asked too.
    const calls = gw.called("GET", "/v1/analytics/usage").filter((c) => c.query.get("meter") === "api_calls");
    expect(calls.map((c) => [c.query.get("from"), c.query.get("to"), c.query.get("granularity")])).toEqual(
      expect.arrayContaining([
        ["2026-09-22T00:00:00Z", "2026-10-22T00:00:00Z", "day"],
        ["2026-08-23T00:00:00Z", "2026-09-22T00:00:00Z", "day"],
      ]),
    );

    const top = page.getByTestId("usage-top");
    await expect(top.getByText("cus_busy")).toBeVisible();
    await expect(top).toContainText("1,200 calls");
    const limits = page.getByTestId("usage-limits");
    await expect(limits).toContainText("1 over");
    await expect(limits).toContainText("1 near");
    await expect(page.getByTestId("limit-cus_busy-api_calls")).toContainText("Over the limit");
    await expect(page.getByTestId("limit-cus_near-api_calls")).toContainText("Near the limit");
    await expect(page.getByTestId("limit-cus_near-api_calls")).toContainText("85%");
    // The meter that is within its limit is not listed among the limits.
    await expect(page.getByTestId("limit-cus_near-storage_gb")).toHaveCount(0);
  });

  test("a meter card chooses what the chart and the ranking show", async ({ page }) => {
    const gw = gateway();
    await open(page, gw);
    await page.getByTestId("meter-card-storage_gb").click();
    await expect(page.getByTestId("usage-chart-card")).toContainText("storage_gb");
    await expect(page.getByTestId("usage-chart-total")).toContainText("Total 10 GB");
    await expect(page.getByTestId("usage-top")).toContainText("2.5 GB");
  });

  test("the comparison can be turned off, and the range chosen", async ({ page }) => {
    const gw = gateway();
    await open(page, gw);
    await expect(page.getByTestId("meter-delta-api_calls")).toBeVisible();
    await page.getByTestId("usage-compare").click();
    await expect(page.getByTestId("meter-delta-api_calls")).toHaveCount(0);

    await pickRange(page, "lastMonth");
    await expect(page.getByTestId("usage-period")).toHaveText("Limits and rankings: 2026-09");
    await expect.poll(() => gw.called("GET", "/v1/analytics/top-customers").some((c) => c.query.get("period") === "2026-09")).toBe(true);
    const last = gw.called("GET", "/v1/analytics/usage").filter((c) => c.query.get("from") === "2026-09-01T00:00:00Z");
    expect(last.length).toBeGreaterThan(0);
    expect(last[0]?.query.get("to")).toBe("2026-10-01T00:00:00Z");

    // A day or two is drawn by the hour.
    await pickRange(page, "today");
    await expect.poll(() => gw.called("GET", "/v1/analytics/usage").some((c) => c.query.get("granularity") === "hour")).toBe(true);
  });

  test("without meters it says what a meter is instead of showing empty charts", async ({ page }) => {
    // The handler added last answers first, so it takes the meters away from the usual ones.
    const gw = gateway().on((c) => (c.method === "GET" && c.path === "/v1/meters" ? { body: { meters: [] } } : undefined));
    await open(page, gw);
    await expect(page.getByTestId("usage-empty")).toContainText("No meters yet");
  });

  test("a failed read is shown, not an empty page", async ({ page }) => {
    const gw = gateway().on((c) => (c.method === "GET" && c.path === "/v1/meters" ? { status: 500, body: { error: { code: "INTERNAL", message: "meters are down" } } } : undefined));
    await open(page, gw);
    await expect(page.getByRole("alert").first()).toContainText("meters are down");
  });
});

test.describe("meters", () => {
  const meterRoutes = (made: Record<string, unknown>[]) => (c: { method: string; path: string; body: Record<string, unknown> | null }) => {
    if (c.method === "POST" && c.path === "/v1/meters") {
      made.push(c.body ?? {});
      return { status: 201, body: { ...c.body, version: 3 } };
    }
    return undefined;
  };

  test("the meters are a table, a row opens the meter with its series and its heaviest users", async ({ page }) => {
    const gw = gateway();
    await open(page, gw);
    await page.getByTestId("usage-tab-meters").click();
    const row = page.getByTestId("meter-api_calls");
    await expect(row).toContainText("Sum");
    await expect(row).toContainText("255");
    await expect(page.getByTestId("meter-storage_gb")).toContainText("Highest value");
    await row.click();
    const detail = page.getByTestId("meter-detail");
    await expect(detail).toContainText("count");
    await expect(detail.getByTestId("usage-chart")).toBeVisible();
    await expect(detail).toContainText("cus_busy");
    await page.getByTestId("detail-next").click();
    await expect(page.getByTestId("meter-detail")).toContainText("tier = hot | warm");
  });

  test("a new meter is made in a sheet, and a bad code is refused before anything is sent", async ({ page }) => {
    const made: Record<string, unknown>[] = [];
    const gw = gateway().on(meterRoutes(made));
    await open(page, gw);
    await page.getByTestId("usage-tab-meters").click();
    await page.getByTestId("meters-new").click();
    const form = page.getByRole("dialog");
    await form.getByLabel("Code", { exact: true }).fill("Bad Code");
    await form.getByLabel("Unit").fill("tokens");
    await form.getByLabel("Value property").fill("n");
    await form.getByRole("button", { name: "Create meter" }).click();
    await expect(form.getByRole("alert")).toContainText("lowercase letters");
    expect(made).toHaveLength(0);

    await form.getByLabel("Code", { exact: true }).fill("tokens");
    await form.getByLabel("Aggregation").selectOption("AGGREGATION_COUNT");
    await expect(form.getByLabel("Value property")).toHaveCount(0);
    await form.getByRole("button", { name: "Create meter" }).click();
    await expect.poll(() => made.length).toBe(1);
    expect(made[0]).toEqual({ code: "tokens", aggregation: "AGGREGATION_COUNT", unit_label: "tokens", quantity_scale: 0 });
    await expect(form).toBeHidden();
  });

  test("the next version of a meter keeps its code and scale and starts from its settings", async ({ page }) => {
    const made: Record<string, unknown>[] = [];
    const gw = gateway().on(meterRoutes(made));
    await open(page, gw);
    await page.getByTestId("usage-tab-meters").click();
    await rowAction(page.getByTestId("meter-storage_gb"), "New version");
    const form = page.getByRole("dialog", { name: "New version of storage_gb" });
    await expect(form.getByLabel("Code", { exact: true })).toBeDisabled();
    await expect(form.getByLabel("Code", { exact: true })).toHaveValue("storage_gb");
    await expect(form.getByLabel("Decimal places")).toBeDisabled();
    await expect(form.getByLabel("Decimal places")).toHaveValue("3");
    await expect(form.getByLabel("Property 1")).toHaveValue("tier");
    await expect(form.getByLabel("Values 1")).toHaveValue("hot, warm");
    await form.getByLabel("Unit").fill("GB-h");
    await form.getByRole("button", { name: "Save new version" }).click();
    await expect.poll(() => made.length).toBe(1);
    expect(made[0]).toEqual({ code: "storage_gb", aggregation: "AGGREGATION_MAX", unit_label: "GB-h", quantity_scale: 3, value_property: "size", filter: { tier: { values: ["hot", "warm"] } } });
  });

  test("a meter that counts distinct values or takes the latest value is made in the same sheet", async ({ page }) => {
    const made: Record<string, unknown>[] = [];
    const gw = gateway().on(meterRoutes(made));
    await open(page, gw);
    await page.getByTestId("usage-tab-meters").click();
    await page.getByTestId("meters-new").click();
    const form = page.getByRole("dialog");
    await form.getByLabel("Code", { exact: true }).fill("active_users");
    await form.getByLabel("Unit").fill("users");
    await form.getByLabel("Decimal places").fill("3");
    await form.getByLabel("Aggregation").selectOption("AGGREGATION_UNIQUE");
    // A count of distinct values has no decimal places, whatever was typed before, and the property is the one to count.
    await expect(form.getByLabel("Decimal places")).toBeDisabled();
    await expect(form.getByLabel("Decimal places")).toHaveValue("0");
    await expect(form.getByText("Counts the different values of an event property")).toBeVisible();
    await form.getByRole("button", { name: "Create meter" }).click();
    await expect(form.getByRole("alert")).toContainText("Name the event property whose distinct values are counted");
    expect(made).toHaveLength(0);
    await form.getByLabel("Property to count").fill("user_id");
    await form.getByRole("button", { name: "Create meter" }).click();
    await expect.poll(() => made.length).toBe(1);
    expect(made[0]).toEqual({ code: "active_users", aggregation: "AGGREGATION_UNIQUE", unit_label: "users", quantity_scale: 0, value_property: "user_id" });
    await expect(form).toBeHidden();

    await page.getByTestId("meters-new").click();
    const second = page.getByRole("dialog");
    await second.getByLabel("Code", { exact: true }).fill("seats_now");
    await second.getByLabel("Unit").fill("seats");
    await second.getByLabel("Aggregation").selectOption("AGGREGATION_LAST");
    await expect(second.getByText("Takes the quantity of the most recent event")).toBeVisible();
    await expect(second.getByLabel("Decimal places")).toBeEnabled();
    await second.getByLabel("Value property").fill("seats");
    await second.getByRole("button", { name: "Create meter" }).click();
    await expect.poll(() => made.length).toBe(2);
    expect(made[1]).toEqual({ code: "seats_now", aggregation: "AGGREGATION_LAST", unit_label: "seats", quantity_scale: 0, value_property: "seats" });
  });

  test("a new version cannot change into or out of distinct values or latest value", async ({ page }) => {
    const made: Record<string, unknown>[] = [];
    const gw = gateway().on(withRowShaped).on(meterRoutes(made));
    await open(page, gw);
    await page.getByTestId("usage-tab-meters").click();

    await rowAction(page.getByTestId("meter-active_users"), "New version");
    const unique = page.getByRole("dialog", { name: "New version of active_users" });
    await expect(unique.getByLabel("Aggregation").locator("option")).toHaveText(["Distinct values"]);
    await expect(unique.getByTestId("aggregation-locked")).toContainText("Fixed for this code");
    await expect(unique.getByLabel("Decimal places")).toBeDisabled();
    await unique.getByLabel("Property to count").fill("account_id");
    await unique.getByRole("button", { name: "Save new version" }).click();
    await expect.poll(() => made.length).toBe(1);
    expect(made[0]).toEqual({ code: "active_users", aggregation: "AGGREGATION_UNIQUE", unit_label: "users", quantity_scale: 0, value_property: "account_id" });
    await expect(unique).toBeHidden();

    await rowAction(page.getByTestId("meter-storage_gb"), "New version");
    const peak = page.getByRole("dialog", { name: "New version of storage_gb" });
    await expect(peak.getByLabel("Aggregation").locator("option")).toHaveText(["Sum", "Count of events", "Highest value"]);
    await expect(peak.getByTestId("aggregation-locked")).toContainText("can switch between sum, count and maximum");
  });

  test("the overview does not pretend to total a meter whose events do not add up", async ({ page }) => {
    const gw = gateway().on(withRowShaped);
    await open(page, gw);
    await expect(page.getByTestId("meter-total-api_calls")).toHaveText("255");
    // Distinct values of two days overlap and a bucket holds many events: the buckets of raw events give no total for these, so it is a dash.
    await expect(page.getByTestId("meter-total-active_users")).toHaveText("—");
    await expect(page.getByTestId("meter-total-seats_now")).toHaveText("—");
    await expect(page.getByTestId("meter-delta-active_users")).toHaveCount(0);
  });

  test("a viewer-style refusal from the API is shown in the sheet", async ({ page }) => {
    const gw = gateway().on((c) => (c.method === "POST" && c.path === "/v1/meters" ? { status: 400, body: { error: { code: "INVALID_ARGUMENT", message: "quantity_scale is fixed per meter code" } } } : undefined));
    await open(page, gw);
    await page.getByTestId("usage-tab-meters").click();
    await page.getByTestId("meters-new").click();
    const form = page.getByRole("dialog");
    await form.getByLabel("Code", { exact: true }).fill("x_meter");
    await form.getByLabel("Unit").fill("x");
    await form.getByLabel("Value property").fill("n");
    await form.getByRole("button", { name: "Create meter" }).click();
    await expect(form.getByRole("alert")).toContainText("quantity_scale is fixed");
  });

  test("meters can be searched and filtered by how they count", async ({ page }) => {
    const gw = gateway();
    await open(page, gw);
    await page.getByTestId("usage-tab-meters").click();
    await pickFilter(page, "Aggregation", "Highest value");
    await page.keyboard.press("Escape");
    await expect(page.getByTestId("meter-api_calls")).toHaveCount(0);
    await expect(page.getByTestId("meter-storage_gb")).toBeVisible();
    await page.getByRole("button", { name: "Clear filters" }).click();
    await page.getByRole("searchbox", { name: "Search meters" }).fill("api");
    await expect(page.getByTestId("meter-storage_gb")).toHaveCount(0);
  });
});

test.describe("customers", () => {
  test("customers with what they used and where they stand against their limits; the limits are read for the page only", async ({ page }) => {
    const gw = gateway();
    await open(page, gw);
    await page.getByTestId("usage-tab-customers").click();
    const rows = page.locator('[data-testid^="usage-customer-"]');
    await expect(rows).toHaveCount(3);
    // The busiest first; a customer without usage last.
    await expect(rows.nth(0)).toContainText("cus_busy");
    await expect(rows.nth(0)).toContainText("api_calls 1,200");
    await expect(rows.nth(0)).toContainText("Over the limit");
    await expect(rows.nth(1)).toContainText("Near the limit");
    await expect(rows.nth(2)).toContainText("cus_idle");
    await expect(rows.nth(2)).toContainText("No limit");
    expect(gw.called("GET", /\/entitlements$/).map((c) => c.path).sort()).toEqual(["/v1/customers/cus_busy/entitlements", "/v1/customers/cus_idle/entitlements", "/v1/customers/cus_near/entitlements"]);
  });

  test("filtered by a meter and searched", async ({ page }) => {
    const gw = gateway();
    await open(page, gw);
    await page.getByTestId("usage-tab-customers").click();
    await pickFilter(page, "Meter", "storage_gb");
    await page.keyboard.press("Escape");
    const rows = page.locator('[data-testid^="usage-customer-"]');
    await expect(rows).toHaveCount(1);
    await expect(rows.first()).toContainText("cus_near");
    await page.getByRole("button", { name: "Clear filters" }).click();
    await pickFilter(page, "Usage", "Without usage");
    await page.keyboard.press("Escape");
    await expect(rows).toHaveCount(1);
    await expect(rows.first()).toContainText("cus_idle");
    await page.getByRole("button", { name: "Clear filters" }).click();
    await page.getByRole("searchbox", { name: "Search customers" }).fill("busy@");
    await expect(rows).toHaveCount(1);
    await expect(rows.first()).toContainText("cus_busy");
  });

  test("a row opens the customer's usage against each limit and the series of a meter", async ({ page }) => {
    const gw = gateway();
    await open(page, gw);
    await page.getByTestId("usage-tab-customers").click();
    await page.getByTestId("usage-customer-cus_near").click();
    const detail = page.getByTestId("usage-customer-detail");
    await expect(detail).toContainText("near@example.com");
    await expect(detail.getByTestId("detail-meter-api_calls")).toContainText("Near the limit");
    await expect(detail.getByTestId("detail-meter-api_calls")).toContainText("850 / 1,000");
    await expect(detail.getByTestId("detail-meter-storage_gb")).toContainText("2.5 / 10");
    await expect(detail.getByTestId("detail-meter-storage_gb")).toContainText("Within the limit");
    // The series is the customer's own: the call names the customer.
    await expect.poll(() => gw.called("GET", "/v1/analytics/usage").some((c) => c.query.get("customer") === "cus_near")).toBe(true);
    await expect(detail.getByTestId("usage-chart")).toBeVisible();
    await detail.getByLabel("Meter").selectOption("storage_gb");
    await expect.poll(() => gw.called("GET", "/v1/analytics/usage").some((c) => c.query.get("customer") === "cus_near" && c.query.get("meter") === "storage_gb")).toBe(true);
  });
});

test("no tab of the usage page scrolls sideways at 375 px", async ({ page }) => {
  await page.setViewportSize({ width: 375, height: 812 });
  const gw = gateway();
  await open(page, gw);
  for (const tab of ["overview", "meters", "customers"]) {
    await page.getByTestId(`usage-tab-${tab}`).click();
    await page.waitForLoadState("networkidle");
    const o = await page.evaluate(() => ({ sw: document.documentElement.scrollWidth, cw: document.documentElement.clientWidth }));
    expect(o.sw, `horizontal overflow on the ${tab} tab`).toBeLessThanOrEqual(o.cw);
  }
});
