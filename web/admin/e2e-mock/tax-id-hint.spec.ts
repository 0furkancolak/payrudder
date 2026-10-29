import { expect, test } from "@playwright/test";
import { baseTenant, MockGateway, openTenantPage } from "./gateway";

// The customer's tax form hints when the number does not look like the kind of number the customer should have, and never blocks the save (docs/plans/05).

test.use({ viewport: { width: 1440, height: 900 } });

const customer = { id: "c1", external_id: "cus_1", name: "Ada Ltd", email: "ada@example.com", billing_currency: "TRY", country: "TR", customer_type: "business", create_time: "2026-10-20T09:00:00Z" };

test("a number that is not a VKN is hinted at, a good one is not, and saving stays possible", async ({ page }) => {
  const gw = baseTenant(new MockGateway()).on((c) => {
    if (c.method === "GET" && c.path === "/v1/customers") return { body: { customers: [customer] } };
    if (c.method === "PUT" && c.path === "/v1/customers/cus_1/tax") return { body: { ...customer, ...c.body } };
    return undefined;
  });
  await gw.install(page);
  await openTenantPage(page, "/customers");
  await page.getByTestId("customer-cus_1").click();
  await page.getByTestId("customer-edit").click();
  await page.getByTestId("customer-edit-tax").click();
  const id = page.getByRole("textbox", { name: "Tax ID" }).first();
  await id.fill("12345");
  await expect(page.getByTestId("tax-id-hint")).toContainText("VKN");
  await id.fill("1234567890");
  await expect(page.getByTestId("tax-id-hint")).toHaveCount(0);
  // A number that fails the checksum is only a hint: the form still saves it.
  await id.fill("1234567891");
  await expect(page.getByTestId("tax-id-hint")).toBeVisible();
  await page.getByRole("button", { name: "Save" }).last().click();
  await expect.poll(() => gw.called("PUT", "/v1/customers/cus_1/tax").length).toBe(1);
  expect(gw.called("PUT", "/v1/customers/cus_1/tax")[0]?.body).toMatchObject({ tax_id: "1234567891" });
});
