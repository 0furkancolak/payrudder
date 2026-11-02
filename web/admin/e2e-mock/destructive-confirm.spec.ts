import { expect, test, type Page } from "@playwright/test";
import { baseTenant, MockGateway, openTenantPage, signInAsOperator, type Call } from "./gateway";
import { ACCOUNT, invitation, productGateway, signInAsAda } from "./product";
import { confirmDialog, rowAction } from "./ui";

// An action that cannot be undone asks first (docs/13-admin-ui.md, "Lists": the bulk equivalents have asked since plan 02). One data-driven test per action:
// the click opens the console's own question and sends nothing, "no" leaves everything as it was, "yes" sends exactly the one request.

test.use({ viewport: { width: 1440, height: 900 } });

interface Case {
  name: string;
  /** Builds the mocked gateway, signs in and opens the page. */
  open: (page: Page) => Promise<MockGateway>;
  /** The click that would, before the fix, have sent the request at once. */
  trigger: (page: Page) => Promise<void>;
  /** What the question says (a part of its title or body). */
  asks: RegExp;
  /** The one request "yes" sends. */
  request: { method: string; path: RegExp };
  /** What the page shows when the answer was no (default: nothing to check). */
  untouched?: (page: Page) => Promise<void>;
}

const sent = (gw: MockGateway, r: Case["request"]) => gw.calls.filter((c: Call) => c.method === r.method && r.path.test(c.path));

// --- coupons and promotion codes (the operator's console; the person's page is the same component)
const WELCOME = { id: "c1", code: "WELCOME", name: "Welcome", percent_bps: 1000, duration: "COUPON_DURATION_ONCE", applies_to: ["COUPON_TARGET_SUBSCRIPTION"], active: true, redeemed: 2, promotion_code_count: 1, create_time: "2026-10-01T09:00:00Z" };
const SUMMER = { id: "p2", coupon_code: "WELCOME", code: "SUMMER", active: true, redeemed: 1 };
async function couponsPage(page: Page, tab?: string): Promise<MockGateway> {
  const gw = baseTenant(new MockGateway()).on((c) => {
    if (c.path === "/v1/coupons" && c.method === "GET") return { body: { coupons: [WELCOME] } };
    if (c.path === "/v1/promotion-codes" && c.method === "GET") return { body: { codes: [SUMMER], total: 1 } };
    if (c.path === "/v1/coupon-redemptions" && c.method === "GET") return { body: { redemptions: [], total: 0 } };
    if (c.method === "DELETE") return { body: {} };
    return undefined;
  });
  await gw.install(page);
  await openTenantPage(page, "/coupons");
  if (tab) await page.getByTestId(`coupons-tab-${tab}`).click();
  return gw;
}

// --- a partner payout
const PAYOUT = "33333333-0000-4000-8000-000000000001";
async function payoutsPage(page: Page): Promise<MockGateway> {
  const gw = baseTenant(new MockGateway()).on((c) => {
    if (c.path === "/v1/affiliates/payouts" && c.method === "GET") {
      return { body: { payouts: [{ id: PAYOUT, affiliate_id: "a1", affiliate_code: "ANNA", currency: "TRY", total_minor: "2500", commission_count: 3, method: "bank_transfer", payout_details: "", status: "open", fake: false, create_time: "2026-10-20T09:00:00Z" }], total: 1 } };
    }
    if (c.method === "POST" && c.path === `/v1/affiliates/payouts/${PAYOUT}/cancel`) return { body: {} };
    if (c.path === "/v1/affiliates" && c.method === "GET") return { body: { affiliates: [], total: 0 } };
    if (c.path === "/v1/coupons") return { body: { coupons: [] } };
    return undefined;
  });
  await gw.install(page);
  await openTenantPage(page, "/affiliates");
  await page.getByRole("tab", { name: "Payouts", exact: true }).click();
  return gw;
}

// --- a customer's payment page and own quota limits
const CUSTOMER = { id: "id-1", external_id: "cus_1", create_time: "2026-10-05T10:00:00Z", name: "Ada", email: "ada@example.com" };
async function customersPage(page: Page): Promise<MockGateway> {
  const gw = baseTenant(new MockGateway()).on((c) => {
    if (c.path === "/v1/customers" && c.method === "GET") return { body: { customers: [CUSTOMER] } };
    if (c.path === "/v1/customers/cus_1/portal-links") return c.method === "POST" ? { status: 201, body: { url: "/c/qc_new" } } : { status: 204 };
    if (c.path === "/v1/customers/cus_1/entitlement-overrides" && c.method === "GET") return { body: { overrides: [{ customer_external_id: "cus_1", meter_code: "gb_hours", limit: "50.000", window: "QUOTA_WINDOW_MONTH", mode: "QUOTA_MODE_HARD", alert_thresholds_pct: [80] }] } };
    if (c.path === "/v1/customers/cus_1/entitlement-overrides/gb_hours" && c.method === "DELETE") return { status: 204 };
    if (c.path === "/v1/customers/cus_1/entitlements") return { body: { items: [] } };
    if (c.path === "/v1/meters" && c.method === "GET") return { body: { meters: [{ code: "gb_hours", aggregation: "AGGREGATION_SUM", value_property: "gb", unit_label: "GB", quantity_scale: 3, version: 1 }] } };
    return undefined;
  });
  await gw.install(page);
  await openTenantPage(page, "/customers");
  return gw;
}

