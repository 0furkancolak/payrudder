import { expect, test, type Page } from "@playwright/test";
import { baseTenant, MockGateway, openTenantPage, TENANT } from "./gateway";
import { pickLanguage } from "./ui";

// The kit's own components use logical utilities (start and end), so Arabic mirrors them without a rule per page: the switch's thumb, the padding of a tick's
// cell and the alignment of a dialog's header (the list pages are in filters-chips.spec.ts).

test.use({ viewport: { width: 1280, height: 800 } });

const mail = () =>
  baseTenant(new MockGateway()).on((c) => {
    if (c.method !== "GET") return undefined;
    if (c.path === "/v1/mail-settings") return { body: { categories: [{ category: "payment_receipts", enabled: false }] } };
    if (c.path === "/v1/mail-messages") return { body: { messages: [], next_page_token: "", connected: true } };
    if (c.path === "/v1/mail-suppressions") return { body: { suppressions: [{ address: "bob@example.com", reason: "bounce", create_time: "2026-11-01T09:00:00Z" }], next_page_token: "", connected: true } };
    return undefined;
  });

/** Where the thumb of a switch sits in it: left of its middle, or right of it. */
async function thumbSide(page: Page, testId: string): Promise<"left" | "right"> {
  const sw = page.getByTestId(testId);
  const box = (await sw.boundingBox())!;
  const thumb = (await sw.locator('[data-slot="switch-thumb"]').boundingBox())!;
  return thumb.x + thumb.width / 2 < box.x + box.width / 2 ? "left" : "right";
}

test("a switch is off at the start and on at the end, in either direction of writing", async ({ page }) => {
  await mail().install(page);
  await openTenantPage(page, "/mail");
  await expect(page.getByTestId("mail-payment_receipts")).not.toBeChecked();
  // The thumb slides (a transition) when the settings arrive and the switch turns off: poll, do not read it mid-way.
  await expect.poll(() => thumbSide(page, "mail-payment_receipts")).toBe("left");
  await expect.poll(() => thumbSide(page, "mail-usage_alerts")).toBe("right");

  await pickLanguage(page, "ar");
  await expect(page.locator("html")).toHaveAttribute("dir", "rtl");
  await expect(page.getByTestId("mail-usage_alerts")).toBeChecked();
  await expect.poll(() => thumbSide(page, "mail-payment_receipts")).toBe("right");
  await expect.poll(() => thumbSide(page, "mail-usage_alerts")).toBe("left");
});

test("in Arabic a dialog's header starts at the start edge and a tick's cell has no padding at the end", async ({ page }) => {
  const gw = mail().on((c) => (c.method === "GET" && c.path === "/v1/customers" ? { body: { customers: [{ id: "c1", external_id: "cus_1", name: "Ada", email: "ada@example.com", create_time: "2026-10-05T10:00:00Z" }] } } : undefined));
  await gw.install(page);
  await openTenantPage(page, "/mail");
  await pickLanguage(page, "ar");
  await page.getByTestId("suppression-remove-bob@example.com").click();
  const header = page.getByTestId("confirm-dialog").locator('[data-slot="dialog-header"]');
  await expect(header).toBeVisible();
  expect(await header.evaluate((el) => getComputedStyle(el).textAlign)).toBe("start");
  await page.getByTestId("confirm-cancel").click();

  await page.goto(`/tenants/${TENANT}/customers`);
  const cell = page.locator("td:has([role=checkbox])").first();
  await expect(cell).toBeVisible();
  const pad = await cell.evaluate((el) => ({ left: getComputedStyle(el).paddingLeft, right: getComputedStyle(el).paddingRight }));
  expect(pad.left).toBe("0px");
  expect(pad.right).not.toBe("0px");
  const head = page.locator("th:has([role=checkbox])").first();
  expect(await head.evaluate((el) => getComputedStyle(el).paddingLeft)).toBe("0px");
});
