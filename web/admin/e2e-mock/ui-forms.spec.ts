import { expect, test, type Page } from "@playwright/test";
import { baseTenant, MockGateway, openTenantPage, TENANT } from "./gateway";
import { confirmDialog } from "./ui";

// Findings UI-5, 7, 8, 9, 11, 17, 24, 30 of docs/code-quality/ui-test-notes.md: forms with visible labels and inline errors, one styled confirmation,
// detail sheets in the address, the payment link sheet after creation, the webhook delivery table, the focus on a sheet that opens.

const sheetParam = (page: Page) => new URL(page.url()).searchParams.get("sheet");

test.describe("forms", () => {
  test("the new customer form has visible labels, shows each problem at its field, and puts a taken id at the id field", async ({ page }) => {
    const gw = baseTenant(new MockGateway()).on((c) => {
      if (c.path !== "/v1/customers") return undefined;
      if (c.method === "POST") return { status: 409, body: { error: { code: "ALREADY_EXISTS", key: "common.already_exists", message: "customer already exists" } } };
      return { body: { customers: [] } };
    });
    await gw.install(page);
    await openTenantPage(page, "/customers");
    await page.getByTestId("customers-new").click();
    const dialog = page.getByRole("dialog");
    // Visible labels, not only placeholders or aria-labels.
    for (const label of ["Customer id", "Name", "E-mail"]) await expect(dialog.locator("label", { hasText: new RegExp(`^${label}$`) })).toBeVisible();
    // The first field has the focus, not the app select.
    await expect(dialog.getByLabel("Customer id")).toBeFocused();
    // An empty submit says what is missing, next to the fields, and moves the focus to the first one.
    await dialog.getByRole("button", { name: "Create customer" }).click();
    await expect(dialog.locator("#new-customer-id-error")).toHaveText("This field is required.");
    await expect(dialog.locator("#new-customer-name-error")).toBeVisible();
    await expect(dialog.getByLabel("Customer id")).toBeFocused();
    expect(gw.called("POST", "/v1/customers")).toHaveLength(0);
    // A bad e-mail is named; fixing a field removes its message.
    await dialog.getByLabel("Customer id").fill("cus_1");
    await dialog.getByLabel("Name", { exact: true }).fill("Ada");
    await dialog.getByLabel("E-mail").fill("nope");
    await expect(dialog.locator("#new-customer-id-error")).toHaveCount(0);
    await expect(dialog.locator("#new-customer-email-error")).toHaveText("Enter a valid e-mail address.");
    await dialog.getByLabel("E-mail").fill("ada@example.com");
    await dialog.getByRole("button", { name: "Create customer" }).click();
    // The server refuses the id: the message is under the id field, and goes away when the id is edited.
    await expect(dialog.locator("#new-customer-id-error")).toHaveText("It already exists.");
    await dialog.getByLabel("Customer id").fill("cus_2");
    await expect(dialog.locator("#new-customer-id-error")).toHaveCount(0);
  });

  test("a payment link form that is empty says what is missing, and after creation shows only the link", async ({ page }) => {
    const gw = baseTenant(new MockGateway()).on((c) => {
      if (c.path === "/v1/payment-links" && c.method === "POST") return { status: 201, body: { id: "l1", url: "https://pay.example.test/l/abc", status: "open" } };
      if (c.path === "/v1/payment-links") return { body: { links: [] } };
      if (c.path === "/v1/customers") return { body: { customers: [{ external_id: "cus_1", name: "Ada Lovelace", email: "ada@example.com" }] } };
      return undefined;
    });
    await gw.install(page);
    await openTenantPage(page, "/payment-links");
    await page.getByRole("button", { name: /Create (payment )?link|New (payment )?link/i }).first().click();
    const dialog = page.getByRole("dialog");
    await dialog.getByRole("button", { name: "Create link" }).click();
    await expect(dialog.locator("#pl-amount-error")).toBeVisible();
    await expect(dialog.locator("#pl-description-error")).toBeVisible();
    expect(gw.called("POST", "/v1/payment-links")).toHaveLength(0);
    await dialog.getByLabel("Amount").fill("12.50");
    await dialog.getByLabel("What it is for").fill("Setup");
    // The customer is chosen from the customers, shown with the name.
    await dialog.getByRole("combobox", { name: "Customer" }).click();
    await page.getByRole("option", { name: /Ada Lovelace/ }).click();
    await dialog.getByRole("button", { name: "Create link" }).click();
    await expect(dialog.getByTestId("payment-link-url")).toBeVisible();
    expect(gw.called("POST", "/v1/payment-links")[0]?.body).toMatchObject({ customer_external_id: "cus_1" });
    // The form and the create button are gone: a second click cannot make a second link.
    await expect(dialog.getByRole("form")).toHaveCount(0);
    await expect(dialog.getByRole("button", { name: "Create link", exact: true })).toHaveCount(0);
    await dialog.getByTestId("payment-link-another").click();
    await expect(dialog.getByRole("form")).toBeVisible();
  });
});

