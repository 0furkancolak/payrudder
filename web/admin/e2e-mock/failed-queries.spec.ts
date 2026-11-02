import { expect, test, type Locator, type Page } from "@playwright/test";
import { baseTenant, MockGateway, openTenantPage } from "./gateway";
import { billingGateway, INVOICE_1, LIVE, productGateway, signInAsAda } from "./product";

// A query that failed must not look like an empty list (docs/13-admin-ui.md, "Failed pages"): the group says it did not load, with the reason and a way to try
// again, and shows its rows once the retry works. One data-driven test per group: every read of `fails` is answered 503 (the console asks three times before it
// gives up) until the test lets it through.

test.use({ viewport: { width: 1440, height: 900 } });

const down = { status: 503, body: { error: { code: "UNAVAILABLE", key: "gateway.unavailable", message: "try later", request_id: "req-1234" } } };

interface Case {
  name: string;
  /** The path whose first GET fails. */
  fails: string;
  /** The mocked gateway for the rest of the page, and how to get to the group. */
  build: () => MockGateway;
  go: (page: Page) => Promise<void>;
  /** The group that must say it did not load, and what it shows when it did. */
  scope: (page: Page) => Locator;
  rows: (scope: Locator) => Locator;
  /** What the group says when it is empty: it must not say it while the read has failed. */
  empty: (scope: Locator) => Locator;
}

const customer = { id: "c1", external_id: "cus_1", name: "Ada Lovelace", email: "ada@example.com", billing_currency: "TRY", country: "TR", create_time: "2026-10-20T09:00:00Z" };
const invoice = { id: "inv1", number: "INV-1", customer_external_id: "cus_1", currency: "TRY", total_minor: "10000", status: "paid", kind: "REGULAR", create_time: "2026-10-20T09:00:00Z", finalize_time: "2026-10-20T09:05:00Z" };
const subscription = { id: "s1", customer_external_id: "cus_1", plan_code: "pro", plan_version: 1, state: "SUBSCRIPTION_STATE_ACTIVE", currency: "TRY", start_time: "2026-10-20T09:00:00Z" };
const payment = { id: "9", invoice_id: "inv1", provider: "iyzico", kind: "charge", status: "paid", amount_minor: "10000", currency: "TRY", occurred_at: "2026-10-21T09:41:00Z" };

/** The operator's customers page with one customer, its sheet open on a tab. */
const customersGateway = () =>
  baseTenant(new MockGateway()).on((c) => {
    if (c.method !== "GET") return undefined;
    if (c.path === "/v1/customers") return { body: { customers: [customer] } };
    if (c.path === "/v1/invoices") return { body: { invoices: [invoice] } };
    if (c.path === "/v1/subscriptions") return { body: { subscriptions: [subscription] } };
    if (c.path === "/v1/payments") return { body: { truncated: false, payments: [payment] } };
    if (c.path === "/v1/customers/cus_1/block-events") return { body: { events: [{ id: "e1", action: "block", category: "fraud", reason: "chargebacks", create_time: "2026-10-21T09:00:00Z", actor: "ops@example.com" }] } };
    return undefined;
  });
const customerSheet = (tab: string) => async (page: Page) => {
  await openTenantPage(page, "/customers");
  await page.getByTestId("customer-cus_1").click();
  await page.getByTestId(`customer-tab-${tab}`).click();
};

