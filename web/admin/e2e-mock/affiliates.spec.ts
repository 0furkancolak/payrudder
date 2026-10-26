import { expect, test, type Page } from "@playwright/test";
import { baseTenant, MockGateway, openTenantPage, TENANT, type Call } from "./gateway";
import { pickFilter, rowAction } from "./ui";

// Growth > Affiliates against a mocked gateway: the program form, adding and approving a partner, reversing a commission, creating and paying payouts
// (a sandbox one is labelled fake), the balanced journal and the right-to-left page. The mock is a small stateful stand-in for /v1/affiliates/*.

const ANNA = "11111111-0000-4000-8000-000000000001";
const ZED = "11111111-0000-4000-8000-000000000002";
const INVOICE = "22222222-0000-4000-8000-000000000001";
const PAYOUT = "33333333-0000-4000-8000-000000000001";
const FAKE_PAYOUT = "33333333-0000-4000-8000-000000000002";

const affiliate = (id: string, code: string, extra: Record<string, unknown> = {}) => ({
  id,
  code,
  email: `${code.toLowerCase()}@partners.example`,
  name: `${code} Partner`,
  status: "active",
  payout_method: "bank_transfer",
  payout_details: "TR00 0000 0000",
  source: "owner",
  signed_in: false,
  clicks: "120",
  referrals: "9",
  create_time: "2026-10-01T09:00:00Z",
  ...extra,
});

const commission = (id: string, extra: Record<string, unknown> = {}) => ({
  id,
  affiliate_id: ANNA,
  affiliate_code: "ANNA",
  customer_external_id: "cus_100",
  kind: "commission",
  invoice_id: INVOICE,
  invoice_number: "INV-0001",
  currency: "TRY",
  invoice_total_minor: "12000",
  net_minor: "10000",
  amount_minor: "1000",
  reversed_minor: "0",
  status: "approved",
  earn_time: "2026-10-10T09:00:00Z",
  ...extra,
});

const payout = (id: string, extra: Record<string, unknown> = {}) => ({
  id,
  affiliate_id: ANNA,
  affiliate_code: "ANNA",
  currency: "TRY",
  total_minor: "2500",
  commission_count: 3,
  method: "bank_transfer",
  payout_details: "TR00 0000 0000",
  status: "open",
  fake: false,
  create_time: "2026-10-20T09:00:00Z",
  ...extra,
});

const err = (status: number, key: string, message: string) => ({ status, body: { error: { code: status === 404 ? "NOT_FOUND" : "INVALID_ARGUMENT", key, message } } });

interface State {
  program: Record<string, unknown> | null;
  affiliates: Record<string, unknown>[];
  commissions: Record<string, unknown>[];
  payouts: Record<string, unknown>[];
  journal: Record<string, unknown>[];
  createdPayouts: Record<string, unknown>[];
}

function fresh(): State {
  return {
    program: null,
    affiliates: [affiliate(ZED, "ZED", { status: "pending", source: "application", clicks: "4", referrals: "0" }), affiliate(ANNA, "ANNA", { link: "https://pay.example.com/r/ANNA" })],
    commissions: [
      commission("c1"),
      commission("c2", { kind: "reversal", amount_minor: "-300", net_minor: "0", status: "approved", parent_id: "c1", invoice_number: "INV-0001", earn_time: "2026-10-12T09:00:00Z" }),
      commission("c3", { invoice_id: "22222222-0000-4000-8000-000000000003", invoice_number: "INV-0003", status: "paid", payout_id: PAYOUT, amount_minor: "700" }),
    ],
    payouts: [payout(PAYOUT), payout(FAKE_PAYOUT, { fake: true, total_minor: "900" })],
    journal: [
      { id: "j1", payout_id: PAYOUT, affiliate_id: ANNA, post_time: "2026-10-21T09:00:00Z", currency: "TRY", account: "commission_expense", debit_minor: "2500", credit_minor: "0", memo: "Payout ANNA", fake: false },
      { id: "j2", payout_id: PAYOUT, affiliate_id: ANNA, post_time: "2026-10-21T09:00:00Z", currency: "TRY", account: "commissions_payable", debit_minor: "0", credit_minor: "2500", memo: "Payout ANNA", fake: false },
      { id: "j3", payout_id: FAKE_PAYOUT, affiliate_id: ANNA, post_time: "2026-10-21T09:05:00Z", currency: "USD", account: "commission_expense", debit_minor: "900", credit_minor: "0", memo: "Payout ANNA", fake: true },
      { id: "j4", payout_id: FAKE_PAYOUT, affiliate_id: ANNA, post_time: "2026-10-21T09:05:00Z", currency: "USD", account: "cash", debit_minor: "0", credit_minor: "900", memo: "Payout ANNA", fake: true },
    ],
    createdPayouts: [],
  };
}

