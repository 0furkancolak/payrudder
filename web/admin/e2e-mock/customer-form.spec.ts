import { expect, test, type Page } from "@playwright/test";
import { baseTenant, MockGateway, openTenantPage } from "./gateway";
import { pickPlace } from "./ui";

// The new-customer form follows the customer's country and type: which tax fields it shows, what the tax id is called, which one is required, what the postal
// code is called (docs/plans/05-customer-locale-tax-documents.md). Four countries with different shapes: Turkey (TCKN and VKN, a tax office, e-fatura), Germany
// (an EU VAT id), the United States (a ZIP code, an EIN only for a business) and the United Kingdom (a VAT number, a postcode).

test.use({ viewport: { width: 1440, height: 900 } });

async function openForm(page: Page, gw: MockGateway) {
  await gw.install(page);
  await openTenantPage(page, "/customers");
  await page.getByTestId("customers-new").click();
  const form = page.getByRole("form", { name: "new customer" });
  await form.getByLabel("Customer id").fill("cus_9");
  await form.getByLabel("Name", { exact: true }).fill("Ayşe");
  await form.getByLabel("E-mail").fill("ayse@example.com");
  return form;
}

const gateway = () =>
  baseTenant(new MockGateway()).on((c) => {
    if (c.method === "POST" && c.path === "/v1/customers") return { status: 201, body: { id: "id-9", external_id: c.body?.external_id, create_time: "2026-10-05T10:00:00Z" } };
    if (c.method === "PUT" && c.path === "/v1/customers/cus_9/tax") return { body: { id: "id-9", external_id: "cus_9", ...c.body } };
    if (c.path === "/v1/customers") return { body: { customers: [] } };
    return undefined;
  });

const taxId = (form: ReturnType<Page["getByRole"]>, name: string) => form.getByLabel(name, { exact: true });

test("Turkey: an individual has a TCKN, a business has a required VKN, a tax office and e-fatura", async ({ page }) => {
  const gw = gateway();
  const form = await openForm(page, gw);
  await pickPlace(form.getByLabel("Country"), "turk");
  // No type yet: either number, and the sheet asks which it is.
  await expect(taxId(form, "Tax number (VKN or TCKN)")).toBeVisible();
  await expect(form.getByText("Choose above whether this customer is an individual")).toBeVisible();

  await form.getByLabel("Type").selectOption("individual");
  await expect(taxId(form, "TCKN (11-digit national ID number)")).toBeVisible();
  await expect(form.getByLabel("Tax office")).toHaveCount(0);
  await expect(form.getByLabel("Registered for e-fatura")).toBeVisible();

  await form.getByLabel("Type").selectOption("business");
  await expect(taxId(form, "VKN (10-digit tax number)")).toBeVisible();
  await expect(form.getByLabel("Tax office")).toBeVisible();
  // The VKN is required for a business in Turkey: the form is not sent without it, and says so at the field.
  await page.getByRole("dialog").getByRole("button", { name: "Create customer" }).click();
  await expect(form.locator("#new-customer-tax-id-error")).toHaveText("This field is required.");
  expect(gw.called("POST", "/v1/customers")).toHaveLength(0);
  // A number of the wrong kind is only hinted at.
  await taxId(form, "VKN (10-digit tax number)").fill("12345");
  await expect(form.locator("#new-customer-tax-id-error")).toHaveCount(0);
  await expect(form.getByTestId("tax-id-hint")).toContainText("VKN");
  await taxId(form, "VKN (10-digit tax number)").fill("1234567890");
  await expect(form.getByTestId("tax-id-hint")).toHaveCount(0);
  await form.getByLabel("Tax office").fill("Kadıköy");
  await page.getByRole("dialog").getByRole("button", { name: "Create customer" }).click();
  // The create call does not take tax data; the tax call follows it.
  await expect.poll(() => gw.called("PUT", "/v1/customers/cus_9/tax").length).toBe(1);
  expect(gw.called("POST", "/v1/customers")[0]?.body).toMatchObject({ external_id: "cus_9", country: "TR", customer_type: "business" });
  expect(gw.called("PUT", "/v1/customers/cus_9/tax")[0]?.body).toEqual({ tax_id: "1234567890", tax_office: "Kadıköy", e_invoice_registered: false });
});

test("Turkey: an individual is created without any tax data and no tax call is made", async ({ page }) => {
  const gw = gateway();
  const form = await openForm(page, gw);
  await pickPlace(form.getByLabel("Country"), "turk");
  await form.getByLabel("Type").selectOption("individual");
  await page.getByRole("dialog").getByRole("button", { name: "Create customer" }).click();
  await expect.poll(() => gw.called("POST", "/v1/customers").length).toBe(1);
  expect(gw.called("PUT", "/v1/customers/cus_9/tax")).toHaveLength(0);
});

