import { expect, test, type Locator, type Page } from "@playwright/test";
import { baseTenant, MockGateway, openTenantPage, TENANT } from "./gateway";
import { billingGateway, productGateway, signInAsAda } from "./product";

// The five complaints of the owner's first list (docs/plans/03-sheets-onboarding-tours.md), each as a test the mocked gateway can express. The real stack
// (a fresh sign-up, a second browser, a real phone) is covered by e2e/complaints-real.spec.ts, which only CI runs.
//
//   (5)  onboarding forces things             here: every screen can be skipped, nothing is created by skipping, a refused form does not trap
//                                             also: onboarding.spec.ts (resume, the checklist, a viewer, the support switch, a second browser, the server down)
//   (6)  the welcome tour keeps appearing      here: three visits and a reload; also: tours.spec.ts (seen on the server, another browser, the server down)
//   (7)  Usage and Meters share an icon        here: no two links of the sidebar draw the same icon
//   (9)  a sheet scrolls the page to the top   here: four long lists, wide and on a phone; also: sheet-lock.spec.ts (nested, back, forward, the lock),
//                                             sheet-scroll.spec.ts, sheet-selects.spec.ts (the lists inside a sheet)
//   (13) sheets that leave for another page    here: no sheet holds a link that leaves the page except the one clearly page-like link; also: peek-*.spec.ts

const wizard = (page: Page) => page.getByTestId("onboarding");

// ---------------------------------------------------------------------------------------------------------------------------------------------------------------
// (5) Onboarding: nothing is forced

test.describe("(5) the first-run wizard forces nothing", () => {
  test.use({ timezoneId: "UTC" });

  test("every screen but the last can be skipped, skipping makes nothing, and the console is usable at once", async ({ page }) => {
    const { gw } = productGateway(new MockGateway(), { apps: [], keys: [] });
    await gw.install(page, { onboarded: false, tourSeen: true });
    await signInAsAda(page);

    // The welcome screen, the form half filled, and then skip: no app, no organisation rename.
    await expect(wizard(page)).toBeVisible();
    await expect(page.getByTestId("onb-skip")).toBeVisible();
    await page.getByTestId("onb-start").click();
    await expect(page.getByTestId("onb-skip")).toBeVisible();
    await wizard(page).getByTestId("onb-org-name").fill("Half typed");
    await wizard(page).getByTestId("onb-app-name").fill("");
    // A required field blocks "Create", never "Skip".
    await expect(wizard(page).getByTestId("onb-continue")).toBeDisabled();
    await expect(page.getByTestId("onb-skip")).toBeEnabled();
    await page.getByTestId("onb-skip").click();
    await expect(wizard(page)).toHaveCount(0);
    expect(gw.called("POST", "/app/v1/apps")).toHaveLength(0);
    expect(gw.called("PATCH", "/app/v1/account")).toHaveLength(0);

    // The console works: a page of the sidebar opens.
    await page.getByTestId("sidebar").getByRole("link", { name: "Members" }).click();
    await expect(page).toHaveURL(/\/app\/members/);
    await expect(page.getByTestId("onboarding")).toHaveCount(0);
  });

  test("a form the server refuses does not trap: the error is shown and Skip still leaves", async ({ page }) => {
    const { gw } = productGateway(new MockGateway(), { apps: [], keys: [] });
    gw.on((c) => (c.method === "POST" && c.path === "/app/v1/apps" ? { status: 409, body: { error: { code: "FAILED_PRECONDITION", key: "identity.app.limit_reached", message: "limit" } } } : undefined));
    await gw.install(page, { onboarded: false, tourSeen: true });
    await signInAsAda(page);
    await page.getByTestId("onb-start").click();
    await wizard(page).getByTestId("onb-continue").click();
    await expect(wizard(page).getByRole("alert")).toBeVisible();
    await expect(wizard(page)).toBeVisible();
    await page.getByTestId("onb-skip").click();
    await expect(wizard(page)).toHaveCount(0);
  });

  test("the last screen is the end, not a trap: it has Finish and no Skip", async ({ page }) => {
    const { gw } = productGateway(new MockGateway(), { apps: [], keys: [] });
    await gw.install(page, { onboarded: false, tourSeen: true });
    await signInAsAda(page);
    await page.getByTestId("onb-start").click();
    await wizard(page).getByTestId("onb-continue").click();
    await expect(wizard(page).getByTestId("onb-finish")).toBeVisible();
    await expect(page.getByTestId("onb-skip")).toHaveCount(0);
    await page.getByTestId("onb-finish").click();
    await expect(wizard(page)).toHaveCount(0);
  });
});

