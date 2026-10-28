import { expect, test, type Page } from "@playwright/test";
import { baseTenant, MockGateway, openTenantPage } from "./gateway";
import { confirmDialog, pickFilter, rowAction } from "./ui";

// The customer list after Stripe's: who, how their subscriptions stand, where, what they spent and when they last paid, worked out from the
// invoices, payments and subscriptions the console loads. A row opens the customer's sheet: figures, quick actions and a tab per kind of record.

const customers = [
  { id: "id-1", external_id: "cus_1", name: "Ada Lovelace", email: "ada@example.com", country: "TR", billing_currency: "TRY", create_time: "2026-10-01T10:00:00Z", address: { line1: "Atatürk Cd. 1", city: "İstanbul", country: "TR" }, tax_id: "11111111111" },
  { id: "id-2", external_id: "cus_2", name: "Grace Hopper", email: "grace@example.com", country: "DE", billing_currency: "EUR", create_time: "2026-10-02T10:00:00Z" },
  { id: "id-3", external_id: "cus_3", name: "", email: "", create_time: "2026-10-10T10:00:00Z" },
];
const invoices = [
  { id: "inv-1", number: "INV-1", customer_external_id: "cus_1", status: "paid", currency: "TRY", total_minor: "150000", create_time: "2026-10-01T10:00:00Z", finalize_time: "2026-10-01T10:00:00Z" },
  { id: "inv-2", number: "INV-2", customer_external_id: "cus_1", status: "open", currency: "TRY", total_minor: "25000", create_time: "2026-10-15T10:00:00Z", finalize_time: "2026-10-15T10:00:00Z" },
  { id: "inv-3", number: "INV-3", customer_external_id: "cus_2", status: "uncollectible", currency: "EUR", total_minor: "9900", create_time: "2026-10-05T10:00:00Z" },
];
const payments = [{ id: "pay-1", invoice_id: "inv-1", provider: "mockpsp", kind: "charge", status: "paid", amount_minor: "150000", currency: "TRY", occurred_at: "2026-10-01T10:05:00Z" }];
const subscriptions = [
  { id: "sub-1", customer_external_id: "cus_1", plan_code: "pro", plan_version: 2, state: "SUBSCRIPTION_STATE_ACTIVE", currency: "TRY", start_time: "2026-10-01T10:00:00Z" },
  { id: "sub-2", customer_external_id: "cus_2", plan_code: "basic", plan_version: 1, state: "SUBSCRIPTION_STATE_PAST_DUE", currency: "EUR", start_time: "2026-10-02T10:00:00Z" },
];

function customerGateway() {
  return baseTenant(new MockGateway()).on((c) => {
    if (c.method !== "GET" && !(c.method === "POST" && c.path === "/v1/subscriptions")) return undefined;    if (c.path === "/v1/customers") return { body: { customers } };
    if (c.path === "/v1/invoices") {
      const who = c.query.get("customer");
      return { body: { invoices: who ? invoices.filter((i) => i.customer_external_id === who) : invoices } };
    }
    if (c.path === "/v1/payments") return { body: { payments } };
    if (c.path === "/v1/subscriptions" && c.method === "POST") return { status: 201, body: { id: "sub-new", ...c.body, state: "SUBSCRIPTION_STATE_ACTIVE" } };
    if (c.path === "/v1/subscriptions") return { body: { subscriptions } };
    if (c.path === "/v1/plans") return { body: { plans: [{ id: "p1", code: "pro", name: "Pro", latest_version: 2 }] } };
    if (c.path === "/v1/plans/pro/versions/latest") return { body: { id: "v2", plan_code: "pro", version: 2, base_fee_minor: "9900", prices: [], publish_time: "2026-10-05T10:00:00Z" } };
    const credits = /^\/v1\/customers\/(cus_\d)\/credits$/.exec(c.path);
    if (credits) return { body: { balance: credits[1] === "cus_1" ? "1200" : "0", lots: [] } };
    if (/^\/v1\/customers\/cus_\d\/credits\/ledger$/.test(c.path)) return { body: { entries: [], next_page_token: "" } };
    return undefined;
  });
}

async function openCustomers(page: Page) {
  const gw = customerGateway();
  await gw.install(page);
  await openTenantPage(page, "/customers");
  await expect(page.getByTestId("customer-cus_1")).toBeVisible();
  return gw;
}

