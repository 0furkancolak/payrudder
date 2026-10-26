import { expect, test, type Page } from "@playwright/test";
import { MockGateway, type Call } from "./gateway";
import { ACCOUNT, memberships, productGateway, signInAsAda, type ProductState } from "./product";
import { pickFilter, pickRange } from "./ui";

// The affiliate partner portal against a mocked gateway: /app/partner (the person's affiliations and the claim), /app/partner/{id} (figures, balances,
// terms, referrals, commissions, payouts, settings) and /app/partner/apply. The mock is a small stand-in for /app/v1/affiliate/*.

const SELLER = "99999999-0000-4000-8000-000000000001";
const SELLER_TENANT = "88888888-0000-4000-8000-000000000001";
const SANDBOX_TENANT = "88888888-0000-4000-8000-000000000002";
const LIVE_AFF = "77777777-0000-4000-8000-000000000001";
const SAND_AFF = "77777777-0000-4000-8000-000000000002";
const PENDING_AFF = "77777777-0000-4000-8000-000000000003";

const program = {
  status: "active",
  commission_kind: "percent_bps",
  commission_value: "1250",
  currency: "TRY",
  commission_term: "months",
  commission_term_value: 12,
  attribution_window_days: 45,
  hold_days: 14,
  min_payout_minor: "2500",
};

const affiliation = (id: string, extra: Record<string, unknown> = {}) => ({
  id,
  tenant_id: SELLER_TENANT,
  environment: "live",
  seller_name: "Globex Seller",
  code: "ADA10",
  link: "https://pay.example.com/r/ADA10",
  status: "active",
  name: "Ada",
  email: "ada@example.com",
  payout_method: "bank_transfer",
  payout_details: "TR00 1111",
  program,
  create_time: "2026-10-01T09:00:00Z",
  ...extra,
});

const commission = (id: string, extra: Record<string, unknown> = {}) => ({
  id,
  kind: "commission",
  customer_email_masked: "j***@example.com",
  currency: "TRY",
  amount_minor: "1000",
  reversed_minor: "0",
  status: "approved",
  earn_time: "2026-10-10T09:00:00Z",
  ...extra,
});

interface Portal {
  affiliations: Record<string, unknown>[];
  claimStatus: number;
  claimed: number;
  applyResult: { status: number; body: unknown };
  lookup: { status: number; body: unknown };
}

function portal(): Portal {
  return {
    affiliations: [affiliation(SAND_AFF, { environment: "sandbox", seller_name: "Test Seller", tenant_id: SANDBOX_TENANT, code: "ADA-T" }), affiliation(LIVE_AFF)],
    claimStatus: 200,
    claimed: 0,
    applyResult: { status: 201, body: affiliation(PENDING_AFF, { status: "pending", seller_name: "New Seller" }) },
    lookup: { status: 200, body: { seller_name: "New Seller", environment: "live", applied: false, program } },
  };
}

const err = (status: number, key: string, message: string) => ({ status, body: { error: { code: status === 404 ? "NOT_FOUND" : status === 409 ? "ALREADY_EXISTS" : "PERMISSION_DENIED", key, message } } });

