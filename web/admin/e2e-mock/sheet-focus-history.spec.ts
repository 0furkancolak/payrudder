import { expect, test, type Page } from "@playwright/test";
import { baseTenant, MockGateway, openTenantPage, TENANT } from "./gateway";

// Plan 03, what had no test: where the keyboard focus goes when a sheet opens and closes, what the browser's own Back and Forward do with a sheet in the address (the
// sheet is written with `replace`, so it belongs to the history entry of the page it was opened on), and whether the next record of a sheet starts at its top.

test.use({ viewport: { width: 1280, height: 520 } });

const customer = (n: number) => ({ id: `id-${n}`, external_id: `cus_${n}`, name: `Name ${n}`, email: `cus_${n}@example.com`, billing_currency: "TRY", country: "TR", create_time: "2026-10-05T10:00:00Z" });
const invoices = Array.from({ length: 14 }, (_, i) => ({ id: `inv-${i}`, number: `INV-${i}`, customer_external_id: "cus_1", status: "paid", kind: "REGULAR", currency: "TRY", total_minor: "1000", create_time: `2026-10-${String(i + 1).padStart(2, "0")}T09:00:00Z`, finalize_time: `2026-10-${String(i + 1).padStart(2, "0")}T09:00:00Z` }));

async function open(page: Page) {
  const gw = baseTenant(new MockGateway()).on((c) => {
    if (c.method !== "GET") return undefined;
    if (c.path === "/v1/customers") return { body: { customers: [customer(1), customer(2), customer(3)] } };
    if (c.path === "/v1/invoices") return { body: { invoices, next_page_token: "" } };
    return undefined;
  });
  await page.clock.setFixedTime(new Date(2026, 9, 21, 12, 0, 0));
  await gw.install(page);
  await openTenantPage(page, "/customers");
  await expect(page.getByTestId("customer-cus_1")).toBeVisible();
}
const sheetParam = (page: Page) => new URL(page.url()).searchParams.get("sheet");
/** Whether the keyboard focus is inside the element. */
const focusIn = (page: Page, selector: string) => page.evaluate((s) => !!document.activeElement?.closest(s), selector);

test("a sheet takes the keyboard focus when it opens, and gives it back to the row it was opened from when it closes", async ({ page }) => {
  await open(page);
  const row = page.getByTestId("customer-cus_2");
  await row.getByTestId("row-actions").focus();
  await page.keyboard.press("Enter");
  await page.getByRole("menuitem", { name: "View", exact: true }).press("Enter");
  await expect(page.getByTestId("detail-sheet")).toBeVisible();
  await expect.poll(() => focusIn(page, '[role="dialog"]')).toBe(true);
  await page.keyboard.press("Escape");
  await expect(page.getByTestId("detail-sheet")).toHaveCount(0);
  // Back on the list, in the row it was opened from, not at the top of the page.
  await expect.poll(() => focusIn(page, '[data-testid="customer-cus_2"]')).toBe(true);
});

test("the browser's Back and Forward carry the sheet with the page it was opened on: Back leaves that page and unlocks the next, Forward brings the sheet back", async ({ page }) => {
  await open(page);
  await page.goto(`/tenants/${TENANT}/subscriptions`);
  await page.goto(`/tenants/${TENANT}/customers`);
  await page.getByTestId("customer-cus_3").click();
  await expect.poll(() => sheetParam(page)).toBe("customer:cus_3");
  await page.goBack();
  await expect(page).toHaveURL(/\/subscriptions$/);
  await expect(page.getByTestId("detail-sheet")).toHaveCount(0);
  // The page it went back to is not left locked by the sheet that was open.
  expect(await page.evaluate(() => getComputedStyle(document.documentElement).overflow)).not.toBe("hidden");
  await page.goForward();
  await expect(page.getByTestId("detail-sheet")).toBeVisible();
  await expect(page.getByTestId("detail-position")).toHaveText("3 / 3");
  expect(await page.evaluate(() => getComputedStyle(document.documentElement).overflow)).toBe("hidden");
  await page.keyboard.press("Escape");
  await expect.poll(() => sheetParam(page)).toBeNull();
});

test("the next record of a sheet starts at its top, not where the last one was scrolled to", async ({ page }) => {
  await open(page);
  await page.getByTestId("customer-cus_1").click();
  const scrollable = await page.evaluateHandle(() => {
    const root = document.querySelector('[data-testid="detail-sheet"]');
    const all = [root, ...(root?.querySelectorAll("*") ?? [])] as HTMLElement[];
    return all.find((e) => e && e.scrollHeight > e.clientHeight + 20 && ["auto", "scroll"].includes(getComputedStyle(e).overflowY)) ?? null;
  });
  expect(await scrollable.evaluate((e) => e !== null)).toBe(true);
  await scrollable.evaluate((e) => ((e as HTMLElement).scrollTop = (e as HTMLElement).scrollHeight));
  expect(await scrollable.evaluate((e) => (e as HTMLElement).scrollTop)).toBeGreaterThan(50);
  await page.getByTestId("detail-next").click();
  await expect.poll(() => sheetParam(page)).toBe("customer:cus_2");
  await expect.poll(() => scrollable.evaluate((e) => (e as HTMLElement).scrollTop)).toBe(0);
});
