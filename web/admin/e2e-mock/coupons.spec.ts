import { expect, test, type Page } from "@playwright/test";
import { baseTenant, MockGateway, openTenantPage } from "./gateway";
import { pickCurrency, pickFilter, rowAction } from "./ui";

const ALL = ["COUPON_TARGET_SUBSCRIPTION", "COUPON_TARGET_CREDIT_PURCHASE", "COUPON_TARGET_PAYMENT"];
const WELCOME = { id: "c1", code: "WELCOME", name: "Welcome", percent_bps: 1000, duration: "COUPON_DURATION_ONCE", applies_to: ALL, active: true, redeemed: 2, promotion_code_count: 2, create_time: "2026-10-01T09:00:00Z" };
const BONUS = { id: "c2", code: "BONUS25", bonus_percent_bps: 2500, applies_to: ["COUPON_TARGET_CREDIT_PURCHASE"], active: true, redeemed: 0, promotion_code_count: 1 };
const OLD = { id: "c3", code: "OLD", amount_minor: "500", currency: "USD", duration: "COUPON_DURATION_FOREVER", active: false, redeemed: 7, max_redemptions: 10 };
const CODES = [
  { id: "p1", coupon_code: "WELCOME", code: "WELCOME", active: true, redeemed: 1 },
  { id: "p2", coupon_code: "WELCOME", code: "SUMMER", active: true, redeemed: 1, max_redemptions: 5, first_purchase_only: true, expire_time: "2026-12-31T20:59:59Z", allowed_plans: ["pro"] },
  { id: "p3", coupon_code: "BONUS25", code: "BONUS25", active: true, redeemed: 0 },
];
const REDEMPTIONS = [
  { id: "r1", coupon_code: "WELCOME", promotion_code: "WELCOME", customer_external_id: "cus_1", target: "COUPON_TARGET_CREDIT_PURCHASE", invoice_id: "d0000000-0000-4000-8000-000000000001", discount_minor: "500", currency: "USD", create_time: "2026-10-20T10:00:00Z" },
  { id: "r2", coupon_code: "WELCOME", promotion_code: "SUMMER", customer_external_id: "cus_2", target: "COUPON_TARGET_SUBSCRIPTION", bonus_credits: "250", create_time: "2026-10-21T10:00:00Z", reverse_time: "2026-10-22T10:00:00Z" },
];

/** The coupon reads and writes of a mocked tenant; the lists answer the filters the console sends. */
function couponGateway(coupons: Record<string, unknown>[] = [WELCOME, BONUS, OLD]) {
  return baseTenant(new MockGateway()).on((c) => {
    if (c.path === "/v1/coupons" && c.method === "GET") return { body: { coupons } };
    if (c.path === "/v1/coupons" && c.method === "POST") {
      coupons.push({ ...c.body, redeemed: 0, active: true, applies_to: c.body?.applies_to ?? ALL });
      return { status: 201, body: coupons.at(-1) };
    }
    if (c.method === "DELETE" && c.path.startsWith("/v1/coupons/")) return { body: {} };
    if (c.method === "DELETE" && c.path.startsWith("/v1/promotion-codes/")) return { body: {} };
    if (c.path === "/v1/promotion-codes" && c.method === "GET") {
      const q = c.query;
      const rows = CODES.filter((x) => (!q.get("coupon") || x.coupon_code === q.get("coupon")) && (!q.get("search") || x.code.includes(q.get("search") ?? "")) && (!q.get("state") || (q.get("state") === "active") === x.active));
      return { body: { codes: rows, total: rows.length } };
    }
    if (/^\/v1\/coupons\/[^/]+\/promotion-codes$/.test(c.path) && c.method === "POST") return { status: 201, body: { codes: [{ id: "p9", code: "SPR-ABCD2345" }, { id: "p10", code: "SPR-EFGH6789" }] } };
    if (c.path === "/v1/coupon-redemptions" && c.method === "GET") {
      const q = c.query;
      const rows = REDEMPTIONS.filter((r) => (!q.get("coupon") || r.coupon_code === q.get("coupon")) && (!q.get("promotion_code") || r.promotion_code === q.get("promotion_code")) && (!q.get("customer_external_id") || r.customer_external_id === q.get("customer_external_id")));
      return { body: { redemptions: rows, total: rows.length } };
    }
    if (c.path === "/v1/plans" && c.method === "GET") return { body: { plans: [{ id: "pl1", code: "pro", name: "Pro" }, { id: "pl2", code: "team", name: "Team" }] } };
    if (c.path === "/v1/credit-packs" && c.method === "GET") return { body: { packs: [] } };
    return undefined;
  });
}

