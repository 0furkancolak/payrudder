import { expect, test, type Page } from "@playwright/test";
import { baseTenant, MockGateway, openTenantPage } from "./gateway";
import { pickCurrency } from "./ui";

const point = (bucket: string, net: string, exp: string) => ({ bucket, gross_income_minor: net, tax_minor: "0", refunds_minor: "0", net_income_minor: net, expenses_minor: exp, profit_minor: String(Number(net) - Number(exp)) });

/** Two currencies: TRY with provider fees entered, USD without. Nothing in them adds up across currencies. */
const summary = {
  from_date: "2026-10-01",
  to_date: "2026-10-31",
  granularity: "day",
  currencies: [
    {
      currency: "TRY",
      gross_income_minor: "12000",
      tax_minor: "2000",
      refunds_minor: "300",
      net_income_minor: "9700",
      expenses_minor: "5400",
      provider_fees_minor: "400",
      provider_fees_recorded: true,
      profit_minor: "4300",
      paid_invoices: "3",
      expense_by_category: [
        { category: "infrastructure", amount_minor: "4000" },
        { category: "software", amount_minor: "1000" },
        { category: "payment_fees", amount_minor: "400" },
      ],
      series: [point("2026-10-01", "9700", "0"), point("2026-10-02", "0", "5400")],
    },
    {
      currency: "USD",
      gross_income_minor: "500",
      tax_minor: "0",
      refunds_minor: "0",
      net_income_minor: "500",
      expenses_minor: "900",
      provider_fees_minor: "0",
      provider_fees_recorded: false,
      profit_minor: "-400",
      paid_invoices: "1",
      expense_by_category: [{ category: "marketing", amount_minor: "900" }],
      series: [point("2026-10-01", "500", "900")],
    },
  ],
};

const expense = (id: string, vendor: string, extra: Record<string, unknown> = {}) => ({
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
  ...extra,
});

async function fixedToday(page: Page) {
  await page.clock.setFixedTime(new Date(2026, 9, 21, 12, 0, 0)); // 21 October 2026, local
}

