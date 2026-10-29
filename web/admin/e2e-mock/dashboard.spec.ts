import { expect, test, type Page } from "@playwright/test";
import { dashboardFixture } from "./dashboard";
import { MockGateway, baseTenant, openTenantPage } from "./gateway";
import { APP, openAllApps, productGateway, signInAsAda } from "./product";
import { pickLanguage, pickTheme } from "./ui";

// The dashboards: a period and the one before it, one block per currency, KPIs with their change, the revenue chart, payments, top lists, usage,
// renewals, activity and health; the empty, loading and error states; the organisation's dashboard over all apps; the operator's twin. The gateway is mocked.
const shots = process.env.SHOTS;
async function shoot(page: Page, name: string): Promise<void> {
  if (shots) await page.screenshot({ path: `${shots}/${name}.png`, fullPage: true });
}

const dash = `/app/apps/${APP}`;

test.describe("one app", () => {
  test.use({ viewport: { width: 1440, height: 900 } });

  test("one tab per currency; each tab has only that currency's money, with the currency's own digits", async ({ page }) => {
    await productGateway(new MockGateway()).gw.install(page);
    await signInAsAda(page);
    await page.goto(dash);
    const tabs = page.getByTestId("currency-tabs");
    for (const c of ["TRY", "EUR", "USD", "JPY", "KWD"]) await expect(tabs.getByRole("tab", { name: new RegExp(`^${c}`) })).toBeVisible();
    await expect(page.getByTestId("currency-tab-TRY")).toHaveAttribute("aria-selected", "true");
    await expect(page.getByTestId("kpi-open-value")).toHaveText("TRY 34,600.00");

    await page.getByTestId("currency-tab-JPY").click();
    await expect(page).toHaveURL(/cur=JPY/);
    await expect(page.getByTestId("kpi-revenue-value")).toContainText("¥");
    await expect(page.getByTestId("kpi-revenue-value")).not.toContainText(".");
    await expect(page.getByTestId("kpi-mrr-value")).toHaveText("¥195,000");
    await expect(page.getByTestId("top-customers")).toContainText("Yuki KK");
    await expect(page.getByTestId("top-customers")).not.toContainText("Acme");

    await page.getByTestId("currency-tab-KWD").click();
    await expect(page.getByTestId("kpi-open-value")).toHaveText("KWD 8.500"); // three digits
    await expect(page.getByTestId("kpi-failed-rate-value")).toHaveText("50%");
    await page.getByTestId("currency-tab-EUR").click();
    await expect(page.getByTestId("kpi-mrr-value")).toContainText("€");
    await expect(page.getByTestId("aging")).toContainText("2 invoices");
    // The tab is in the address: reload keeps it, and the arrow keys move between the tabs.
    await page.reload();
    await expect(page.getByTestId("currency-tab-EUR")).toHaveAttribute("aria-selected", "true");
    await page.getByTestId("currency-tab-EUR").focus();
    await page.keyboard.press("ArrowRight");
    await expect(page.getByTestId("currency-tab-USD")).toHaveAttribute("aria-selected", "true");
    await shoot(page, "40-dashboard-usd");
  });

  test("the period: presets, a custom range, and the comparison switched off", async ({ page }) => {
    const { gw } = productGateway(new MockGateway());
    await gw.install(page);
    await signInAsAda(page);
    await page.goto(dash);
    const asked = () => gw.called("GET", `/app/v1/apps/${APP}/dashboard`).map((c) => Object.fromEntries(c.query.entries()));
    await expect(page.getByTestId("kpi-revenue-delta")).toBeVisible();
    await expect(page.getByTestId("revenue-chart")).toBeVisible();
    await expect(page.getByText("Previous period", { exact: true })).toBeVisible();

    await page.getByText("7 days", { exact: true }).click();
    await expect.poll(() => asked().some((q) => q.period === "7d" && q.compare === "1")).toBe(true);
    await expect(page).toHaveURL(/period=7d/);
    await page.getByText("This year", { exact: true }).click();
    await expect.poll(() => asked().some((q) => q.period === "ytd")).toBe(true);

    await page.getByText("Custom", { exact: true }).click();
    await page.getByTestId("dash-from").fill("2026-09-01");
    await page.getByTestId("dash-to").fill("2026-09-30");
    await expect.poll(() => asked().some((q) => q.period === "custom" && q.from === "2026-09-01" && q.to === "2026-09-30")).toBe(true);
    await expect(page).toHaveURL(/period=custom.*from=2026-09-01.*to=2026-09-30|from=2026-09-01.*period=custom/);
    // A first day after the last is said, not sent.
    await page.getByTestId("dash-from").fill("2026-10-05");
    await expect(page.getByRole("alert").filter({ hasText: "after the last" })).toBeVisible();

    await page.getByTestId("dash-compare").click();
    await expect.poll(() => asked().some((q) => q.compare === "0")).toBe(true);
    await expect(page.getByTestId("kpi-revenue-delta")).toHaveCount(0);
    await expect(page.getByText("Previous period", { exact: true })).toHaveCount(0);
    await expect(page).toHaveURL(/compare=off/);
  });

  test("a figure says how it moved, and whether that is good news", async ({ page }) => {
    await productGateway(new MockGateway()).gw.install(page);
    await signInAsAda(page);
    await page.goto(dash);
    const good = page.getByTestId("kpi-revenue-delta");
    await expect(good).toHaveClass(/success/); // revenue up
    await expect(good.locator(".sr-only")).toContainText("compared with the previous period");
    await expect(page.getByTestId("kpi-failed-rate-delta")).toHaveClass(/destructive/); // failed payments up is bad news
    await expect(page.getByTestId("kpi-churn-delta")).toHaveClass(/success/); // churn down is good news
    await expect(page.getByTestId("kpi-revenue-delta")).toHaveAttribute("title", /Before: TRY/);
  });

  test("the chart shows the day under the pointer, and the arrow keys do the same", async ({ page }) => {
    await productGateway(new MockGateway()).gw.install(page);
    await signInAsAda(page);
    await page.goto(`${dash}?period=7d`);
    const chart = page.getByTestId("revenue-chart");
    await expect(chart).toHaveAttribute("aria-label", /Net revenue from .* to .*: TRY/);
    const box = (await chart.boundingBox())!;
    await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
    const tip = page.getByTestId("chart-tooltip");
    await expect(tip).toBeVisible();
    await expect(tip).toContainText("TRY");
    await expect(tip).toContainText("Oct");
    await shoot(page, "41-chart-tooltip");
    await page.mouse.move(0, 0);
    await expect(tip).toBeHidden();
    await chart.focus();
    await expect(tip).toBeVisible();
    const first = await tip.innerText();
    await page.keyboard.press("ArrowLeft");
    await expect.poll(async () => tip.innerText()).not.toBe(first);
    await page.keyboard.press("Escape");
    await expect(tip).toBeHidden();
  });

  test("usage against limits, renewals and activity", async ({ page }) => {
    await productGateway(new MockGateway()).gw.install(page);
    await signInAsAda(page);
    await page.goto(dash);
    const usage = page.getByTestId("usage");
    await expect(usage).toContainText("api_calls");
    await expect(usage).toContainText("1 over the limit");
    await expect(usage).toContainText("2 close to it");
    await expect(usage.getByTestId("usage-all")).toHaveAttribute("href", /\/usage/);
    await expect(usage.getByRole("progressbar", { name: "globex" })).toHaveAttribute("aria-valuenow", "100");
    await expect(page.getByTestId("renewals")).toContainText("acme");
    await expect(page.getByTestId("renewals")).toContainText("Trial");
    const feed = page.getByTestId("activity");
    await expect(feed).toContainText("Invoice INV-2026-000412");
    await expect(feed).toContainText("Refund");
    await expect(feed).toContainText("-$49.00");
  });

  test("nothing yet: an empty state with what to do next", async ({ page }) => {
    const body = { ...dashboardFixture({}), currencies: [{ currency: "TRY", revenue: {}, series: [], top_customers: [], top_packages: [], aging: [], renewals: [] }], customers: { total: 0, new: 0, prev_new: 0 }, activity: [], usage: { period_id: "2026-10", sampled: 0, meters: [] } };
    await productGateway(new MockGateway(), { dashboard: body }).gw.install(page);
    await signInAsAda(page);
    await page.goto(dash);
    const empty = page.getByTestId("dash-empty");
    await expect(empty).toContainText("Nothing to show yet");
    await expect(empty.getByRole("link", { name: "Customers" })).toBeVisible();
    await shoot(page, "42-dashboard-empty");
  });

  test("while it loads the page has its shape, and when it fails there is a way to try again", async ({ page }) => {
    let armed = false;
    let failing = true;
    const answer = (q: Record<string, string>) => {
      // Signing in lands on a dashboard too: only what is asked once the page under test is open counts. It fails until the test says it stops: a count of failures
      // ("the first three calls") raced the query's own retries, and the page showed no error when a retry fell on the first good answer.
      if (!armed || !failing) return { body: dashboardFixture(q) };
      return { status: 500, body: { error: { code: "INTERNAL", key: "common.internal", message: "boom" } } };
    };
    await productGateway(new MockGateway(), { dashboard: answer }).gw.install(page);
    await signInAsAda(page);
    armed = true;
    await page.goto(dash);
    const err = page.getByTestId("dash-error");
    await expect(err).toBeVisible();
    failing = false;
    await err.getByRole("button", { name: "Try again" }).click();
    await expect(page.getByTestId("kpi-revenue-value")).toBeVisible();
    await expect(err).toHaveCount(0);
  });

  test("a slow answer shows skeletons, not a blank page", async ({ page }) => {
    const gw = productGateway(new MockGateway(), { dashboard: (q) => ({ body: dashboardFixture(q) }) }).gw;
    await gw.install(page);
    // Registered after the mock gateway, so it sees the request first and holds it back before the mock answers.
    await page.route("**/gw/app/v1/apps/*/dashboard*", async (route) => {
      await new Promise((r) => setTimeout(r, 900));
      await route.fallback();
    });
    await signInAsAda(page);
    await page.goto(dash);
    await expect(page.getByTestId("dash-skeleton")).toBeVisible();
    await expect(page.getByTestId("kpi-revenue-value")).toBeVisible();
    await expect(page.getByTestId("dash-skeleton")).toHaveCount(0);
  });
});