// ---------------------------------------------------------------------------------------------------------------------------------------------------------------
// (6) The welcome tour does not keep appearing

test.describe("(6) the welcome tour appears once", () => {
  test("a tour that starts by itself, is walked away from and is met on three later visits (another page, a reload, a reload with the server down) does not start again", async ({ page }) => {
    const { gw } = productGateway(new MockGateway());
    billingGateway(gw);
    await gw.install(page, { tourSeen: false });
    await signInAsAda(page);
    await expect(page.getByTestId("tour-card")).toBeVisible();

    // Walked away from: another page, no Skip pressed (the tour dims the page, so the person types an address or presses back).
    await page.goto("/app/members");
    await page.goto("/app");
    await expect(page.getByTestId("app-switcher")).toBeVisible();
    await expect(page.getByTestId("tour-card")).toHaveCount(0);

    await page.reload();
    await expect(page.getByTestId("app-switcher")).toBeVisible();
    await expect(page.getByTestId("tour-card")).toHaveCount(0);

    // The server cannot be reached on the next visit: this browser remembers what it saw.
    gw.onboardingDown = true;
    await page.reload();
    await expect(page.getByTestId("app-switcher")).toBeVisible();
    await expect(page.getByTestId("tour-card")).toHaveCount(0);
  });
});

// ---------------------------------------------------------------------------------------------------------------------------------------------------------------
// (7) Usage and Meters have different icons (and nothing else in the sidebar shares one)

test.describe("(7) the sidebar's icons", () => {
  test("no two links of the sidebar draw the same icon, and Usage and Meters differ", async ({ page }) => {
    const { gw } = productGateway(new MockGateway(), { operator: true });
    await gw.install(page);
    await signInAsAda(page);
    const icons = await page.getByTestId("sidebar-nav").getByRole("link").evaluateAll((links) =>
      links.map((a) => ({ name: (a.textContent ?? "").trim(), icon: [...(a.querySelector("svg")?.classList ?? [])].find((c) => c.startsWith("lucide-") && c !== "lucide") ?? "" })),
    );
    expect(icons.length).toBeGreaterThan(15);
    const usage = icons.find((i) => i.name === "Usage");
    const meters = icons.find((i) => i.name === "Meters");
    expect(usage?.icon).not.toBe("");
    expect(meters?.icon).not.toBe("");
    expect(usage?.icon).not.toBe(meters?.icon);
    const seen = new Map<string, string[]>();
    for (const i of icons) seen.set(i.icon, [...(seen.get(i.icon) ?? []), i.name]);
    const shared = [...seen.entries()].filter(([, names]) => names.length > 1);
    expect(shared, `links that share an icon: ${JSON.stringify(shared)}`).toEqual([]);
  });
});

// ---------------------------------------------------------------------------------------------------------------------------------------------------------------
// (9) A sheet opened on a scrolled list leaves the page where it was, on open and on close

const rows = (n: number, make: (i: number, id: string) => Record<string, unknown>) => Array.from({ length: n }, (_, i) => make(i, String(i).padStart(2, "0")));
const day = (i: number) => `2026-10-${String(1 + (i % 20)).padStart(2, "0")}T09:00:00Z`;

interface ScrollCase {
  name: string;
  path: string;
  mock: (gw: MockGateway) => MockGateway;
  /** The row to open: the tenth of sixty. */
  row: (page: Page) => Locator;
}