function portalApi(p: Portal) {
  return (c: Call) => {
    if (!c.path.startsWith("/app/v1/affiliate/")) return undefined;
    const path = c.path.replace("/app/v1/affiliate", "");
    if (path === "/claim" && c.method === "POST") return p.claimStatus === 200 ? { body: { claimed: p.claimed, affiliations: p.affiliations } } : err(p.claimStatus, "identity.email.not_verified", "confirm your e-mail");
    if (path === "/affiliations") return { body: { affiliations: p.affiliations } };
    if (path.startsWith("/programs/")) return p.lookup;
    if (path === "/applications") {
      if (p.applyResult.status === 201) p.affiliations.unshift(p.applyResult.body as Record<string, unknown>);
      return p.applyResult;
    }
    const detail = /^\/affiliations\/([^/]+)(?:\/(referrals|commissions|payouts))?$/.exec(path);
    if (!detail) return undefined;
    const a = p.affiliations.find((x) => x.id === detail[1]);
    if (!a) return err(404, "affiliate.not_found", "no such affiliation");
    if (!detail[2] && c.method === "PATCH") {
      Object.assign(a, c.body);
      return { body: a };
    }
    if (!detail[2]) {
      return {
        body: {
          affiliation: a,
          from_date: c.query.get("from"),
          to_date: c.query.get("to"),
          clicks: "320",
          referrals: "40",
          paying_customers: "12",
          earned: [
            { currency: "USD", earned_minor: "2500", reversed_minor: "0" },
            { currency: "TRY", earned_minor: "10000", reversed_minor: "1500" },
          ],
          balances: [
            { currency: "TRY", pending_minor: "400", approved_minor: "2500", paid_minor: "7000", reversed_minor: "300" },
            { currency: "USD", pending_minor: "0", approved_minor: "900", paid_minor: "0", reversed_minor: "0" },
          ],
        },
      };
    }
    if (detail[2] === "referrals") return { body: { referrals: [{ customer_email_masked: "j***@example.com", source: "link", attribute_time: "2026-10-05T09:00:00Z" }, { customer_email_masked: "m***@corp.example", source: "code", attribute_time: "2026-10-06T09:00:00Z" }], total: 2 } };
    if (detail[2] === "commissions") {
      const rows = [
        commission("c1", { status: "pending", approve_after_time: "2026-11-01T00:00:00Z" }),
        commission("c2", { kind: "reversal", amount_minor: "-300", status: "approved" }),
        commission("c3", { status: "paid", amount_minor: "700", reversed_minor: "100" }),
      ];
      const status = c.query.get("status");
      const out = status ? rows.filter((r) => r.status === status) : rows;
      return { body: { commissions: out, total: out.length } };
    }
    return {
      body: {
        payouts: [
          { id: "p1", currency: "TRY", total_minor: "2500", status: "paid", reference: "EFT-1", fake: a.environment === "sandbox", create_time: "2026-10-20T09:00:00Z", pay_time: "2026-10-21T09:00:00Z" },
          { id: "p2", currency: "TRY", total_minor: "900", status: "open", fake: false, create_time: "2026-10-22T09:00:00Z" },
        ],
        total: 2,
      },
    };
  };
}

async function open(page: Page, p: Portal, state: Partial<ProductState> = {}, path = "/app/partner"): Promise<{ gw: MockGateway; state: ProductState }> {
  const made = productGateway(new MockGateway(), state);
  made.gw.on(portalApi(p));
  await made.gw.install(page);
  await signInAsAda(page);
  await page.goto(path);
  return made;
}

const seller = { account_id: SELLER, account_name: "Globex Seller", role: "affiliate" };

