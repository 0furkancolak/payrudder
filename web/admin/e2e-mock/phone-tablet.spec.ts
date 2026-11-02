import { expect, test, type Page } from "@playwright/test";
import { MockGateway } from "./gateway";
import { billingGateway, disputesGateway, LIVE, productGateway, SANDBOX, signInAsAda } from "./product";

// A phone (375) and a tablet (768): the pages and the sheets the review of 2026-11-02 touched do not scroll sideways, and a sheet stays inside the screen with nothing in its
// body wider than the body (a long address, a code block or a table that would push the sheet's edge off the screen). The text of the pages is checked by the specs of
// each page; this is the box.

const customer = { id: "id-1", external_id: "cus_a", name: "Ada Lovelace-Byron of a very long company name Ltd", email: "ada.lovelace.byron.of.a.long.address@a-very-long-company-domain.example", create_time: "2026-10-05T10:00:00Z", payment_method: "ck:ct", country: "TR", billing_currency: "TRY" };
const card = { token: "ct", alias: "work", bin: "55287900", last4: "0008", type: "CREDIT_CARD", association: "MASTER_CARD", family: "World", bank: "Halkbank", expire_month: "12", expire_year: "2030", in_use: true };
const sub = { id: "s1", customer_external_id: "cus_a", plan_code: "basic", plan_version: 1, state: "SUBSCRIPTION_STATE_ACTIVE", currency: "TRY", owner: "payrudder", start_time: "2026-10-01T00:00:00Z", fee_timing: "FEE_TIMING_ADVANCE", scheduled_change: { plan_code: "professional-annual-with-a-long-code", interval: "SUBSCRIPTION_INTERVAL_YEAR", effective_time: "2026-11-30T00:00:00Z" } };
const point = (bucket: string, net: string, exp: string) => ({ bucket, gross_income_minor: net, tax_minor: "0", refunds_minor: "0", net_income_minor: net, expenses_minor: exp, profit_minor: String(Number(net) - Number(exp)) });
const accountingSummary = {
  from_date: "2026-10-01",
  to_date: "2026-10-31",
  granularity: "day",
  currencies: [
    { currency: "TRY", gross_income_minor: "12000", tax_minor: "2000", refunds_minor: "300", net_income_minor: "9700", expenses_minor: "5400", provider_fees_minor: "0", provider_fees_recorded: false, profit_minor: "4300", paid_invoices: "3", expense_by_category: [{ category: "infrastructure", amount_minor: "5400" }], series: [point("2026-10-01", "9700", "0"), point("2026-10-02", "0", "5400"), point("2026-10-30", "100", "100")] },
  ],
};
const get = (path: string, body: unknown) => (gw: MockGateway) => gw.on((c) => (c.method === "GET" && c.path === path ? { body } : undefined));

interface Case {
  name: string;
  path: string;
  tenant?: string;
  mock: (gw: MockGateway) => MockGateway;
  /** Something the page shows once it has loaded. */
  ready: (page: Page) => ReturnType<Page["locator"]>;
  /** Opens a sheet, when the page has one to look at. */
  sheet?: (page: Page) => Promise<void>;
  /** Something else about the page's box. */
  extra?: (page: Page) => Promise<void>;
}

