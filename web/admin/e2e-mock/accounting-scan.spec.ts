import { expect, test } from "@playwright/test";
import { baseTenant, MockGateway, openTenantPage } from "./gateway";
import { textPdf } from "./pdf";

const SAAS = ["Acme Cloud, Inc.", "Invoice", "Invoice date 10/26/2026", "Subtotal $118.96", "Sales tax (8.25%) $9.81", "Total $128.77", "Amount due $128.77 USD"];
const FATURA = ["Mavi Bulut Bilisim Hizmetleri A.S.", "e-Arsiv Fatura", "Fatura Tarihi: 26.10.2026", "KDV Haric Tutar 1.250,00 TL", "Hesaplanan KDV (%20) 250,00 TL", "Odenecek Tutar 1.500,00 TL"];

function accountingGateway() {
  let n = 0;
  const gw = baseTenant(new MockGateway()).on((c) => {
    if (c.path === "/v1/accounting/summary") return { body: { currencies: [] } };
    if (c.method === "GET" && c.path === "/v1/expenses") return { body: { expenses: [] } };
    if (c.method === "POST" && c.path === "/v1/assets") return { body: { results: [{ status: "created", asset: { id: `asset-${++n}` } }] } };
    if (c.method === "POST" && c.path === "/v1/expenses") return { status: 201, body: { id: `exp-${n}`, ...c.body } };
    return undefined;
  });
  return gw;
}