test.describe("accounting", () => {
  test("each currency has its own cards, a missing fee is 'not recorded', and a loss is shown as one", async ({ page }) => {
    await fixedToday(page);
    const gw = baseTenant(new MockGateway()).on((c) => {
      if (c.path === "/v1/accounting/summary") return { body: summary };
      if (c.path === "/v1/expenses") return { body: { expenses: [expense("e1", "AWS")] } };
      return undefined;
    });
    await gw.install(page);
    await openTenantPage(page, "/accounting");

    const lira = page.getByRole("region", { name: "TRY" });
    await expect(lira.locator('[data-stat="income"]')).toContainText("TRY 120.00");
    await expect(lira.locator('[data-stat="tax"]')).toContainText("TRY 20.00");
    await expect(lira.locator('[data-stat="refunds"]')).toContainText("TRY 3.00");
    await expect(lira.locator('[data-stat="net"]')).toContainText("TRY 97.00");
    await expect(lira.locator('[data-stat="fees"]')).toContainText("TRY 4.00");
    await expect(lira.locator('[data-stat="expenses"]')).toContainText("TRY 54.00");
    await expect(lira.locator('[data-stat="profit"]')).toContainText("TRY 43.00");
    // The donut has a legend with the name, the amount and the share of every category.
    await expect(lira.getByText("Infrastructure")).toBeVisible();
    await expect(lira.getByText(/TRY\s40\.00\s·\s74\.1%/)).toBeVisible();

    const dollars = page.getByRole("region", { name: "USD" });
    await expect(dollars.locator('[data-stat="income"]')).toContainText("$5.00");
    await expect(dollars.locator('[data-stat="fees"]')).toContainText("Not recorded");
    await expect(dollars.locator('[data-stat="profit"]')).toContainText("-$4.00");
    await expect(dollars.locator('[data-stat="profit"] dd')).toHaveClass(/text-destructive/);
    await expect(page.getByText("never added together or converted")).toBeVisible();

    // The first request is for this month, by day.
    const first = gw.called("GET", "/v1/accounting/summary")[0];
    expect(first?.query.get("from")).toBe("2026-10-01");
    expect(first?.query.get("to")).toBe("2026-10-31");
    expect(first?.query.get("granularity")).toBe("day");
  });

  test("the period picker asks for the chosen range, and a long one by month", async ({ page }) => {
    await fixedToday(page);
    const gw = baseTenant(new MockGateway()).on((c) => {
      if (c.path === "/v1/accounting/summary") return { body: { from_date: c.query.get("from"), to_date: c.query.get("to"), granularity: c.query.get("granularity"), currencies: [] } };
      if (c.path === "/v1/expenses") return { body: { expenses: [] } };
      return undefined;
    });
    await gw.install(page);
    await openTenantPage(page, "/accounting");
    await expect(page.getByText("No income or expenses in this period.")).toBeVisible();

    await page.getByRole("button", { name: "Last month" }).click();
    await expect.poll(() => gw.called("GET", "/v1/accounting/summary").some((c) => c.query.get("from") === "2026-09-01" && c.query.get("to") === "2026-09-30")).toBe(true);
    await page.getByRole("button", { name: "Last 30 days" }).click();
    await expect.poll(() => gw.called("GET", "/v1/accounting/summary").some((c) => c.query.get("from") === "2026-09-22" && c.query.get("to") === "2026-10-21")).toBe(true);
    await page.getByRole("button", { name: "This year" }).click();
    await expect
      .poll(() => gw.called("GET", "/v1/accounting/summary").some((c) => c.query.get("from") === "2026-01-01" && c.query.get("to") === "2026-10-21" && c.query.get("granularity") === "month"))
      .toBe(true);

    const before = gw.called("GET", "/v1/accounting/summary").length;
    await page.getByRole("button", { name: "Custom" }).click();
    await page.getByLabel("From").fill("2026-02-01");
    await page.getByLabel("To", { exact: true }).fill("2026-01-01");
    await expect(page.getByText("Choose a first and a last day")).toBeVisible();
    expect(gw.called("GET", "/v1/accounting/summary").length).toBe(before); // an invalid range is not sent
    await page.getByLabel("To", { exact: true }).fill("2026-02-10");
    await expect.poll(() => gw.called("GET", "/v1/accounting/summary").some((c) => c.query.get("from") === "2026-02-01" && c.query.get("to") === "2026-02-10")).toBe(true);
  });

  test("an expense is added with a recurrence, edited, and voided with a reason", async ({ page }) => {
    await fixedToday(page);
    const list = [expense("e1", "AWS")];
    const gw = baseTenant(new MockGateway()).on((c) => {
      if (c.path === "/v1/accounting/summary") return { body: { ...summary, currencies: [] } };
      if (c.method === "POST" && c.path === "/v1/expenses") {
        list.unshift(expense("e2", String(c.body?.vendor), { amount_minor: c.body?.amount_minor, currency: c.body?.currency, category: c.body?.category, recurring: c.body?.recurring, recurring_until: c.body?.recurring_until }));
        return { status: 201, body: list[0] };
      }
      if (c.method === "PUT" && c.path === "/v1/expenses/e1") {
        list[list.findIndex((e) => e.id === "e1")] = expense("e1", String(c.body?.vendor), { amount_minor: c.body?.amount_minor });
        return { body: list.find((e) => e.id === "e1") };
      }
      if (c.method === "DELETE" && c.path === "/v1/expenses/e1") {
        const i = list.findIndex((e) => e.id === "e1");
        list[i] = { ...list[i]!, void_time: "2026-10-21T10:00:00Z", void_reason: c.query.get("reason") } as never;
        return { body: list[i] };
      }
      if (c.path === "/v1/expenses") return { body: { expenses: c.query.get("include_voided") === "true" ? list : list.filter((e) => !("void_time" in e)) } };
      return undefined;
    });
    await gw.install(page);
    await openTenantPage(page, "/accounting");
    await expect(page.getByRole("row", { name: /AWS/ })).toContainText("TRY 40.00");

    // Add: the amount is typed in major units and sent in minor units, the recurrence and its last day go with it.
    await page.getByRole("button", { name: "Add expense" }).click();
    const dialog = page.getByRole("dialog");
    await dialog.getByLabel("Vendor").fill("Linear");
    await dialog.getByLabel("Category").selectOption("software");
    await dialog.getByLabel("Amount").fill("12,50");
    await pickCurrency(dialog.getByLabel("Currency"), "EUR");
    await dialog.getByLabel("Date").fill("2026-10-05");
    await dialog.getByLabel("Repeats").selectOption("monthly");
    await dialog.getByLabel("Last day (optional)").fill("2027-03-05");
    await dialog.getByRole("button", { name: "Save" }).click();
    await expect(page.getByRole("row", { name: /Linear/ })).toContainText("€12.50");
    await expect(page.getByRole("row", { name: /Linear/ })).toContainText("Monthly");
    expect(gw.called("POST", "/v1/expenses")[0]?.body).toEqual({
      vendor: "Linear",
      category: "software",
      amount_minor: "1250",
      currency: "EUR",
      incurred_on: "2026-10-05",
      recurring: "monthly",
      recurring_until: "2027-03-05",
    });

    // Edit: the dialog starts from the stored values, the amount back in major units.
    await page.getByRole("row", { name: /AWS/ }).getByRole("button", { name: "Edit" }).click();
    await expect(page.getByRole("dialog").getByLabel("Amount")).toHaveValue("40.00");
    await page.getByRole("dialog").getByLabel("Vendor").fill("Amazon Web Services");
    await page.getByRole("dialog").getByLabel("Amount").fill("45");
    await page.getByRole("dialog").getByRole("button", { name: "Save" }).click();
    await expect(page.getByRole("row", { name: /Amazon Web Services/ })).toContainText("TRY 45.00");
    expect(gw.called("PUT", "/v1/expenses/e1")[0]?.body).toMatchObject({ vendor: "Amazon Web Services", amount_minor: "4500", currency: "TRY", recurring: "none" });

    // Void: a reason is required, and the voided entry can be shown again.
    await page.getByRole("row", { name: /Amazon Web Services/ }).getByRole("button", { name: "Void" }).click();
    const voidDialog = page.getByRole("dialog");
    await expect(voidDialog.getByRole("button", { name: "Void expense" })).toBeDisabled();
    await voidDialog.getByLabel("Reason").fill("duplicate of the invoice");
    await voidDialog.getByRole("button", { name: "Void expense" }).click();
    await expect(page.getByRole("row", { name: /Amazon Web Services/ })).toHaveCount(0);
    expect(gw.called("DELETE", "/v1/expenses/e1")[0]?.query.get("reason")).toBe("duplicate of the invoice");
    await page.getByLabel("Show voided").click();
    await expect(page.getByRole("row", { name: /Amazon Web Services/ })).toContainText("Voided");
  });

  test("a refusal from the API is shown in the reader's language", async ({ page }) => {
    await fixedToday(page);
    const gw = baseTenant(new MockGateway()).on((c) => {
      if (c.path === "/v1/accounting/summary") return { body: { currencies: [] } };
      if (c.method === "POST" && c.path === "/v1/expenses") return { status: 400, body: { error: { code: "INVALID_ARGUMENT", key: "billing.accounting.unknown_currency", message: "the currency is not supported" } } };
      if (c.path === "/v1/expenses") return { body: { expenses: [] } };
      return undefined;
    });
    await gw.install(page);
    await openTenantPage(page, "/accounting");
    await page.getByRole("button", { name: "Add expense" }).click();
    const dialog = page.getByRole("dialog");
    await dialog.getByLabel("Vendor").fill("Acme");
    await dialog.getByLabel("Amount").fill("3");
    await dialog.getByRole("button", { name: "Save" }).click();
    await expect(dialog.getByRole("alert")).toContainText("The currency is not supported.");
  });

  test("an app filter limits the expenses and says so", async ({ page }) => {
    await fixedToday(page);
    const gw = baseTenant(new MockGateway(), { account_id: "acc-1" }).on((c) => {
      if (c.path === "/admin/v1/accounts/acc-1/apps") return { body: { apps: [{ id: "app-1", name: "Mobile" }, { id: "app-2", name: "Web" }] } };
      if (c.path === "/v1/accounting/summary") return { body: { currencies: [] } };
      if (c.path === "/v1/expenses") return { body: { expenses: [] } };
      return undefined;
    });
    await gw.install(page);
    await openTenantPage(page, "/accounting");
    await page.getByLabel("App", { exact: true }).selectOption("app-2");
    await expect(page.getByText("The app limits the expenses.")).toBeVisible();
    await expect.poll(() => gw.called("GET", "/v1/accounting/summary").some((c) => c.query.get("app_id") === "app-2")).toBe(true);
    await expect.poll(() => gw.called("GET", "/v1/expenses").some((c) => c.query.get("app_id") === "app-2")).toBe(true);
  });
});