test.describe("customer list", () => {
  test.use({ viewport: { width: 1440, height: 900 } });

  test("each row says who, how their subscriptions stand, where, what they spent and when they last paid", async ({ page }) => {
    await openCustomers(page);
    const ada = page.getByTestId("customer-cus_1");
    await expect(ada).toContainText("Ada Lovelace");
    await expect(ada).toContainText("ada@example.com");
    await expect(ada).toContainText("AL");
    await expect(ada).toContainText("Active");
    await expect(ada).toContainText("pro");
    await expect(ada).toContainText("1 open");
    await expect(ada).toContainText("🇹🇷");
    await expect(ada).toContainText("1,500.00");
    await expect(ada).toContainText("Oct 1, 2026");
    const grace = page.getByTestId("customer-cus_2");
    await expect(grace).toContainText("Past due");
    await expect(grace).toContainText("🇩🇪");
    // A customer with no name or e-mail is named by their id, and has no figures yet.
    await expect(page.getByTestId("customer-cus_3")).toContainText("cus_3");
  });

  test("filters narrow the list as chips and clear in one go", async ({ page }) => {
    await openCustomers(page);
    await pickFilter(page, "Subscription", "Has an active subscription");
    await expect(page.getByTestId("customer-cus_3")).toHaveCount(0);
    await expect(page.getByTestId("customer-cus_1")).toBeVisible();
    await pickFilter(page, "Billing", "Delinquent");
    await expect(page.getByTestId("customer-cus_1")).toHaveCount(0);
    await expect(page.getByTestId("customer-cus_2")).toBeVisible();
    await page.getByRole("button", { name: "Clear filters" }).click();
    await pickFilter(page, "Billing", "Open invoices");
    await expect(page.getByTestId("customer-cus_1")).toBeVisible();
    await expect(page.getByTestId("customer-cus_2")).toHaveCount(0);
    await page.getByRole("button", { name: "Clear filters" }).click();
    await pickFilter(page, "Country", /Germany/);
    await expect(page.getByTestId("customer-cus_2")).toBeVisible();
    await expect(page.getByTestId("customer-cus_1")).toHaveCount(0);
    await page.getByRole("button", { name: "Clear filters" }).click();
    await page.getByRole("searchbox").fill("grace@");
    await expect(page.getByTestId("customer-cus_2")).toBeVisible();
    await expect(page.getByTestId("customer-cus_1")).toHaveCount(0);
  });

  test("no customers: the page says what a customer is and offers the first one", async ({ page }) => {
    await baseTenant(new MockGateway()).on((c) => (c.path === "/v1/customers" ? { body: { customers: [] } } : undefined)).install(page);
    await openTenantPage(page, "/customers");
    await expect(page.getByTestId("customers-empty")).toContainText("No customers yet.");
    await page.getByTestId("customers-new").click();
    await expect(page.getByRole("form", { name: "new customer" })).toBeVisible();
  });
});

test.describe("customer order", () => {
  test.use({ viewport: { width: 1440, height: 900 } });

  test("one select orders the list by column and direction together, empty names last", async ({ page }) => {
    await openCustomers(page);
    const ids = async () => await page.getByTestId(/^customer-cus_/).evaluateAll((els) => els.map((e) => e.getAttribute("data-testid")));
    await page.getByTestId("sort-select").selectOption("newest");
    expect(await ids()).toEqual(["customer-cus_3", "customer-cus_2", "customer-cus_1"]);
    await page.getByTestId("sort-select").selectOption("oldest");
    expect(await ids()).toEqual(["customer-cus_1", "customer-cus_2", "customer-cus_3"]);
    await page.getByTestId("sort-select").selectOption("name_desc");
    // A customer with no name is ordered by its id, which sorts before the names here.
    expect((await ids())[0]).toBe("customer-cus_2");
    await page.getByTestId("sort-select").selectOption("");
    expect(await ids()).toEqual(["customer-cus_1", "customer-cus_2", "customer-cus_3"]);
  });
});

test.describe("customer bulk export", () => {
  test.use({ viewport: { width: 1440, height: 900 } });

  test("ticked customers are exported as a CSV file, and ticking a row does not open its sheet", async ({ page }) => {
    await openCustomers(page);
    await page.getByTestId("customer-cus_1").getByTestId("row-check").click();
    await expect(page.getByTestId("detail-sheet")).toHaveCount(0);
    await page.getByTestId("customer-cus_2").getByTestId("row-check").click();
    await expect(page.getByTestId("bulk-count")).toHaveText("2 selected");
    const download = page.waitForEvent("download");
    await page.getByTestId("bulk-export").click();
    const file = await download;
    expect(file.suggestedFilename()).toBe("customers.csv");
    const text = (await (await import("node:fs/promises")).readFile((await file.path())!, "utf8")).replace("\uFEFF", "");
    expect(text.split("\r\n")[0]).toBe("external_id,name,email,country,billing_currency,tax_id,created");
    expect(text).toContain("cus_1,Ada Lovelace,ada@example.com,TR,TRY");
    expect(text).toContain("cus_2,Grace Hopper");
    expect(text).not.toContain("cus_3");
    // The header tick takes the whole page, a second press takes it off.
    await page.getByTestId("bulk-clear").click();
    await page.getByRole("checkbox", { name: "Select all rows on this page" }).click();
    await expect(page.getByTestId("bulk-count")).toHaveText("3 selected");
    await page.getByRole("checkbox", { name: "Select all rows on this page" }).click();
    await expect(page.getByTestId("bulk-bar")).toHaveCount(0);
  });
});

