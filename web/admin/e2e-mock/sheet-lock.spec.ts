import { expect, test, type Page } from "@playwright/test";
import { baseTenant, MockGateway, openTenantPage } from "./gateway";

// The stack of sheets owns the page's scroll (docs/plans/03, complaint 9): while any sheet is open the page behind does not scroll, nested sheets and
// back/forward do not move it, and when the last one closes the page is where it was, whatever moved it meanwhile.

test.use({ viewport: { width: 1280, height: 400 } });

const customers = Array.from({ length: 60 }, (_, i) => ({
  id: `c${i}`,
  external_id: `cus_${String(i).padStart(2, "0")}`,
  name: `Customer ${String(i).padStart(2, "0")}`,
  email: `c${i}@example.com`,
  billing_currency: "TRY",
  country: "TR",
  create_time: "2026-10-20T09:00:00Z",
}));
const subscription = { id: "s1", customer_external_id: "cus_09", plan_code: "pro", plan_version: 1, state: "SUBSCRIPTION_STATE_ACTIVE", interval: "SUBSCRIPTION_INTERVAL_MONTHLY", currency: "TRY", start_time: "2026-10-20T09:00:00Z" };

/** How far the page is scrolled (the document scrolls; the console's own areas do not). */
const scrolled = (page: Page) => page.evaluate(() => Math.round(window.scrollY));
/** The scroll once a wheel's smooth scrolling has come to rest. */
async function settled(page: Page): Promise<number> {
  let last = await scrolled(page);
  for (let i = 0; i < 20; i++) {
    await page.waitForTimeout(100);
    const now = await scrolled(page);
    if (now === last) return now;
    last = now;
  }
  return last;
}
const locked = (page: Page) => page.evaluate(() => getComputedStyle(document.documentElement).overflow === "hidden");

test("the page behind the sheets stays put while they are open, over nested sheets, back and forward, and is where it was after the last one closes", async ({ page }) => {
  await baseTenant(new MockGateway())
    .on((c) => {
      if (c.method === "GET" && c.path === "/v1/customers") return { body: { customers } };
      if (c.method === "GET" && c.path === "/v1/subscriptions") return { body: { subscriptions: [subscription] } };
      if (c.method === "GET" && c.path === "/v1/subscriptions/s1") return { body: subscription };
      return undefined;
    })
    .install(page);
  await openTenantPage(page, "/customers");
  await page.getByTestId("customer-cus_00").waitFor();
  await page.getByTestId("customer-cus_09").scrollIntoViewIfNeeded();
  await page.mouse.wheel(0, 300);
  expect(await settled(page)).toBeGreaterThan(100);
  expect(await locked(page)).toBe(false);

  // One sheet: the page is locked where it was when the sheet opened, and a wheel on the overlay behind it (up or down) moves nothing.
  await page.getByTestId("customer-cus_09").click();
  await expect(page.getByTestId("customer-detail")).toBeVisible();
  expect(await locked(page)).toBe(true);
  const before = await settled(page);
  await page.mouse.move(60, 200);
  await page.mouse.wheel(0, -300);
  expect(Math.abs((await settled(page)) - before)).toBeLessThanOrEqual(2);
  await page.mouse.wheel(0, 300);
  expect(Math.abs((await settled(page)) - before)).toBeLessThanOrEqual(2);

  // A sheet over it (the subscription named in the customer's sheet): still locked, still in place.
  await page.getByTestId("customer-tab-subscriptions").click();
  await page.getByTestId("related-subscription").click();
  await expect(page.getByTestId("sheet-tab")).toHaveCount(2);
  expect(await locked(page)).toBe(true);
  expect(Math.abs((await scrolled(page)) - before)).toBeLessThanOrEqual(2);

  // Back closes the front sheet, forward opens it again: the lock holds through both.
  await page.getByTestId("sheet-back").click();
  await expect(page.getByTestId("sheet-tab")).toHaveCount(1);
  expect(await locked(page)).toBe(true);
  await page.getByTestId("sheet-forward").click();
  await expect(page.getByTestId("sheet-tab")).toHaveCount(2);
  expect(await locked(page)).toBe(true);
  expect(Math.abs((await scrolled(page)) - before)).toBeLessThanOrEqual(2);

  // Something moves the page while the sheets are open (a script, a navigation that scrolls to the top): it is put back when the last sheet closes.
  await page.evaluate(() => window.scrollTo(0, 0));
  expect(await scrolled(page)).toBeLessThan(before);
  await page.keyboard.press("Escape");
  await expect(page.getByTestId("sheet-tab")).toHaveCount(0);
  await page.keyboard.press("Escape");
  await expect(page.getByTestId("customer-detail")).toHaveCount(0);
  expect(await locked(page)).toBe(false);
  // Back where it was (the page's own header changes height with the scroll, so a few pixels of it may differ), not at the top.
  expect(Math.abs((await settled(page)) - before)).toBeLessThanOrEqual(12);
});

test("a sheet that is open when the page is left does not leave the next page locked", async ({ page }) => {
  await baseTenant(new MockGateway()).on((c) => (c.method === "GET" && c.path === "/v1/customers" ? { body: { customers } } : undefined)).install(page);
  await openTenantPage(page, "/customers");
  await page.getByTestId("customer-cus_00").click();
  await expect(page.getByTestId("customer-detail")).toBeVisible();
  expect(await locked(page)).toBe(true);
  await page.goBack();
  await expect(page.getByTestId("customer-detail")).toHaveCount(0);
  expect(await locked(page)).toBe(false);
});
