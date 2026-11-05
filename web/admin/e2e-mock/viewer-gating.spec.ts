import { expect, test, type Page } from "@playwright/test";
import { contractsGateway, version, withdrawal } from "./contracts";
import { MockGateway } from "./gateway";
import { METERS } from "./usage";
import { ACCOUNT, APP, billingGateway, INVOICE_2, invoice, invoiceLines, LIVE, productGateway, SANDBOX, signInAsAda } from "./product";

// A viewer reads; the server refuses a viewer's write. So a page shared with the operator's console (useCanWrite, shared/lib/access.ts) offers a viewer no button
// that would be refused: no "new" button, no ticks for a bulk action, no row action that writes. One data-driven test per page and per role: the owner is offered
// the controls (so the locators are right) and the viewer is not (the pages are the ones the review of 2026-11-02 found offering them).

test.use({ viewport: { width: 1440, height: 900 } });

type Role = "owner" | "viewer";

interface Case {
  name: string;
  /** The page, under the tenant of the person's live environment (or an absolute path of the person's console when it starts with /app). */
  path: string;
  /** The environment the page is under: live unless a control only the sandbox has is the point. */
  tenant?: string;
  /** What the mocked API holds: enough for the page to show its first row. */
  mock: (gw: MockGateway) => MockGateway;
  /** Something only a loaded page shows, read by a viewer too: after it the header has had time to draw its buttons. */
  ready: (page: Page) => ReturnType<Page["locator"]>;
  /** Opens what the controls are in (a row's sheet), once the page has loaded. */
  after?: (page: Page) => Promise<void>;
  /** The controls an owner has and a viewer does not. */
  offered: (page: Page) => ReturnType<Page["locator"]>[];
  /** Whether the list has ticks (the header's "select all" tick) for an owner and none for a viewer. */
  ticks?: boolean;
  /** What a row's "more actions" menu offers (names), by role; a viewer's may be shorter. */
  menu?: { row: (page: Page) => ReturnType<Page["locator"]>; owner: (string | RegExp)[]; viewer: (string | RegExp)[]; absent: (string | RegExp)[] };
}

const customer = { id: "id-1", external_id: "cus_a", name: "Ada", email: "ada@example.com", create_time: "2026-10-05T10:00:00Z" };
const endpoint = { id: "e1", url: "https://a.example/hook", description: "", event_types: ["invoice.paid"], active: true, create_time: "2026-10-20T10:00:00Z" };
const sub = {
  id: "s1",
  customer_external_id: "cus_a",
  plan_code: "basic",
  plan_version: 1,
  state: "SUBSCRIPTION_STATE_ACTIVE",
  currency: "TRY",
  owner: "payrudder",
  start_time: "2026-10-01T00:00:00Z",
};
const link = { id: "link0001", kind: "adhoc", status: "active", single_use: true, amount_minor: "1250", currency: "USD", description: "Link", create_time: "2026-10-20T10:00:00Z", expire_time: "2026-11-19T10:00:00Z", paid_count: 0, use_count: 0 };
const pack = { id: "p1", name: "Starter", credits: "1000", price_minor: "500", currency: "USD", active: true, create_time: "2026-10-21T09:00:00Z" };
const advanceSub = { ...sub, fee_timing: "FEE_TIMING_ADVANCE", scheduled_change: { plan_code: "pro", interval: "SUBSCRIPTION_INTERVAL_MONTH", effective_time: "2026-11-30T00:00:00Z" } };
const card = { token: "ct", alias: "work", bin: "55287900", last4: "0008", type: "CREDIT_CARD", association: "MASTER_CARD", family: "World", bank: "Halkbank", expire_month: "12", expire_year: "2030", in_use: true };
const get = (path: string, body: unknown) => (gw: MockGateway) => gw.on((c) => (c.method === "GET" && c.path === path ? { body } : undefined));

const coupon = { id: "c1", code: "WELCOME", name: "Welcome", percent_bps: 1000, duration: "COUPON_DURATION_ONCE", applies_to: ["COUPON_TARGET_SUBSCRIPTION"], active: true, redeemed: 2, promotion_code_count: 2, create_time: "2026-10-01T09:00:00Z" };

