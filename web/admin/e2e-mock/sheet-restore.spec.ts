import { expect, test, type Page } from "@playwright/test";
import { baseTenant, MockGateway, openTenantPage, TENANT } from "./gateway";
import { rowAction } from "./ui";

// Sheets that work on a record (edit, void, record payment, the sheet of a row of a token-paged list) are in the address with the record's id: a reload
// or a shared link opens the same sheet, closing takes it off, and a record that is not there is dropped from the address.

const sheetParam = (page: Page) => new URL(page.url()).searchParams.get("sheet");
const fixedToday = (page: Page) => page.clock.setFixedTime(new Date(2026, 9, 21, 12, 0, 0)); // 21 October 2026, local

const expense = (id: string, vendor: string) => ({
  id,
  vendor,
  category: "infrastructure",
  amount_minor: "4000",
  currency: "TRY",
  incurred_on: "2026-10-10",
  note: "",
  recurring: "none",
  create_time: "2026-10-10T09:00:00Z",
  update_time: "2026-10-10T09:00:00Z",
});

test.describe("accounting", () => {
  async function open(page: Page, query = "") {
    await fixedToday(page);
    const gw = baseTenant(new MockGateway()).on((c) => {
      if (c.path === "/v1/accounting/summary") return { body: { from_date: "2026-10-01", to_date: "2026-10-31", granularity: "day", currencies: [] } };
      if (c.path === "/v1/expenses") return { body: { expenses: [expense("e1", "AWS"), expense("e2", "Linear")] } };
      return undefined;
    });
    await gw.install(page);
    await openTenantPage(page, `/accounting${query}`);
  }

  test("editing an expense is in the address, comes back on reload and is gone when closed", async ({ page }) => {
    await open(page);
    await rowAction(page.getByRole("row", { name: /AWS/ }), "Edit");
    await expect.poll(() => sheetParam(page)).toBe("edit-expense:e1");
    await page.reload();
    const dialog = page.getByRole("dialog");
    await expect(dialog.getByLabel("Vendor")).toHaveValue("AWS");
    await expect.poll(() => sheetParam(page)).toBe("edit-expense:e1");
    await dialog.getByRole("button", { name: "Cancel" }).click();
    await expect(page.getByRole("dialog")).toHaveCount(0);
    await expect.poll(() => sheetParam(page)).toBeNull();
  });

  test("voiding an expense comes back from a shared link, and an expense that is not there is dropped", async ({ page }) => {
    await open(page, "?sheet=void-expense:e2");
    const dialog = page.getByRole("dialog");
    await expect(dialog).toContainText("Linear");
    await expect(dialog.getByLabel("Reason")).toBeVisible();
    await page.keyboard.press("Escape");
    await expect.poll(() => sheetParam(page)).toBeNull();

    await page.goto(`/tenants/${TENANT}/accounting?sheet=void-expense:nope`);
    await expect(page.getByRole("row", { name: /AWS/ })).toBeVisible();
    await expect(page.getByRole("dialog")).toHaveCount(0);
  });

  test("a new expense and the vendor rules have names of their own", async ({ page }) => {
    await open(page);
    await page.getByRole("button", { name: "Add expense" }).click();
    await expect.poll(() => sheetParam(page)).toBe("new-expense");
    await page.reload();
    await expect(page.getByRole("dialog").getByLabel("Vendor")).toBeVisible();
  });
});

test.describe("customers", () => {
  test("the address form of a customer comes back from the address, filled from the customer", async ({ page }) => {
    await fixedToday(page);
    const gw = baseTenant(new MockGateway()).on((c) =>
      c.path === "/v1/customers" && c.method === "GET"
        ? { body: { customers: [{ id: "id-cus_1", external_id: "cus_1", name: "Ada", email: "ada@example.com", address: { city: "Izmir", country: "TR" } }] } }
        : undefined,
    );
    await gw.install(page);
    await openTenantPage(page, "/customers");
    await rowAction(page.getByTestId("customer-cus_1"), "Edit address");
    await expect.poll(() => sheetParam(page)).toBe("customer-address:cus_1");
    await page.reload();
    const dialog = page.getByRole("dialog");
    await expect(dialog).toContainText("cus_1");
    await expect(dialog.getByLabel("Address line 1")).toBeVisible();
    // The sheet of a row and a form over it come back in the order they were opened.
    await dialog.getByRole("button", { name: "Cancel" }).click();
    await expect.poll(() => sheetParam(page)).toBeNull();
    await page.goto(`/tenants/${TENANT}/customers?sheet=customer:cus_1,customer-tax:cus_1`);
    await expect(page.getByTestId("sheet-tab")).toHaveCount(2);
    await expect.poll(() => sheetParam(page)).toBe("customer:cus_1,customer-tax:cus_1");
  });
});

