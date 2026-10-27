import { expect, test, type Page } from "@playwright/test";
import { MockGateway } from "./gateway";
import { APP, LIVE, SANDBOX, productGateway, signInAsAda } from "./product";
import { pickCurrency, pickLanguage } from "./ui";

// The frame of the product: the sidebar (open by default, follows the route, folds, remembered), the breadcrumbs, the app and environment switchers,
// privacy mode, the searchable currency and time zone fields, pointer cursors and the layout from phone to desktop. The gateway is mocked.
const shots = process.env.SHOTS;
async function shoot(page: Page, name: string): Promise<void> {
  if (shots) await page.screenshot({ path: `${shots}/${name}.png` });
}

/** The tenant pages ask the tenant API for lists; an empty answer is enough for the frame. */
function withTenantApi(gw: MockGateway): MockGateway {
  return gw.on((c) => (c.path.startsWith("/v1/") ? { body: { customers: [], plans: [], subscriptions: [], invoices: [], webhook_endpoints: [], deliveries: [] } } : undefined));
}

async function open(page: Page, path = "/app"): Promise<MockGateway> {
  const gw = withTenantApi(productGateway(new MockGateway()).gw);
  await gw.install(page);
  await signInAsAda(page);
  if (path !== "/app") await page.goto(path);
  return gw;
}