const partner = { id: "11111111-0000-4000-8000-000000000001", code: "ANNA", email: "anna@partners.example", name: "ANNA Partner", status: "active", payout_method: "bank_transfer", payout_details: "TR00", source: "owner", signed_in: false, clicks: "1", referrals: "1", create_time: "2026-10-01T09:00:00Z" };
const payout = { id: "33333333-0000-4000-8000-000000000001", affiliate_id: partner.id, affiliate_code: "ANNA", currency: "TRY", total_minor: "2500", commission_count: 3, method: "bank_transfer", payout_details: "TR00", status: "open", fake: false, create_time: "2026-10-20T09:00:00Z" };
const commission = { id: "55555555-0000-4000-8000-000000000001", affiliate_id: partner.id, affiliate_code: "ANNA", customer_external_id: "cus_100", kind: "commission", invoice_id: "22222222-0000-4000-8000-000000000001", invoice_number: "INV-0001", currency: "TRY", invoice_total_minor: "12000", net_minor: "10000", amount_minor: "1000", reversed_minor: "0", status: "approved", earn_time: "2026-10-10T09:00:00Z" };

const CASES: Case[] = [
  {
    name: "coupons",
    path: "/coupons",
    mock: get("/v1/coupons", { coupons: [coupon] }),
    ready: (page) => page.getByTestId("coupon-WELCOME"),
    offered: (page) => [page.getByTestId("coupons-new")],
    ticks: true,
  },
  {
    name: "customers",
    path: "/customers",
    mock: get("/v1/customers", { customers: [customer] }),
    ready: (page) => page.getByTestId("customer-cus_a"),
    offered: (page) => [page.getByTestId("customers-new")],
    ticks: true,
    menu: {
      row: (page) => page.getByTestId("customer-cus_a"),
      owner: ["View", "Block", "Edit name and e-mail"],
      viewer: ["View", "Quotas"],
      absent: ["Block", "Edit name and e-mail", "Edit address", "Payment link", "New subscription"],
    },
  },
  {
    name: "webhooks",
    path: "/webhooks",
    mock: get("/v1/webhook-endpoints", { endpoints: [endpoint] }),
    ready: (page) => page.getByText("https://a.example/hook").first(),
    offered: (page) => [page.getByTestId("webhooks-new")],
    ticks: true,
    menu: { row: (page) => page.getByRole("row", { name: /a\.example/ }), owner: ["Deliveries", "Delete", "Rotate secret"], viewer: ["Deliveries"], absent: ["Delete", "Rotate secret", "Pause", "Send test"] },
  },
  {
    name: "subscriptions",
    path: "/subscriptions",
    mock: get("/v1/subscriptions", { subscriptions: [sub] }),
    ready: (page) => page.getByText("cus_a").first(),
    offered: (page) => [page.getByTestId("subscriptions-new")],
    ticks: true,
  },
  {
    name: "payment links",
    path: "/payment-links",
    mock: get("/v1/payment-links", { links: [link] }),
    ready: (page) => page.getByText("Link", { exact: true }).first(),
    offered: (page) => [page.getByRole("button", { name: "Create link" })],
    ticks: true,
    menu: { row: (page) => page.getByRole("row", { name: /Link/ }).nth(1), owner: ["View", "Revoke"], viewer: ["View"], absent: ["Revoke"] },
  },
  {
    name: "credit packs",
    path: "/credits",
    mock: (gw) => get("/v1/credit-packs", { packs: [pack] })(get("/v1/customers", { customers: [] })(gw)),
    ready: (page) => page.getByRole("row", { name: /Starter/ }),
    offered: (page) => [page.getByRole("button", { name: "New credit pack" })],
    menu: { row: (page) => page.getByRole("row", { name: /Starter/ }), owner: ["Withdraw", "Create purchase link"], viewer: [], absent: ["Withdraw", "Create purchase link"] },
  },
  {
    name: "accounting",
    path: "/accounting",
    mock: (gw) => gw.on((c) => (c.method === "GET" && c.path === "/v1/expenses" ? { body: { expenses: [{ id: "x1", vendor: "Hetzner", category: "infrastructure", amount_minor: "4000", currency: "USD", incurred_on: "2026-10-05T00:00:00Z" }] } } : undefined)),
    ready: (page) => page.getByRole("row", { name: /Hetzner/ }),
    offered: (page) => [page.getByTestId("add-expense"), page.getByTestId("scan-receipts"), page.getByTestId("inbox-review"), page.getByTestId("inbox-rules")],
    menu: { row: (page) => page.getByRole("row", { name: /Hetzner/ }), owner: ["Edit", "Void"], viewer: [], absent: ["Edit", "Void"] },
  },
  {
    name: "meters",
    path: "/usage",
    mock: get("/v1/meters", { meters: METERS }),
    ready: (page) => page.getByTestId("usage-tab-meters"),
    after: async (page) => {
      await page.getByTestId("usage-tab-meters").click();
      await expect(page.getByRole("row", { name: /api_calls/ })).toBeVisible();
    },
    offered: (page) => [page.getByTestId("meters-new")],
    menu: { row: (page) => page.getByRole("row", { name: /api_calls/ }), owner: ["View", "New version"], viewer: ["View"], absent: ["New version"] },
  },
  {
    name: "affiliates: partners",
    path: "/affiliates",
    mock: get("/v1/affiliates", { affiliates: [partner], total: 1 }),
    ready: (page) => page.getByRole("tab", { name: "Affiliates", exact: true }),
    after: async (page) => {
      await page.getByRole("tab", { name: "Affiliates", exact: true }).click();
      await expect(page.getByRole("row", { name: /ANNA/ })).toBeVisible();
    },
    offered: () => [],
    menu: {
      row: (page) => page.getByRole("row", { name: /ANNA/ }),
      owner: ["View", "Suspend", "Edit", "Link and code", "Referrals"],
      viewer: ["View", "Link and code", "Referrals"],
      absent: ["Suspend", "Edit"],
    },
  },
  {
    name: "affiliates: payouts",
    path: "/affiliates",
    mock: get("/v1/affiliates/payouts", { payouts: [payout], total: 1 }),
    ready: (page) => page.getByRole("tab", { name: "Payouts", exact: true }),
    after: async (page) => {
      await page.getByRole("tab", { name: "Payouts", exact: true }).click();
      await expect(page.getByRole("row", { name: /ANNA/ })).toBeVisible();
    },
    offered: () => [],
    menu: { row: (page) => page.getByRole("row", { name: /ANNA/ }), owner: ["Commissions", "Mark paid", "Cancel"], viewer: ["Commissions"], absent: ["Mark paid", "Cancel"] },
  },
  {
    name: "affiliates: commissions",
    path: "/affiliates",
    mock: get("/v1/affiliates/commissions", { commissions: [commission], total: 1 }),
    ready: (page) => page.getByRole("tab", { name: "Commissions", exact: true }),
    after: async (page) => {
      await page.getByRole("tab", { name: "Commissions", exact: true }).click();
      await expect(page.getByRole("row", { name: /ANNA/ })).toBeVisible();
    },
    offered: (page) => [page.getByTestId("row-action-reverse")],
  },
  {
    name: "contracts: agreements",
    path: "/contracts",
    mock: (gw) => {
      contractsGateway(gw, { versions: [version("tos", "consumer", 1, { en: "Terms" })] });
      return gw;
    },
    ready: (page) => page.getByTestId("agreement-tos~consumer"),
    offered: (page) => [page.getByTestId("agreement-publish-tos~consumer")],
  },
  {
    name: "contracts: withdrawals",
    path: "/contracts?tab=withdrawals",
    mock: (gw) => {
      contractsGateway(gw, { withdrawals: [withdrawal("w1", "cus_a", "received", "I changed my mind"), withdrawal("w2", "cus_b", "accepted", "Refund please", { invoice_id: "inv_1" })] });
      return gw;
    },
    ready: (page) => page.getByTestId("withdrawal-w1"),
    offered: (page) => [page.getByTestId("withdrawal-accepted-w1"), page.getByTestId("withdrawal-rejected-w1"), page.getByTestId("withdrawal-refund-w2")],
  },
  {
    name: "a customer's sheet: the agreements and withdrawals",
    path: "/customers",
    mock: (gw) => {
      contractsGateway(gw, { withdrawals: [withdrawal("w1", "cus_a", "received", "I changed my mind")] });
      return get("/v1/customers", { customers: [customer] })(gw);
    },
    ready: (page) => page.getByTestId("customer-cus_a"),
    after: async (page) => {
      await page.getByTestId("customer-cus_a").click();
      await page.getByTestId("customer-tab-agreements").click();
      await expect(page.getByTestId("customer-withdrawal")).toContainText("I changed my mind"); // a viewer reads it
    },
    offered: (page) => [page.getByTestId("withdrawal-record"), page.getByTestId("customer-withdrawals-link")],
  },
  // --- what lives inside a sheet or a card of a page (the review of 2026-11-02 found these still offering writes)
  {
    name: "a subscription's sheet",
    path: "/subscriptions",
    mock: (gw) => get("/v1/subscriptions", { subscriptions: [advanceSub] })(get("/v1/plans", { plans: [] })(gw)),
    ready: (page) => page.getByText("cus_a").first(),
    after: async (page) => {
      await page.getByRole("row", { name: /cus_a/ }).click();
      await expect(page.getByRole("region", { name: /Manage cus_a/ })).toBeVisible();
    },
    offered: (page) => [
      page.getByRole("button", { name: "Pause", exact: true }),
      page.getByTestId("sub-end-now"),
      page.getByRole("button", { name: "Cancel at period end" }),
      page.getByRole("button", { name: "Apply coupon" }),
      page.getByRole("button", { name: "Preview", exact: true }),
      page.getByRole("button", { name: "Withdraw", exact: true }),
    ],
  },
  {
    name: "credits: a customer's balance and the low-balance alert",
    path: "/credits?customer=cus_a",
    mock: (gw) =>
      [
        get("/v1/credit-packs", { packs: [] }),
        get("/v1/customers", { customers: [customer] }),
        get("/v1/customers/cus_a/credits", { balance: "300", next_expire_time: null, lots: [] }),
        get("/v1/customers/cus_a/credits/ledger", { entries: [], next_page_token: "" }),
        get("/v1/credits/settings", { low_threshold: "50" }),
      ].reduce((g, m) => m(g), gw),
    ready: (page) => page.getByTestId("credits-balance-value"),
    offered: (page) => [page.getByTestId("credits-grant"), page.getByTestId("credits-alert").getByRole("button", { name: "Save" })],
  },
  {
    name: "connect your app (the sandbox)",
    path: "/connect",
    tenant: SANDBOX,
    mock: (gw) => [get("/v1/meters", { meters: [] }), get("/v1/customers", { customers: [] }), get("/v1/webhook-endpoints", { endpoints: [endpoint] }), get("/v1/rejected-events", { events: [], next_page_token: "" }), get("/v1/rejected-events/summary", { total: 0, by_code: [], hourly: [] })].reduce((g, m) => m(g), gw),
    ready: (page) => page.getByTestId("connect-step-key"),
    offered: (page) => [page.getByTestId("connect-key-create"), page.getByTestId("connect-sample-create"), page.getByTestId("connect-test-event-send"), page.getByTestId("connect-webhook-send")],
  },
  {
    name: "a customer's sheet: the card on file",
    path: "/customers",
    mock: (gw) => get("/v1/customers/cus_a/cards", { provider: "iyzico", cards: [card] })(get("/v1/customers", { customers: [{ ...customer, payment_method: "ck:ct" }] })(gw)),
    ready: (page) => page.getByTestId("customer-cus_a"),
    after: async (page) => {
      await page.getByTestId("customer-cus_a").click();
      await page.getByTestId("customer-tab-details").click();
      await expect(page.getByTestId("customer-card")).toBeVisible();
    },
    offered: (page) => [page.getByTestId("card-delete")],
  },
  {
    name: "a blocked customer's sheet: the appeal",
    path: "/customers",
    mock: (gw) =>
      get("/v1/customers/cus_a/appeals", { appeals: [{ id: "ap1", status: "open", statement: "It was a mistake.", answer: "", decided_by: "", create_time: "2026-11-01T10:00:00Z", decide_time: null }] })(
        get("/v1/customers", { customers: [{ ...customer, block_time: "2026-10-28T10:00:00Z" }] })(gw),
      ),
    ready: (page) => page.getByTestId("customer-cus_a"),
    after: async (page) => {
      await page.getByTestId("customer-cus_a").click();
      await expect(page.getByTestId("customer-appeal-statement")).toContainText("It was a mistake."); // a viewer reads it
    },
    offered: (page) => [page.getByTestId("customer-appeal-accept"), page.getByTestId("customer-appeal-reject"), page.getByTestId("customer-appeal-answer-input")],
  },
  {
    name: "an open invoice's sheet in the sandbox",
    path: "/invoices",
    tenant: SANDBOX,
    mock: (gw) => {
      billingGateway(gw).on((c) => (c.method === "GET" && c.path === `/v1/invoices/${INVOICE_2}` ? { body: invoice(INVOICE_2, "INV-2026-000002", "open", "29900", "cus_globex", { lines: invoiceLines, payments: [] }) } : undefined));
      return gw;
    },
    ready: (page) => page.getByTestId("invoice-INV-2026-000002"),
    after: async (page) => {
      await page.getByTestId("invoice-INV-2026-000002").click();
      await expect(page.getByTestId("detail-sheet")).toContainText("INV-2026-000002");
    },
    offered: (page) => [page.getByTestId("simulate-run")],
  },
];