test.describe("the organisation", () => {
  test.use({ viewport: { width: 1440, height: 900 } });

  test("all apps over one period, per currency, with the apps listed under it", async ({ page }) => {
    const { gw } = productGateway(new MockGateway());
    await gw.install(page);
    await signInAsAda(page);
    await openAllApps(page);
    const org = page.getByTestId("org-dashboard");
    await expect(org).toBeVisible();
    await expect(org.getByTestId("kpi-org-revenue-value")).toContainText("TRY");
    await expect(org.getByTestId("org-by-app")).toContainText("Web shop");
    await org.getByTestId("currency-tab-EUR").click();
    await expect(page).toHaveURL(/cur=EUR/);
    await expect(org.getByTestId("kpi-org-revenue-value")).toContainText("€");
    await org.getByText("7 days", { exact: true }).click();
    await expect.poll(() => gw.called("GET", `/app/v1/apps/${APP}/dashboard`).some((c) => c.query.get("period") === "7d")).toBe(true);
    // The environment switch is in the header of the frame now (it is on every page), not in the dashboard.
    await page.getByTestId("env-switch").click();
    await page.getByTestId("env-switch-confirm").click();
    await expect.poll(() => gw.called("GET", `/app/v1/apps/${APP}/dashboard`).some((c) => c.query.get("environment") === "sandbox")).toBe(true);
    // The apps themselves are still listed below the totals.
    await expect(page.getByRole("link", { name: "Web shop" }).first()).toBeVisible();
    await shoot(page, "43-org-dashboard");
  });
});