test("Germany: a business gives an optional VAT number, checked for its check digits and never refused", async ({ page }) => {
  const gw = gateway();
  const form = await openForm(page, gw);
  await pickPlace(form.getByLabel("Country"), "germ");
  await form.getByLabel("Type").selectOption("business");
  await expect(taxId(form, "VAT number")).toBeVisible();
  await expect(form.getByText("A business in the EU gives its VAT id")).toBeVisible();
  await expect(form.getByLabel("Tax office")).toHaveCount(0);
  await expect(form.getByLabel("Registered for e-fatura")).toHaveCount(0);
  await expect(form.getByLabel("Postal code")).toBeVisible();
  // Optional: the form is sent empty.
  // A good number passes, a wrong check digit is hinted at, and the form still saves it.
  await taxId(form, "VAT number").fill("DE136695976");
  await expect(form.getByTestId("tax-id-hint")).toHaveCount(0);
  await taxId(form, "VAT number").fill("DE136695977");
  await expect(form.getByTestId("tax-id-hint")).toContainText("VAT id");
  await page.getByRole("dialog").getByRole("button", { name: "Create customer" }).click();
  await expect.poll(() => gw.called("PUT", "/v1/customers/cus_9/tax").length).toBe(1);
  expect(gw.called("PUT", "/v1/customers/cus_9/tax")[0]?.body).toMatchObject({ tax_id: "DE136695977" });
});

test("United States: a ZIP code, no tax id for an individual, an optional EIN for a business", async ({ page }) => {
  const gw = gateway();
  const form = await openForm(page, gw);
  await pickPlace(form.getByLabel("Country"), "united states");
  await expect(form.getByLabel("ZIP code")).toBeVisible();
  await form.getByLabel("ZIP code").fill("9410");
  await expect(form.getByText("does not look like a postal code of that country")).toBeVisible();
  await form.getByLabel("ZIP code").fill("94105");
  await expect(form.getByText("does not look like a postal code of that country")).toHaveCount(0);
  await form.getByLabel("Type").selectOption("individual");
  await expect(form.getByLabel(/Tax ID/)).toHaveCount(0);
  await expect(form.getByLabel("Tax office")).toHaveCount(0);
  await form.getByLabel("Type").selectOption("business");
  await expect(taxId(form, "Tax ID (EIN)")).toBeVisible();
  await page.getByRole("dialog").getByRole("button", { name: "Create customer" }).click();
  await expect.poll(() => gw.called("POST", "/v1/customers").length).toBe(1);
  expect(gw.called("PUT", "/v1/customers/cus_9/tax")).toHaveLength(0);
});

test("United Kingdom: a postcode, and a VAT number for a business", async ({ page }) => {
  const gw = gateway();
  const form = await openForm(page, gw);
  await pickPlace(form.getByLabel("Country"), "united king");
  await expect(form.getByLabel("Postcode")).toBeVisible();
  await form.getByLabel("Type").selectOption("business");
  await expect(taxId(form, "VAT number")).toBeVisible();
  await expect(form.getByText("A business in the United Kingdom gives its VAT number")).toBeVisible();
  await taxId(form, "VAT number").fill("GB 123 4567 89");
  await expect(form.getByTestId("tax-id-hint")).toHaveCount(0); // not checked
  await page.getByRole("dialog").getByRole("button", { name: "Create customer" }).click();
  await expect.poll(() => gw.called("PUT", "/v1/customers/cus_9/tax").length).toBe(1);
  expect(gw.called("PUT", "/v1/customers/cus_9/tax")[0]?.body).toMatchObject({ tax_id: "GB 123 4567 89" });
});

test("when the tax call fails after the customer was created, the sheet says so and does not offer to create it again", async ({ page }) => {
  const gw = baseTenant(new MockGateway()).on((c) => {
    if (c.method === "POST" && c.path === "/v1/customers") return { status: 201, body: { id: "id-9", external_id: "cus_9", create_time: "2026-10-05T10:00:00Z" } };
    if (c.method === "PUT" && c.path === "/v1/customers/cus_9/tax") return { status: 500, body: { error: { code: "INTERNAL", message: "boom" } } };
    if (c.path === "/v1/customers") return { body: { customers: [] } };
    return undefined;
  });
  const form = await openForm(page, gw);
  await pickPlace(form.getByLabel("Country"), "germ");
  await form.getByLabel("Type").selectOption("business");
  await taxId(form, "VAT number").fill("DE136695976");
  const create = page.getByRole("dialog").getByRole("button", { name: "Create customer" });
  await create.click();
  await expect(page.getByTestId("customer-created-without-tax")).toBeVisible();
  await expect(create).toBeDisabled();
  expect(gw.called("POST", "/v1/customers")).toHaveLength(1);
});
