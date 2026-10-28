import { expect, test, type Page } from "@playwright/test";
import { dashboardFixture } from "./dashboard";
import { MockGateway } from "./gateway";
import { ACCOUNT, APP, LIVE, SANDBOX, app, productGateway, signInAsAda, tenant } from "./product";
import { pickLanguage, pickTheme } from "./ui";

// The app shell (phase 51): the scope in the address (?app=<id|all>), the app switcher, the categorised sidebar, the person's menu with the language and
// the theme, the deliberate sandbox / live switch, the drawer on a phone and the tour button's slot. The gateway is mocked; the shop has two apps.
const APP2 = "a0000000-0000-4000-8000-000000000002";
const LIVE2 = "a2000000-0000-4000-8000-000000000001";
const SANDBOX2 = "a2000000-0000-4000-8000-000000000002";
const app2 = { id: APP2, account_id: ACCOUNT, name: "Mobile game", slug: "mobile-game", create_time: "2026-10-03T09:00:00Z", tenants: [{ ...tenant(LIVE2, "LIVE"), app_id: APP2, name: "Mobile game" }, { ...tenant(SANDBOX2, "SANDBOX"), app_id: APP2, name: "Mobile game (sandbox)" }] };

const customersOf: Record<string, { id: string; external_id: string; create_time: string; name: string; email: string }[]> = {
  [LIVE]: [{ id: "c1", external_id: "cus_shop", create_time: "2026-10-05T10:00:00Z", name: "Shop customer", email: "a@shop.test" }],
  [LIVE2]: [{ id: "c2", external_id: "cus_game", create_time: "2026-10-05T10:00:00Z", name: "Game customer", email: "b@game.test" }],
  [SANDBOX]: [],
  [SANDBOX2]: [],
};

async function open(page: Page, path = "/app", state: Parameters<typeof productGateway>[1] = {}): Promise<MockGateway> {
  const gw = productGateway(new MockGateway(), { apps: [app, app2], ...state }).gw;
  gw.on((c) => (c.path.startsWith("/v1/") ? { body: { customers: [], plans: [], subscriptions: [], invoices: [], payments: [], meters: [], webhook_endpoints: [], deliveries: [] } } : undefined));
  gw.on((c) => (c.path === "/v1/customers" ? { body: { customers: customersOf[c.headers["x-payrudder-tenant"] ?? ""] ?? [] } } : undefined));
  gw.on((c) => (c.path === `/app/v1/apps/${APP2}/dashboard` ? { body: dashboardFixture(Object.fromEntries(c.query.entries())) } : undefined));
  gw.on((c) => (c.path === `/app/v1/apps/${APP2}` && c.method === "GET" ? { body: { app: app2, role: "owner" } } : undefined));
  await gw.install(page);
  await signInAsAda(page);
  if (path !== "/app") await page.goto(path);
  return gw;
}

const nav = (page: Page) => page.getByTestId("sidebar").getByRole("navigation", { name: "Main navigation" });