const CASES: Case[] = [
  {
    name: "a customer's invoices",
    fails: "/v1/invoices",
    build: customersGateway,
    go: customerSheet("invoices"),
    scope: (page) => page.getByRole("region", { name: "Invoices" }),
    rows: (scope) => scope.getByTestId("related-invoice"),
    empty: (scope) => scope.getByTestId("related-empty"),
  },
  {
    name: "a customer's subscriptions",
    fails: "/v1/subscriptions",
    build: customersGateway,
    go: customerSheet("subscriptions"),
    scope: (page) => page.getByRole("region", { name: "Subscriptions" }),
    rows: (scope) => scope.getByTestId("related-subscription"),
    empty: (scope) => scope.getByTestId("related-empty"),
  },
  {
    name: "a customer's payments",
    fails: "/v1/payments",
    build: customersGateway,
    go: customerSheet("payments"),
    scope: (page) => page.getByRole("region", { name: "Payments" }),
    rows: (scope) => scope.getByTestId("related-payment"),
    empty: (scope) => scope.getByTestId("related-empty"),
  },
  {
    name: "a customer's block history",
    fails: "/v1/customers/cus_1/block-events",
    build: customersGateway,
    go: customerSheet("details"),
    scope: (page) => page.getByTestId("customer-block-history"),
    rows: (scope) => scope.getByTestId("customer-block-event"),
    empty: (scope) => scope.getByText("No blocks or lifts yet.", { exact: false }),
  },
  {
    name: "the credit notes of an invoice (the person's invoice sheet)",
    fails: "/v1/credit-notes",
    build: () =>
      billingGateway(productGateway(new MockGateway()).gw).on((c) =>
        c.method === "GET" && c.path === "/v1/credit-notes" ? { body: { credit_notes: [{ id: "cn1", number: "CN-1", invoice_id: INVOICE_1, currency: "USD", total_minor: "500", create_time: "2026-10-22T09:00:00Z" }] } } : undefined,
      ),
    go: async (page) => {
      await signInAsAda(page);
      await page.goto(`/app/t/${LIVE}/invoices?sheet=invoice:${INVOICE_1}`);
    },
    scope: (page) => page.getByRole("region", { name: "Credit notes" }),
    rows: (scope) => scope.getByTestId("related-credit-note"),
    empty: (scope) => scope.getByTestId("related-empty"),
  },
  {
    name: "the addresses mail is not sent to",
    fails: "/v1/mail-suppressions",
    build: () =>
      baseTenant(new MockGateway()).on((c) => {
        if (c.method !== "GET") return undefined;
        if (c.path === "/v1/mail-settings") return { body: { categories: [] } };
        if (c.path === "/v1/mail-messages") return { body: { messages: [], next_page_token: "", connected: true } };
        if (c.path === "/v1/mail-suppressions") return { body: { suppressions: [{ address: "bob@example.com", reason: "bounce", create_time: "2026-11-01T09:00:00Z" }], next_page_token: "", connected: true } };
        return undefined;
      }),
    go: (page) => openTenantPage(page, "/mail"),
    scope: (page) => page.getByTestId("mail-suppressions"),
    rows: (scope) => scope.getByTestId("suppression-bob@example.com"),
    empty: (scope) => scope.getByTestId("mail-suppressions-empty"),
  },
  {
    name: "the webhook endpoints",
    fails: "/v1/webhook-endpoints",
    build: () => baseTenant(new MockGateway()).on((c) => (c.method === "GET" && c.path === "/v1/webhook-endpoints" ? { body: { endpoints: [{ id: "e1", url: "https://a.example/hook", description: "", event_types: [], active: true, create_time: "2026-10-20T10:00:00Z" }] } } : undefined)),
    go: (page) => openTenantPage(page, "/webhooks"),
    scope: (page) => page.locator("main"),
    rows: (scope) => scope.getByText("https://a.example/hook"),
    empty: (scope) => scope.getByText("No webhook endpoints", { exact: false }),
  },
  {
    name: "the coupons",
    fails: "/v1/coupons",
    build: () => baseTenant(new MockGateway()).on((c) => (c.method === "GET" && c.path === "/v1/coupons" ? { body: { coupons: [{ id: "c1", code: "WELCOME", percent_bps: 1000, duration: "COUPON_DURATION_ONCE", active: true, redeemed: 0, applies_to: ["COUPON_TARGET_SUBSCRIPTION"] }] } } : undefined)),
    go: (page) => openTenantPage(page, "/coupons"),
    scope: (page) => page.getByTestId("coupons-list"),
    rows: (scope) => scope.getByTestId("coupon-WELCOME"),
    empty: (scope) => scope.getByText("No coupons yet."),
  },
];

for (const c of CASES) {
  test(`${c.name}: a read that failed says so, with a way to try again, and does not read as empty`, async ({ page }) => {
    const gw = c.build();
    let failing = true;
    gw.on((call) => (failing && call.method === "GET" && call.path === c.fails ? down : undefined));
    await gw.install(page);
    await c.go(page);
    const scope = c.scope(page);
    const error = scope.getByTestId("error-state").first();
    await expect(error).toBeVisible({ timeout: 20_000 });
    await expect(error).toContainText("req-1234");
    await expect(c.empty(scope)).toHaveCount(0);
    await expect(c.rows(scope)).toHaveCount(0);

    failing = false;
    await error.getByTestId("error-retry").click();
    await expect(c.rows(scope).first()).toBeVisible();
    await expect(scope.getByTestId("error-state")).toHaveCount(0);
  });
}