test.describe("sidebar on a desktop", () => {
  test.use({ viewport: { width: 1440, height: 900 } });

  test("is open by default with icons and labels, and its list follows the page", async ({ page }) => {
    await open(page);
    const sidebar = page.getByTestId("sidebar");
    await expect(sidebar).toBeVisible();
    await expect(sidebar).toHaveAttribute("data-collapsed", "false");
    const nav = sidebar.getByRole("navigation", { name: "Main navigation" });
    // An app is always selected: signing in lands inside it, and the sidebar is that app's pages with the organisation's at the bottom.
    await page.waitForURL(`**/app/apps/${APP}?*`);
    await expect(nav.getByRole("link", { name: "Dashboard" })).toHaveAttribute("aria-current", "page");
    await expect(nav.getByRole("link", { name: "Members" })).toBeVisible();
    await expect(nav.getByRole("link", { name: "Settings", exact: true })).toBeVisible();
    expect((await sidebar.boundingBox())?.width).toBeGreaterThan(200);
    await shoot(page, "30-sidebar-org");

    // The organisation's pages keep the app's sidebar, with their own item current.
    await nav.getByRole("link", { name: "Members" }).click();
    await page.waitForURL("**/app/members?*");
    await expect(nav.getByRole("link", { name: "Members" })).toHaveAttribute("aria-current", "page");
    await expect(nav.getByRole("link", { name: "Customers" })).toBeVisible();

    // App level: the active item follows the route.
    await page.goto(`/app/apps/${APP}`);
    await expect(nav.getByRole("link", { name: "Dashboard" })).toHaveAttribute("aria-current", "page");
    for (const name of ["Customers", "Subscriptions", "Credits", "Packages", "Pricing and tax", "Payment links", "Accounting", "Reports", "API keys", "Webhooks", "App settings"]) {
      await expect(nav.getByRole("link", { name }), name).toBeVisible();
    }
    await nav.getByRole("link", { name: "Customers" }).click();
    await page.waitForURL(`**/app/t/${LIVE}/customers*`);
    await expect(nav.getByRole("link", { name: "Customers" })).toHaveAttribute("aria-current", "page");
    await expect(nav.getByRole("link", { name: "Dashboard" })).not.toHaveAttribute("aria-current", "page");
    await expect(page.getByTestId("page-title")).toHaveText("Customers");
    // The breadcrumbs say where this is: organisation, app, page.
    const crumbs = page.getByRole("navigation", { name: "Breadcrumbs" });
    await expect(crumbs).toContainText("Ada Labs");
    await expect(crumbs).toContainText("Web shop");
    await expect(crumbs.getByText("Customers")).toHaveAttribute("aria-current", "page");
    await nav.getByRole("link", { name: "API keys" }).click();
    await page.waitForURL(`**/app/apps/${APP}/keys*`);
    await expect(nav.getByRole("link", { name: "API keys" })).toHaveAttribute("aria-current", "page");
    await nav.getByRole("link", { name: "Webhooks" }).click();
    await expect(nav.getByRole("link", { name: "Webhooks" })).toHaveAttribute("aria-current", "page");
    await shoot(page, "31-sidebar-app");
  });

  test("the environment switch keeps the person on the same page of the other environment, after asking", async ({ page }) => {
    await open(page, `/app/t/${LIVE}/subscriptions`);
    const nav = page.getByTestId("sidebar").getByRole("navigation", { name: "Main navigation" });
    await expect(nav.getByRole("link", { name: "Subscriptions" })).toHaveAttribute("aria-current", "page");
    await page.getByTestId("env-switch").click();
    await page.getByTestId("env-switch-confirm").click();
    await page.waitForURL(`**/app/t/${SANDBOX}/subscriptions*`);
    await expect(nav.getByRole("link", { name: "Subscriptions" })).toHaveAttribute("aria-current", "page");
    // The app switcher lists the organisation's apps; choosing the same app stays on the same page.
    await page.getByTestId("app-switcher").click();
    await page.getByRole("menuitem", { name: "Web shop" }).click();
    await expect(page).toHaveURL(new RegExp(`/app/t/${SANDBOX}/subscriptions`));
  });

  test("folds to an icon rail and the choice is remembered", async ({ page }) => {
    await open(page);
    const sidebar = page.getByTestId("sidebar");
    await page.getByTestId("sidebar-toggle").click();
    await expect(sidebar).toHaveAttribute("data-collapsed", "true");
    await expect.poll(async () => (await sidebar.boundingBox())?.width ?? 999).toBeLessThan(100);
    // Labels are still there for assistive technology and as tooltips.
    await expect(sidebar.getByRole("link", { name: "Settings", exact: true })).toHaveAttribute("title", "Settings");
    await shoot(page, "32-sidebar-collapsed");
    expect(await page.evaluate(() => window.localStorage.getItem("payrudder.sidebar.collapsed"))).toBe("1");
    await page.reload();
    await expect(page.getByTestId("sidebar")).toHaveAttribute("data-collapsed", "true");
    await page.getByTestId("sidebar-toggle").click();
    await expect(page.getByTestId("sidebar")).toHaveAttribute("data-collapsed", "false");
    expect(await page.evaluate(() => window.localStorage.getItem("payrudder.sidebar.collapsed"))).toBeNull();
  });

  test("a browser that blocks storage still gets an open sidebar", async ({ page, context }) => {
    await context.addInitScript(() => {
      Object.defineProperty(window, "localStorage", { get: () => { throw new Error("blocked"); } });
    });
    await open(page);
    // Nothing can be remembered, so the home's tour starts by itself; it is out of the way of what this test is about.
    await page.getByTestId("tour-skip").click();
    await expect(page.getByTestId("sidebar")).toHaveAttribute("data-collapsed", "false");
    await page.getByTestId("sidebar-toggle").click();
    await expect(page.getByTestId("sidebar")).toHaveAttribute("data-collapsed", "true");
  });
});