// --- the people of an organisation
async function membersPage(page: Page): Promise<MockGateway> {
  const { gw } = productGateway(new MockGateway(), { invitations: [invitation("inv-1", "ned@example.com", "admin")] });
  await gw.install(page);
  await signInAsAda(page);
  await page.goto("/app/members");
  await expect(page.getByTestId("invite-ned@example.com")).toBeVisible();
  return gw;
}

// --- a ticket of the operator's inbox
const NOW = "2026-10-28T09:00:00Z";
const TICKET = {
  id: "c0000000-0000-4000-8000-000000000001", number: 1042, account_id: ACCOUNT, account_name: "Ada Labs", app_id: "", app_name: "", opened_by: "u1", opened_by_name: "Ada", subject: "Paddle webhooks are late", category: "integration",
  priority: "high", status: "open", assignee: "", create_time: NOW, update_time: NOW, last_message_time: NOW, last_author: "customer", first_response_due: "2026-10-28T13:00:00Z", first_response_time: null, sla_breached: false,
  solve_time: null, message_count: 1, unread: false, tags: [] as string[],
};
async function inboxPage(page: Page): Promise<MockGateway> {
  const gw = new MockGateway().on((c) => {
    if (c.path === "/admin/v1/support/tickets" && c.method === "GET") return { body: { tickets: [TICKET] } };
    if (c.path === `/admin/v1/support/tickets/${TICKET.id}` && c.method === "GET") return { body: { ticket: TICKET, messages: [{ id: "m1", author_kind: "customer", author_name: "Ada", body: "Since this morning.", internal: false, create_time: NOW }], events: [] } };
    if (c.path === `/admin/v1/support/tickets/${TICKET.id}` && c.method === "PATCH") return { body: { ticket: { ...TICKET, ...c.body } } };
    if (c.path === "/admin/v1/support/operators") return { body: { operators: [] } };
    return undefined;
  });
  await gw.install(page);
  await signInAsOperator(page);
  await page.goto("/support");
  await page.getByTestId("inbox-row-1042").click();
  return gw;
}

const CASES: Case[] = [
  {
    name: "a coupon is deactivated from its row",
    open: (page) => couponsPage(page),
    trigger: (page) => rowAction(page.getByTestId("coupon-WELCOME"), "Deactivate"),
    asks: /Deactivate coupon WELCOME\?/,
    request: { method: "DELETE", path: /^\/v1\/coupons\/WELCOME$/ },
  },
  {
    name: "a coupon is deactivated from its sheet",
    open: async (page) => {
      const gw = await couponsPage(page);
      await page.getByTestId("coupon-WELCOME").click();
      await expect(page.getByTestId("coupon-detail")).toBeVisible();
      return gw;
    },
    trigger: async (page) => void (await page.getByTestId("detail-sheet").getByRole("button", { name: "Deactivate" }).last().click()),
    asks: /Deactivate coupon WELCOME\?/,
    request: { method: "DELETE", path: /^\/v1\/coupons\/WELCOME$/ },
  },
  {
    name: "a promotion code is deactivated from its row",
    open: (page) => couponsPage(page, "codes"),
    trigger: async (page) => void (await page.getByTestId("code-SUMMER").getByRole("button", { name: "Deactivate" }).click()),
    asks: /Deactivate the code SUMMER\?/,
    request: { method: "DELETE", path: /^\/v1\/promotion-codes\/SUMMER$/ },
  },
  {
    name: "a promotion code is deactivated from its sheet",
    open: async (page) => {
      const gw = await couponsPage(page, "codes");
      await page.getByTestId("code-SUMMER").click();
      return gw;
    },
    trigger: async (page) => void (await page.getByTestId("detail-sheet").getByRole("button", { name: "Deactivate" }).last().click()),
    asks: /Deactivate the code SUMMER\?/,
    request: { method: "DELETE", path: /^\/v1\/promotion-codes\/SUMMER$/ },
  },
  {
    name: "a payout is cancelled",
    open: payoutsPage,
    trigger: (page) => rowAction(page.locator(`tr[data-payout="${PAYOUT}"]`), "Cancel"),
    asks: /Cancel this payout\?/,
    request: { method: "POST", path: new RegExp(`^/v1/affiliates/payouts/${PAYOUT}/cancel$`) },
    untouched: (page) => expect(page.locator(`tr[data-payout="${PAYOUT}"]`)).toContainText("Open"),
  },
  {
    name: "a customer's payment page is regenerated (the links already sent stop working)",
    open: async (page) => {
      const gw = await customersPage(page);
      await page.getByTestId("customer-cus_1").click();
      return gw;
    },
    trigger: async (page) => {
      await page.getByTestId("customer-payment-page").click();
      await page.getByTestId("payment-page-regenerate").click();
    },
    asks: /The links already sent stop working/,
    request: { method: "DELETE", path: /^\/v1\/customers\/cus_1\/portal-links$/ },
  },
  {
    name: "a customer's own quota limit is removed",
    open: async (page) => {
      const gw = await customersPage(page);
      await rowAction(page.getByTestId("customer-cus_1"), "Quotas");
      await expect(page.getByRole("dialog", { name: "Quotas of cus_1" })).toBeVisible();
      return gw;
    },
    trigger: async (page) => void (await page.getByRole("button", { name: "Remove the override on gb_hours" }).click()),
    asks: /Remove the override on gb_hours\?/,
    request: { method: "DELETE", path: /^\/v1\/customers\/cus_1\/entitlement-overrides\/gb_hours$/ },
    untouched: (page) => expect(page.getByRole("region", { name: "Own limits" }).getByRole("row", { name: /gb_hours/ })).toHaveCount(1),
  },
  {
    name: "an invitation is cancelled",
    open: membersPage,
    trigger: async (page) => {
      await page.getByTestId("invite-ned@example.com").getByTestId("row-actions").click();
      await page.getByTestId("row-action-cancel").click();
    },
    asks: /ned@example\.com will no longer be able to join/,
    request: { method: "DELETE", path: /^\/app\/v1\/account\/invitations\/inv-1$/ },
    untouched: (page) => expect(page.getByTestId("invite-ned@example.com")).toBeVisible(),
  },
  {
    name: "a member's role is lowered (an administrator becomes a viewer)",
    open: membersPage,
    trigger: async (page) => void (await page.getByLabel("Role of Grace Hopper").selectOption("viewer")),
    asks: /From Administrator to Viewer: Grace Hopper loses at once what Administrator may do/,
    request: { method: "PATCH", path: /^\/app\/v1\/account\/members\/44444444/ },
    // The select goes back to the role the member really has.
    untouched: (page) => expect(page.getByLabel("Role of Grace Hopper")).toHaveValue("admin"),
  },
  {
    name: "a ticket is closed",
    open: inboxPage,
    trigger: async (page) => void (await page.getByTestId("ticket-status").selectOption("closed")),
    asks: /Close ticket #1042\?/,
    request: { method: "PATCH", path: new RegExp(`^/admin/v1/support/tickets/${TICKET.id}$`) },
    untouched: (page) => expect(page.getByTestId("ticket-status")).toHaveValue("open"),
  },
];