async function open(page: Page, gw: MockGateway) {
  await page.clock.setFixedTime(new Date(2026, 9, 21, 12, 0, 0));
  await gw.install(page);
  await openTenantPage(page, "/coupons");
}

test.describe("coupons", () => {
  test("the page has its own title and three tabs, and the coupons are a table with the state and the places they work", async ({ page }) => {
    await open(page, couponGateway());
    await expect(page.getByTestId("page-title")).toHaveText("Coupons");
    for (const tab of ["coupons", "codes", "redemptions"]) await expect(page.getByTestId(`coupons-tab-${tab}`)).toBeVisible();
    const row = page.getByTestId("coupon-WELCOME");
    await expect(row).toContainText("10 %");
    await expect(row).toContainText("Everywhere");
    await expect(row).toContainText("First fee only");
    await expect(page.getByTestId("coupon-BONUS25")).toContainText("25 % more credits");
    await expect(page.getByTestId("coupon-BONUS25")).toContainText("Credit purchase");
    await expect(page.getByTestId("coupon-OLD")).toContainText("7 / 10");
    await pickFilter(page, "State", "Inactive");
    await page.keyboard.press("Escape");
    await expect(page.getByTestId("coupon-WELCOME")).toHaveCount(0);
    await expect(page.getByTestId("coupon-OLD")).toBeVisible();
  });

  test("the pricing page no longer carries coupons", async ({ page }) => {
    const gw = couponGateway().on((c) => (c.method === "GET" && c.path === "/v1/tax-rates" ? { body: { rates: [] } } : c.method === "GET" && c.path === "/v1/dunning-settings" ? { body: { retry_hours: [24], defaults_in_use: true } } : undefined));
    await gw.install(page);
    await openTenantPage(page, "/pricing");
    await expect(page.getByRole("button", { name: "Save rates" })).toBeVisible();
    await expect(page.getByRole("button", { name: "New coupon" })).toHaveCount(0);
    expect(gw.called("GET", "/v1/coupons")).toHaveLength(0);
  });

  test("a row opens the coupon with its promotion codes and who redeemed it, with the invoice and the amount", async ({ page }) => {
    await open(page, couponGateway());
    await page.getByTestId("coupon-WELCOME").click();
    const detail = page.getByTestId("coupon-detail");
    await expect(detail).toContainText("10 %");
    await expect(detail.getByTestId("code-SUMMER")).toContainText("First purchase");
    await expect(detail.getByTestId("code-WELCOME")).toBeVisible();
    const red = detail.getByTestId("redemption-r1");
    await expect(red).toContainText("cus_1");
    await expect(red).toContainText("$5.00");
    await red.getByRole("button").click();
    await expect(page).toHaveURL(/sheet=.*peek(%3A|:)invoice(%3A|:)[^,]*d0000000-0000-4000-8000-000000000001/);
    await expect(detail.getByTestId("redemption-r2")).toContainText("Reversed");
    await expect(detail.getByTestId("redemption-r2")).toContainText("+250 credits");
  });

  test("a coupon is made in a sheet that says what it will do, as a percentage or as an amount", async ({ page }) => {
    const gw = couponGateway([]);
    await open(page, gw);
    await expect(page.getByText("No coupons yet.")).toBeVisible();
    await page.getByTestId("coupons-new").click();
    const form = page.getByRole("dialog");
    await expect(form.getByTestId("coupon-effect")).toContainText("Enter a value");
    await form.getByLabel("Code", { exact: true }).fill("WELCOME");
    await form.getByLabel("Percentage off (%)").fill("12,5");
    await expect(form.getByTestId("coupon-effect")).toContainText("12.5 % off: a $100.00 fee becomes $87.50.");
    await form.getByText("A number of fees").click();
    await form.getByLabel("Fees", { exact: true }).fill("3");
    await expect(form.getByTestId("coupon-preview")).toContainText("On a subscription: first 3 fees");
    await form.getByLabel("Redemptions limit").fill("100");
    await expect(form.getByTestId("coupon-preview")).toContainText("At most 100 uses in total.");
    await form.getByRole("button", { name: "Create coupon" }).click();
    await expect.poll(() => gw.called("POST", "/v1/coupons").length).toBe(1);
    expect(gw.called("POST", "/v1/coupons")[0]?.body).toEqual({ code: "WELCOME", name: "", percent_bps: 1250, duration: "COUPON_DURATION_REPEATING", duration_periods: 3, max_redemptions: 100 });
    await expect(form).toBeHidden();
    await expect(page.getByTestId("coupon-WELCOME")).toContainText("First 3 fees");

    // An amount in a currency, for as long as the subscription lasts; a day for the expiry; no promotion code of its own.
    await page.getByTestId("coupons-new").click();
    await form.getByLabel("Code", { exact: true }).fill("MINUS5");
    await form.getByText("Amount off", { exact: true }).click();
    await form.getByRole("textbox", { name: "Amount off" }).fill("4.99");
    await pickCurrency(form.getByLabel("Currency"), "USD");
    await expect(form.getByTestId("coupon-effect")).toContainText("$4.99 off: a $100.00 fee becomes $95.01.");
    await form.getByText("Every fee").click();
    await form.getByLabel("Expires on (optional)").fill("2026-12-31");
    await form.getByLabel("Also create a promotion code with this text").uncheck();
    await form.getByRole("button", { name: "Create coupon" }).click();
    await expect.poll(() => gw.called("POST", "/v1/coupons").length).toBe(2);
    expect(gw.called("POST", "/v1/coupons")[1]?.body).toMatchObject({ code: "MINUS5", amount_minor: "499", currency: "USD", duration: "COUPON_DURATION_FOREVER", without_default_code: true });
    expect(gw.called("POST", "/v1/coupons")[1]?.body?.expire_time).toBe(new Date(2026, 11, 31, 23, 59, 59).toISOString());
  });

  test("a bonus coupon is for credit purchases, and a percentage that is not one is refused before anything is sent", async ({ page }) => {
    const gw = couponGateway([]);
    await open(page, gw);
    await page.getByTestId("coupons-new").click();
    const form = page.getByRole("dialog");
    await form.getByLabel("Code", { exact: true }).fill("BONUS25");
    await form.getByText("Bonus credits", { exact: true }).click();
    await form.getByLabel("Extra credits (%)").fill("25");
    await expect(form.getByTestId("coupon-effect")).toContainText("25 % more credits: buying 1,000 credits gives 1,250.");
    await expect(form.getByText("Subscription fee")).toHaveCount(0);
    await form.getByRole("button", { name: "Create coupon" }).click();
    await expect.poll(() => gw.called("POST", "/v1/coupons").length).toBe(1);
    expect(gw.called("POST", "/v1/coupons")[0]?.body).toEqual({ code: "BONUS25", name: "", bonus_percent_bps: 2500 });
    // The sheet closes when the answer arrives; opening it again before that opens nothing, and the fields typed next go to the closing sheet.
    await expect(form).toBeHidden();

    await page.getByTestId("coupons-new").click();
    await form.getByLabel("Code", { exact: true }).fill("BAD");
    await form.getByLabel("Percentage off (%)").fill("101");
    await form.getByRole("button", { name: "Create coupon" }).click();
    await expect(form.getByRole("alert")).toContainText("Enter what the coupon takes off");
    expect(gw.called("POST", "/v1/coupons")).toHaveLength(1);
  });

  test("a coupon that works nowhere is refused, and the API's own refusal is shown", async ({ page }) => {
    const gw = couponGateway([]).on((c) => (c.method === "POST" && c.path === "/v1/coupons" ? { status: 409, body: { error: { code: "ALREADY_EXISTS", message: "coupon already exists" } } } : undefined));
    await open(page, gw);
    await page.getByTestId("coupons-new").click();
    const form = page.getByRole("dialog");
    await form.getByLabel("Code", { exact: true }).fill("DUPE");
    await form.getByLabel("Percentage off (%)").fill("5");
    for (const t of ["Subscription fee", "Credit purchase", "Payment link or invoice"]) await form.getByLabel(t).uncheck();
    await form.getByRole("button", { name: "Create coupon" }).click();
    await expect(form.getByRole("alert")).toContainText("Pick where the coupon works");
    expect(gw.called("POST", "/v1/coupons")).toHaveLength(0);
    await form.getByLabel("Payment link or invoice").check();
    await form.getByRole("button", { name: "Create coupon" }).click();
    await expect(form.getByRole("alert")).toContainText("coupon already exists");
  });

  test("a coupon is deactivated from its row, and from its sheet", async ({ page }) => {
    const gw = couponGateway();
    await open(page, gw);
    await rowAction(page.getByTestId("coupon-WELCOME"), "Deactivate");
    await expect.poll(() => gw.called("DELETE", "/v1/coupons/WELCOME").length).toBeGreaterThan(0);
    await page.getByTestId("coupon-BONUS25").click();
    await page.getByTestId("detail-sheet").getByRole("button", { name: "Deactivate" }).last().click();
    await expect.poll(() => gw.called("DELETE", "/v1/coupons/BONUS25").length).toBeGreaterThan(0);
  });
});