test.describe("partner portal", () => {
  test("memberships with the role affiliate are not accounts of the person, and give a Partner portal entry", async ({ page }) => {
    const { gw } = await open(page, portal(), { memberships: [seller, ...memberships] }, "/app");
    const nav = page.getByTestId("sidebar").getByRole("navigation", { name: "Main navigation" });
    await expect(nav.getByRole("link", { name: "Partner portal" })).toBeVisible();
    // The seller's account is neither listed nor switched to: only one account of the person is left, so there is nothing to choose.
    await expect(page.getByLabel("Switch account")).toHaveCount(0);
    // The seller's account came first, but the account routes name the person's own.
    await expect.poll(() => gw.called("GET", "/app/v1/apps").some((c) => c.query.get("account_id") === ACCOUNT)).toBe(true);
    await nav.getByRole("link", { name: "Partner portal" }).click();
    await expect(page).toHaveURL(/\/app\/partner$/);
    await expect(page.getByRole("link", { name: "Partner portal" }).first()).toHaveAttribute("aria-current", "page");
  });

  test("a person who is nobody's partner has no Partner portal entry, and a partner without an account of their own gets a way in", async ({ page }) => {
    await open(page, portal(), {}, "/app");
    await expect(page.getByTestId("sidebar").getByRole("link", { name: "Partner portal" })).toHaveCount(0);
  });

  test("only a seller's account: the page says there is no account and leads to the portal", async ({ page }) => {
    await open(page, portal(), { memberships: [seller] }, "/app");
    await expect(page.getByTestId("no-account")).toBeVisible();
    await page.getByTestId("to-partner").click();
    await expect(page).toHaveURL(/\/app\/partner$/);
  });

  test("the home claims once, lists every affiliation with its badges and link, and says a sandbox earns fake money", async ({ page }) => {
    const p = portal();
    p.claimed = 1;
    const { gw } = await open(page, p, { memberships: [seller, ...memberships] });
    const live = page.locator(`[data-affiliation="${LIVE_AFF}"]`);
    const sandbox = page.locator(`[data-affiliation="${SAND_AFF}"]`);
    await expect(live).toContainText("Globex Seller");
    await expect(live.getByTestId("env-badge")).toHaveText("Live");
    await expect(live.getByTestId("status-badge")).toHaveText("Active");
    await expect(live.getByTestId("affiliation-link")).toHaveValue("https://pay.example.com/r/ADA10");
    await expect(live.getByTestId("terms")).toContainText("You earn 12.5 % of the net amount");
    await expect(live.getByTestId("terms")).toContainText("Earned for the first 12 months of each customer.");
    await expect(sandbox.getByTestId("env-badge")).toHaveText("Sandbox");
    await expect(sandbox.getByTestId("banner-sandbox")).toContainText("fake money");
    await expect(live.getByTestId("banner-sandbox")).toHaveCount(0);
    // The claim is made once and the list is read again after it claimed something.
    await expect.poll(() => gw.called("POST", "/app/v1/affiliate/claim").length).toBe(1);
    // The first read may be cancelled before it leaves the browser when the claim answers first, so the check is the order, not the count.
    await expect
      .poll(() => {
        const claimAt = gw.calls.findIndex((c) => c.method === "POST" && c.path === "/app/v1/affiliate/claim");
        const listAt = gw.calls.map((c) => `${c.method} ${c.path}`).lastIndexOf("GET /app/v1/affiliate/affiliations");
        return claimAt >= 0 && listAt > claimAt;
      })
      .toBe(true);
    // Live ones first.
    const order = await page.locator("[data-affiliation]").evaluateAll((els) => els.map((e) => e.getAttribute("data-affiliation")));
    expect(order).toEqual([LIVE_AFF, SAND_AFF]);
  });

  test("an unverified e-mail address gets a hint and the list still shows", async ({ page }) => {
    const p = portal();
    p.claimStatus = 403;
    await open(page, p);
    await expect(page.getByTestId("verify-hint")).toContainText("Confirm your e-mail address");
    await expect(page.locator(`[data-affiliation="${LIVE_AFF}"]`)).toBeVisible();
  });

  test("an empty portal explains how to become a partner", async ({ page }) => {
    const p = portal();
    p.affiliations = [];
    await open(page, p);
    await expect(page.getByTestId("partner-empty")).toContainText("Ask a seller");
  });

  test("a pending and a suspended affiliation say so and still show their data", async ({ page }) => {
    const p = portal();
    p.affiliations = [affiliation(PENDING_AFF, { status: "pending" }), affiliation(LIVE_AFF, { status: "suspended" })];
    await open(page, p, {}, `/app/partner/${PENDING_AFF}`);
    await expect(page.getByTestId("banner-pending")).toContainText("waiting for the seller's approval");
    await expect(page.getByTestId("status-badge")).toHaveText("Waiting for approval");
    await expect(page.locator('[data-stat="clicks"]')).toContainText("320");
    await page.goto(`/app/partner/${LIVE_AFF}`);
    await expect(page.getByTestId("banner-suspended")).toContainText("suspended you");
    await expect(page.locator('[data-stat="clicks"]')).toContainText("320");
  });

  test("the dashboard: figures, earned and balances per currency (never summed), the terms in words, and the period", async ({ page }) => {
    await page.clock.setFixedTime(new Date(2026, 9, 21, 12, 0, 0));
    const { gw } = await open(page, portal(), {}, `/app/partner/${LIVE_AFF}`);
    await expect(page.getByRole("heading", { name: "Globex Seller" })).toBeVisible();
    await expect(page.locator('[data-stat="clicks"]').first()).toContainText("320");
    await expect(page.locator('[data-stat="referrals"]').first()).toContainText("40");
    await expect(page.locator('[data-stat="paying"]').first()).toContainText("12");

    const earned = page.getByTestId("earned");
    await expect(earned.locator('[data-currency="TRY"]')).toContainText("TRY 100.00");
    await expect(earned.locator('[data-currency="TRY"]')).toContainText("TRY 15.00 taken back");
    await expect(earned.locator('[data-currency="USD"]')).toContainText("$25.00");

    const lira = page.getByRole("region", { name: "TRY" });
    await expect(lira.locator('[data-stat="pending"]')).toContainText("TRY 4.00");
    await expect(lira.locator('[data-stat="available"]')).toContainText("TRY 25.00");
    await expect(lira.locator('[data-stat="paid"]')).toContainText("TRY 70.00");
    await expect(lira.locator('[data-stat="reversed"]')).toContainText("TRY 3.00");
    await expect(page.getByRole("region", { name: "USD" }).locator('[data-stat="available"]')).toContainText("$9.00");

    const terms = page.getByTestId("terms").last();
    await expect(terms.locator('[data-term="percent"]')).toHaveText("You earn 12.5 % of the net amount, before tax, of every invoice a customer you brought pays.");
    await expect(terms.locator('[data-term="months"]')).toContainText("first 12 months");
    await expect(terms.locator('[data-term="window"]')).toContainText("45 days");
    await expect(terms.locator('[data-term="hold"]')).toContainText("14 days");
    await expect(terms.locator('[data-term="minPayout"]')).toContainText("TRY 25.00");

    // The period is sent as days, and changing it keeps the page.
    await pickRange(page, "lastMonth");
    await expect.poll(() => gw.called("GET", `/app/v1/affiliate/affiliations/${LIVE_AFF}`).some((q) => q.query.get("from") === "2026-09-01" && q.query.get("to") === "2026-09-30")).toBe(true);
    await expect(page.locator('[data-stat="clicks"]').first()).toContainText("320");
  });

  test("referrals show masked e-mails only; commissions mark a reversal row and hide seller data; payouts carry the fake badge", async ({ page }) => {
    const p = portal();
    const { gw } = await open(page, p, {}, `/app/partner/${SAND_AFF}`);
    await expect(page.getByTestId("referrals")).toContainText("j***@example.com");
    await expect(page.getByTestId("referrals")).toContainText("m***@corp.example");
    await expect(page.getByTestId("referrals")).not.toContainText("@example.com j");

    await page.getByRole("tab", { name: "Commissions" }).click();
    const rows = page.getByTestId("commissions");
    const reversal = rows.locator('tbody tr[data-kind="reversal"]');
    await expect(reversal).toContainText("Reversal");
    await expect(reversal).toContainText("-TRY 3.00");
    await expect(reversal.locator("td").nth(2)).toHaveClass(/text-destructive/);
    await expect(rows.locator('tbody tr[data-kind="commission"]').first()).toContainText("Payable from");
    // Nothing of the seller's: no invoice number or customer id column.
    await expect(rows.getByRole("columnheader", { name: /invoice/i })).toHaveCount(0);
    await pickFilter(page, "Status", "Paid");
    await expect.poll(() => gw.called("GET", `/app/v1/affiliate/affiliations/${SAND_AFF}/commissions`).some((q) => q.query.get("status") === "paid")).toBe(true);

    await page.getByRole("tab", { name: "Payouts" }).click();
    await expect(page.locator('[data-payout="p1"]').getByTestId("fake-badge")).toHaveText("Fake (sandbox)");
    await expect(page.locator('[data-payout="p1"]')).toContainText("EFT-1");
    await expect(page.locator('[data-payout="p2"]')).toContainText("Open");

    // A live affiliation's payouts are not fake.
    await page.goto(`/app/partner/${LIVE_AFF}`);
    await page.getByRole("tab", { name: "Payouts" }).click();
    await expect(page.getByTestId("fake-badge")).toHaveCount(0);
  });

  test("the settings change the name and the payout details", async ({ page }) => {
    const { gw } = await open(page, portal(), {}, `/app/partner/${LIVE_AFF}`);
    await page.getByRole("tab", { name: "Settings" }).click();
    await expect(page.getByLabel("Your name")).toHaveValue("Ada");
    await expect(page.getByLabel("Payout details")).toHaveValue("TR00 1111");
    await page.getByLabel("Your name").fill("Ada L.");
    await page.getByLabel("Payout method").selectOption("manual");
    await page.getByLabel("Payout details").fill("Cash, please");
    await page.getByRole("button", { name: "Save" }).click();
    await expect(page.getByRole("status").filter({ hasText: "Saved." })).toBeVisible();
    expect(gw.called("PATCH", `/app/v1/affiliate/affiliations/${LIVE_AFF}`)[0]?.body).toEqual({ name: "Ada L.", payout_method: "manual", payout_details: "Cash, please" });
  });
});

