import { expect, test } from "@playwright/test";
import { baseTenant, MockGateway, openTenantPage } from "./gateway";

test.describe("mail", () => {
  test("a category is switched off with a partial update and shown off", async ({ page }) => {
    const off = new Set<string>();
    const all = ["invoice_issued", "payment_receipts", "payment_failed", "usage_alerts", "system"];
    const state = () => ({ categories: all.map((category) => ({ category, enabled: !off.has(category) })) });
    const gw = baseTenant(new MockGateway()).on((c) => {
      if (c.path !== "/v1/mail-settings") return undefined;
      if (c.method === "PUT") {
        for (const s of (c.body?.categories ?? []) as { category: string; enabled: boolean }[]) {
          if (s.enabled) off.delete(s.category);
          else off.add(s.category);
        }
      }
      return { body: state() };
    });
    await gw.install(page);
    await openTenantPage(page, "/mail");
    await expect(page.getByTestId("mail-payment_receipts")).toBeChecked();
    await page.getByTestId("mail-payment_receipts").uncheck();
    await expect.poll(() => gw.called("PUT", "/v1/mail-settings").length).toBe(1);
    expect(gw.called("PUT", "/v1/mail-settings")[0]?.body).toEqual({ categories: [{ category: "payment_receipts", enabled: false }] });
    await expect(page.getByTestId("mail-payment_receipts")).not.toBeChecked();
    await expect(page.getByTestId("mail-usage_alerts")).toBeChecked();
  });
});