/** The owner API, as far as the page uses it. `st` is what the "server" holds. */
function affiliateApi(st: State) {
  return (c: Call) => {
    if (c.path === "/v1/coupons") return { body: { coupons: [{ code: "WELCOME", name: "Welcome", active: true }, { code: "OLD", name: "Old", active: false }] } };
    if (!c.path.startsWith("/v1/affiliates")) return undefined;
    const path = c.path.replace("/v1/affiliates", "");
    if (path === "/program") {
      if (c.method === "PUT") {
        st.program = { ...(c.body ?? {}), create_time: "2026-10-21T10:00:00Z" };
        return { body: st.program };
      }
      return st.program ? { body: st.program } : err(404, "affiliate.program.not_set", "this environment has no partner program yet");
    }
    if (path === "" && c.method === "POST") {
      const a = affiliate("44444444-0000-4000-8000-000000000001", String(c.body?.code ?? "RANDOM1"), { email: c.body?.email, name: c.body?.name ?? "", status: c.body?.pending ? "pending" : "active", clicks: "0", referrals: "0" });
      st.affiliates.unshift(a);
      return { status: 201, body: a };
    }
    if (path === "") {
      const status = c.query.get("status");
      const search = (c.query.get("search") ?? "").toLowerCase();
      const rows = st.affiliates.filter((a) => (!status || a.status === status) && (!search || `${a.code}${a.email}${a.name}`.toLowerCase().includes(search)));
      return { body: { affiliates: rows, total: rows.length } };
    }
    if (path === "/balances") {
      return {
        body: {
          balances: [
            { affiliate_id: ANNA, currency: "TRY", pending_minor: "400", approved_minor: "2500", paid_minor: "7000", reversed_minor: "300" },
            { affiliate_id: ANNA, currency: "USD", pending_minor: "0", approved_minor: "900", paid_minor: "0", reversed_minor: "0" },
            { affiliate_id: ZED, currency: "TRY", pending_minor: "100", approved_minor: "0", paid_minor: "0", reversed_minor: "0" },
          ],
        },
      };
    }
    if (path === "/funnel") {
      return {
        body: {
          from_date: c.query.get("from"),
          to_date: c.query.get("to"),
          clicks: "200",
          referrals: "50",
          paying_customers: "10",
          commissions: [
            { currency: "TRY", earned_minor: "10000", reversed_minor: "1500" },
            { currency: "USD", earned_minor: "2500", reversed_minor: "0" },
          ],
        },
      };
    }
    if (path === "/referrals") return { body: { referrals: [{ affiliate_id: ANNA, affiliate_code: "ANNA", customer_external_id: "cus_100", customer_email_masked: "j***@example.com", source: "link", attribute_time: "2026-10-05T09:00:00Z" }], total: 1 } };
    if (path === "/commissions/reverse") {
      st.commissions.push(commission("c9", { kind: "reversal", amount_minor: `-${c.body?.amount_minor ?? "700"}`, status: "approved" }));
      return { body: { reversed_minor: String(c.body?.amount_minor ?? "700"), commission: commission("c1") } };
    }
    if (path === "/commissions") {
      let rows = st.commissions;
      for (const f of ["status", "kind", "affiliate_id", "payout_id"]) {
        const v = c.query.get(f);
        if (v) rows = rows.filter((r) => r[f] === v);
      }
      return { body: { commissions: rows, total: rows.length } };
    }
    if (path === "/payouts" && c.method === "POST") {
      const made = payout("33333333-0000-4000-8000-000000000009", { total_minor: "4200", commission_count: 2 });
      st.createdPayouts.push(made);
      st.payouts.unshift(made);
      return { status: 201, body: { payouts: [made], skipped: [{ affiliate_id: ZED, currency: "TRY", total_minor: "300", reason: "below_minimum" }, { affiliate_id: ANNA, currency: "EUR", total_minor: "-50", reason: "not_positive" }] } };
    }
    if (path === "/payouts") {
      const status = c.query.get("status");
      const rows = st.payouts.filter((p) => !status || p.status === status);
      return { body: { payouts: rows, total: rows.length } };
    }
    const pay = /^\/payouts\/([^/]+)\/(pay|cancel)$/.exec(path);
    if (pay && c.method === "POST") {
      const p = st.payouts.find((x) => x.id === pay[1]);
      if (!p) return err(404, "affiliate.payout.not_found", "no such payout");
      p.status = pay[2] === "pay" ? "paid" : "cancelled";
      if (pay[2] === "pay") p.reference = c.body?.reference ?? "";
      return { body: p };
    }
    if (path === "/journal") {
      return {
        body: {
          entries: st.journal,
          total: st.journal.length,
          totals: [
            { currency: "TRY", debit_minor: "2500", credit_minor: "2500" },
            { currency: "USD", debit_minor: "900", credit_minor: "900" },
          ],
        },
      };
    }
    if (path === "/journal.csv") return { body: {} };
    const link = /^\/([^/]+)\/promotion-code$/.exec(path);
    if (link && c.method === "PUT") {
      const a = st.affiliates.find((x) => x.id === link[1]);
      return a ? { body: a } : err(404, "affiliate.promotion_code_not_found", "no such promotion code");
    }
    const one = /^\/([^/]+)$/.exec(path);
    if (one && c.method === "PATCH") {
      const a = st.affiliates.find((x) => x.id === one[1]);
      if (!a) return err(404, "affiliate.not_found", "no such partner");
      Object.assign(a, c.body);
      return { body: a };
    }
    return undefined;
  };
}