const SCROLLED: ScrollCase[] = [
  {
    name: "customers",
    path: "/customers",
    mock: (gw) => gw.on((c) => (c.method === "GET" && c.path === "/v1/customers" ? { body: { customers: rows(60, (i, id) => ({ id: `c${i}`, external_id: `cus_${id}`, name: `Customer ${id}`, email: `c${i}@example.com`, billing_currency: "TRY", country: "TR", create_time: day(i) })), next_page_token: "" } } : undefined)),
    row: (page) => page.getByTestId("customer-cus_09"),
  },
  {
    name: "subscriptions",
    path: "/subscriptions",
    mock: (gw) => gw.on((c) => (c.method === "GET" && c.path === "/v1/subscriptions" ? { body: { subscriptions: rows(60, (i, id) => ({ id: `s${i}`, customer_external_id: `cus_${id}`, plan_code: "pro", plan_version: 1, state: "SUBSCRIPTION_STATE_ACTIVE", currency: "TRY", owner: "payrudder", start_time: day(i) })) } } : undefined)),
    row: (page) => page.getByRole("row").filter({ hasText: "cus_09" }),
  },
  {
    name: "invoices",
    path: "/invoices",
    mock: (gw) => gw.on((c) => (c.path === `/admin/v1/tenants/${TENANT}/invoices` ? { body: { invoices: rows(60, (i, id) => ({ id: `i${i}`, number: `INV-${id}`, customer_external_id: "cus_1", period_id: "2026-10", kind: "REGULAR", currency: "TRY", total_minor: "100", status: "paid", saga_state: "COMPLETED", create_time: day(i) })) } } : c.path.startsWith(`/admin/v1/tenants/${TENANT}/invoices/`) ? { body: { id: "i9", number: "INV-09", customer_external_id: "cus_1", period_id: "2026-10", kind: "REGULAR", currency: "TRY", total_minor: "100", status: "paid", saga_state: "COMPLETED", lines: [], events: [] } } : undefined)),
    row: (page) => page.getByRole("row").filter({ hasText: "INV-09" }),
  },
  {
    name: "the payment ledger",
    path: "/payments",
    mock: (gw) => gw.on((c) => (c.path === `/admin/v1/tenants/${TENANT}/ledger` ? { body: { entries: rows(60, (i, id) => ({ id: i + 1, provider: "iyzico", provider_ref: `ref_${id}`, kind: "charge", status: "succeeded", source: "webhook", amount_minor: "500", currency: "EUR", occurred_at: day(i) })) } } : undefined)),
    row: (page) => page.getByRole("row").filter({ hasText: "ref_09" }),
  },
];

/** What the header's own change of height (the scrollbar leaving, a title wrapping) may move the page by, counted the way `scrolled` counts (the document twice). */
const DRIFT = 12;

/** How far the page is scrolled: the document and every scrolled ancestor of the list, summed (the console scrolls its content area, not always the window). */
async function scrolled(page: Page): Promise<number> {
  return await page.evaluate(() => {
    let sum = window.scrollY;
    for (let el: Element | null = document.querySelector("main"); el; el = el.parentElement) sum += (el as HTMLElement).scrollTop || 0;
    return Math.round(sum);
  });
}

/** The scroll once it has stopped: a wheel scrolls smoothly (and, on a busy machine, starts late), so it is read until four reads in a row agree. */
async function settled(page: Page): Promise<number> {
  let last = await scrolled(page);
  let same = 0;
  for (let i = 0; i < 60 && same < 4; i++) {
    await page.waitForTimeout(100);
    const now = await scrolled(page);
    same = now === last ? same + 1 : 0;
    last = now;
  }
  return last;
}

for (const viewport of [{ width: 1280, height: 400 }, { width: 375, height: 600 }]) {
  for (const c of SCROLLED) {
    test(`(9) ${c.name}: a sheet opened on a scrolled list leaves the page where it was, on open and on close (${viewport.width} px)`, async ({ page }) => {
      await page.setViewportSize(viewport);
      const gw = c.mock(baseTenant(new MockGateway()));
      await gw.install(page);
      await openTenantPage(page, c.path);
      const row = c.row(page);
      await row.waitFor();
      await row.scrollIntoViewIfNeeded();
      await page.mouse.wheel(0, 300);
      await expect.poll(() => scrolled(page)).toBeGreaterThan(100);
      const before = await settled(page);
      // The row is clicked where it is: a real click first scrolls the page if the row is under the sticky header, which would be the test's doing.
      await row.dispatchEvent("click");
      const sheet = page.getByRole("dialog").first();
      await expect(sheet).toBeVisible();
      expect(Math.abs((await settled(page)) - before)).toBeLessThanOrEqual(DRIFT);
      // The page behind does not follow a wheel over the sheet, nor a touch of its overlay.
      await page.mouse.wheel(0, 400);
      expect(Math.abs((await settled(page)) - before)).toBeLessThanOrEqual(DRIFT);
      await page.keyboard.press("Escape");
      await expect(page.getByRole("dialog")).toHaveCount(0);
      expect(Math.abs((await settled(page)) - before)).toBeLessThanOrEqual(DRIFT);
    });
  }
}