test.describe("privacy mode", () => {
  test.use({ viewport: { width: 1440, height: 900 } });

  test("one switch masks every amount, survives navigation and reload, and turns off again", async ({ page }) => {
    await open(page, `/app/apps/${APP}`);
    const revenue = page.getByTestId("kpi-revenue-value");
    await expect(revenue).toContainText(/TRY\s[\d,]+\.\d\d/);
    await page.getByTestId("privacy-toggle").click();
    await expect(page.getByTestId("privacy-toggle")).toHaveAttribute("aria-pressed", "true");
    await expect(revenue).toHaveText(/^TRY\s••••$/);
    // Nothing of the amounts is left anywhere on the page (the text itself is masked, not hidden by a style).
    const text = await page.locator("main").innerText();
    expect(text).not.toMatch(/TRY\s[\d,]+\.\d\d/);
    expect(text).not.toMatch(/€[\d,]+\.\d\d/);
    await shoot(page, "33-privacy");
    // It survives navigation, reload and a new page of the app.
    await page.getByTestId("sidebar").getByRole("link", { name: "Customers" }).click();
    await page.getByTestId("sidebar").getByRole("link", { name: "Dashboard" }).click();
    await expect(page.getByTestId("kpi-revenue-value")).toHaveText(/^TRY\s••••$/);
    await page.reload();
    await expect(page.getByTestId("kpi-revenue-value")).toHaveText(/^TRY\s••••$/);
    await expect(page.locator("html")).toHaveAttribute("data-privacy", "");
    expect(await page.evaluate(() => window.localStorage.getItem("payrudder.privacy"))).toBe("1");
    // Alt+P is the shortcut.
    await page.keyboard.press("Alt+KeyP");
    await expect(page.getByTestId("kpi-revenue-value")).toContainText(/TRY\s[\d,]+\.\d\d/);
    await page.keyboard.press("Alt+KeyP");
    await expect(page.getByTestId("kpi-revenue-value")).toHaveText(/^TRY\s••••$/);
    await page.getByTestId("privacy-toggle").click();
    await expect(page.getByTestId("kpi-revenue-value")).toContainText(/TRY\s[\d,]+\.\d\d/);
    expect(await page.evaluate(() => window.localStorage.getItem("payrudder.privacy"))).toBeNull();
  });

  test("customer e-mail addresses are masked too", async ({ page }) => {
    const gw = productGateway(new MockGateway()).gw;
    gw.on((c) => (c.path === "/v1/customers" ? { body: { customers: [{ id: "c1", external_id: "cus_1", name: "Acme Ltd", email: "billing@acme.test" }] } } : undefined));
    await gw.install(page);
    await signInAsAda(page);
    await page.goto(`/app/t/${LIVE}/customers`);
    await expect(page.getByRole("cell", { name: "billing@acme.test" })).toBeVisible();
    await page.getByTestId("privacy-toggle").click();
    await expect(page.getByRole("cell", { name: "b•••@acme.test" })).toBeVisible();
    await expect(page.getByText("billing@acme.test")).toHaveCount(0);
  });
});

