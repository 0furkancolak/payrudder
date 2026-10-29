import { expect, test, type Page } from "@playwright/test";
import { baseTenant, MockGateway, openTenantPage } from "./gateway";

// Opening a sheet on a scrolled list must not scroll the page behind it, and closing it must leave the page where it was (docs/plans/03, complaint 9).

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

/** How far the page is scrolled: the document and every scrolled ancestor of the list, summed (the console scrolls its content area, not always the window). */
async function scrolled(page: Page): Promise<number> {
  return await page.evaluate(() => {
    let sum = window.scrollY;
    for (let el: Element | null = document.querySelector("main"); el; el = el.parentElement) sum += (el as HTMLElement).scrollTop || 0;
    return Math.round(sum);
  });
}

test("a sheet opened on a scrolled list leaves the page where it was, on open and on close", async ({ page }) => {
  await baseTenant(new MockGateway()).on((c) => (c.method === "GET" && c.path === "/v1/customers" ? { body: { customers } } : undefined)).install(page);
  await openTenantPage(page, "/customers");
  await page.getByTestId("customer-cus_00").waitFor();
  await page.getByTestId("customer-cus_09").scrollIntoViewIfNeeded();
  await page.mouse.wheel(0, 300);
  const before = await scrolled(page);
  expect(before).toBeGreaterThan(100);
  await page.getByTestId("customer-cus_09").click();
  await expect(page.getByTestId("customer-detail")).toBeVisible();
  expect(Math.abs((await scrolled(page)) - before)).toBeLessThanOrEqual(2);
  await page.keyboard.press("Escape");
  await expect(page.getByTestId("customer-detail")).toHaveCount(0);
  expect(Math.abs((await scrolled(page)) - before)).toBeLessThanOrEqual(2);
});