async function open(page: Page, c: Case, role: Role) {
  const { gw } = productGateway(new MockGateway(), { role, memberships: [{ account_id: ACCOUNT, account_name: "Ada Labs", role }] });
  c.mock(gw);
  await gw.install(page);
  await signInAsAda(page);
  await page.goto(`/app/t/${c.tenant ?? LIVE}${c.path}`);
  await expect(c.ready(page)).toBeVisible();
  await c.after?.(page);
}

for (const c of CASES) {
  test(`${c.name}: an owner is offered the write controls`, async ({ page }) => {
    await open(page, c, "owner");
    for (const control of c.offered(page)) await expect(control).toBeVisible();
    if (c.ticks) await expect(page.getByRole("checkbox", { name: "Select all rows on this page" })).toBeVisible();
    if (c.menu) {
      await c.menu.row(page).getByTestId("row-actions").click();
      for (const name of c.menu.owner) await expect(page.getByRole("menuitem", { name, exact: true })).toBeVisible();
    }
  });

  test(`${c.name}: a viewer is offered none of them`, async ({ page }) => {
    await open(page, c, "viewer");
    for (const control of c.offered(page)) await expect(control).toHaveCount(0);
    if (c.ticks) await expect(page.getByRole("checkbox", { name: "Select all rows on this page" })).toHaveCount(0);
    if (c.menu) {
      const row = c.menu.row(page);
      // One action stands as a button, several fold into the menu, none draws nothing.
      if (c.menu.viewer.length > 1) {
        await row.getByTestId("row-actions").click();
        for (const name of c.menu.viewer) await expect(page.getByRole("menuitem", { name, exact: true })).toBeVisible();
      }
      else await expect(row.getByTestId("row-actions")).toHaveCount(0);
      for (const name of c.menu.absent) await expect(page.getByRole(c.menu.viewer.length > 1 ? "menuitem" : "button", { name, exact: true })).toHaveCount(0);
    }
  });
}

