import { expect, test, type Page } from "@playwright/test";
import { baseTenant, MockGateway, openTenantPage, signInAsOperator, type Call } from "./gateway";
import { LIVE, productGateway, signInAsAda } from "./product";

// Bulk actions on five more lists (docs/13-admin-ui.md, "Lists"), one data-driven test per list: tick the page, act, the question carries the count of the rows the
// action really applies to (the ticked rows it does not fit are left out), one request per row, and a row the server refuses is named and stays ticked. A viewer
// is offered no ticks. Packages and meters have no archive call, and an invoice has no resend or download-many call: they get no bulk action (plan 02).

test.use({ viewport: { width: 1440, height: 900 } });

const refuse = { status: 409, body: { error: { code: "FAILED_PRECONDITION", key: "plans.request.invalid", message: "no" } } };

interface Case {
  /** The list. */
  name: string;
  /** Where it is, under the tenant of the operator's console (or `root` for an operator page of its own). */
  path: string;
  root?: boolean;
  /** The rows the mock holds and how it answers a per-row call. */
  mock: (gw: MockGateway) => MockGateway;
  /** The bulk button, the words the question must carry (the title names the count) and the per-row calls in order. */
  button: string;
  asks: RegExp[];
  calls: { method: string; path: string }[];
  /** What the note says after the run, and which rows stay ticked. */
  note: string;
  named: string;
  stay: string;
  /** The tick of the header, to find the list. */
  firstRow: string;
}

const sub = (id: string, customer: string, extra: Record<string, unknown> = {}) => ({
  id,
  customer_external_id: customer,
  plan_code: "basic",
  plan_version: 1,
  state: "SUBSCRIPTION_STATE_ACTIVE",
  currency: "TRY",
  owner: "payrudder",
  start_time: "2026-10-01T00:00:00Z",
  ...extra,
});
const link = (id: string, status: string) => ({ id, kind: "adhoc", status, single_use: true, amount_minor: "1250", currency: "USD", description: `Link ${id}`, create_time: "2026-10-20T10:00:00Z", expire_time: "2026-11-19T10:00:00Z", paid_count: 0, use_count: 0 });
const coupon = (code: string, active: boolean) => ({ id: code, code, name: code, percent_bps: 1000, duration: "COUPON_DURATION_ONCE", applies_to: ["COUPON_TARGET_SUBSCRIPTION"], active, redeemed: 0, promotion_code_count: 1, create_time: "2026-10-01T09:00:00Z" });
const partner = (id: string, code: string, status: string) => ({ id, code, email: `${code.toLowerCase()}@example.com`, name: code, status, method: "bank_transfer", clicks: "3", referrals: "1", create_time: "2026-10-20T09:00:00Z" });

