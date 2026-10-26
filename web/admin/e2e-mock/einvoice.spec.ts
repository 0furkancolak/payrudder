import { expect, test } from "@playwright/test";
import { baseTenant, MockGateway, openTenantPage, TENANT } from "./gateway";

const invoice = {
  id: "inv1",
  number: "INV-2026-000001",
  customer_external_id: "cus_1",
  period_id: "2026-10",
  kind: "REGULAR",
  currency: "TRY",
  total_minor: "10000",
  status: "paid",
  saga_state: "COMPLETED",
  lines: [],
};
const doc = (extra: Record<string, unknown>) => ({
  id: "doc1",
  kind: "invoice",
  invoice_id: "inv1",
  number: "INV-2026-000001",
  status: "pending",
  document_type: "e-arsiv",
  provider: "nilvera",
  environment: "live",
  attempts: 0,
  has_xml: false,
  has_pdf: false,
  ...extra,
});

function withInvoice(documents: Record<string, unknown>[]) {
  return baseTenant(new MockGateway()).on((c) => {
    if (c.path === `/admin/v1/tenants/${TENANT}/invoices/inv1`) return { body: invoice };
    if (c.path === `/admin/v1/tenants/${TENANT}/invoices/inv1/saga`) return { body: { state: "COMPLETED", events: [] } };
    if (c.method === "GET" && c.path === "/v1/credit-notes") return { body: { credit_notes: [] } };
    if (c.method === "GET" && c.path === `/admin/v1/tenants/${TENANT}/e-invoices`) return { body: { documents } };
    return undefined;
  });
}

test.describe("e-invoice status on the invoice page", () => {
  test("a failed document shows its error and can be retried", async ({ page }) => {
    const documents = [doc({ status: "failed", attempts: 1, last_error_key: "einvoice.buyer.incomplete", last_error: "buyer incomplete" })];
    const gw = withInvoice(documents).on((c) => {
      if (c.method === "POST" && c.path === `/admin/v1/tenants/${TENANT}/e-invoices/doc1/retry`) {
        documents[0] = doc({ status: "pending", attempts: 0 });
        return { body: documents[0] };
      }
      return undefined;
    });
    await gw.install(page);
    await openTenantPage(page, "/invoices/inv1");
    const status = page.getByTestId("einvoice-status");
    await expect(status.getByTestId("einvoice-badge")).toHaveText("Failed");
    await expect(status.getByRole("alert")).toContainText("customer's tax data is incomplete");
    await status.getByRole("button", { name: "Retry" }).click();
    await expect.poll(() => gw.called("POST", `/admin/v1/tenants/${TENANT}/e-invoices/doc1/retry`).length).toBe(1);
    await expect(status.getByTestId("einvoice-badge")).toHaveText("Pending");
  });

  test("an issued document shows its legal number and offers downloads", async ({ page }) => {
    const gw = withInvoice([doc({ status: "issued", legal_number: "ABC2026000000001", has_xml: true, has_pdf: true, issued_at: "2026-10-20T10:00:00Z" })]).on((c) =>
      c.method === "GET" && c.path.endsWith("/e-invoices/doc1/files/xml") ? { body: { url: "http://localhost:3003/file.xml", expires_at: "2026-10-20T10:05:00Z" } } : undefined,
    );
    await gw.install(page);
    await page.route("**/file.xml", (r) => r.fulfill({ status: 200, contentType: "application/xml", body: "<Invoice/>" }));
    await openTenantPage(page, "/invoices/inv1");
    const status = page.getByTestId("einvoice-status");
    await expect(status.getByTestId("einvoice-number")).toHaveText("ABC2026000000001");
    await expect(status.getByRole("button", { name: "Retry" })).toHaveCount(0);
    await expect(status.getByRole("button", { name: "Download PDF" })).toBeVisible();
    const popup = page.waitForEvent("popup");
    await status.getByRole("button", { name: "Download XML" }).click();
    await popup;
    expect(gw.called("GET", `/admin/v1/tenants/${TENANT}/e-invoices/doc1/files/xml`)).toHaveLength(1);
  });

  test("nothing is drawn for an invoice without a document", async ({ page }) => {
    await withInvoice([]).install(page);
    await openTenantPage(page, "/invoices/inv1");
    await expect(page.getByRole("heading", { name: "INV-2026-000001" })).toBeVisible();
    await expect(page.getByTestId("einvoice-status")).toHaveCount(0);
  });
});