test.describe("customer block", () => {
  test.use({ viewport: { width: 1440, height: 900 } });

  test("a customer is blocked from a row action after asking, shown as blocked, and unblocked; many at once name the ones that failed", async ({ page }) => {
    const gw = customerGateway();
    const blocked = new Map<string, string>();
    gw.on((c) => {
      const m = /^\/v1\/customers\/(cus_\d)\/(block|unblock)$/.exec(c.path);
      if (!m || c.method !== "POST") return undefined;
      if (m[1] === "cus_2" && m[2] === "block") return { status: 400, body: { error: { code: "INVALID_ARGUMENT", key: "plans.request.invalid", message: "no" } } };
      if (m[2] === "block") blocked.set(m[1]!, "x");
      else blocked.delete(m[1]!);
      return { body: {} };
    });
    // The list answers with the block state the server now holds.
    gw.on((c) => (c.method === "GET" && c.path === "/v1/customers" ? { body: { customers: customers.map((x) => (blocked.has(x.external_id) ? { ...x, block_time: "2026-10-28T10:00:00Z", block_reason: "chargebacks" } : x)) } } : undefined));
    await gw.install(page);
    await openTenantPage(page, "/customers");
    await expect(page.getByTestId("customer-cus_1")).toBeVisible();

    await rowAction(page.getByTestId("customer-cus_1"), "Block");
    await confirmDialog(page);
    await expect(page.getByTestId("customer-cus_1").getByTestId("customer-blocked")).toBeVisible();
    expect(gw.called("POST", "/v1/customers/cus_1/block")).toHaveLength(1);

    await rowAction(page.getByTestId("customer-cus_1"), "Lift the block");
    await expect(page.getByTestId("customer-cus_1").getByTestId("customer-blocked")).toHaveCount(0);

    // Two ticked: one is refused, the other is blocked, and the refused one is named.
    await page.getByTestId("customer-cus_1").getByTestId("row-check").click();
    await page.getByTestId("customer-cus_2").getByTestId("row-check").click();
    await page.getByTestId("bulk-block").click();
    await confirmDialog(page);
    await expect(page.getByTestId("bulk-note")).toContainText("1 done, 1 not changed");
    await expect(page.getByTestId("bulk-note")).toContainText("cus_2");
    await expect(page.getByTestId("customer-cus_1").getByTestId("customer-blocked")).toBeVisible();
  });
});