for (const c of CASES) {
  test(`${c.name}: it asks first, "no" changes nothing and "yes" sends the request`, async ({ page }) => {
    const gw = await c.open(page);
    await c.trigger(page);
    const dialog = page.getByTestId("confirm-dialog");
    await expect(dialog).toBeVisible();
    await expect(dialog).toContainText(c.asks);
    expect(sent(gw, c.request)).toHaveLength(0);

    await confirmDialog(page, "no");
    await c.untouched?.(page);
    expect(sent(gw, c.request)).toHaveLength(0);

    await c.trigger(page);
    await confirmDialog(page, "yes");
    await expect.poll(() => sent(gw, c.request).length).toBe(1);
  });
}

test("a role that is raised does not ask", async ({ page }) => {
  const gw = await membersPage(page);
  await page.getByLabel("Role of Linus Torvalds").selectOption("admin");
  await expect.poll(() => gw.called("PATCH", /\/app\/v1\/account\/members\/55555555/).length).toBe(1);
  await expect(page.getByTestId("confirm-dialog")).toHaveCount(0);
});

test("several tickets closed together ask once, with the count", async ({ page }) => {
  const second = { ...TICKET, id: "c0000000-0000-4000-8000-000000000002", number: 1043, subject: "Second" };
  const gw = new MockGateway().on((c) => {
    if (c.path === "/admin/v1/support/tickets" && c.method === "GET") return { body: { tickets: [TICKET, second] } };
    if (c.path.startsWith("/admin/v1/support/tickets/") && c.method === "PATCH") return { body: { ticket: { ...TICKET, ...c.body } } };
    if (c.path === "/admin/v1/support/operators") return { body: { operators: [] } };
    return undefined;
  });
  await gw.install(page);
  await signInAsOperator(page);
  await page.goto("/support");
  await page.getByRole("checkbox", { name: "Select all rows on this page" }).click();
  await page.getByTestId("bulk-status").selectOption("closed");
  await page.getByTestId("bulk-apply").click();
  await expect(page.getByTestId("confirm-dialog")).toContainText("Close 2 tickets?");
  await confirmDialog(page, "no");
  expect(gw.called("PATCH", /\/admin\/v1\/support\/tickets\//)).toHaveLength(0);
  // The answer was no: the ticks stay, and the status is chosen again.
  await page.getByTestId("bulk-status").selectOption("closed");
  await page.getByTestId("bulk-apply").click();
  await confirmDialog(page, "yes");
  await expect.poll(() => gw.called("PATCH", /\/admin\/v1\/support\/tickets\//).length).toBe(2);
});