test.describe("applying", () => {
  const applyPath = `/app/partner/apply?tenant=${SELLER_TENANT}`;

  test("shows the seller's terms, applies, and says the application is pending", async ({ page }) => {
    const p = portal();
    const { gw } = await open(page, p, {}, applyPath);
    await expect(page.getByTestId("apply-seller")).toHaveText("New Seller");
    await expect(page.getByTestId("env-badge")).toHaveText("Live");
    await expect(page.getByTestId("terms")).toContainText("12.5 %");
    expect(gw.called("GET", `/app/v1/affiliate/programs/${SELLER_TENANT}`)).toHaveLength(1);
    await expect(page.getByLabel("Your name")).toHaveValue("Ada Lovelace");
    await page.getByLabel("Payout method").selectOption("bank_transfer");
    await page.getByLabel("Payout details").fill("TR55 0000");
    await page.getByRole("button", { name: "Apply" }).click();
    await expect(page.getByTestId("apply-done")).toContainText("Application sent");
    expect(gw.called("POST", "/app/v1/affiliate/applications")[0]?.body).toEqual({ tenant_id: SELLER_TENANT, name: "Ada Lovelace", payout_method: "bank_transfer", payout_details: "TR55 0000" });
    await page.getByTestId("apply-done").getByRole("link", { name: "Open" }).click();
    await expect(page).toHaveURL(new RegExp(`/app/partner/${PENDING_AFF}$`));
  });

  test("an automatic program makes the partner active at once", async ({ page }) => {
    const p = portal();
    p.applyResult = { status: 201, body: affiliation(PENDING_AFF, { status: "active", seller_name: "New Seller" }) };
    await open(page, p, {}, applyPath);
    await page.getByRole("button", { name: "Apply" }).click();
    await expect(page.getByTestId("apply-done")).toContainText("You are a partner");
  });

  test("a seller without an open program is said in words", async ({ page }) => {
    const p = portal();
    p.lookup = err(404, "affiliate.portal.program_unavailable", "unavailable");
    await open(page, p, {}, applyPath);
    await expect(page.getByRole("alert")).toContainText("no partner program open to applications");
  });

  test("a second application is refused in words", async ({ page }) => {
    const q = portal();
    q.applyResult = err(409, "affiliate.portal.already_applied", "applied");
    await open(page, q, {}, applyPath);
    await page.getByRole("button", { name: "Apply" }).click();
    await expect(page.getByRole("alert")).toContainText("partner of this seller already");
  });

  test("somebody who has applied already is told, and the page without a tenant says what it needs", async ({ page }) => {
    const p = portal();
    p.lookup = { status: 200, body: { seller_name: "New Seller", environment: "sandbox", applied: true, program } };
    await open(page, p, {}, applyPath);
    await expect(page.getByTestId("apply-already")).toBeVisible();
    await expect(page.getByTestId("env-badge")).toHaveText("Sandbox");
    await page.goto("/app/partner/apply");
    await expect(page.getByTestId("apply-missing")).toBeVisible();
  });

  test("signed out, the sign-in brings the person back to the application page", async ({ page }) => {
    const made = productGateway(new MockGateway());
    made.gw.on(portalApi(portal()));
    await made.gw.install(page);
    await page.goto(applyPath);
    await expect(page).toHaveURL(/\/signin\?next=/);
    await page.getByLabel("E-mail").fill("ada@example.com");
    await page.getByLabel("Password", { exact: true }).fill("right password!");
    await page.getByRole("button", { name: "Sign in" }).click();
    await expect(page).toHaveURL(new RegExp(`/app/partner/apply\\?tenant=${SELLER_TENANT}`));
    await expect(page.getByTestId("apply-seller")).toHaveText("New Seller");
  });

  test("a return path that leaves the console is ignored", async ({ page }) => {
    const made = productGateway(new MockGateway());
    await made.gw.install(page);
    await page.goto("/signin?next=https://evil.example/x");
    await page.getByLabel("E-mail").fill("ada@example.com");
    await page.getByLabel("Password", { exact: true }).fill("right password!");
    await page.getByRole("button", { name: "Sign in" }).click();
    await page.waitForURL("**/app");
  });
});