test.describe("customer sheet", () => {
  test.use({ viewport: { width: 1440, height: 900 } });

  test("the head says who, the strip shows the figures, and the tabs hold the records", async ({ page }) => {
    await openCustomers(page);
    await page.getByTestId("customer-cus_1").click();
    const sheet = page.getByTestId("customer-detail");
    await expect(page.getByTestId("detail-sheet").getByRole("heading", { name: "Ada Lovelace" })).toBeVisible();
    await expect(sheet.getByTestId("customer-email")).toHaveText("ada@example.com");
    await expect(sheet.getByTestId("customer-copy-id")).toContainText("cus_1");
    await expect(sheet.getByTestId("customer-spend")).toContainText("1,500.00");
    await expect(sheet.getByTestId("customer-open")).toContainText("250.00");
    await expect(sheet.getByTestId("customer-credits")).toHaveText("1,200");
    await expect(sheet.getByTestId("customer-active-subscriptions")).toHaveText("1");

    // The overview lists the newest records of each kind; every row links to its own sheet.
    await expect(sheet.getByRole("region", { name: "Subscriptions" })).toContainText("pro v2");
    await expect(sheet.getByRole("region", { name: "Invoices" })).toContainText("INV-2");
    await expect(sheet.getByRole("region", { name: "Payments" })).toContainText("Mock PSP");

    await sheet.getByTestId("customer-tab-invoices").click();
    // An invoice opens over the customer's sheet, not instead of it.
    await sheet.getByRole("button", { name: /INV-1/ }).click();
    await expect(page.getByTestId("sheet-tab")).toHaveCount(2);
    await page.getByTestId("sheet-back").click();
    await expect(page.getByTestId("sheet-tab")).toHaveCount(1); // back to the customer; the strip stays for "forward"
    await sheet.getByTestId("customer-tab-details").click();
    await expect(sheet.getByTestId("customer-details")).toContainText("Atatürk Cd. 1, İstanbul, TR");
    await expect(sheet.getByTestId("customer-details")).toContainText("11111111111");
    await sheet.getByTestId("customer-tab-credits").click();
    await expect(sheet).toContainText("Credit history");

    // The arrows step to the next customer, whose sheet has nothing yet.
    await page.getByTestId("detail-next").click();
    await expect(page.getByTestId("detail-sheet").getByRole("heading", { name: "Grace Hopper" })).toBeVisible();
    await sheet.getByTestId("customer-tab-payments").click();
    await expect(sheet).toContainText("No payments yet.");
  });

  test("a subscription is started from the sheet for that customer", async ({ page }) => {
    const gw = await openCustomers(page);
    await page.getByTestId("customer-cus_1").click();
    await page.getByTestId("customer-new-subscription").click();
    const form = page.getByRole("form", { name: "new subscription" });
    await expect(form.getByLabel("Customer")).toHaveValue("cus_1");
    await form.getByLabel("Package").selectOption("pro");
    await page.getByRole("button", { name: "Create subscription" }).click();
    await expect.poll(() => gw.called("POST", "/v1/subscriptions").length).toBe(1);
    expect(gw.called("POST", "/v1/subscriptions")[0]?.body).toMatchObject({ customer_external_id: "cus_1", plan_code: "pro" });
  });

  test("the edit menu opens the contact, address and tax sheets above the customer's sheet", async ({ page }) => {
    const gw = await openCustomers(page);
    gw.on((c) => (c.method === "PUT" && /^\/v1\/customers\/cus_1\/(contact|address|tax)$/.test(c.path) ? { body: { ...customers[0], ...c.body } } : undefined));
    await page.getByTestId("customer-cus_1").click();
    await page.getByTestId("customer-edit").click();
    await page.getByTestId("customer-edit-contact").click();
    const contact = page.getByRole("dialog", { name: "Contact of cus_1" });
    await contact.getByLabel("Name").fill("Yeni Ad");
    await contact.getByLabel("E-mail").fill("yeni@example.com");
    await contact.getByLabel("Language").selectOption("tr");
    await contact.getByLabel("Type").selectOption("business");
    await contact.getByRole("button", { name: "Save contact" }).click();
    await expect.poll(() => gw.called("PUT", "/v1/customers/cus_1/contact").length).toBe(1);
    expect(gw.called("PUT", "/v1/customers/cus_1/contact")[0]?.body).toEqual({ name: "Yeni Ad", email: "yeni@example.com", locale: "tr", customer_type: "business" });

    await page.getByTestId("customer-edit").click();
    await page.getByTestId("customer-edit-address").click();
    const address = page.getByRole("dialog", { name: "Address of cus_1" });
    await expect(address.getByLabel("City")).toHaveValue("İstanbul");
    await address.getByLabel("City").fill("Ankara");
    await address.getByRole("button", { name: "Save address" }).click();
    await expect.poll(() => gw.called("PUT", "/v1/customers/cus_1/address").length).toBe(1);
    expect(gw.called("PUT", "/v1/customers/cus_1/address")[0]?.body).toMatchObject({ line1: "Atatürk Cd. 1", city: "Ankara", country: "TR" });

    await page.getByTestId("customer-edit").click();
    await page.getByTestId("customer-edit-tax").click();
    const tax = page.getByRole("dialog", { name: "Tax data of cus_1" });
    await expect(tax.getByLabel("Tax id (VKN/TCKN or VAT id)")).toHaveValue("11111111111");
    await tax.getByLabel("Tax office").fill("Kadıköy");
    await tax.getByRole("button", { name: "Save tax data" }).click();
    await expect.poll(() => gw.called("PUT", "/v1/customers/cus_1/tax").length).toBe(1);
  });
});

test.describe("customers on a phone", () => {
  test.use({ viewport: { width: 375, height: 812 } });

  test("the list and the sheet fit the screen", async ({ page }) => {
    await openCustomers(page);
    const overflow = () => page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
    expect(await overflow()).toBeLessThanOrEqual(0);
    await page.getByTestId("customer-cus_1").click();
    await expect(page.getByTestId("customer-summary")).toBeVisible();
    // The sheet slides in; measure once it has come to rest.
    const right = async (testid: string) => {
      const box = await page.getByTestId(testid).boundingBox();
      return (box?.x ?? 0) + (box?.width ?? 0);
    };
    await expect.poll(() => right("detail-sheet")).toBeLessThanOrEqual(375);
    for (const part of ["customer-summary", "customer-copy-id", "customer-new-subscription", "customer-edit"]) await expect.poll(() => right(part), { message: part }).toBeLessThanOrEqual(375);
    expect(await overflow()).toBeLessThanOrEqual(0);
  });
});