// --- pages whose controls are not a header button

test("contracts: a viewer reads the settings and cannot change them", async ({ page }) => {
  const { gw } = productGateway(new MockGateway(), { role: "viewer", memberships: [{ account_id: ACCOUNT, account_name: "Ada Labs", role: "viewer" }] });
  contractsGateway(gw);
  await gw.install(page);
  await signInAsAda(page);
  await page.goto(`/app/t/${LIVE}/contracts?tab=settings`);
  await expect(page.getByTestId("contracts-retention")).toHaveValue("36");
  await expect(page.getByTestId("contracts-retention")).toBeDisabled();
  await expect(page.getByTestId("contracts-immediate")).toBeDisabled();
  await expect(page.getByTestId("contracts-refund")).toBeDisabled();
  await expect(page.getByTestId("contracts-function")).toBeDisabled();
  await expect(page.getByTestId("contracts-save")).toBeDisabled();
});

test("contracts: an owner can change the settings", async ({ page }) => {
  const { gw } = productGateway(new MockGateway(), { role: "owner" });
  contractsGateway(gw);
  await gw.install(page);
  await signInAsAda(page);
  await page.goto(`/app/t/${LIVE}/contracts?tab=settings`);
  await expect(page.getByTestId("contracts-retention")).toBeEnabled();
  await expect(page.getByTestId("contracts-function")).toBeEnabled();
});