test.describe("promotion codes", () => {
  test("every code with its coupon, uses, expiry and rules; filtered by coupon and searched; a row opens the code", async ({ page }) => {
    const gw = couponGateway();
    await open(page, gw);
    await page.getByTestId("coupons-tab-codes").click();
    await expect(page.getByTestId("code-SUMMER")).toContainText("WELCOME");
    await expect(page.getByTestId("code-SUMMER")).toContainText("1 / 5");
    await expect(page.getByTestId("code-SUMMER")).toContainText("First purchase");
    await expect(page.getByTestId("code-BONUS25")).toBeVisible();
    await pickFilter(page, "Coupon", "BONUS25");
    await page.keyboard.press("Escape");
    await expect(page.getByTestId("code-SUMMER")).toHaveCount(0);
    expect(gw.called("GET", "/v1/promotion-codes").some((c) => c.query.get("coupon") === "BONUS25")).toBe(true);
    await page.getByRole("button", { name: "Clear filters" }).click();
    await page.getByRole("searchbox", { name: "Search codes" }).fill("SUM");
    await expect(page.getByTestId("code-SUMMER")).toBeVisible();
    await expect(page.getByTestId("code-WELCOME")).toHaveCount(0);
    await page.getByTestId("code-SUMMER").click();
    const detail = page.getByTestId("code-detail");
    await expect(detail).toContainText("WELCOME · 10 %");
    await expect(detail).toContainText("Plans: pro");
    await expect(detail.getByTestId("redemption-r2")).toContainText("cus_2");
    expect(gw.called("GET", "/v1/coupon-redemptions").some((c) => c.query.get("promotion_code") === "SUMMER")).toBe(true);
  });

  test("random codes are made in a sheet with the rules every code shares, and then shown to copy", async ({ page }) => {
    const gw = couponGateway();
    await open(page, gw);
    await page.getByTestId("coupons-tab-codes").click();
    await page.getByTestId("codes-new").click();
    const sheet = page.getByRole("dialog", { name: "Create codes" });
    await expect(sheet.getByLabel("Coupon")).toHaveValue("WELCOME");
    await sheet.getByText("Generate random codes").click();
    await sheet.getByLabel("How many (1 to 1000)").fill("2");
    await sheet.getByLabel("Prefix (optional)").fill("SPR-");
    await sheet.getByLabel("Uses per code (empty: no limit)").fill("1");
    await sheet.getByRole("checkbox", { name: /^pro/ }).check();
    await sheet.getByLabel("First purchase only").check();
    await expect(sheet.getByTestId("code-preview")).toContainText("Creates 2 promotion codes for WELCOME (10 %).");
    await expect(sheet.getByTestId("code-preview")).toContainText("Each code can be used 1 times.");
    await sheet.getByRole("button", { name: "Create codes" }).click();
    await expect.poll(() => gw.called("POST", "/v1/coupons/WELCOME/promotion-codes").length).toBe(1);
    expect(gw.called("POST", "/v1/coupons/WELCOME/promotion-codes")[0]?.body).toEqual({ template: { max_redemptions: 1, first_purchase_only: true, allowed_plans: ["pro"] }, count: 2, prefix: "SPR-" });
    await expect(page.getByTestId("made-codes")).toHaveValue("SPR-ABCD2345\nSPR-EFGH6789");
    await expect(page.getByText("2 codes created")).toBeVisible();
    await sheet.getByRole("button", { name: "Done" }).click();
    await expect(sheet).toBeHidden();
  });

  test("a bad code name is refused before anything is sent; from a coupon the coupon is fixed", async ({ page }) => {
    const gw = couponGateway();
    await open(page, gw);
    await page.getByTestId("coupon-WELCOME").click();
    await page.getByTestId("detail-new-code").click();
    const sheet = page.getByRole("dialog", { name: "Create codes" });
    await expect(sheet.getByLabel("Coupon")).toBeDisabled();
    await expect(sheet.getByLabel("Coupon")).toHaveValue("WELCOME");
    await sheet.getByLabel("Codes, one per line").fill("x");
    await sheet.getByRole("button", { name: "Create codes" }).click();
    await expect(sheet.getByRole("alert")).toContainText("2 to 40 letters");
    expect(gw.called("POST", "/v1/coupons/WELCOME/promotion-codes")).toHaveLength(0);
    await sheet.getByLabel("Codes, one per line").fill("SPRING26\nSPRING27");
    await sheet.getByRole("button", { name: "Create codes" }).click();
    await expect.poll(() => gw.called("POST", "/v1/coupons/WELCOME/promotion-codes").length).toBe(1);
    expect(gw.called("POST", "/v1/coupons/WELCOME/promotion-codes")[0]?.body).toEqual({ template: {}, codes: ["SPRING26", "SPRING27"] });
  });

  test("a code is deactivated from its row", async ({ page }) => {
    const gw = couponGateway();
    await open(page, gw);
    await page.getByTestId("coupons-tab-codes").click();
    await page.getByTestId("code-SUMMER").getByRole("button", { name: "Deactivate" }).click();
    await expect.poll(() => gw.called("DELETE", "/v1/promotion-codes/SUMMER").length).toBeGreaterThan(0);
  });
});

