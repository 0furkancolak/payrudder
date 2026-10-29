import { expect, test } from "@playwright/test";
import { baseTenant, MockGateway, openTenantPage, TENANT } from "./gateway";

// A mention of a customer in another sheet opens the customer's sheet over it (docs/plans/03): the page behind stays, the sheet is read-only, and the way to the
// customers page is a link at its foot.

test.use({ viewport: { width: 1440, height: 900 } });

const invoice = { id: "inv1", number: "INV-2026-000001", customer_external_id: "cus_1", period_id: "2026-10", kind: "REGULAR", currency: "TRY", total_minor: "10000", status: "paid", saga_state: "COMPLETED", lines: [] };
const customer = { id: "c1", external_id: "cus_1", name: "Ada Lovelace", email: "ada@example.com", billing_currency: "TRY", country: "TR", create_time: "2026-10-20T09:00:00Z" };

test("the customer in an invoice's sheet opens as a sheet over it, without the page's forms", async ({ page }) => {
  const gw = baseTenant(new MockGateway()).on((c) => {
    if (c.path === `/admin/v1/tenants/${TENANT}/invoices/inv1`) return { body: invoice };
    if (c.path === `/admin/v1/tenants/${TENANT}/invoices/inv1/saga`) return { body: { state: "COMPLETED", events: [] } };
    if (c.method === "GET" && c.path === "/v1/customers") return { body: { customers: [customer] } };
    if (c.method === "GET" && c.path === "/v1/customers/cus_1") return { body: customer };
    if (c.method === "GET" && c.path === "/v1/credit-notes") return { body: { credit_notes: [] } };
    return undefined;
  });
  await gw.install(page);
  await openTenantPage(page, "/invoices?invoice=inv1");
  const here = new URL(page.url()).pathname;
  await page.getByTestId("related-customer").click();
  const sheet = page.getByTestId("customer-detail");
  await expect(sheet).toBeVisible();
  await expect(sheet.getByTestId("customer-email")).toBeVisible();
  // The page did not change, and the forms of the customers page are not offered here.
  expect(new URL(page.url()).pathname).toBe(here);
  await expect(page.getByTestId("customer-new-subscription")).toHaveCount(0);
  await expect(page.getByTestId("customer-edit")).toHaveCount(0);
  // The way to the customers page is a plain link.
  await expect(page.getByTestId("customer-peek-open-page")).toHaveAttribute("href", /customers\?customer=cus_1/);
});

const subscription = { id: "s1", customer_external_id: "cus_1", plan_code: "pro", plan_version: 1, state: "SUBSCRIPTION_STATE_ACTIVE", interval: "SUBSCRIPTION_INTERVAL_MONTHLY", currency: "TRY", start_time: "2026-10-20T09:00:00Z" };

test("a subscription in a customer's sheet opens as a sheet over it, with what the list's sheet offers", async ({ page }) => {
  const gw = baseTenant(new MockGateway()).on((c) => {
    if (c.method === "GET" && c.path === "/v1/customers") return { body: { customers: [customer] } };
    if (c.method === "GET" && c.path === "/v1/subscriptions") return { body: { subscriptions: [subscription] } };
    if (c.method === "GET" && c.path === "/v1/subscriptions/s1") return { body: subscription };
    return undefined;
  });
  await gw.install(page);
  await openTenantPage(page, "/customers");
  await page.getByTestId("customer-cus_1").click();
  await expect(page.getByTestId("customer-detail")).toBeVisible();
  const here = new URL(page.url()).pathname;
  await page.getByTestId("customer-tab-subscriptions").click();
  await page.getByTestId("related-subscription").click();
  await expect(page.getByRole("dialog", { name: /Manage cus_1/ })).toBeVisible();
  expect(new URL(page.url()).pathname).toBe(here);
});