test.describe("invoice", () => {
  const base = `/admin/v1/tenants/${TENANT}/invoices/inv1`;
  const invoice = {
    id: "inv1",
    number: "INV-2026-000007",
    customer_external_id: "cus_1",
    period_id: "2026-10",
    kind: "REGULAR",
    currency: "USD",
    total_minor: "10000",
    status: "open",
    saga_state: "PAYMENT_DECLINED",
    lines: [],
  };
  async function open(page: Page, query: string) {
    const gw = baseTenant(new MockGateway()).on((c) => {
      if (c.method !== "GET") return undefined;
      if (c.path === base) return { body: invoice };
      if (c.path === `${base}/saga`) return { body: { state: invoice.saga_state, events: [] } };
      if (c.path === `${base}/transfer`) return { status: 503, body: { error: { code: "UNAVAILABLE", key: "payments.transfer.unavailable", message: "down" } } };
      return undefined;
    });
    await gw.install(page);
    await openTenantPage(page, `/invoices${query}`);
  }

  test("an invoice that is not in the list, and the payment dialog over it, come back on reload", async ({ page }) => {
    await open(page, "?invoice=inv1");
    await page.getByRole("button", { name: "Record payment" }).click();
    await expect.poll(() => sheetParam(page)).toContain("record-payment:inv1");
    expect(sheetParam(page)).toMatch(/^peek:invoice:.+:inv1,record-payment:inv1$/);
    await page.reload();
    const dialog = page.getByRole("dialog", { name: "Record payment" });
    await expect(dialog).toBeVisible();
    await expect(page.getByTestId("sheet-tab")).toHaveCount(2);
    await dialog.getByRole("button", { name: "Cancel" }).click();
    await expect(page.getByRole("dialog", { name: "Record payment" })).toHaveCount(0);
    await expect.poll(() => sheetParam(page)).toMatch(/^peek:invoice:.+:inv1$/);
  });
});

test.describe("a token-paged list", () => {
  const link = (id: string, create_time: string) => ({
    id,
    kind: "adhoc",
    status: "active",
    single_use: true,
    amount_minor: "1250",
    currency: "USD",
    description: `Link ${id}`,
    create_time,
    expire_time: "2026-12-19T10:00:00Z",
    paid_count: 0,
    use_count: 0,
  });
  async function open(page: Page, query: string) {
    await fixedToday(page);
    const gw = baseTenant(new MockGateway()).on((c) => {
      if (c.path !== "/v1/payment-links" || c.method !== "GET") return undefined;
      // Three pages of two links; the last is from last month, which this month's date range hides.
      const token = c.query.get("page_token");
      if (token === "t3") return { body: { links: [link("l5", "2026-09-02T10:00:00Z"), link("l6", "2026-09-01T10:00:00Z")] } };
      if (token === "t2") return { body: { links: [link("l3", "2026-10-12T10:00:00Z"), link("l4", "2026-10-11T10:00:00Z")], next_page_token: "t3" } };
      return { body: { links: [link("l1", "2026-10-20T10:00:00Z"), link("l2", "2026-10-19T10:00:00Z")], next_page_token: "t2" } };
    });
    await gw.install(page);
    await openTenantPage(page, `/payment-links${query}`);
  }

  test("the sheet of a row is in the address, comes back on reload, and is gone when closed", async ({ page }) => {
    await open(page, "");
    await page.getByRole("row", { name: /Link l2/ }).click();
    await expect.poll(() => sheetParam(page)).toBe("payment-link:l2");
    await page.getByTestId("detail-next").click();
    await expect.poll(() => sheetParam(page)).toBe("payment-link:l3");
    await expect(page.getByTestId("detail-sheet")).toContainText("l3");
    await page.reload();
    await expect(page.getByTestId("detail-sheet")).toContainText("l3");
    await page.keyboard.press("Escape");
    await expect(page.getByTestId("detail-sheet")).toHaveCount(0);
    await expect.poll(() => sheetParam(page)).toBeNull();
  });

  test("a shared link finds the row on a later page, even one that the date range hides", async ({ page }) => {
    await open(page, "?sheet=payment-link:l5");
    await expect(page.getByTestId("detail-sheet")).toContainText("l5");
    await expect.poll(() => sheetParam(page)).toBe("payment-link:l5");
  });

  test("a row that no page holds is taken off the address", async ({ page }) => {
    await open(page, "?sheet=payment-link:nope");
    await expect(page.getByRole("row", { name: /Link l1/ })).toBeVisible();
    await expect.poll(() => sheetParam(page)).toBeNull();
    await expect(page.getByTestId("detail-sheet")).toHaveCount(0);
  });
});