const CASES: Case[] = [
  {
    name: "customers and a customer's sheet",
    path: "/customers",
    mock: (gw) => get("/v1/customers/cus_a/cards", { provider: "iyzico", cards: [card] })(get("/v1/customers", { customers: [customer] })(gw)),
    ready: (page) => page.getByTestId("customer-cus_a"),
    sheet: async (page) => {
      await page.getByTestId("customer-cus_a").click();
      await expect(page.getByTestId("customer-detail")).toBeVisible();
    },
  },
  {
    name: "subscriptions and a subscription's sheet",
    path: "/subscriptions",
    mock: (gw) => get("/v1/subscriptions", { subscriptions: [sub] })(get("/v1/plans", { plans: [] })(gw)),
    ready: (page) => page.getByText("cus_a").first(),
    sheet: async (page) => {
      await page.getByText("cus_a").first().click();
      await expect(page.getByRole("region", { name: /Manage cus_a/ })).toBeVisible();
    },
  },
  {
    name: "credits",
    path: "/credits?customer=cus_a",
    mock: (gw) =>
      [
        get("/v1/credit-packs", { packs: [] }),
        get("/v1/customers", { customers: [customer] }),
        get("/v1/customers/cus_a/credits", { balance: "300", next_expire_time: null, lots: [{ id: "l1", granted: "420", remaining: "300", source: "promo", expire_time: null, create_time: "2026-10-21T09:00:00Z" }] }),
        get("/v1/customers/cus_a/credits/ledger", { entries: [], next_page_token: "" }),
        get("/v1/credits/settings", { low_threshold: "50" }),
      ].reduce((g, m) => m(g), gw),
    ready: (page) => page.getByTestId("credits-balance-value"),
  },
  {
    name: "connect your app (the sandbox)",
    path: "/connect",
    tenant: SANDBOX,
    mock: (gw) => [get("/v1/meters", { meters: [] }), get("/v1/customers", { customers: [] })].reduce((g, m) => m(g), gw),
    ready: (page) => page.getByTestId("connect-step-key"),
  },
  {
    name: "accounting: the chart's dates and its legend",
    path: "/accounting",
    mock: (gw) => [get("/v1/accounting/summary", accountingSummary), get("/v1/expenses", { expenses: [] })].reduce((g, m) => m(g), gw),
    ready: (page) => page.getByTestId("bars-legend"),
    // The review of 2026-11-02 found the legend over the axis dates at 375 px (docs/ui-test-report.md): the legend is under the dates, and neither is cut off.
    extra: async (page) => {
      const [axis, legend] = await Promise.all(["bars-axis", "bars-legend"].map((id) => page.getByTestId(id).evaluate((e) => { const r = e.getBoundingClientRect(); return { top: r.top, bottom: r.bottom, left: r.left, right: r.right }; })));
      expect(legend!.top, "the legend starts under the dates").toBeGreaterThanOrEqual(axis!.bottom);
      for (const box of [axis!, legend!]) {
        expect(box.left).toBeGreaterThanOrEqual(0);
        expect(box.right).toBeLessThanOrEqual(await page.evaluate(() => window.innerWidth));
      }
    },
  },
  {
    name: "disputes",
    path: "/payments?tab=disputes",
    mock: (gw) => disputesGateway(billingGateway(gw)),
    ready: (page) => page.getByTestId("dispute-row").first(),
  },
];

const sizes = [
  { name: "a phone", width: 375, height: 812 },
  { name: "a tablet", width: 768, height: 1024 },
];

for (const size of sizes) {
  for (const c of CASES) {
    test(`${c.name} on ${size.name}: nothing scrolls sideways, and a sheet stays on the screen`, async ({ page }) => {
      await page.setViewportSize({ width: size.width, height: size.height });
      const { gw } = productGateway(new MockGateway());
      c.mock(gw);
      await gw.install(page);
      await signInAsAda(page);
      await page.goto(`/app/t/${c.tenant ?? LIVE}${c.path}`);
      await expect(c.ready(page)).toBeVisible();
      const sideways = () => page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
      expect(await sideways(), "the page").toBeLessThanOrEqual(0);
      await c.extra?.(page);
      if (!c.sheet) return;
      await c.sheet(page);
      // The sheet slides in: measure it where it stops.
      await page.waitForFunction(() => (document.querySelector('[data-slot="sheet-content"]') as HTMLElement | null)?.getAnimations().every((a) => a.playState !== "running") ?? false);
      const box = await page.evaluate(() => {
        const sheet = document.querySelector('[data-slot="sheet-content"]') as HTMLElement;
        const body = sheet.querySelector('[data-slot="sheet-body"]') as HTMLElement;
        const r = sheet.getBoundingClientRect();
        return { left: Math.round(r.left), right: Math.round(r.right), width: window.innerWidth, bodyOverflow: body.scrollWidth - body.clientWidth };
      });
      expect(box.left, "the sheet's left edge").toBeGreaterThanOrEqual(0);
      expect(box.right, "the sheet's right edge").toBeLessThanOrEqual(box.width);
      expect(box.bodyOverflow, "something in the sheet is wider than the sheet").toBeLessThanOrEqual(0);
      expect(await sideways(), "the page with the sheet open").toBeLessThanOrEqual(0);
    });
  }
}