test.describe("the partner portal in Arabic", () => {
  for (const viewport of [{ width: 1280, height: 800 }, { width: 375, height: 800 }]) {
    test(`reads right to left without sideways scroll at ${viewport.width}px`, async ({ page }) => {
      await page.setViewportSize(viewport);
      await open(page, portal(), { memberships: [seller, ...memberships] }, "/app");
      await page.evaluate(() => localStorage.setItem("payrudder.locale", "ar"));
      const noSidewaysScroll = () => page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth);
      const visit = async (path: string, ready: string) => {
        await page.goto(path);
        await expect(page.locator("html")).toHaveAttribute("dir", "rtl");
        await expect(page.locator(ready).first()).toBeVisible();
        expect(await noSidewaysScroll(), `${path} overflows at ${viewport.width}px`).toBe(true);
      };
      await visit("/app/partner", `[data-affiliation="${LIVE_AFF}"]`);
      await expect(page.getByTestId("env-badge").first()).toHaveText(/^(حقيقي|تجريبي)$/);
      await visit(`/app/partner/${SAND_AFF}`, '[data-testid="earned"]');
      for (const name of ["الإحالات", "العمولات", "الدفعات", "الإعدادات"]) {
        await page.getByRole("tab", { name, exact: true }).click();
        await page.waitForTimeout(300);
        expect(await noSidewaysScroll(), `${name} overflows at ${viewport.width}px`).toBe(true);
      }
      await visit(`/app/partner/apply?tenant=${SELLER_TENANT}`, '[data-testid="apply-seller"]');
    });
  }
});