test.describe("currency and time zone fields", () => {
  test.use({ viewport: { width: 1440, height: 900 } });

  test("search every currency and every zone, and send what was chosen", async ({ page }) => {
    const gw = await open(page, "/app?app=all");
    await page.getByRole("button", { name: "Create app" }).first().click();
    const dialog = page.getByRole("dialog");
    await dialog.getByLabel("App name").fill("Yen shop");

    const currency = dialog.getByLabel("Currency");
    await currency.click();
    // The whole ISO 4217 list is offered, with names, not the seven of before.
    // It shows the first ten and says how many more there are.
    await expect(page.getByRole("option")).toHaveCount(10);
    await expect(page.locator('[data-testid="combobox-more"]:visible')).toContainText(/^1\d\d more/);
    await currency.fill("japanese");
    await expect(page.getByRole("option")).toHaveCount(1);
    await expect(page.getByRole("option", { name: /JPY · Japanese Yen/ })).toBeVisible();
    await currency.fill("dinar");
    await expect(page.getByRole("option", { name: /KWD · Kuwaiti Dinar/ })).toBeVisible();
    await expect(page.getByRole("option", { name: /BHD · Bahraini Dinar/ })).toBeVisible();
    await shoot(page, "34-currency-search");
    await pickCurrency(currency, "JPY");
    await expect(currency).toHaveValue("JPY");

    const zone = dialog.getByLabel("Time zone");
    await zone.click();
    await expect(page.locator('[data-testid="combobox-more"]:visible')).toContainText(/^[234]\d\d more/);
    await zone.fill("kathmandu");
    // Engines list "Asia/Katmandu"; the search knows the modern spelling.
    await expect(page.getByRole("option", { name: /Asia\/Kat/ })).toContainText("UTC+05:45");
    await shoot(page, "35-zone-search");
    await zone.press("Enter");
    await expect(zone).toHaveValue(/^Asia\/Kat(h)?mandu$/);
    // The keyboard works: arrows move, Enter picks, Escape closes.
    await zone.click();
    await zone.fill("tokyo");
    await page.keyboard.press("Enter");
    await expect(zone).toHaveValue("Asia/Tokyo");
    await zone.click();
    await zone.fill("kathmandu");
    await zone.press("Enter");

    await dialog.getByRole("button", { name: "Create app" }).click();
    await page.waitForURL(/\/app\/apps\//);
    expect(gw.called("POST", "/app/v1/apps")[0]?.body).toMatchObject({ name: "Yen shop", currency: "JPY", timezone: expect.stringMatching(/^Asia\/Kat(h)?mandu$/) });
  });
});

test.describe("pointer cursors", () => {
  test("everything clickable shows the pointer and a disabled control says not-allowed", async ({ page }) => {
    await open(page, "/app?app=all");
    const cursor = (loc: ReturnType<Page["locator"]>) => loc.evaluate((el) => getComputedStyle(el).cursor);
    expect(await cursor(page.getByTestId("privacy-toggle"))).toBe("pointer");
    expect(await cursor(page.getByTestId("user-menu"))).toBe("pointer");
    expect(await cursor(page.getByTestId("env-switch"))).toBe("pointer");
    expect(await cursor(page.getByTestId("sidebar").getByRole("link", { name: "Settings", exact: true }))).toBe("pointer");
    await page.getByRole("button", { name: "Create app" }).first().click();
    const dialog = page.getByRole("dialog");
    expect(await cursor(dialog.getByRole("button", { name: "Cancel" }))).toBe("pointer");
    expect(await cursor(dialog.getByRole("button", { name: "Create app" }))).toBe("not-allowed");
    await dialog.getByLabel("App name").fill("x");
    expect(await cursor(dialog.getByRole("button", { name: "Create app" }))).toBe("pointer");
  });
});

test.describe("responsive", () => {
  for (const [width, height, drawer] of [[360, 740, true], [375, 812, true], [768, 1024, true], [1024, 768, false], [1440, 900, false]] as const) {
    test(`${width} px: ${drawer ? "a drawer" : "the sidebar"}, nothing scrolls sideways`, async ({ page }) => {
      await page.setViewportSize({ width, height });
      await open(page, `/app/apps/${APP}`);
      await expect(page.getByTestId("kpi-revenue")).toBeVisible();
      const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
      expect(overflow).toBeLessThanOrEqual(0);
      if (drawer) {
        await expect(page.getByTestId("sidebar")).toBeHidden();
        const menu = page.getByTestId("menu-button");
        await expect(menu).toBeVisible();
        const box = await menu.boundingBox();
        expect(box?.height).toBeGreaterThanOrEqual(40);
      } else {
        await expect(page.getByTestId("sidebar")).toBeVisible();
        await expect(page.getByTestId("menu-button")).toBeHidden();
      }
      await page.goto(`/app/t/${LIVE}/customers`);
      await expect(page.getByTestId("page-title")).toBeVisible();
      expect(await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)).toBeLessThanOrEqual(0);
      await shoot(page, `36-responsive-${width}`);
    });
  }

  test("right to left: the sidebar sits on the right and the drawer opens from the right", async ({ page }) => {
    await open(page, `/app/apps/${APP}`);
    await pickLanguage(page, "ar");
    await expect(page.locator("html")).toHaveAttribute("dir", "rtl");
    const box = await page.getByTestId("sidebar").boundingBox();
    const width = page.viewportSize()?.width ?? 1280;
    expect(box && box.x > width / 2).toBe(true);
    await shoot(page, "37-rtl");
    await page.setViewportSize({ width: 390, height: 844 });
    await page.getByTestId("menu-button").click();
    // The drawer slides in from the right edge: once it has settled, its far edge is the edge of the window.
    await expect.poll(async () => {
      const b = await page.getByTestId("drawer").boundingBox();
      return b ? Math.round(b.x + b.width) : 0;
    }).toBe(390);
    await shoot(page, "38-rtl-drawer");
  });
});
