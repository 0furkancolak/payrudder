import { expect, test } from "@playwright/test";
import { baseTenant, MockGateway, openTenantPage, TENANT } from "./gateway";

// A payment named in another sheet opens its own sheet over it (docs/plans/03): the page behind stays, the address holds the sheet, and what the payment names
// (the customer, the invoice) opens over it in turn. The customer's "Manage credits" leads to the credits page with the customer chosen.

test.use({ viewport: { width: 1440, height: 900 } });

const invoice = { id: "inv1", number: "INV-2026-000001", customer_external_id: "cus_1", period_id: "2026-10", kind: "REGULAR", currency: "TRY", total_minor: "10000", status: "paid", saga_state: "COMPLETED", lines: [] };
const customer = { id: "c1", external_id: "cus_1", name: "Ada Lovelace", email: "ada@example.com", billing_currency: "TRY", country: "TR", create_time: "2026-10-20T09:00:00Z" };
const payment = { id: "pay1", invoice_id: "inv1", provider: "stripe", kind: "charge", status: "paid", amount_minor: "10000", currency: "TRY", occurred_at: "2026-10-20T10:00:00Z" };
const sheetParam = (page: import("@playwright/test").Page) => new URL(page.url()).searchParams.get("sheet");

function gateway(payments: unknown[] = [payment]) {
  return baseTenant(new MockGateway()).on((c) => {
    if (c.path === `/admin/v1/tenants/${TENANT}/invoices/inv1`) return { body: invoice };
    if (c.path === `/admin/v1/tenants/${TENANT}/invoices/inv1/saga`) return { body: { state: "COMPLETED", events: [] } };
    if (c.method === "GET" && c.path === "/v1/customers") return { body: { customers: [customer] } };
    if (c.method === "GET" && c.path === "/v1/customers/cus_1") return { body: customer };
    if (c.method === "GET" && c.path === "/v1/credit-notes") return { body: { credit_notes: [] } };
    if (c.method === "GET" && c.path === "/v1/payments") return { body: { payments } };
    if (c.method === "GET" && c.path === "/v1/invoices/inv1") return { body: invoice };
    return undefined;
  });
}

test("a payment in an invoice's sheet opens as a sheet over it, with what it names opening over that, and a reload brings them back", async ({ page }) => {
  await gateway().install(page);
  await openTenantPage(page, "/invoices?invoice=inv1");
  const here = new URL(page.url()).pathname;
  await page.getByTestId("related-payment").click();
  const detail = page.getByTestId("payment-detail");
  await expect(detail).toBeVisible();
  await expect(detail).toContainText("Charge");
  await expect(detail).toContainText("stripe");
  await expect(page.getByRole("dialog", { name: /100\.00/ })).toBeVisible();
  // The page did not change; the payment is in the address after the invoice.
  expect(new URL(page.url()).pathname).toBe(here);
  await expect.poll(() => sheetParam(page)).toBe(`peek:invoice:${TENANT}:inv1,peek:payment:${TENANT}:pay1`);

  // The customer it names opens over the payment: three sheets, the strip lists them, "back" closes the front one.
  await page.getByTestId("payment-context").getByTestId("related-customer").click();
  await expect(page.getByTestId("customer-detail")).toBeVisible();
  await expect(page.getByTestId("sheet-tab")).toHaveCount(3);
  expect(new URL(page.url()).pathname).toBe(here);
  await page.getByTestId("sheet-back").click();
  await expect(page.getByTestId("customer-detail")).toHaveCount(0);
  await expect(detail).toBeVisible();

  await page.reload();
  await expect(page.getByTestId("payment-detail")).toBeVisible();
  await expect.poll(() => sheetParam(page)).toBe(`peek:invoice:${TENANT}:inv1,peek:payment:${TENANT}:pay1`);
});

test("a payment that is not among the latest ones the console loads says so instead of staying empty", async ({ page }) => {
  await gateway([]).install(page);
  await openTenantPage(page, `/invoices?sheet=peek:payment:${TENANT}:old1`);
  await expect(page.getByTestId("payment-peek-missing")).toBeVisible();
  await expect(page.getByRole("dialog", { name: "old1" })).toBeVisible();
});

test("'Manage credits' in a customer's sheet leads to the credits page with that customer chosen", async ({ page }) => {
  const gw = gateway().on((c) => {
    if (c.method === "GET" && c.path === "/v1/credit-packs") return { body: { packs: [] } };
    if (c.method === "GET" && c.path === "/v1/customers/cus_1/credits") return { body: { balance: "300", next_expire_time: null, lots: [] } };
    if (c.method === "GET" && c.path === "/v1/customers/cus_1/credits/ledger") return { body: { entries: [], next_page_token: "" } };
    return undefined;
  });
  await gw.install(page);
  await openTenantPage(page, "/customers?customer=cus_1");
  await page.getByTestId("customer-tab-credits").click();
  await page.getByRole("button", { name: "Manage credits" }).click();
  await expect(page).toHaveURL(/\/credits\?.*customer=cus_1/);
  await expect(page.getByRole("status", { name: "credits available" })).toContainText("300");
});