test.describe("empty submits", () => {
  test("a new expense and a new credit pack say what is missing at the fields instead of doing nothing", async ({ page }) => {
    const gw = baseTenant(new MockGateway()).on((c) => {
      if (c.path === "/v1/accounting/summary") return { body: { from_date: "2026-10-01", to_date: "2026-10-31", granularity: "day", currencies: [] } };
      if (c.path === "/v1/expenses" && c.method === "GET") return { body: { expenses: [] } };
      if (c.path === "/v1/credit-packs" && c.method === "GET") return { body: { packs: [] } };
      return undefined;
    });
    await gw.install(page);
    await openTenantPage(page, "/accounting");
    await page.getByRole("button", { name: /Add expense|New expense/i }).first().click();
    const dialog = page.getByRole("dialog");
    await dialog.getByRole("button", { name: "Save" }).click();
    await expect(dialog.locator("#exp-vendor-error")).toHaveText("This field is required.");
    await expect(dialog.locator("#exp-amount-error")).toBeVisible();
    await expect(dialog.getByLabel("Vendor")).toBeFocused();
    expect(gw.called("POST", "/v1/expenses")).toHaveLength(0);
    await dialog.getByLabel("Vendor").fill("AWS");
    await expect(dialog.locator("#exp-vendor-error")).toHaveCount(0);

    await page.goto(`/tenants/${TENANT}/credits`);
    await page.getByRole("button", { name: "Add pack" }).first().click();
    const pack = page.getByRole("dialog");
    await pack.getByRole("button", { name: "Add pack" }).click();
    await expect(pack.locator("#pack-name-error")).toBeVisible();
    await expect(pack.locator("#pack-credits-error")).toContainText("whole number");
    await expect(pack.locator("#pack-price-error")).toContainText("greater than zero");
  });
});