test.describe("the scope in the address", () => {
  test.use({ viewport: { width: 1440, height: 900 } });

  test("an app is always selected: entering /app lands in an app and the address names it", async ({ page }) => {
    await open(page);
    await page.waitForURL(new RegExp(`/app/apps/${APP}\\?.*app=${APP}`));
    await expect(page.getByTestId("app-switcher-name")).toHaveText("Web shop");
    // The app used last is where the next visit lands.
    await page.getByTestId("app-switcher").click();
    await page.getByTestId("switch-app-mobile-game").click();
    await page.waitForURL(new RegExp(`/app/apps/${APP2}\\?.*app=${APP2}`));
    await page.goto("/app");
    await page.waitForURL(new RegExp(`/app/apps/${APP2}`));
    await expect(page.getByTestId("app-switcher-name")).toHaveText("Mobile game");
  });

  test("every link inside the app keeps the scope, and a reload keeps it", async ({ page }) => {
    await open(page, `/app/apps/${APP}`);
    await nav(page).getByRole("link", { name: "Customers" }).click();
    await page.waitForURL(new RegExp(`/app/t/${LIVE}/customers\\?.*app=${APP}`));
    await nav(page).getByRole("link", { name: "Members" }).click();
    await page.waitForURL(new RegExp(`/app/members\\?.*app=${APP}`));
    // The organisation's pages are inside the same app: its sidebar is still the app's.
    await expect(nav(page).getByRole("link", { name: "Customers" })).toBeVisible();
    await page.reload();
    await expect(page).toHaveURL(new RegExp(`app=${APP}`));
    await expect(page.getByTestId("app-switcher-name")).toHaveText("Web shop");
  });

  test("an address that names another app than its page is corrected", async ({ page }) => {
    await open(page, `/app/t/${LIVE}/customers?app=${APP2}`);
    await expect(page).toHaveURL(new RegExp(`app=${APP}`));
    await expect(page.getByTestId("app-switcher-name")).toHaveText("Web shop");
  });

  test("the switcher moves the page to the other app and offers All apps first", async ({ page }) => {
    await open(page, `/app/t/${LIVE}/customers`);
    await expect(page.getByRole("cell", { name: "Shop customer" })).toBeVisible();
    await page.getByTestId("app-switcher").click();
    const items = page.getByRole("menuitem");
    await expect(items.first()).toContainText("All apps");
    await page.getByTestId("switch-app-mobile-game").click();
    await page.waitForURL(new RegExp(`/app/t/${LIVE2}/customers\\?.*app=${APP2}`));
    await expect(page.getByRole("cell", { name: "Game customer" })).toBeVisible();
    await expect(page.getByRole("cell", { name: "Shop customer" })).toHaveCount(0);
  });

  test("all apps: one table of every app's customers with the app in front, and a row leads to its own app", async ({ page }) => {
    await open(page, `/app/t/${LIVE}/customers`);
    await page.getByTestId("app-switcher").click();
    await page.getByTestId("switch-app-all").click();
    await page.waitForURL(/\/app\/t\/all\/customers\?.*app=all/);
    await expect(page.getByTestId("app-switcher-name")).toHaveText("All apps");
    const view = page.getByTestId("all-apps-view");
    await expect(view.getByRole("row", { name: /Web shop.*Shop customer/ })).toBeVisible();
    await expect(view.getByRole("row", { name: /Mobile game.*Game customer/ })).toBeVisible();
    await expect(nav(page).getByRole("link", { name: "Customers" })).toHaveAttribute("aria-current", "page");
    // The pages of one app are not offered over all apps.
    await expect(nav(page).getByRole("link", { name: "API keys" })).toHaveCount(0);
    await page.reload();
    await expect(view.getByRole("row", { name: /Mobile game.*Game customer/ })).toBeVisible();
    await view.getByRole("link", { name: /Open Mobile game/ }).click();
    await page.waitForURL(new RegExp(`/app/t/${LIVE2}/customers\\?.*app=${APP2}`));
  });

  test("all apps: the dashboard is the address' ?app=all and a page of one app offers the apps", async ({ page }) => {
    await open(page, "/app?app=all");
    await expect(page).toHaveURL(/app=all/);
    await expect(page).not.toHaveURL(/\/app\/apps\//);
    await expect(page.getByTestId("app-switcher-name")).toHaveText("All apps");
    await nav(page).getByRole("link", { name: "Reports" }).click();
    await expect(page.getByTestId("all-apps-choose")).toBeVisible();
    await page.getByTestId(`choose-app-${APP2}`).click();
    await page.waitForURL(new RegExp(`/app/t/${LIVE2}/reports`));
  });

  test("a form starts with the App field on the selected app; another app moves the page and the form stays open", async ({ page }) => {
    await open(page, `/app/t/${LIVE}/customers`);
    await page.getByTestId("customers-new").or(page.getByRole("button", { name: "New customer" })).first().click();
    const field = page.getByTestId("app-field").getByRole("combobox");
    await expect(field).toHaveValue(APP);
    await field.selectOption(APP2);
    await page.waitForURL(new RegExp(`/app/t/${LIVE2}/customers\\?.*app=${APP2}`));
    await expect(page.getByTestId("app-field").getByRole("combobox")).toHaveValue(APP2);
    await expect(page.getByRole("form", { name: "new customer" })).toBeVisible();
  });
});

test.describe("the sidebar", () => {
  test.use({ viewport: { width: 1440, height: 900 } });

  test("is in categories, with the organisation at the bottom of the app group", async ({ page }) => {
    await open(page, `/app/apps/${APP}`);
    const sections = page.getByTestId("sidebar-nav").locator("section");
    await expect(sections.locator("h2")).toHaveText(["Overview", "Customers and billing", "Usage and products", "Money", "Developers", "App settings", "Organisation"]);
    const groups = async (name: string) => await sections.filter({ has: page.getByRole("heading", { name, exact: true }) }).getByRole("link").allInnerTexts();
    expect(await groups("Customers and billing")).toEqual(["Customers", "Subscriptions", "Invoices", "Payments", "Payment links", "Credits"]);
    expect(await groups("Usage and products")).toEqual(["Usage", "Meters", "Packages", "Tax and retries", "Coupons"]);
    expect(await groups("Money")).toEqual(["Accounting", "Reports", "Affiliates"]);
    expect(await groups("Organisation")).toEqual(["Members", "Organisation integrations", "Help", "Organisation settings"]);
    // Nobody but an operator is offered the console.
    await expect(nav(page).getByRole("link", { name: "Operator console" })).toHaveCount(0);
  });

  test("an operator has the platform group last, below the organisation", async ({ page }) => {
    await open(page, `/app/apps/${APP}`, { operator: true });
    const headings = page.getByTestId("sidebar-nav").locator("section h2");
    await expect(headings.last()).toHaveText("Platform");
    await expect(headings.nth(-2)).toHaveText("Organisation");
  });

  test("scrolls on its own: a scroll that reaches its end does not carry on into the page", async ({ page }) => {
    await open(page, `/app/apps/${APP}`);
    const behavior = await page.getByTestId("sidebar-scroll").evaluate((el) => getComputedStyle(el).overscrollBehaviorY);
    expect(behavior).toBe("contain");
  });

  test("folded to the icon rail the app switcher is an initial and the groups keep their names for screen readers", async ({ page }) => {
    await open(page, `/app/apps/${APP}`);
    await page.getByTestId("sidebar-toggle").click();
    await expect(page.getByTestId("sidebar")).toHaveAttribute("data-collapsed", "true");
    await expect(page.getByTestId("app-switcher")).toHaveAttribute("title", "Web shop");
    await expect(page.getByTestId("sidebar").getByRole("link", { name: "Customers" })).toHaveAttribute("title", "Customers");
  });
});

test.describe("the person's menu", () => {
  test.use({ viewport: { width: 1440, height: 900 } });

  test("holds the language and the theme, which leave the header", async ({ page }) => {
    await open(page, `/app/apps/${APP}`);
    await expect(page.getByTestId("topbar").getByTestId("language-switcher")).toHaveCount(0);
    await expect(page.getByTestId("topbar").getByRole("button", { name: /Switch to (dark|light) mode/ })).toHaveCount(0);
    await pickLanguage(page, "tr");
    await expect(page.locator("html")).toHaveAttribute("lang", "tr");
    await expect(page.getByTestId("app-switcher")).toBeVisible();
    await pickLanguage(page, "en");
    await expect(page.locator("html")).toHaveAttribute("lang", "en");
    await pickTheme(page, "dark");
    await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
    await pickTheme(page, "light");
    await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
    await pickTheme(page, "system");
    await expect(page.locator("html")).not.toHaveAttribute("data-theme", /.+/);
  });

  test("keeps the organisations, the settings and signing out", async ({ page }) => {
    await open(page, `/app/apps/${APP}`);
    await page.getByTestId("user-menu").click();
    await expect(page.getByRole("menuitem", { name: "Ada Labs" })).toBeVisible();
    await expect(page.getByRole("menuitem", { name: "Settings" })).toBeVisible();
    await expect(page.getByRole("menuitem", { name: "Sign out" })).toBeVisible();
  });
});

test.describe("the sandbox / live switch", () => {
  test.use({ viewport: { width: 1440, height: 900 } });

  test("is a pill that asks first: nothing moves until the dialog is confirmed, and Cancel keeps the page", async ({ page }) => {
    await open(page, `/app/t/${LIVE}/payments`);
    const pill = page.getByTestId("env-switch");
    await expect(pill).toHaveAttribute("data-env", "live");
    await expect(pill).toContainText("Live");
    await expect(page.getByTestId("sandbox-banner")).toHaveCount(0);
    await pill.click();
    const dialog = page.getByTestId("env-switch-dialog");
    await expect(dialog).toBeVisible();
    await expect(dialog.getByRole("heading", { name: "Switch to the sandbox?" })).toBeVisible();
    await expect(dialog).toContainText("Web shop is not affected");
    await expect(dialog).toContainText("only sandbox data");
    await expect(dialog).toContainText("Nothing is sent to real payment providers or to real customers");
    expect(new URL(page.url()).pathname).toBe(`/app/t/${LIVE}/payments`);
    await page.getByTestId("env-switch-cancel").click();
    await expect(dialog).toBeHidden();
    expect(new URL(page.url()).pathname).toBe(`/app/t/${LIVE}/payments`);
    await expect(pill).toHaveAttribute("data-env", "live");
    // Confirming moves to the same page of the sandbox, which wears the sandbox banner and a pill that says so.
    await pill.click();
    await page.getByTestId("env-switch-confirm").click();
    await page.waitForURL(new RegExp(`/app/t/${SANDBOX}/payments`));
    await expect(page.getByTestId("env-switch")).toHaveAttribute("data-env", "sandbox");
    await expect(page.getByTestId("sandbox-banner")).toBeVisible();
    // And back: the dialog says live is real.
    await page.getByTestId("env-switch").click();
    await expect(page.getByTestId("env-switch-dialog")).toContainText("Live is real");
    await page.getByTestId("env-switch-confirm").click();
    await page.waitForURL(new RegExp(`/app/t/${LIVE}/payments`));
  });

  test("over all apps it says that none of the apps is affected", async ({ page }) => {
    await open(page, "/app?app=all");
    await page.getByTestId("env-switch").click();
    await expect(page.getByTestId("env-switch-dialog")).toContainText("None of your apps is affected");
    await page.getByTestId("env-switch-confirm").click();
    await page.waitForURL(/app=all.*env=sandbox|env=sandbox.*app=all/);
  });
});

test.describe("the header", () => {
  test.use({ viewport: { width: 1440, height: 900 } });

  test("has the slot where the tour button mounts", async ({ page }) => {
    await open(page, `/app/apps/${APP}`);
    await expect(page.getByTestId("topbar").locator("#tour-slot")).toHaveCount(1);
  });
});

test.describe("on a phone", () => {
  test.use({ viewport: { width: 375, height: 812 } });

  test("the drawer holds the app switcher and the groups, and choosing a page closes it", async ({ page }) => {
    await open(page, `/app/apps/${APP}`);
    await expect(page.getByTestId("sidebar")).toBeHidden();
    await page.getByTestId("menu-button").click();
    const drawer = page.getByTestId("drawer");
    await expect(drawer).toBeVisible();
    await expect(drawer.getByTestId("app-switcher")).toBeVisible();
    await expect(drawer.getByRole("heading", { name: "Customers and billing" })).toBeVisible();
    await expect(drawer.getByRole("heading", { name: "Organisation" })).toBeVisible();
    await drawer.getByRole("link", { name: "Customers" }).click();
    await page.waitForURL(new RegExp(`/app/t/${LIVE}/customers\\?.*app=${APP}`));
    await expect(drawer).toBeHidden();
    // The switchers of the header fit: nothing scrolls sideways.
    expect(await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)).toBeLessThanOrEqual(0);
  });

  test("the drawer's switcher changes the app and closes the drawer", async ({ page }) => {
    await open(page, `/app/t/${LIVE}/customers`);
    await page.getByTestId("menu-button").click();
    await page.getByTestId("drawer").getByTestId("app-switcher").click();
    await page.getByTestId("switch-app-mobile-game").click();
    await page.waitForURL(new RegExp(`/app/t/${LIVE2}/customers`));
    await expect(page.getByTestId("drawer")).toBeHidden();
  });
});
