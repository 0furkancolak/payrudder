import { expect, test, type Page } from "@playwright/test";
import { baseTenant, MockGateway, openTenantPage, TENANT } from "./gateway";

const customer = (id: string) => ({ id: `id-${id}`, external_id: id, name: `Name ${id}`, email: `${id}@example.com` });

async function open(page: Page, query = "") {
  const gw = baseTenant(new MockGateway()).on((c) => (c.path === "/v1/customers" && c.method === "GET" ? { body: { customers: [customer("cus_1"), customer("cus_2"), customer("cus_3")] } } : undefined));
  await page.clock.setFixedTime(new Date(2026, 9, 21, 12, 0, 0));
  await gw.install(page);
  await openTenantPage(page, "/customers");
  if (query) await page.goto(`/tenants/${TENANT}/customers?sheet=${query}`);
}
const sheetParam = (page: Page) => new URL(page.url()).searchParams.get("sheet");

test.describe("sheets in the address", () => {
  test("the sheet of a row is written into the address, follows the arrows, and is gone when closed", async ({ page }) => {
    await open(page);
    await page.getByTestId("customer-cus_1").click();
    await expect(page.getByTestId("detail-sheet")).toBeVisible();
    await expect.poll(() => sheetParam(page)).toBe("customer:cus_1");
    await page.getByTestId("detail-next").click();
    await expect.poll(() => sheetParam(page)).toBe("customer:cus_2");
    await page.keyboard.press("Escape");
    await expect(page.getByTestId("detail-sheet")).toHaveCount(0);
    await expect.poll(() => sheetParam(page)).toBeNull();
  });

  test("an address that names a sheet opens it (a reload brings the sheet back)", async ({ page }) => {
    await open(page, "customer:cus_3");
    await expect(page.getByTestId("detail-sheet")).toBeVisible();
    await expect(page.getByTestId("detail-position")).toHaveText("3 / 3");
    await page.reload();
    await expect(page.getByTestId("detail-position")).toHaveText("3 / 3");
  });

  test("a form sheet is named too, and sheets open over each other with a strip of tabs, back and forward", async ({ page }) => {
    await open(page, "customer:cus_2,new-customer");
    await expect(page.getByTestId("sheet-tab")).toHaveCount(2);
    await expect(page.getByTestId("sheet-strip")).toBeVisible();
    // The row's sheet opened last, so it is in front: back closes it, forward opens it again.
    await page.getByTestId("sheet-back").click();
    await expect.poll(() => sheetParam(page)).toBe("new-customer");
    await expect(page.getByTestId("sheet-strip")).toHaveCount(1); // the one that closed is leaving
    await expect(page.getByTestId("sheet-forward")).toBeEnabled();
    await page.getByTestId("sheet-forward").click();
    await expect.poll(() => sheetParam(page)).toBe("new-customer,customer:cus_2");
    // A tab brings its sheet to the front (it goes last in the strip); a tab's cross closes that sheet.
    await page.getByTestId("sheet-tab").first().getByRole("tab").click();
    await page.getByTestId("sheet-tab").nth(1).getByRole("button", { name: "Close" }).click();
    await expect.poll(() => sheetParam(page)).toBe("customer:cus_2");
  });
});
