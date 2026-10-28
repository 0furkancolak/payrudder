import { expect, test } from "@playwright/test";
import { baseTenant, MockGateway, openTenantPage } from "./gateway";
import { METERS } from "./usage";

// Lists that gained the one select that orders by column and direction together (docs/13-admin-ui.md, "Lists"): the order follows the choice and is kept in the address.

test.use({ viewport: { width: 1440, height: 900 } });

test("meters are ordered by one select, and the order is kept in the address", async ({ page }) => {
  await baseTenant(new MockGateway()).on((c) => (c.method === "GET" && c.path === "/v1/meters" ? { body: { meters: METERS } } : undefined)).install(page);
  await openTenantPage(page, "/meters");
  const codes = METERS.map((m) => String(m.code)).sort();
  const order = async () => (await page.getByRole("row").allInnerTexts()).map((t) => codes.find((c) => t.includes(c))).filter(Boolean);
  await page.getByTestId("sort-select").selectOption("name_desc");
  await expect.poll(order).toEqual([...codes].reverse());
  await expect.poll(() => new URL(page.url()).searchParams.get("sort")).toBe("name_desc");
  await page.getByTestId("sort-select").selectOption("name_asc");
  await expect.poll(order).toEqual(codes);
});