test.describe("for the operator", () => {
  test.use({ viewport: { width: 1440, height: 900 } });

  test("a tenant's page opens with its dashboard", async ({ page }) => {
    const gw = baseTenant(new MockGateway());
    await gw.install(page);
    await openTenantPage(page, "");
    await expect(page.getByTestId("dashboard")).toBeVisible();
    await expect(page.getByTestId("kpi-revenue-value")).toContainText("TRY");
    expect(gw.called("GET", /\/admin\/v1\/tenants\/.*\/dashboard$/).length).toBeGreaterThan(0);
    await page.getByTestId("currency-tab-USD").click();
    await expect(page.getByTestId("kpi-mrr-value")).toContainText("$");
  });
});

test.describe("on every screen", () => {
  for (const [w, h] of [[360, 740], [768, 1024], [1024, 768], [1440, 900]] as const) {
    test(`${w} px: nothing scrolls sideways, in every currency`, async ({ page }) => {
      await page.setViewportSize({ width: w, height: h });
      await productGateway(new MockGateway()).gw.install(page);
      await signInAsAda(page);
      for (const cur of ["TRY", "JPY", "KWD"]) {
        await page.goto(`${dash}?cur=${cur}`);
        await expect(page.getByTestId(`kpi-revenue-value`)).toBeVisible();
        await expect(page.getByTestId("revenue-chart")).toBeVisible();
        expect(await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth), `${cur} at ${w}`).toBeLessThanOrEqual(0);
      }
      await page.goto(`${dash}?period=custom&from=2026-09-01&to=2026-10-01`);
      await expect(page.getByTestId("dash-from")).toBeVisible();
      expect(await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)).toBeLessThanOrEqual(0);
      await page.goto("/app?app=all");
      await expect(page.getByTestId("org-dashboard")).toBeVisible();
      expect(await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)).toBeLessThanOrEqual(0);
      await shoot(page, `44-dashboard-${w}`);
    });
  }

  test("right to left, dark, private: it reads and the amounts are hidden", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await productGateway(new MockGateway()).gw.install(page);
    await signInAsAda(page);
    await page.goto(dash);
    await pickLanguage(page, "ar");
    await expect(page.locator("html")).toHaveAttribute("dir", "rtl");
    await pickTheme(page, "dark");
    await page.getByTestId("privacy-toggle").click();
    await expect(page.getByTestId("kpi-revenue-value")).toContainText("••••");
    expect(await page.getByTestId("dashboard").innerText()).not.toMatch(/\d{1,3}(,\d{3})+\.\d\d/);
    expect(await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)).toBeLessThanOrEqual(0);
    await shoot(page, "45-dashboard-rtl-dark-private");
  });
});