test("mail: the switches of a viewer show the state and cannot be changed", async ({ page }) => {
  const { gw } = productGateway(new MockGateway(), { role: "viewer", memberships: [{ account_id: ACCOUNT, account_name: "Ada Labs", role: "viewer" }] });
  gw.on((c) => {
    if (c.method === "GET" && c.path === "/v1/mail-settings") return { body: { categories: [{ category: "payment_receipts", enabled: false }] } };
    if (c.method === "GET" && c.path === "/v1/mail-messages") return { body: { messages: [], next_page_token: "", connected: true } };
    if (c.method === "GET" && c.path === "/v1/mail-suppressions") return { body: { suppressions: [], next_page_token: "", connected: true } };
    return undefined;
  });
  await gw.install(page);
  await signInAsAda(page);
  await page.goto(`/app/t/${LIVE}/mail`);
  await expect(page.getByTestId("mail-payment_receipts")).not.toBeChecked();
  await expect(page.getByTestId("mail-payment_receipts")).toBeDisabled();
  await expect(page.getByTestId("mail-system")).toBeDisabled();
});

test("API keys: a viewer sees the keys and is offered no new key, no ticks and no row action but looking", async ({ page }) => {
  const { gw } = productGateway(new MockGateway(), { role: "viewer", memberships: [{ account_id: ACCOUNT, account_name: "Ada Labs", role: "viewer" }] });
  await gw.install(page);
  await signInAsAda(page);
  await page.goto(`/app/apps/${APP}/keys?env=live`);
  await expect(page.getByRole("row").nth(1)).toBeVisible();
  await expect(page.getByTestId("new-live-key")).toHaveCount(0);
  await expect(page.getByRole("checkbox", { name: "Select all rows on this page" })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Revoke", exact: true })).toHaveCount(0);
});

test("credits: a viewer sees the low-balance threshold and cannot type in it", async ({ page }) => {
  const { gw } = productGateway(new MockGateway(), { role: "viewer", memberships: [{ account_id: ACCOUNT, account_name: "Ada Labs", role: "viewer" }] });
  [get("/v1/credit-packs", { packs: [] }), get("/v1/customers", { customers: [] }), get("/v1/credits/settings", { low_threshold: "50" })].reduce((g, m) => m(g), gw);
  await gw.install(page);
  await signInAsAda(page);
  await page.goto(`/app/t/${LIVE}/credits`);
  const threshold = page.getByTestId("credits-alert").getByLabel("Alert below (credits)");
  await expect(threshold).toHaveValue("50");
  await expect(threshold).toBeDisabled();
});

test("a subscription's sheet: a viewer is still told that a change is waiting, and still sees the trail and the usage against the limits", async ({ page }) => {
  const { gw } = productGateway(new MockGateway(), { role: "viewer", memberships: [{ account_id: ACCOUNT, account_name: "Ada Labs", role: "viewer" }] });
  [get("/v1/subscriptions", { subscriptions: [advanceSub] }), get("/v1/plans", { plans: [] })].reduce((g, m) => m(g), gw);
  await gw.install(page);
  await signInAsAda(page);
  await page.goto(`/app/t/${LIVE}/subscriptions`);
  await page.getByRole("row", { name: /cus_a/ }).click();
  const region = page.getByRole("region", { name: /Manage cus_a/ });
  await expect(region.getByTestId("sub-seats")).toBeVisible();
  await expect(region.getByRole("region", { name: "Change plan, interval or seats" })).toContainText("Scheduled: pro");
  await expect(region.getByRole("button", { name: /Pause|Resume|End now|period end|Withdraw|Preview|Apply|Remove coupon/ })).toHaveCount(0);
});

async function openPurchaseLink(page: Page, role: Role) {
  const { gw } = productGateway(new MockGateway(), { role, memberships: [{ account_id: ACCOUNT, account_name: "Ada Labs", role }] });
  [get("/v1/credit-packs", { packs: [pack] }), get("/v1/customers", { customers: [] }), get("/v1/credits/settings", { low_threshold: "0" })].reduce((g, m) => m(g), gw);
  await gw.install(page);
  await signInAsAda(page);
  await page.goto(`/app/t/${LIVE}/credits?sheet=purchase-link:p1`);
  await expect(page.getByRole("row", { name: /Starter/ })).toBeVisible();
}

test("credits: a purchase-link sheet named in the address opens for an owner", async ({ page }) => {
  await openPurchaseLink(page, "owner");
  await expect(page.getByRole("dialog", { name: /Purchase link: Starter/ })).toBeVisible();
});

test("credits: a purchase-link sheet named in the address does not open for a viewer", async ({ page }) => {
  await openPurchaseLink(page, "viewer");
  // Give the sheet the time it would need, then look.
  await page.waitForTimeout(800);
  await expect(page.getByRole("dialog")).toHaveCount(0);
});