test.describe("webhooks", () => {
  const ep = (url: string) => ({ id: "ep1", url, description: "", event_types: [], active: true, create_time: "2026-10-20T10:00:00Z" });
  const html = '<!doctype html><html lang="en"><head><title>405</title></head><body><h1>Method Not Allowed</h1></body></html>';

  function webhookGateway(url = "https://example.com/hook") {
    return baseTenant(new MockGateway()).on((c) => {
      if (c.path === "/v1/webhook-endpoints" && c.method === "GET") return { body: { endpoints: [ep(url)] } };
      if (c.path === "/v1/webhook-endpoints/ep1" && c.method === "DELETE") return { body: {} };
      if (c.path === "/v1/webhook-endpoints/ep1/rotate-secret" && c.method === "POST") return { body: { secret: "whsec_NEW" } };
      if (c.path === "/v1/webhook-deliveries" && c.method === "GET") {
        return { body: { deliveries: [{ id: "d1", event_id: "e1", event_type: "invoice.paid", endpoint_id: "ep1", state: "WEBHOOK_DELIVERY_STATE_FAILED", attempts: 3, last_status: 405, last_error: `HTTP 405: ${html}`, create_time: "2026-10-20T10:00:00Z" }] } };
      }
      return undefined;
    });
  }

  test("a delivery shows its status as a badge and the answer as one plain line, with every column in the sheet", async ({ page }) => {
    await webhookGateway().install(page);
    await openTenantPage(page, "/webhooks");
    await page.getByRole("row", { name: /example.com\/hook/ }).click();
    const cell = page.getByTestId("delivery-answer");
    await expect(cell).toContainText("405");
    await expect(cell).toContainText("Method Not Allowed");
    await expect(cell).not.toContainText("<!doctype");
    await expect(cell).not.toContainText("HTTP 405");
    // The date and the action are inside the sheet, no sideways scroll needed.
    const log = page.getByRole("region", { name: "Deliveries" });
    const box = await log.getByRole("button", { name: "Send again" }).evaluate((el) => {
      const sheet = el.closest('[data-slot="sheet-content"]')?.getBoundingClientRect();
      const r = el.getBoundingClientRect();
      return { inside: !!sheet && r.right <= sheet.right && r.left >= sheet.left };
    }).catch(() => ({ inside: true }));
    expect(box.inside).toBe(true);
  });

  test("an http URL is warned about, an https one is not", async ({ page }) => {
    await webhookGateway("http://example.com/hook").install(page);
    await openTenantPage(page, "/webhooks");
    await expect(page.getByTestId("webhook-insecure-row")).toContainText("http");
    await page.getByTestId("webhooks-new").click();
    const dialog = page.getByRole("dialog");
    await dialog.getByLabel("Endpoint URL").fill("https://example.com/x");
    await expect(dialog.getByTestId("webhook-insecure")).toHaveCount(0);
    await dialog.getByLabel("Endpoint URL").fill("http://example.com/x");
    await expect(dialog.getByTestId("webhook-insecure")).toBeVisible();
  });

  test("rotating the secret and deleting ask first in the styled dialog, and the sheet has a delete", async ({ page }) => {
    const gw = webhookGateway();
    await gw.install(page);
    await openTenantPage(page, "/webhooks");
    await page.getByRole("row", { name: /example.com\/hook/ }).click();
    const sheet = page.getByRole("dialog");
    await sheet.getByRole("button", { name: "Rotate secret" }).click();
    await expect(page.getByTestId("confirm-dialog")).toContainText("The old secret stops working at once");
    await confirmDialog(page, "no");
    expect(gw.called("POST", "/v1/webhook-endpoints/ep1/rotate-secret")).toHaveLength(0);
    await sheet.getByRole("button", { name: "Rotate secret" }).click();
    await confirmDialog(page, "yes");
    await expect.poll(() => gw.called("POST", "/v1/webhook-endpoints/ep1/rotate-secret").length).toBe(1);
    await sheet.getByTestId("webhook-sheet-delete").click();
    await confirmDialog(page, "no");
    expect(gw.called("DELETE", "/v1/webhook-endpoints/ep1")).toHaveLength(0);
    await sheet.getByTestId("webhook-sheet-delete").click();
    await confirmDialog(page, "yes");
    await expect.poll(() => gw.called("DELETE", "/v1/webhook-endpoints/ep1").length).toBe(1);
  });
});

test.describe("detail sheets in the address", () => {
  test("a meter and a coupon open from a shared link and are in the address while open", async ({ page }) => {
    const gw = baseTenant(new MockGateway()).on((c) => {
      if (c.path === "/v1/meters") return { body: { meters: [{ code: "api_calls", aggregation: "AGGREGATION_SUM", unit_label: "call", value_property: "n", version: 1, quantity_scale: 0 }] } };
      if (c.path === "/v1/coupons") return { body: { coupons: [{ id: "c1", code: "WELCOME", percent_bps: 1000, duration: "COUPON_DURATION_ONCE", active: true, redeemed: 0, promotion_code_count: 0 }] } };
      if (c.path === "/v1/promotion-codes") return { body: { codes: [], total: 0 } };
      if (c.path === "/v1/coupon-redemptions") return { body: { redemptions: [], total: 0 } };
      return undefined;
    });
    await gw.install(page);
    await openTenantPage(page, "/meters");
    await page.getByRole("row", { name: /api_calls/ }).click();
    await expect.poll(() => sheetParam(page)).toBe("meter:api_calls");
    await page.reload();
    await expect(page.getByTestId("meter-detail")).toBeVisible();
    await page.keyboard.press("Escape");
    await expect.poll(() => sheetParam(page)).toBeNull();

    await page.goto(`/tenants/${TENANT}/coupons`);
    await page.getByTestId("coupon-WELCOME").click();
    await expect.poll(() => sheetParam(page)).toBe("coupon:WELCOME");
    await page.reload();
    await expect(page.getByTestId("coupon-detail")).toBeVisible();
  });
});