const CASES: Case[] = [
  {
    name: "subscriptions: cancel at the period end",
    path: "/subscriptions",
    // A third subscription is run by a provider: the action does not apply to it, so it is neither asked about nor sent.
    mock: (gw) => {
      const subs = [sub("s1", "cus_a"), sub("s2", "cus_b"), sub("s3", "cus_c", { owner: "provider", provider: "paddle" })];
      return gw.on((c) => {
        if (c.method === "GET" && c.path === "/v1/subscriptions") return { body: { subscriptions: subs } };
        if (c.method === "POST" && c.path === "/v1/subscriptions/s2/cancel-at-period-end") return refuse;
        if (c.method === "POST" && c.path === "/v1/subscriptions/s1/cancel-at-period-end") return { body: { ...subs[0], cancel_at_period_end: true } };
        return undefined;
      });
    },
    button: "bulk-cancel",
    asks: [/Cancel 2 subscriptions at the period end\?/],
    calls: [
      { method: "POST", path: "/v1/subscriptions/s1/cancel-at-period-end" },
      { method: "POST", path: "/v1/subscriptions/s2/cancel-at-period-end" },
    ],
    note: "1 done, 1 not changed",
    named: "cus_b",
    stay: "2 selected",
    firstRow: "cus_a",
  },
  {
    name: "payment links: revoke",
    path: "/payment-links",
    mock: (gw) =>
      gw.on((c) => {
        if (c.method === "GET" && c.path === "/v1/payment-links") return { body: { links: [link("link0001", "active"), link("link0002", "active"), link("link0003", "revoked"), link("link0004", "paid")] } };
        if (c.method === "DELETE" && c.path === "/v1/payment-links/link0002") return refuse;
        if (c.method === "DELETE" && c.path.startsWith("/v1/payment-links/")) return { body: link(c.path.split("/").at(-1) ?? "", "revoked") };
        return undefined;
      }),
    button: "bulk-revoke",
    asks: [/Revoke 3 payment links\?/, /Unpaid invoices made from them are voided/],
    calls: [
      { method: "DELETE", path: "/v1/payment-links/link0001" },
      { method: "DELETE", path: "/v1/payment-links/link0002" },
      { method: "DELETE", path: "/v1/payment-links/link0004" },
    ],
    note: "2 done, 1 not changed",
    named: "link0002",
    stay: "2 selected",
    firstRow: "link0001",
  },
  {
    name: "coupons: deactivate",
    path: "/coupons",
    mock: (gw) =>
      gw.on((c) => {
        if (c.method === "GET" && c.path === "/v1/coupons") return { body: { coupons: [coupon("WELCOME", true), coupon("OLD", false), coupon("BLACK", true)] } };
        if (c.method === "DELETE" && c.path === "/v1/coupons/BLACK") return refuse;
        if (c.method === "DELETE" && c.path.startsWith("/v1/coupons/")) return { body: coupon(c.path.split("/").at(-1) ?? "", false) };
        return undefined;
      }),
    button: "bulk-deactivate",
    asks: [/Deactivate 2 coupons\?/, /cannot be switched on again/],
    calls: [
      { method: "DELETE", path: "/v1/coupons/WELCOME" },
      { method: "DELETE", path: "/v1/coupons/BLACK" },
    ],
    note: "1 done, 1 not changed",
    named: "BLACK",
    stay: "2 selected",
    firstRow: "WELCOME",
  },
  {
    name: "dead letters: redrive",
    path: "/dead-letters",
    root: true,
    mock: (gw) =>
      gw.on((c) => {
        if (c.method === "GET" && c.path === "/admin/v1/dead-letters") return { body: { queues: [{ queue: "alpha", messages: "2" }, { queue: "beta", messages: "0" }, { queue: "gamma", messages: "5" }], messages: [] } };
        if (c.method === "POST" && c.path === "/admin/v1/dead-letters/redrive") return c.body?.queue === "gamma" ? refuse : { body: { redriven: "2" } };
        return undefined;
      }),
    button: "bulk-redrive",
    asks: [/Redrive the selected queues\?/, /Queues: 2\. Messages that move back .*: 7/],
    calls: [
      { method: "POST", path: "/admin/v1/dead-letters/redrive" },
      { method: "POST", path: "/admin/v1/dead-letters/redrive" },
    ],
    note: "1 done, 1 not changed",
    named: "gamma.dlq",
    stay: "2 selected",
    firstRow: "alpha.dlq",
  },
  {
    name: "partners: suspend",
    path: "/affiliates?tab=partners",
    mock: (gw) => {
      const rows = [partner("a1", "ANNA", "active"), partner("a2", "ZED", "pending"), partner("a3", "BOB", "active"), partner("a4", "OLD", "suspended")];
      return gw.on((c) => {
        if (c.method === "GET" && c.path === "/v1/affiliates") return { body: { affiliates: rows, total: rows.length } };
        if (c.method === "GET" && c.path === "/v1/affiliates/balances") return { body: { balances: [] } };
        if (c.method === "GET" && c.path === "/v1/affiliates/program") return { status: 404, body: { error: { code: "NOT_FOUND", key: "affiliate.program.not_found", message: "none" } } };
        if (c.method === "PATCH" && c.path === "/v1/affiliates/a3") return refuse;
        if (c.method === "PATCH" && c.path.startsWith("/v1/affiliates/")) return { body: rows.find((r) => r.id === c.path.split("/").at(-1)) };
        return undefined;
      });
    },
    button: "bulk-suspend",
    asks: [/Suspend 2 partners\?/, /gets no new attributions or commissions/],
    calls: [
      { method: "PATCH", path: "/v1/affiliates/a1" },
      { method: "PATCH", path: "/v1/affiliates/a3" },
    ],
    note: "1 done, 1 not changed",
    named: "BOB",
    stay: "3 selected",
    firstRow: "ANNA",
  },
];

async function open(page: Page, gw: MockGateway, c: Case): Promise<void> {
  await gw.install(page);
  if (c.root) {
    await signInAsOperator(page);
    await page.goto(c.path);
  } else {
    await openTenantPage(page, c.path);
  }
  await expect(page.getByText(c.firstRow, { exact: false }).first()).toBeVisible();
}

