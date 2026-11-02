import { expect, test, type Page } from "@playwright/test";
import { MockGateway } from "./gateway";
import { ACCOUNT, APP, LIVE, productGateway, signInAsAda } from "./product";

// A viewer reads; the server refuses a viewer's write. So a page shared with the operator's console (useCanWrite, shared/lib/access.ts) offers a viewer no button
// that would be refused: no "new" button, no ticks for a bulk action, no row action that writes. One data-driven test per page and per role: the owner is offered
// the controls (so the locators are right) and the viewer is not (the pages are the ones the review of 2026-11-02 found offering them).

test.use({ viewport: { width: 1440, height: 900 } });

type Role = "owner" | "viewer";

interface Case {
  name: string;
  /** The page, under the tenant of the person's live environment (or an absolute path of the person's console when it starts with /app). */
  path: string;
  /** What the mocked API holds: enough for the page to show its first row. */
  mock: (gw: MockGateway) => MockGateway;
  /** Something only a loaded page shows, read by a viewer too: after it the header has had time to draw its buttons. */
  ready: (page: Page) => ReturnType<Page["locator"]>;
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
const get = (path: string, body: unknown) => (gw: MockGateway) => gw.on((c) => (c.method === "GET" && c.path === path ? { body } : undefined));

const CASES: Case[] = [
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
];

async function open(page: Page, c: Case, role: Role) {
  const { gw } = productGateway(new MockGateway(), { role, memberships: [{ account_id: ACCOUNT, account_name: "Ada Labs", role }] });
  c.mock(gw);
  await gw.install(page);
  await signInAsAda(page);
  await page.goto(`/app/t/${LIVE}${c.path}`);
  await expect(c.ready(page)).toBeVisible();
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
      for (const name of c.menu.absent) await expect(page.getByRole(c.menu.viewer.length > 1 ? "menuitem" : "button", { name, exact: true })).toHaveCount(0);
    }
  });
}

// --- pages whose controls are not a header button

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
