import { expect, test } from "@playwright/test";
import { baseTenant, MockGateway, openTenantPage, signInAsOperator, TENANT } from "./gateway";
import { METERS } from "./usage";

// Lists that gained the one select that orders by column and direction together (docs/13-admin-ui.md, "Lists"): the order follows the choice and is kept in the address.

test.use({ viewport: { width: 1440, height: 900 } });

test("meters are ordered by one select, and the order is kept in the address", async ({ page }) => {
  await baseTenant(new MockGateway()).on((c) => (c.method === "GET" && c.path === "/v1/meters" ? { body: { meters: METERS } } : undefined)).install(page);
  await openTenantPage(page, "/meters");
  const codes = METERS.map((m) => String(m.code)).sort();
  const order = async () => (await page.getByRole("row").allInnerTexts()).map((t) => codes.find((c) => t.includes(c))).filter(Boolean);
  await page.getByTestId("sort-select").selectOption("name_desc");
  await expect.poll(order).toEqual([...codes].reverse());
  await expect.poll(() => new URL(page.url()).searchParams.get("sort")).toBe("name_desc");
  await page.getByTestId("sort-select").selectOption("name_asc");
  await expect.poll(order).toEqual(codes);
});

/** The names (or any text) of a table's rows, in the order shown, as far as they are among `known`. */
const orderOf = (page: import("@playwright/test").Page, known: string[]) => async () =>
  (await page.getByRole("row").allInnerTexts()).map((t) => known.find((c) => t.includes(c))).filter(Boolean);

test("credit packs are ordered by name, and the choice goes back to the order they came in", async ({ page }) => {
  const packs = ["Zeta", "Alpha", "Mid"].map((name, i) => ({ id: `p${i}`, name, credits: "100", price_minor: "500", currency: "USD", active: true, create_time: `2026-10-0${i + 1}T09:00:00Z` }));
  await baseTenant(new MockGateway()).on((c) => (c.method === "GET" && c.path === "/v1/credit-packs" ? { body: { packs } } : c.path === "/v1/customers" ? { body: { customers: [] } } : undefined)).install(page);
  await openTenantPage(page, "/credits");
  const order = orderOf(page, ["Zeta", "Alpha", "Mid"]);
  await expect.poll(order).toEqual(["Zeta", "Alpha", "Mid"]);
  await page.getByTestId("sort-select").selectOption("name_asc");
  await expect.poll(order).toEqual(["Alpha", "Mid", "Zeta"]);
  await page.getByTestId("sort-select").selectOption("newest");
  await expect.poll(order).toEqual(["Mid", "Alpha", "Zeta"]);
  await page.getByTestId("sort-select").selectOption("");
  await expect.poll(order).toEqual(["Zeta", "Alpha", "Mid"]);
});

test("coupons are ordered by how often they were redeemed", async ({ page }) => {
  const coupon = (code: string, redeemed: number) => ({ id: code, code, name: code, percent_bps: 1000, duration: "COUPON_DURATION_ONCE", applies_to: ["COUPON_TARGET_SUBSCRIPTION"], active: true, redeemed, promotion_code_count: 1, create_time: "2026-10-01T09:00:00Z" });
  await baseTenant(new MockGateway()).on((c) => (c.method === "GET" && c.path === "/v1/coupons" ? { body: { coupons: [coupon("FEW", 1), coupon("MANY", 9), coupon("SOME", 4)] } } : undefined)).install(page);
  await openTenantPage(page, "/coupons");
  await page.getByTestId("sort-select").selectOption("count_desc");
  await expect.poll(orderOf(page, ["FEW", "MANY", "SOME"])).toEqual(["MANY", "SOME", "FEW"]);
});

test("the payment ledger of a tenant is ordered by amount", async ({ page }) => {
  const entry = (id: number, ref: string, amount: string) => ({ id, provider: "stripe", provider_ref: ref, kind: "charge", status: "succeeded", source: "webhook", amount_minor: amount, currency: "EUR", occurred_at: `2026-10-0${id}T09:00:00Z` });
  const gw = baseTenant(new MockGateway()).on((c) => (c.method === "GET" && c.path === `/admin/v1/tenants/${TENANT}/ledger` ? { body: { entries: [entry(1, "ref_one", "500"), entry(2, "ref_two", "90000"), entry(3, "ref_three", "7000")] } } : undefined));
  await gw.install(page);
  await openTenantPage(page, "/payments");
  const refs = ["ref_one", "ref_two", "ref_three"];
  await page.getByTestId("sort-select").first().selectOption("amount_desc");
  await expect.poll(orderOf(page, refs)).toEqual(["ref_two", "ref_three", "ref_one"]);
  await expect.poll(() => new URL(page.url()).searchParams.get("sort")).toBe("amount_desc");
  await page.getByTestId("sort-select").first().selectOption("amount_asc");
  await expect.poll(orderOf(page, refs)).toEqual(["ref_one", "ref_three", "ref_two"]);
});

test("dead-letter queues are ordered by how many messages they hold", async ({ page }) => {
  const gw = new MockGateway().on((c) => (c.method === "GET" && c.path === "/admin/v1/dead-letters" ? { body: { queues: [{ queue: "alpha", messages: "2" }, { queue: "beta", messages: "30" }, { queue: "gamma", messages: "11" }], messages: [] } } : undefined));
  await gw.install(page);
  await signInAsOperator(page);
  await page.goto("/dead-letters");
  await page.getByTestId("sort-select").selectOption("count_desc");
  await expect.poll(orderOf(page, ["alpha.dlq", "beta.dlq", "gamma.dlq"])).toEqual(["beta.dlq", "gamma.dlq", "alpha.dlq"]);
  await page.getByTestId("sort-select").selectOption("name_desc");
  await expect.poll(orderOf(page, ["alpha.dlq", "beta.dlq", "gamma.dlq"])).toEqual(["gamma.dlq", "beta.dlq", "alpha.dlq"]);
});