for (const c of CASES) {
  test(`${c.name}: asks with the count of the rows it applies to, sends one request each, names the one refused and leaves it ticked`, async ({ page }) => {
    const gw = c.mock(baseTenant(new MockGateway()));
    await open(page, gw, c);
    await expect(page.getByTestId("bulk-bar")).toHaveCount(0);
    await page.getByRole("checkbox", { name: "Select all rows on this page" }).click();
    await expect(page.getByTestId("bulk-bar")).toBeVisible();
    await page.getByTestId(c.button).click();
    const ask = page.getByTestId("confirm-dialog");
    for (const words of c.asks) await expect(ask).toContainText(words);
    await page.getByTestId("confirm-accept").click();
    await expect(page.getByTestId("bulk-note")).toContainText(c.note);
    await expect(page.getByTestId("bulk-note")).toContainText(c.named);
    const sent = gw.calls.filter((x: Call) => c.calls.some((w) => w.method === x.method && x.path === w.path));
    expect(sent.map((x) => ({ method: x.method, path: x.path }))).toEqual(c.calls);
    // What was refused (and what the action did not fit) stays ticked, so the same action can be repeated.
    await expect(page.getByTestId("bulk-count")).toHaveText(c.stay);
  });

  test(`${c.name}: a question that is refused sends nothing`, async ({ page }) => {
    const gw = c.mock(baseTenant(new MockGateway()));
    await open(page, gw, c);
    await page.getByRole("checkbox", { name: "Select all rows on this page" }).click();
    await page.getByTestId(c.button).click();
    await expect(page.getByTestId("confirm-dialog")).toBeVisible();
    await page.getByTestId("confirm-cancel").click();
    await expect(page.getByTestId("confirm-dialog")).toHaveCount(0);
    for (const w of c.calls) expect(gw.called(w.method, w.path)).toHaveLength(0);
    await expect(page.getByTestId("bulk-note")).toHaveCount(0);
  });
}

// --- the actions that need no question: partners approved, subscriptions kept and resumed

test("partners are approved and reinstated at once (no question: both are undone by the opposite step), each only where it fits", async ({ page }) => {
  const c = CASES.find((x) => x.name.startsWith("partners"))!;
  const gw = c.mock(baseTenant(new MockGateway()));
  await open(page, gw, c);
  await page.getByRole("checkbox", { name: "Select all rows on this page" }).click();
  await page.getByTestId("bulk-approve").click();
  await expect(page.getByTestId("bulk-note")).toContainText("1 done, 0 not changed");
  expect(gw.called("PATCH", /\/v1\/affiliates\//).map((x) => [x.path, x.body])).toEqual([["/v1/affiliates/a2", { status: "active" }]]);
  await page.getByTestId("bulk-reinstate").click();
  await expect(page.getByTestId("bulk-note")).toContainText("1 done, 0 not changed");
  expect(gw.called("PATCH", /\/v1\/affiliates\//).map((x) => x.path)).toEqual(["/v1/affiliates/a2", "/v1/affiliates/a4"]);
});

test("subscriptions are kept (a scheduled cancellation withdrawn) and resumed at once, each only where it fits", async ({ page }) => {
  const subs = [sub("s1", "cus_a", { cancel_at_period_end: true, end_time: "2026-11-01T00:00:00Z" }), sub("s2", "cus_b", { state: "SUBSCRIPTION_STATE_PAUSED" }), sub("s3", "cus_c")];
  const gw = baseTenant(new MockGateway()).on((c) => {
    if (c.method === "GET" && c.path === "/v1/subscriptions") return { body: { subscriptions: subs } };
    if (c.path.startsWith("/v1/subscriptions/")) return { body: subs[0] };
    return undefined;
  });
  await gw.install(page);
  await openTenantPage(page, "/subscriptions");
  await page.getByRole("checkbox", { name: "Select all rows on this page" }).click();
  await page.getByTestId("bulk-keep").click();
  await expect(page.getByTestId("bulk-note")).toContainText("1 done, 0 not changed");
  await page.getByTestId("bulk-resume").click();
  await expect(page.getByTestId("bulk-note")).toContainText("1 done, 0 not changed");
  expect(gw.calls.filter((x) => x.path.startsWith("/v1/subscriptions/")).map((x) => `${x.method} ${x.path}`)).toEqual(["DELETE /v1/subscriptions/s1/cancel-at-period-end", "POST /v1/subscriptions/s2/resume"]);
});

// --- a viewer is offered no ticks (the server refuses a viewer anyway)

const PERSON_PATHS: [string, string, (gw: MockGateway) => MockGateway][] = CASES.filter((c) => !c.root).map((c) => [c.name, c.path, c.mock]);
for (const [name, path, mock] of PERSON_PATHS) {
  test(`${name}: a viewer sees the rows and no ticks`, async ({ page }) => {
    const { gw } = productGateway(new MockGateway(), { role: "viewer", memberships: [{ account_id: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", account_name: "Ada Labs", role: "viewer" }] });
    mock(gw);
    await gw.install(page);
    await signInAsAda(page);
    await page.goto(`/app/t/${LIVE}${path}`);
    await expect(page.getByRole("row").nth(1)).toBeVisible();
    await expect(page.getByRole("checkbox", { name: "Select all rows on this page" })).toHaveCount(0);
  });
}