async function install(page: Page, st: State = fresh()): Promise<{ gw: MockGateway; st: State }> {
  const gw = baseTenant(new MockGateway())
    .on((c) => (c.method === "GET" && c.path === "/admin/v1/tenants" ? { body: { tenants: [{ id: TENANT, name: "Acme", currency: "TRY", environment: "ENVIRONMENT_LIVE" }] } } : undefined))
    .on(affiliateApi(st));
  await gw.install(page);
  return { gw, st };
}

const tab = (page: Page, name: string) => page.getByRole("tab", { name, exact: true });

test.describe("affiliates", () => {
  test("the program form starts from sensible defaults, saves basis points, and shows what the API refuses", async ({ page }) => {
    const { gw, st } = await install(page);
    await openTenantPage(page, "/affiliates");
    // No program yet: the overview points at the setup instead of failing.
    await expect(page.getByTestId("no-program")).toBeVisible();
    await page.getByRole("button", { name: "Set up the program" }).click();

    await expect(page.getByLabel("Program status")).toHaveValue("draft");
    await expect(page.getByLabel("Percentage")).toHaveValue("10");
    await expect(page.getByLabel("How long a customer earns")).toHaveValue("lifetime");
    await expect(page.getByLabel("Attribution window (days)")).toHaveValue("30");
    await expect(page.getByLabel("Hold (days)")).toHaveValue("30");
    await expect(page.getByLabel("Daily click cap per partner")).toHaveValue("1000");
    await expect(page.getByText("A change applies to commissions earned from then on")).toBeVisible();
    // The tenant's currency (TRY) is the one money fields start in.
    await expect(page.getByLabel("Minimum payout (TRY)")).toBeVisible();

    // A bad value is refused in the page, before anything is sent.
    await page.getByLabel("Percentage").fill("100.5");
    await page.getByRole("button", { name: "Save program" }).click();
    await expect(page.getByRole("alert")).toContainText("Enter a commission above zero");
    expect(gw.called("PUT", "/v1/affiliates/program")).toHaveLength(0);

    await page.getByLabel("Program status").selectOption("active");
    await page.getByLabel("Percentage").fill("12,5");
    await page.getByLabel("How long a customer earns").selectOption("months");
    await page.getByLabel("Number of months").fill("12");
    await page.getByLabel("Attribution window (days)").fill("45");
    await page.getByLabel("Hold (days)").fill("14");
    await page.getByLabel("Minimum payout (TRY)").fill("25");
    await page.getByLabel("Coupon for referred customers").selectOption("WELCOME");
    await page.getByLabel("Daily click cap per partner").fill("500");
    await page.getByLabel("Landing page").fill("https://example.com/welcome");
    await page.getByLabel("Approve applications automatically").click();
    await page.getByRole("button", { name: "Save program" }).click();
    await expect(page.getByRole("status").filter({ hasText: "Program saved." })).toBeVisible();
    expect(gw.called("PUT", "/v1/affiliates/program")[0]?.body).toEqual({
      status: "active",
      commission_kind: "percent_bps",
      commission_value: "1250",
      commission_term: "months",
      commission_term_value: 12,
      attribution_window_days: 45,
      hold_days: 14,
      min_payout_minor: "2500",
      referred_coupon_code: "WELCOME",
      daily_click_cap: 500,
      landing_url: "https://example.com/welcome",
      auto_approve: true,
    });
    expect(st.program).not.toBeNull();

    // The saved program comes back into the form as the person typed it (basis points shown as a percentage).
    await page.reload();
    await tab(page, "Program").click();
    await expect(page.getByLabel("Percentage")).toHaveValue("12.5");
    await expect(page.getByLabel("Minimum payout (TRY)")).toHaveValue("25.00");

    // A fixed amount is typed in major units and sent in minor units of its currency.
    await page.getByLabel("Commission", { exact: true }).selectOption("fixed");
    await page.getByLabel("Amount per invoice").fill("5.50");
    await page.route("**/gw/v1/affiliates/program", async (route) => {
      if (route.request().method() !== "PUT") return route.fallback();
      return route.fulfill({ status: 400, contentType: "application/json", body: JSON.stringify({ error: { code: "INVALID_ARGUMENT", key: "affiliate.program.coupon_inactive", message: "the coupon is not active" } }) });
    });
    await page.getByRole("button", { name: "Save program" }).click();
    await expect(page.getByRole("alert")).toContainText("The coupon for referred customers is not active.");
  });

  test("overview: the funnel and the balances per currency, never added together", async ({ page }) => {
    const st = fresh();
    st.program = { status: "active", commission_kind: "percent_bps", commission_value: "1000", commission_term: "lifetime", attribution_window_days: 30, hold_days: 30, daily_click_cap: 1000 };
    const { gw } = await install(page, st);
    await page.clock.setFixedTime(new Date(2026, 9, 21, 12, 0, 0));
    await openTenantPage(page, "/affiliates");

    const funnel = page.getByTestId("funnel");
    await expect(funnel.locator('[data-step="clicks"]')).toContainText("200");
    await expect(funnel.locator('[data-step="referrals"]')).toContainText("50");
    await expect(funnel.locator('[data-step="referrals"]')).toContainText("25% of the step before");
    await expect(funnel.locator('[data-step="paying"]')).toContainText("10");
    await expect(funnel.locator('[data-step="commission"] [data-currency="TRY"]')).toContainText("TRY 100.00");
    await expect(funnel.locator('[data-step="commission"] [data-currency="TRY"]')).toContainText("TRY 15.00 taken back");
    await expect(funnel.locator('[data-step="commission"] [data-currency="USD"]')).toContainText("$25.00");

    const lira = page.getByRole("region", { name: "TRY" });
    await expect(lira.locator('[data-stat="pending"]')).toContainText("TRY 5.00"); // 400 + 100 of two partners
    await expect(lira.locator('[data-stat="available"]')).toContainText("TRY 25.00");
    await expect(lira.locator('[data-stat="paid"]')).toContainText("TRY 70.00");
    await expect(lira.locator('[data-stat="reversed"]')).toContainText("TRY 3.00");
    await expect(page.getByRole("region", { name: "USD" }).locator('[data-stat="available"]')).toContainText("$9.00");

    // The period picker asks for the chosen days (the first request is the last 30 days).
    const first = gw.called("GET", "/v1/affiliates/funnel")[0];
    expect(first?.query.get("from")).toBe("2026-09-22");
    expect(first?.query.get("to")).toBe("2026-10-21");
    await page.getByRole("button", { name: "Last month" }).click();
    await expect.poll(() => gw.called("GET", "/v1/affiliates/funnel").some((q) => q.query.get("from") === "2026-09-01" && q.query.get("to") === "2026-09-30")).toBe(true);
  });

  test("a partner is added, a pending application is approved, and a partner is suspended and linked", async ({ page }) => {
    const { gw } = await install(page);
    await openTenantPage(page, "/affiliates");
    await tab(page, "Affiliates").click();

    // The pending application is visible at once, with a banner that filters to it.
    await expect(page.getByTestId("pending-banner")).toContainText("Waiting for your approval: 1");
    const zed = page.getByRole("row", { name: /ZED/ });
    await expect(zed).toContainText("Pending");
    await expect(zed).toContainText("Applied through the form");
    await page.getByRole("button", { name: "Show them" }).click();
    await expect(page.getByRole("row", { name: /ANNA/ })).toHaveCount(0);
    await rowAction(zed, "Approve");
    expect(gw.called("PATCH", `/v1/affiliates/${ZED}`)[0]?.body).toEqual({ status: "active" });
    await expect(page.getByTestId("pending-banner")).toHaveCount(0);

    // Back to all: the balance of ANNA is per currency.
    await pickFilter(page, "Status", "All statuses");
    const anna = page.getByRole("row", { name: /ANNA/ });
    await expect(anna).toContainText("TRY 25.00 available");
    await expect(anna).toContainText("$9.00 available");
    await expect(anna).toContainText("Active");

    // Add: the body carries what was typed; "leave pending" makes a pending partner.
    await page.getByRole("button", { name: "Add affiliate" }).click();
    const dialog = page.getByRole("dialog");
    await dialog.getByLabel("E-mail").fill("bob@partners.example");
    await dialog.getByLabel("Name").fill("Bob Builder");
    await dialog.getByLabel("Code (optional)").fill("BOB-1");
    await dialog.getByLabel("Payout method").selectOption("manual");
    await dialog.getByLabel("Payout details").fill("Pay by cash");
    await dialog.getByLabel("Leave pending until I approve").click();
    await dialog.getByRole("button", { name: "Add affiliate" }).click();
    await expect(page.getByRole("row", { name: /BOB-1/ })).toContainText("Pending");
    expect(gw.called("POST", "/v1/affiliates")[0]?.body).toEqual({ email: "bob@partners.example", payout_method: "manual", name: "Bob Builder", code: "BOB-1", payout_details: "Pay by cash", pending: true });

    // Suspend and reinstate.
    await rowAction(anna, "Suspend");
    await expect(anna).toContainText("Suspended");
    expect(gw.called("PATCH", `/v1/affiliates/${ANNA}`).at(-1)?.body).toEqual({ status: "suspended" });
    await rowAction(anna, "Reinstate");
    await expect(anna).toContainText("Active");

    // Edit the payout details.
    await rowAction(anna, "Edit");
    await page.getByRole("dialog").getByLabel("Payout details").fill("TR99 1111");
    await page.getByRole("dialog").getByRole("button", { name: "Save" }).click();
    expect(gw.called("PATCH", `/v1/affiliates/${ANNA}`).at(-1)?.body).toMatchObject({ payout_details: "TR99 1111", payout_method: "bank_transfer" });

    // The referral link is copied from a field, and a promotion code is linked.
    await rowAction(anna, "Link and code");
    const linkDialog = page.getByRole("dialog");
    await expect(linkDialog.getByTestId("referral-link")).toHaveValue("https://pay.example.com/r/ANNA");
    await linkDialog.getByLabel("Promotion code").fill("ANNA10");
    await linkDialog.getByRole("button", { name: "Link code" }).click();
    await expect.poll(() => gw.called("PUT", `/v1/affiliates/${ANNA}/promotion-code`).length).toBe(1);
    expect(gw.called("PUT", `/v1/affiliates/${ANNA}/promotion-code`)[0]?.body).toEqual({ promotion_code: "ANNA10" });

    // The partner's referrals come with a masked e-mail.
    await rowAction(anna, "Referrals");
    await expect(page.getByRole("dialog")).toContainText("j***@example.com");
    expect(gw.called("GET", "/v1/affiliates/referrals").at(-1)?.query.get("affiliate_id")).toBe(ANNA);
  });

  test("a commission is reversed; a reversal row is visibly its own kind; a manual reversal needs a reference", async ({ page }) => {
    const { gw } = await install(page);
    await openTenantPage(page, "/affiliates");
    await tab(page, "Commissions").click();

    const commissions = page.getByTestId("commissions");
    const reversalRow = commissions.locator('tbody tr[data-kind="reversal"]');
    await expect(reversalRow).toContainText("Reversal");
    await expect(reversalRow).toContainText("-TRY 3.00");
    await expect(reversalRow.getByRole("button", { name: "Reverse" })).toHaveCount(0);
    // The paid commission links to its payout.
    await expect(commissions.locator('tbody tr[data-kind="commission"]').nth(1).getByRole("button", { name: "Open the payout" })).toBeVisible();

    // The filters narrow the list on the server.
    await pickFilter(page, "Kind", "Reversal");
    await expect.poll(() => gw.called("GET", "/v1/affiliates/commissions").some((q) => q.query.get("kind") === "reversal")).toBe(true);
    await pickFilter(page, "Kind", "Commissions and reversals");

    await rowAction(commissions.locator('tbody tr[data-kind="commission"]').first(), "Reverse");
    const dialog = page.getByRole("dialog");
    await expect(dialog).toContainText("Refunds, voids and credit notes reverse commissions on their own");
    await expect(dialog).toContainText("netted from the partner's next payout");
    await expect(dialog.getByTestId("reverse-left")).toContainText("TRY 10.00");

    await dialog.getByLabel("Reason").selectOption("manual");
    await dialog.getByLabel("Amount (TRY)").fill("4.50");
    await dialog.getByRole("button", { name: "Reverse commission" }).click();
    await expect(dialog.getByRole("alert")).toContainText("A manual reversal needs a reference.");
    expect(gw.called("POST", "/v1/affiliates/commissions/reverse")).toHaveLength(0);

    await dialog.getByLabel("Amount (TRY)").fill("40");
    await dialog.getByLabel("Reference (required)").fill("dispute-77");
    await dialog.getByRole("button", { name: "Reverse commission" }).click();
    await expect(dialog.getByRole("alert")).toContainText("not more than what is left");

    await dialog.getByLabel("Amount (TRY)").fill("4.50");
    await dialog.getByLabel("Note").fill("customer disputed");
    await dialog.getByRole("button", { name: "Reverse commission" }).click();
    await expect(page.getByText("TRY 4.50 taken back.")).toBeVisible();
    expect(gw.called("POST", "/v1/affiliates/commissions/reverse")[0]?.body).toEqual({ invoice_id: INVOICE, reason: "manual", amount_minor: "450", reference: "dispute-77", note: "customer disputed" });
  });

  test("payouts are created with a skipped batch explained, marked paid, and a sandbox one is fake", async ({ page }) => {
    const { gw, st } = await install(page);
    await openTenantPage(page, "/affiliates");
    await tab(page, "Payouts").click();

    // The sandbox payout carries a visible fake badge; the live one does not.
    const fakeRow = page.locator(`tr[data-payout="${FAKE_PAYOUT}"]`);
    const liveRow = page.locator(`tr[data-payout="${PAYOUT}"]`);
    await expect(fakeRow.getByTestId("fake-badge")).toHaveText("Fake (sandbox)");
    await expect(liveRow.getByTestId("fake-badge")).toHaveCount(0);

    // Create payouts for everybody: what was created and what was left out, with the reason in words.
    await page.getByRole("button", { name: "Create payouts" }).click();
    const result = page.getByTestId("payout-result");
    await expect(result.getByTestId("created-count")).toContainText("Payouts created: 1");
    await expect(result).toContainText("TRY 42.00");
    await expect(result.getByTestId("skipped")).toContainText("ZED");
    await expect(result.getByTestId("skipped")).toContainText("below the minimum payout");
    await expect(result.getByTestId("skipped")).toContainText("not above zero");
    expect(gw.called("POST", "/v1/affiliates/payouts")).toHaveLength(1);
    expect(st.createdPayouts).toHaveLength(1);

    // Mark the live payout paid, with the transfer reference; the dialog says that no money moves.
    await rowAction(liveRow, "Mark paid");
    let dialog = page.getByRole("dialog");
    await expect(dialog).toContainText("Payrudder moves no money");
    await expect(dialog.getByTestId("fake-warning")).toHaveCount(0);
    await dialog.getByLabel("Reference (optional)").fill("EFT-2026-10-21");
    await dialog.getByRole("button", { name: "Mark paid" }).click();
    await expect(liveRow).toContainText("Paid");
    expect(gw.called("POST", `/v1/affiliates/payouts/${PAYOUT}/pay`)[0]?.body).toEqual({ reference: "EFT-2026-10-21" });

    // The sandbox payout's dialog says nothing is really paid.
    await rowAction(fakeRow, "Mark paid");
    dialog = page.getByRole("dialog");
    await expect(dialog.getByTestId("fake-warning")).toContainText("nothing is really paid");
    await dialog.getByRole("button", { name: "Mark paid" }).click();
    await expect(fakeRow).toContainText("Paid");
    expect(gw.called("POST", `/v1/affiliates/payouts/${FAKE_PAYOUT}/pay`)[0]?.body).toEqual({});

    // The commissions of a payout reuse the commissions table, filtered by the payout.
    await rowAction(liveRow, "Commissions");
    await expect.poll(() => gw.called("GET", "/v1/affiliates/commissions").some((q) => q.query.get("payout_id") === PAYOUT)).toBe(true);
    await expect(page.getByRole("dialog", { name: "Commissions of the payout of ANNA" })).toBeVisible();
    await page.keyboard.press("Escape");

    // The status filter.
    await pickFilter(page, "Status", "Open");
    await expect.poll(() => gw.called("GET", "/v1/affiliates/payouts").some((q) => q.query.get("status") === "open")).toBe(true);
  });

  test("the journal shows its postings, balanced per currency, with the fake flag and a CSV download", async ({ page }) => {
    const { gw } = await install(page);
    await openTenantPage(page, "/affiliates");
    await tab(page, "Journal").click();

    const lira = page.getByRole("region", { name: "TRY" });
    await expect(lira).toContainText("Balanced");
    await expect(lira.locator('[data-stat="debit"]')).toContainText("TRY 25.00");
    await expect(lira.locator('[data-stat="credit"]')).toContainText("TRY 25.00");
    await expect(page.getByRole("region", { name: "USD" })).toContainText("Balanced");
    await expect(page.locator('tr[data-account="commission_expense"]').first()).toContainText("Commission expense");
    await expect(page.locator('tr[data-account="commissions_payable"]')).toContainText("Commissions payable");
    await expect(page.getByTestId("fake-badge")).toHaveCount(2);

    const download = page.waitForEvent("download");
    await page.getByRole("button", { name: "Download CSV" }).click();
    expect((await download).suggestedFilename()).toBe("affiliate-journal.csv");
    expect(gw.called("GET", "/v1/affiliates/journal.csv")).toHaveLength(1);
  });

  test("a journal that does not balance says so", async ({ page }) => {
    const st = fresh();
    await install(page, st);
    await page.route("**/gw/v1/affiliates/journal?**", (route) =>
      route.fulfill({ contentType: "application/json", body: JSON.stringify({ entries: [], total: 0, totals: [{ currency: "TRY", debit_minor: "100", credit_minor: "99" }] }) }),
    );
    await openTenantPage(page, "/affiliates");
    await tab(page, "Journal").click();
    await expect(page.getByRole("region", { name: "TRY" })).toContainText("Not balanced");
  });
});