test.describe("accounting: add expense and receipt scanning", () => {
  test("'Add expense' is in the page header, not under the table", async ({ page }) => {
    const gw = accountingGateway();
    await gw.install(page);
    await openTenantPage(page, "/accounting");
    const add = page.getByTestId("add-expense");
    await expect(add).toBeVisible();
    // It sits in the header row with the period, above the figures and the table.
    const table = page.getByRole("heading", { name: "Expenses" });
    expect((await add.boundingBox())!.y).toBeLessThan((await table.boundingBox())!.y);
    await add.click();
    await expect(page.getByRole("dialog").getByLabel("Vendor")).toBeVisible();
  });

  test("many files are queued, read in the browser, confirmed or skipped, and saved with their attachments", async ({ page }) => {
    const gw = accountingGateway();
    await gw.install(page);
    await openTenantPage(page, "/accounting");
    await page.getByTestId("scan-receipts").click();
    await page.getByTestId("scan-input").setInputFiles([
      { name: "acme-invoice.pdf", mimeType: "application/pdf", buffer: textPdf(SAAS) },
      { name: "mavi-fatura.pdf", mimeType: "application/pdf", buffer: textPdf(FATURA) },
      { name: "notes.txt", mimeType: "text/plain", buffer: Buffer.from("not a receipt") },
    ]);
    const rows = page.getByTestId("scan-row");
    await expect(rows).toHaveCount(3);

    // The English invoice: every field read, tax included, and the dollar currency.
    const acme = rows.filter({ hasText: "acme-invoice.pdf" });
    await expect(acme.locator('[data-field="vendor"]')).toHaveValue("Acme Cloud, Inc.");
    await expect(acme.locator('[data-field="date"]')).toHaveValue("2026-10-26");
    await expect(acme.locator('[data-field="amount"]')).toHaveValue("128.77");
    await expect(acme.locator('[data-field="currency"]')).toHaveValue("USD");
    await expect(acme.locator('[data-field="tax"]')).toHaveValue("9.81");

    // The Turkish one: comma decimals and the lira.
    const mavi = rows.filter({ hasText: "mavi-fatura.pdf" });
    await expect(mavi.locator('[data-field="amount"]')).toHaveValue("1500.00");
    await expect(mavi.locator('[data-field="currency"]')).toHaveValue("TRY");
    await expect(mavi.locator('[data-field="tax"]')).toHaveValue("250.00");
    await expect(mavi.locator('[data-field="date"]')).toHaveValue("2026-10-26");

    // A file that is not a receipt says so and is not included.
    const notes = rows.filter({ hasText: "notes.txt" });
    await expect(notes.getByRole("alert")).toContainText("not a PDF, PNG or JPEG");

    // A person edits one row and skips nothing else; saving needs the confirmation of the button.
    await acme.locator('[data-field="category"]').selectOption("software");
    await mavi.locator('[data-field="vendor"]').fill("Mavi Bulut");
    await expect(page.getByTestId("scan-save")).toContainText("Save 2 expenses");
    expect(gw.called("POST", "/v1/expenses")).toHaveLength(0);
    await page.getByTestId("scan-save").click();

    await expect.poll(() => gw.called("POST", "/v1/expenses").length).toBe(2);
    expect(gw.called("POST", "/v1/assets")).toHaveLength(2);
    const bodies = gw.called("POST", "/v1/expenses").map((c) => c.body);
    expect(bodies).toContainEqual(expect.objectContaining({ vendor: "Acme Cloud, Inc.", category: "software", amount_minor: "12877", currency: "USD", incurred_on: "2026-10-26", tax_minor: "981", attachment_asset_id: expect.stringMatching(/^asset-/) }));
    expect(bodies).toContainEqual(expect.objectContaining({ vendor: "Mavi Bulut", amount_minor: "150000", currency: "TRY", tax_minor: "25000", attachment_asset_id: expect.stringMatching(/^asset-/) }));
  });

  test("a row can be skipped, and a row with a missing amount cannot be saved", async ({ page }) => {
    const gw = accountingGateway();
    await gw.install(page);
    await openTenantPage(page, "/accounting");
    await page.getByTestId("scan-receipts").click();
    await page.getByTestId("scan-input").setInputFiles([
      { name: "a.pdf", mimeType: "application/pdf", buffer: textPdf(SAAS) },
      { name: "b.pdf", mimeType: "application/pdf", buffer: textPdf(["Some Vendor Ltd", "no figures at all here, just words and more words"]) },
    ]);
    const rows = page.getByTestId("scan-row");
    await expect(rows.filter({ hasText: "a.pdf" }).locator('[data-field="amount"]')).toHaveValue("128.77");
    await expect(rows.filter({ hasText: "b.pdf" }).locator('[data-field="vendor"]')).toHaveValue("Some Vendor Ltd");
    await expect(page.getByTestId("scan-save")).toContainText("Save 1 expenses");
    await rows.filter({ hasText: "a.pdf" }).getByRole("button", { name: "Skip this file" }).click();
    await expect(rows).toHaveCount(1);
    await expect(page.getByTestId("scan-save")).toBeDisabled();
    await rows.locator('[data-field="amount"]').fill("12,5");
    await expect(page.getByTestId("scan-save")).toBeEnabled();
  });

  test("the invoice inbox: the address is shown, a rule records a regular invoice by itself and the rest wait for review", async ({ page }) => {
    const rule = { id: "r1", vendor_match: "acme cloud", category: "software", currency: "USD", tax_bps: 0, expected_amount_minor: "12877", tolerance_bps: 500, auto_approve: true };
    const gw = accountingGateway().on((c) => {
      if (c.method === "GET" && c.path === "/v1/expense-rules") return { body: { rules: [rule] } };
      if (c.method === "GET" && c.path === "/v1/expense-inbox/items") {
        return { body: { items: [
          { id: "i1", filename: "acme.pdf", content_type: "application/pdf", status: "to_review", from: "billing@acme.test" },
          { id: "i2", filename: "mavi.pdf", content_type: "application/pdf", status: "to_review", from: "fatura@mavi.test" },
        ] } };
      }
      if (c.method === "GET" && c.path === "/v1/expense-inbox/items/i1/file") return { raw: textPdf(SAAS), contentType: "application/pdf" };
      if (c.method === "GET" && c.path === "/v1/expense-inbox/items/i2/file") return { raw: textPdf(FATURA), contentType: "application/pdf" };
      if (c.method === "POST" && /\/v1\/expense-inbox\/items\/i\d\/resolve/.test(c.path)) return { body: { id: c.path.split("/")[4], status: c.body?.status } };
      return undefined;
    });
    await gw.install(page);
    await openTenantPage(page, "/accounting");
    await expect(page.getByTestId("inbox-address")).toHaveText("invoices+abcdefghijklmnopqrstuvwxyz234567@in.example.test");
    await expect(page.getByTestId("inbox-review")).toContainText("Review 2 invoices");
    await page.getByTestId("inbox-review").click();

    // i1 matches the rule and is within the tolerance: recorded, its item accepted. i2 has no rule: it waits.
    await expect.poll(() => gw.called("POST", "/v1/expenses").length).toBe(1);
    expect(gw.called("POST", "/v1/expenses")[0]?.body).toMatchObject({ vendor: "Acme Cloud, Inc.", category: "software", amount_minor: "12877", currency: "USD" });
    await expect.poll(() => gw.called("POST", "/v1/expense-inbox/items/i1/resolve").length).toBe(1);
    expect(gw.called("POST", "/v1/expense-inbox/items/i1/resolve")[0]?.body).toMatchObject({ status: "accepted", expense_id: expect.stringMatching(/^exp-/) });
    const waiting = page.getByTestId("scan-row").filter({ hasText: "mavi.pdf" });
    await expect(waiting.locator('[data-field="amount"]')).toHaveValue("1500.00");
    expect(gw.called("POST", "/v1/expense-inbox/items/i2/resolve")).toHaveLength(0);

    // Skipping the waiting one dismisses it for good.
    await waiting.getByRole("button", { name: "Skip this file" }).click();
    await expect.poll(() => gw.called("POST", "/v1/expense-inbox/items/i2/resolve").length).toBe(1);
    expect(gw.called("POST", "/v1/expense-inbox/items/i2/resolve")[0]?.body).toMatchObject({ status: "dismissed" });
  });

  test("a vendor rule is saved with its percentages in basis points", async ({ page }) => {
    const gw = accountingGateway().on((c) => {
      if (c.method === "POST" && c.path === "/v1/expense-rules") return { status: 201, body: { id: "r9", ...c.body } };
      return undefined;
    });
    await gw.install(page);
    await openTenantPage(page, "/accounting");
    await page.getByTestId("inbox-rules").click();
    const sheet = page.getByRole("dialog");
    await sheet.getByLabel("Vendor name contains").fill("Figma");
    await sheet.getByLabel("Category").selectOption("software");
    await sheet.getByLabel("Tax rate in the amount (%)").fill("20");
    await sheet.getByLabel("Expected amount").fill("45");
    await sheet.getByLabel("Tolerance (%)").fill("7,5");
    await sheet.getByText("Approve automatically").click();
    await sheet.getByRole("button", { name: "Save the rule" }).click();
    await expect.poll(() => gw.called("POST", "/v1/expense-rules").length).toBe(1);
    expect(gw.called("POST", "/v1/expense-rules")[0]?.body).toMatchObject({ vendor_match: "Figma", category: "software", tax_bps: 2000, expected_amount_minor: "4500", tolerance_bps: 750, auto_approve: true });
  });
});