test.describe("e-invoice settings", () => {
  test("the setting and the company data are saved with the tenant, the account with its credentials", async ({ page }) => {
    const gw = baseTenant(new MockGateway(), { e_invoice_on: "off", legal: {} }).on((c) => {
      if (c.method === "PATCH" && c.path === `/admin/v1/tenants/${TENANT}`) return { body: { id: TENANT, name: "Acme", ...c.body } };
      if (c.method === "GET" && c.path === `/admin/v1/tenants/${TENANT}/e-invoice-accounts`) return { body: { accounts: [] } };
      if (c.method === "PUT" && c.path === `/admin/v1/tenants/${TENANT}/e-invoice-accounts/live`) {
        return { body: { environment: "live", provider: c.body?.provider, mode: c.body?.mode, has_credentials: true } };
      }
      return undefined;
    });
    await gw.install(page);
    await openTenantPage(page, "/e-invoice");

    const form = page.getByRole("form", { name: "e-invoice settings" });
    await form.getByLabel("Issue e-documents").selectOption("paid");
    await form.getByLabel("Legal name").fill("Acme AŞ");
    await form.getByLabel("Tax id (VKN or VAT id)").fill("1234567890");
    await form.getByLabel("Tax office").fill("Kadıköy");
    await form.getByRole("button", { name: "Save settings" }).click();
    await expect.poll(() => gw.called("PATCH", `/admin/v1/tenants/${TENANT}`).length).toBe(1);
    expect(gw.called("PATCH", `/admin/v1/tenants/${TENANT}`)[0]?.body).toMatchObject({
      e_invoice_on: "paid",
      legal: { legal_name: "Acme AŞ", tax_id: "1234567890", tax_office: "Kadıköy" },
    });

    await page.getByRole("button", { name: "Add or replace an account" }).click();
    const account = page.getByRole("form", { name: "e-invoice account" });
    await account.getByLabel("API key").fill("secret-key");
    await page.getByRole("button", { name: "Save account" }).click();
    await expect.poll(() => gw.called("PUT", `/admin/v1/tenants/${TENANT}/e-invoice-accounts/live`).length).toBe(1);
    expect(gw.called("PUT", `/admin/v1/tenants/${TENANT}/e-invoice-accounts/live`)[0]?.body).toEqual({ provider: "nilvera", mode: "test", credentials: { api_key: "secret-key" } });
    // The sheet closes once the account is saved, and it opens empty the next time.
    await expect(account).toHaveCount(0);
    await page.getByRole("button", { name: "Add or replace an account" }).click();
    await expect(page.getByRole("form", { name: "e-invoice account" }).getByLabel("API key")).toHaveValue("");
  });

  test("the sandbox offers the mock provider only", async ({ page }) => {
    const gw = baseTenant(new MockGateway()).on((c) => (c.path === `/admin/v1/tenants/${TENANT}/e-invoice-accounts` ? { body: { accounts: [] } } : undefined));
    await gw.install(page);
    await openTenantPage(page, "/e-invoice");
    await page.getByRole("button", { name: "Add or replace an account" }).click();
    const account = page.getByRole("form", { name: "e-invoice account" });
    await account.getByLabel("Environment").selectOption("sandbox");
    await expect(account.getByLabel("Provider").locator("option")).toHaveText(["mock"]);
  });
});

test("a customer's tax data is saved with the customer tax endpoint", async ({ page }) => {
  const gw = baseTenant(new MockGateway()).on((c) => {
    if (c.method === "GET" && c.path === "/v1/customers") return { body: { customers: [{ id: "id1", external_id: "cus_1", name: "Ada", email: "ada@example.com" }] } };
    if (c.method === "PUT" && c.path === "/v1/customers/cus_1/tax") return { body: { id: "id1", external_id: "cus_1", ...c.body } };
    return undefined;
  });
  await gw.install(page);
  await openTenantPage(page, "/customers");
  await page.getByRole("button", { name: "Tax data" }).click();
  const region = page.getByRole("dialog", { name: "Tax data of cus_1" });
  await region.getByLabel("Tax id (VKN/TCKN or VAT id)").fill("11111111111");
  await region.getByLabel("Tax office").fill("Beşiktaş");
  await region.getByLabel("Registered for e-fatura").check();
  await region.getByRole("button", { name: "Save tax data" }).click();
  await expect.poll(() => gw.called("PUT", "/v1/customers/cus_1/tax").length).toBe(1);
  expect(gw.called("PUT", "/v1/customers/cus_1/tax")[0]?.body).toEqual({ tax_id: "11111111111", tax_office: "Beşiktaş", e_invoice_registered: true });
});
