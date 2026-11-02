import { expect, test, type Locator, type Page } from "@playwright/test";
import { baseTenant, MockGateway, openTenantPage, signInAsOperator } from "./gateway";
import { ACCOUNT, productGateway, signInAsAda } from "./product";

// A row that opens a sheet can be reached by keyboard: its last cell has a View button, which Enter and Space press (a click on the row alone is for the
// pointer). One data-driven test per list that used to have no other way in (app/rows.test.ts scans for the rest).

test.use({ viewport: { width: 1440, height: 900 } });

const NOW = "2026-10-28T09:00:00Z";
const ticket = {
  id: "c0000000-0000-4000-8000-000000000001", number: 1042, account_id: ACCOUNT, account_name: "Ada Labs", app_id: "", app_name: "", opened_by: "u1", opened_by_name: "Ada", subject: "Paddle webhooks are late",
  category: "integration", priority: "high", status: "open", assignee: "", create_time: NOW, update_time: NOW, last_message_time: NOW, last_author: "customer", first_response_due: "2026-10-28T13:00:00Z",
  first_response_time: null, sla_breached: false, solve_time: null, message_count: 1, unread: false, tags: [] as string[],
};
const thread = { ticket, messages: [{ id: "m1", author_kind: "customer", author_name: "Ada", body: "Since this morning.", internal: false, create_time: NOW }], events: [] };

interface Case {
  name: string;
  open: (page: Page) => Promise<void>;
  row: (page: Page) => Locator;
  /** What shows once the row's sheet is open. */
  sheet: (page: Page) => Locator;
}

const CASES: Case[] = [
  {
    name: "the person's tickets",
    open: async (page) => {
      const { gw } = productGateway(new MockGateway());
      gw.on((c) => {
        if (c.path === "/app/v1/support/tickets" && c.method === "GET") return { body: { tickets: [ticket] } };
        if (c.path === `/app/v1/support/tickets/${ticket.id}` && c.method === "GET") return { body: thread };
        return undefined;
      });
      await gw.install(page);
      await signInAsAda(page);
      await page.goto("/app/support");
    },
    row: (page) => page.getByTestId("support-row-1042"),
    sheet: (page) => page.getByRole("dialog", { name: /#1042/ }),
  },
  {
    name: "the operator's support inbox",
    open: async (page) => {
      const gw = new MockGateway().on((c) => {
        if (c.path === "/admin/v1/support/tickets" && c.method === "GET") return { body: { tickets: [ticket] } };
        if (c.path === `/admin/v1/support/tickets/${ticket.id}` && c.method === "GET") return { body: thread };
        return undefined;
      });
      await gw.install(page);
      await signInAsOperator(page);
      await page.goto("/support");
    },
    row: (page) => page.getByTestId("inbox-row-1042"),
    sheet: (page) => page.getByTestId("inbox-ticket"),
  },
  {
    name: "the redemptions of a coupon",
    open: async (page) => {
      const gw = baseTenant(new MockGateway()).on((c) => {
        if (c.path === "/v1/coupon-redemptions" && c.method === "GET") {
          return { body: { redemptions: [{ id: "r1", coupon_code: "WELCOME", promotion_code: "WELCOME", customer_external_id: "cus_1", target: "COUPON_TARGET_SUBSCRIPTION", discount_minor: "500", currency: "USD", create_time: "2026-10-20T10:00:00Z" }], total: 1 } };
        }
        if (c.path === "/v1/coupons") return { body: { coupons: [] } };
        return undefined;
      });
      await gw.install(page);
      await openTenantPage(page, "/coupons");
      await page.getByTestId("coupons-tab-redemptions").click();
    },
    row: (page) => page.getByTestId("redemption-r1"),
    sheet: (page) => page.getByTestId("redemption-detail"),
  },
];

for (const c of CASES) {
  for (const key of ["Enter", "Space"]) {
    test(`${c.name}: the row's View button opens its sheet with ${key}`, async ({ page }) => {
      await c.open(page);
      const view = c.row(page).getByRole("button", { name: /^View/ });
      await expect(view).toBeVisible();
      // It is in the tab order (not tabindex -1) and takes the focus.
      expect(await view.evaluate((el) => (el as HTMLButtonElement).tabIndex)).toBeGreaterThanOrEqual(0);
      await view.focus();
      await expect(view).toBeFocused();
      await page.keyboard.press(key);
      await expect(c.sheet(page)).toBeVisible();
    });
  }
}