test.describe("redemptions", () => {
  test("every use with the customer, the invoice and the amount; narrowed by coupon or customer; a row opens it", async ({ page }) => {
    const gw = couponGateway();
    await open(page, gw);
    await page.getByTestId("coupons-tab-redemptions").click();
    const first = page.getByTestId("redemption-r1");
    await expect(first).toContainText("cus_1");
    await expect(first).toContainText("Credit purchase");
    await expect(first).toContainText("$5.00");
    await expect(page.getByTestId("redemption-r2")).toContainText("Reversed");
    await page.getByRole("searchbox", { name: "Search by customer id" }).fill("cus_2");
    await expect(page.getByTestId("redemption-r1")).toHaveCount(0);
    expect(gw.called("GET", "/v1/coupon-redemptions").some((c) => c.query.get("customer_external_id") === "cus_2")).toBe(true);
    await page.getByRole("button", { name: "Clear filters" }).click();
    await page.getByTestId("redemption-r1").click();
    const detail = page.getByTestId("redemption-detail");
    await expect(detail).toContainText("WELCOME");
    await expect(detail).toContainText("cus_1");
    await detail.getByRole("button").click();
    await expect(page).toHaveURL(/sheet=.*peek(%3A|:)invoice(%3A|:)[^,]*d0000000/);
  });

  test("nothing redeemed yet says so", async ({ page }) => {
    const gw = couponGateway().on((c) => (c.method === "GET" && c.path === "/v1/coupon-redemptions" ? { body: { redemptions: [], total: 0 } } : undefined));
    await open(page, gw);
    await page.getByTestId("coupons-tab-redemptions").click();
    await expect(page.getByText("Nothing has been redeemed yet.")).toBeVisible();
  });
});

test("no tab of the coupons page scrolls sideways at 375 px", async ({ page }) => {
  await page.setViewportSize({ width: 375, height: 812 });
  await open(page, couponGateway());
  for (const tab of ["coupons", "codes", "redemptions"]) {
    await page.getByTestId(`coupons-tab-${tab}`).click();
    await page.waitForLoadState("networkidle");
    const o = await page.evaluate(() => ({ sw: document.documentElement.scrollWidth, cw: document.documentElement.clientWidth }));
    expect(o.sw, `horizontal overflow on the ${tab} tab`).toBeLessThanOrEqual(o.cw);
  }
});