// ---------------------------------------------------------------------------------------------------------------------------------------------------------------
// (13) A sheet does not take the person to another page

const invoice = { id: "inv1", number: "INV-2026-000001", customer_external_id: "cus_1", period_id: "2026-10", kind: "REGULAR", currency: "TRY", total_minor: "10000", status: "paid", saga_state: "COMPLETED", lines: [] };
const customer = { id: "c1", external_id: "cus_1", name: "Ada Lovelace", email: "ada@example.com", billing_currency: "TRY", country: "TR", create_time: "2026-10-20T09:00:00Z" };
const subscription = { id: "s1", customer_external_id: "cus_1", plan_code: "pro", plan_version: 1, state: "SUBSCRIPTION_STATE_ACTIVE", interval: "SUBSCRIPTION_INTERVAL_MONTH", currency: "TRY", owner: "payrudder", start_time: "2026-10-20T09:00:00Z" };
const payment = { id: "pay1", provider: "iyzico", kind: "charge", status: "succeeded", amount_minor: "10000", currency: "TRY", invoice_id: "inv1", occurred_at: "2026-10-20T09:00:00Z", provider_ref: "ref1", source: "webhook", environment: "live" };

const sheetsMock = (gw: MockGateway) =>
  gw.on((c) => {
    if (c.path === `/admin/v1/tenants/${TENANT}/invoices`) return { body: { invoices: [invoice] } };
    if (c.path === `/admin/v1/tenants/${TENANT}/invoices/inv1`) return { body: invoice };
    if (c.path === `/admin/v1/tenants/${TENANT}/invoices/inv1/saga`) return { body: { state: "COMPLETED", events: [] } };
    if (c.path === `/admin/v1/tenants/${TENANT}/ledger`) return { body: { entries: [{ ...payment, id: 1 }] } };
    if (c.method === "GET" && c.path === "/v1/customers") return { body: { customers: [customer], next_page_token: "" } };
    if (c.method === "GET" && c.path === "/v1/customers/cus_1") return { body: customer };
    if (c.method === "GET" && c.path === "/v1/subscriptions") return { body: { subscriptions: [subscription] } };
    if (c.method === "GET" && c.path === "/v1/payments") return { body: { payments: [payment] } };
    if (c.method === "GET" && c.path === "/v1/credit-notes") return { body: { credit_notes: [] } };
    return undefined;
  });

const SHEETS: { name: string; path: string }[] = [
  { name: "an invoice", path: "/invoices?invoice=inv1" },
  { name: "a customer", path: "/customers?sheet=customer:cus_1" },
  { name: "a subscription", path: "/subscriptions?sheet=subscription:s1" },
];

for (const s of SHEETS) {
  test(`(13) ${s.name}'s sheet holds no link that leaves the page, except a link that says it is a page`, async ({ page }) => {
    const gw = sheetsMock(baseTenant(new MockGateway()));
    await gw.install(page);
    await openTenantPage(page, s.path);
    const sheet = page.getByRole("dialog").first();
    await expect(sheet).toBeVisible();
    await page.waitForTimeout(500);
    const here = new URL(page.url()).origin;
    const links = await sheet.locator("a[href]").evaluateAll((as) => as.map((a) => ({ href: (a as HTMLAnchorElement).href, target: a.getAttribute("target"), testid: a.getAttribute("data-testid") ?? "", text: (a.textContent ?? "").trim() })));
    const leaving = links.filter((l) => new URL(l.href).origin === here && l.target !== "_blank" && !l.href.includes("sheet=") && !/open-page$/.test(l.testid));
    expect(leaving, `links in the sheet that go to another page of the console: ${JSON.stringify(leaving)}`).toEqual([]);
  });
}