test.describe("affiliates in Arabic", () => {
  for (const viewport of [{ width: 1280, height: 800 }, { width: 375, height: 800 }]) {
    test(`every tab reads right to left without sideways scroll at ${viewport.width}px`, async ({ page }) => {
      await page.setViewportSize(viewport);
      const st = fresh();
      st.program = { status: "active", commission_kind: "percent_bps", commission_value: "1000", commission_term: "lifetime", attribution_window_days: 30, hold_days: 30, daily_click_cap: 1000 };
      await install(page, st);
      await openTenantPage(page, "/affiliates");
      await page.evaluate(() => localStorage.setItem("payrudder.locale", "ar"));
      await page.reload();

      await expect(page.locator("html")).toHaveAttribute("dir", "rtl");
      await expect(page.locator("html")).toHaveAttribute("lang", "ar");
      const tabs = page.getByRole("tab");
      await expect(tabs).toHaveCount(6);
      await expect(tabs.first()).toHaveText("نظرة عامة");

      const noSidewaysScroll = () => page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth);
      for (let i = 0; i < 6; i++) {
        await tabs.nth(i).click();
        await expect(tabs.nth(i)).toHaveAttribute("data-state", "active");
        // Wait for the tab's own content: its first heading or table.
        await expect(page.locator("main h2, main table, main [data-testid='funnel']").first()).toBeVisible();
        expect(await noSidewaysScroll(), `tab ${i} overflows at ${viewport.width}px`).toBe(true);
      }

      // A dialog is right to left too.
      await tabs.nth(2).click();
      await page.getByRole("button", { name: "إضافة شريك" }).click();
      const dialog = page.getByRole("dialog");
      await expect(dialog).toBeVisible();
      await expect(dialog).toHaveCSS("direction", "rtl");
      expect(await noSidewaysScroll()).toBe(true);
    });
  }
});
