import { randomUUID } from "node:crypto";
import { expect, test, type Page } from "@playwright/test";
import { verifyAddress } from "./operator";
import { pickLanguage, pickTheme } from "../e2e-mock/ui";

// The product app against the REAL stack (make dev / make up): nothing is mocked. It signs a fresh person up, seeds real money data in the app's sandbox
// through the public API (the mock payment provider plays the payments out), and checks what the pages show against what was seeded.
// SHOTS=<dir> also saves screenshots (desktop and phone) so the pages can be looked at.
const gateway = process.env.PAYRUDDER_GATEWAY_URL ?? "http://localhost:42800";
const operatorToken = process.env.PAYRUDDER_OPERATOR_TOKEN ?? "local-operator-token-change-me-0123456789";
const shots = process.env.SHOTS;
const password = "correct horse battery";

async function shoot(page: Page, name: string): Promise<void> {
  if (shots) await page.screenshot({ path: `${shots}/${name}.png`, fullPage: true });
}

/** Collects console errors/warnings, page errors and failed responses (the ones a test expects are filtered by the caller). */
function watch(page: Page): string[] {
  const problems: string[] = [];
  page.on("console", (m) => {
    if (m.type() === "error" || m.type() === "warning") problems.push(`console.${m.type()}: ${m.text()}`);
  });
  page.on("pageerror", (e) => problems.push(`pageerror: ${e.message}`));
  page.on("response", (r) => {
    if (r.status() >= 400) problems.push(`${r.status()} ${r.request().method()} ${r.url()}`);
  });
  return problems;
}

type Json = Record<string, any>; // eslint-disable-line @typescript-eslint/no-explicit-any

/** A call to the gateway; a 429 (the free tier's write burst) waits and tries again. */
async function api(method: string, path: string, token: string, body?: unknown, headers: Record<string, string> = {}): Promise<{ status: number; body: Json }> {
  for (let attempt = 0; attempt < 8; attempt++) {
    const res = await fetch(gateway + path, {
      method,
      headers: { authorization: `Bearer ${token}`, "content-type": "application/json", ...headers },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    if (res.status === 429) {
      await new Promise((r) => setTimeout(r, 1500));
      continue;
    }
    return { status: res.status, body: (await res.json().catch(() => ({}))) as Json };
  }
  throw new Error(`${method} ${path}: still rate limited`);
}

async function must(method: string, path: string, token: string, body: unknown, want = 201): Promise<Json> {
  const r = await api(method, path, token, body);
  expect(r.status, `${method} ${path}: ${JSON.stringify(r.body)}`).toBe(want);
  return r.body;
}

async function signUp(): Promise<{ email: string; token: string }> {
  const email = `ui-${randomUUID().slice(0, 8)}@example.com`;
  const since = Date.now() - 2000;
  const res = await fetch(gateway + "/auth/signup", {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ email, password, display_name: "Real Browser", account_name: "Browser Labs" }),
  });
  // Sign-up answers 202 and opens no session (phase 39): the person signs in with the password just chosen.
  expect(res.status).toBe(202);
  // A live API key needs a proven address (phase 39): follow the mailed link like the person would.
  await verifyAddress(email, since);
  const signin = await fetch(gateway + "/auth/signin", { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify({ email, password }) });
  expect(signin.status).toBe(200);
  return { email, token: ((await signin.json()) as Json).token };
}

async function signInUi(page: Page, email: string, pw = password): Promise<void> {
  await page.goto("/signin");
  await page.getByLabel("E-mail").fill(email);
  await page.getByLabel("Password", { exact: true }).fill(pw);
  await page.getByRole("button", { name: "Sign in" }).click();
}

const today = () => new Date().toISOString().slice(0, 10);

test.describe("product app, real stack", () => {
  test.describe.configure({ mode: "serial" });

  let email = "";
  let session = "";
  let appId = "";
  let sandbox = "";
  let sandboxKey = "";
  let adhocUrl = "";
  let portalUrl = "";
  let invoiceId = "";

  test.beforeAll(async () => {
    test.setTimeout(6 * 60_000);
    ({ email, token: session } = await signUp());
    const app = await must("POST", "/app/v1/apps", session, { name: "Seed Shop", currency: "TRY", timezone: "Europe/Istanbul" });
    appId = app.id;
    sandbox = (app.tenants as Json[]).find((t) => t.environment === "ENVIRONMENT_SANDBOX")!.id;
    sandboxKey = (await must("POST", `/app/v1/apps/${appId}/api-keys`, session, { environment: "sandbox" })).plaintext;

    // Money in the sandbox: a plan with a TRY and a EUR fee; two customers who pay, one whose card is declined, one billed in EUR.
    await must("POST", "/v1/plans", sandboxKey, { code: "pro", name: "Pro" });
    await must("POST", "/v1/plans/pro/versions", sandboxKey, { base_fee_minor: "9900", fees: [{ currency: "EUR", base_fee_minor: "2500" }] });
    for (const [id, cur, scenario] of [["c_ok1", "TRY", "success"], ["c_ok2", "TRY", "success"], ["c_dec", "TRY", "declined"], ["c_eur", "EUR", "success"]]) {
      await must("POST", "/v1/customers", sandboxKey, { external_id: id, name: id, email: `${id}@example.com`, billing_currency: cur, country: cur === "EUR" ? "DE" : "TR", payment_method: `pm_sandbox_${scenario}` });
      await must("POST", "/v1/subscriptions", sandboxKey, { customer_external_id: id, plan_code: "pro", currency: cur, fee_timing: "FEE_TIMING_ARREARS" });
    }
    await must("POST", "/v1/sandbox/close-period", sandboxKey, {}, 202);
    // The billing run starts about 30 s after the cutoff; wait until the four invoices and their payments have settled.
    for (let i = 0; i < 80; i++) {
      await new Promise((r) => setTimeout(r, 3000));
      const list = ((await api("GET", "/v1/invoices?page_size=50", sandboxKey)).body.invoices ?? []) as Json[];
      if (list.length >= 4 && list.every((x) => x.saga_state === "COMPLETED" || x.saga_state === "PAYMENT_DECLINED")) break;
    }
    const invoices = ((await api("GET", "/v1/invoices?page_size=50", sandboxKey)).body.invoices ?? []) as Json[];
    expect(invoices).toHaveLength(4);
    invoiceId = invoices.find((x) => x.customer_external_id === "c_ok2")!.id;
    // A refund of 30.00 of a paid invoice, to the card.
    await must("POST", `/admin/v1/invoices/${invoiceId}/credit-notes`, operatorToken, { tenant_id: sandbox, amount_minor: "3000", reason: "ui test", refund_to_card: true });

    // Accounting: income per currency next to expenses per currency, a recurring one and a voided one.
    await must("POST", "/v1/expenses", sandboxKey, { vendor: "Hetzner", category: "infrastructure", amount_minor: "20000", currency: "TRY", incurred_on: today() });
    await must("POST", "/v1/expenses", sandboxKey, { vendor: "Notion", category: "software", amount_minor: "1000", currency: "EUR", incurred_on: today(), recurring: "monthly" });
    const voided = await must("POST", "/v1/expenses", sandboxKey, { vendor: "Void me", category: "other", amount_minor: "777", currency: "TRY", incurred_on: today() });
    await must("DELETE", `/v1/expenses/${voided.id}?reason=entered%20twice`, sandboxKey, undefined, 200);
    const month = today().slice(0, 8);
    const summary = await must("GET", `/v1/accounting/summary?from=${month}01&to=${month}28`, sandboxKey, undefined, 200);
    const text = JSON.stringify(summary);
    expect(text).not.toContain("777"); // the voided expense is out of every number
    expect(text).toContain("20000");

    await must("POST", "/v1/webhook-endpoints", sandboxKey, { url: "https://example.invalid/hook", description: "dead hook" });
    await must("POST", "/v1/credit-packs", sandboxKey, { name: "100 credits", credits: "100", price_minor: "5000", currency: "TRY" });
    adhocUrl = (await must("POST", "/v1/payment-links", sandboxKey, { kind: "adhoc", amount_minor: "2500", currency: "EUR", description: "Hosted test", customer_external_id: "c_eur" })).url;
    portalUrl = (await must("POST", "/v1/customers/c_ok1/portal-links", sandboxKey, {})).url;
  });

  test("the dashboard shows what was seeded, per currency, in both environments", async ({ page }) => {
    const problems = watch(page);
    await signInUi(page, email);
    await page.waitForURL(/\/app(\/apps\/[^/?]+)?(?![^?])/);
    await expect(page.getByRole("main").getByRole("link", { name: /Seed Shop/ })).toBeVisible();
    await shoot(page, "r1-home");

    await page.goto(`/app/apps/${appId}?env=sandbox`);
    await expect(page.getByRole("heading", { name: "Seed Shop" })).toBeVisible();
    // One currency at a time, never added up: the tenant's own (TRY) first. Net of the 30.00 refund: 198.00 - 30.00, the figure of the accounting page.
    const main = page.getByRole("main");
    await expect(page.getByTestId("currency-tab-TRY")).toHaveAttribute("aria-selected", "true");
    await expect(page.getByTestId("kpi-revenue-value")).toHaveText(/^TRY\s168\.00$/);
    await expect(page.getByTestId("kpi-refunds-value")).toHaveText(/^TRY\s30\.00$/);
    await expect(page.getByTestId("kpi-open-value")).toHaveText(/^TRY\s99\.00$/);
    await expect(page.getByTestId("kpi-mrr-value")).toHaveText(/^TRY\s297\.00$/);
    await page.getByTestId("currency-tab-EUR").click();
    await expect(page.getByTestId("kpi-revenue-value")).toHaveText("€25.00");
    await expect(page.getByTestId("kpi-mrr-value")).toHaveText("€25.00");
    await expect(main.getByTestId("health")).toContainText("endpoint");
    await shoot(page, "r2-dashboard-sandbox");

    await page.goto(`/app/apps/${appId}?env=live`);
    await expect(page.getByRole("heading", { name: "Seed Shop" })).toBeVisible();
    await expect(page.getByTestId("live-checklist")).toBeVisible(); // live is not active yet: the go-live checklist, not a page of zeros
    await expect(page.getByRole("main")).not.toContainText("TRY 198.00"); // the live tenant is another tenant
    await shoot(page, "r3-dashboard-live");
    expect(problems.filter((p) => !/favicon/.test(p))).toEqual([]);
  });

  test("keys: create a sandbox key, see it once, revoke it", async ({ page, context }) => {
    await context.grantPermissions(["clipboard-read", "clipboard-write"]);
    await signInUi(page, email);
    await page.waitForURL(/\/app(\/apps\/[^/?]+)?(?![^?])/);
    await page.goto(`/app/apps/${appId}/keys?env=sandbox`);
    const keys = page.getByRole("region", { name: "API keys" });
    await keys.getByRole("button", { name: "New sandbox key" }).click();
    await page.getByTestId("new-key-sheet").getByTestId("create-key").click();
    const reveal = page.getByRole("dialog");
    const secret = (await reveal.getByTestId("new-key").innerText()).trim();
    expect(secret).toMatch(/^qk_/);
    await reveal.getByRole("button", { name: "I have saved it" }).click();
    await expect(page.getByText(secret)).toHaveCount(0);
    const prefix = secret.split("_").slice(0, 3).join("_");
    const fresh = keys.getByRole("row").filter({ hasText: prefix });
    await fresh.getByRole("button", { name: /Revoke|More actions/ }).click();
    await page.getByRole("menuitem", { name: "Revoke" }).click();
    await page.getByRole("dialog").getByRole("button", { name: "Revoke" }).click();
    await expect(fresh).toContainText("Revoked");
    // The revoked key no longer opens the API.
    expect((await api("GET", "/v1/customers", secret)).status).toBe(401);
  });

  test("six languages, Arabic right to left, dark mode, phone and tablet width: no sideways scroll, no console noise", async ({ page }) => {
    test.setTimeout(900_000); // every page of the menu at three widths, each waiting for the network to settle
    const problems = watch(page);
    await signInUi(page, email);
    await page.waitForURL(/\/app(\/apps\/[^/?]+)?(?![^?])/);
    const overflow = async (tag: string) => {
      const o = await page.evaluate(() => ({ sw: document.documentElement.scrollWidth, cw: document.documentElement.clientWidth }));
      expect(o.sw, `horizontal overflow on ${tag}`).toBeLessThanOrEqual(o.cw);
    };
    const pages: string[] = [`/app`, `/app/apps/${appId}?env=sandbox`, `/app/settings`, `/app/t/${sandbox}/customers`, `/app/t/${sandbox}/subscriptions`, `/app/t/${sandbox}/usage`, `/app/t/${sandbox}/packages`, `/app/t/${sandbox}/pricing`, `/app/t/${sandbox}/coupons`, `/app/t/${sandbox}/invoices`, `/app/t/${sandbox}/payments`, `/app/t/${sandbox}/payment-links`, `/app/t/${sandbox}/credits`, `/app/t/${sandbox}/accounting`, `/app/t/${sandbox}/reports`, `/app/t/${sandbox}/webhooks`, `/app/t/${sandbox}/mail`, `/app/apps/${appId}/keys?env=sandbox`, `/app/apps/${appId}/settings?env=sandbox`, `/app/members`];
    // Every page the environment's menu links to is checked as well, so a page added to the navigation is covered without touching this list.
    await page.setViewportSize({ width: 1280, height: 900 });
    await page.goto(`/app/t/${sandbox}/customers`);
    await page.getByTestId("sidebar").waitFor();
    const menu = await page.getByTestId("sidebar").getByRole("link").evaluateAll((as) => as.map((a) => a.getAttribute("href") ?? ""));
    for (const h of menu) if (h.startsWith("/app") && !pages.includes(h)) pages.push(h);
    pages.push(`/app/t/${sandbox}/invoices?invoice=${invoiceId}`);
    for (const [w, h] of [[375, 812], [768, 1024], [1280, 900]] as [number, number][]) {
      await page.setViewportSize({ width: w, height: h });
      for (const p of pages) {
        await page.goto(p);
        await page.getByRole("main").waitFor();
        await page.waitForLoadState("networkidle");
        await overflow(`${p} @${w}`);
        await shoot(page, `r4-${w}-${p.replace(/[^a-z]+/g, "-")}`);
      }
    }
    await page.setViewportSize({ width: 1280, height: 900 });
    await page.goto(`/app/apps/${appId}?env=sandbox`);
    for (const [code, dir] of [["tr", "ltr"], ["de", "ltr"], ["es", "ltr"], ["fr", "ltr"], ["ar", "rtl"], ["en", "ltr"]] as [string, string][]) {
      await pickLanguage(page, code);
      await expect(page.locator("html")).toHaveAttribute("lang", code);
      await expect(page.locator("html")).toHaveAttribute("dir", dir);
      await page.waitForLoadState("networkidle");
      await overflow(`dashboard in ${code}`);
      await shoot(page, `r5-lang-${code}`);
    }
    await pickTheme(page, "dark");
    await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
    await shoot(page, "r6-dark");
    // The affiliate program answers 404 until a seller sets one up: a normal state the page handles (the browser still logs the failed load, with no URL).
    expect(problems.filter((p) => !/favicon|affiliates\/program|status of 404/.test(p))).toEqual([]);
  });

  test("hosted payment link: pay it; the customer portal sells the credit pack", async ({ page }) => {
    const problems = watch(page);
    await page.goto(adhocUrl);
    await expect(page.getByText("Hosted test")).toBeVisible();
    await shoot(page, "r7-link");
    await page.getByRole("button", { name: "Continue" }).click();
    // The invoice is made and goes through its saga first ("being processed", the page refreshes by itself); then the sandbox offers the mock method.
    await expect(page.getByRole("button", { name: "Pay (test)" })).toBeVisible({ timeout: 60_000 });
    await shoot(page, "r7b-link-pay");
    await page.getByRole("button", { name: "Pay (test)" }).click();
    await expect(page.getByText("Paid").first()).toBeVisible({ timeout: 60_000 });
    await shoot(page, "r7c-link-paid");

    await page.goto(portalUrl);
    await page.getByRole("button", { name: "Buy" }).click();
    await expect(page.getByRole("button", { name: "Pay (test)" })).toBeVisible({ timeout: 60_000 });
    await page.getByRole("button", { name: "Pay (test)" }).click();
    // The mock provider settles it: the portal then shows 100 credits.
    await expect(async () => {
      await page.goto(portalUrl);
      await expect(page.locator("body")).toContainText(/Credit balance\s*100\s*credits/);
    }).toPass({ timeout: 90_000 });
    expect(problems.filter((p) => !/favicon/.test(p))).toEqual([]);
  });

  test("sign out and in; a wrong password says so; five wrong passwords lock the person out", async ({ page }) => {
    await signInUi(page, email);
    await page.waitForURL(/\/app(\/apps\/[^/?]+)?(?![^?])/);
    await page.getByRole("link", { name: "Settings" }).click();
    await page.getByRole("button", { name: "Sign out" }).click();
    await page.waitForURL("**/signin");
    await page.goto("/app");
    await page.waitForURL("**/signin");

    const other = await signUp();
    for (let i = 0; i < 5; i++) {
      await signInUi(page, other.email, "wrong password " + i);
      await expect(page.getByRole("alert")).toContainText("Wrong e-mail or password");
    }
    // Locked: even the right password is refused, with the same words (the endpoint does not say which addresses exist).
    await signInUi(page, other.email);
    await expect(page.getByRole("alert")).toContainText("Wrong e-mail or password");
    await expect(page).toHaveURL(/\/signin/);
  });

  test("another person cannot open this app; a person is not an operator; a dead session goes back to sign-in", async ({ page }) => {
    const other = await signUp();
    await signInUi(page, other.email);
    await page.waitForURL(/\/app(\/apps\/[^/?]+)?(?![^?])/);
    await page.goto(`/app/apps/${appId}?env=sandbox`);
    await page.waitForLoadState("networkidle");
    await expect(page.getByRole("main")).not.toContainText("TRY 198.00");
    await expect(page.getByRole("main")).not.toContainText("Seed Shop");
    await page.goto(`/app/t/${sandbox}/customers`);
    await page.waitForLoadState("networkidle");
    await expect(page.getByText("c_ok1")).toHaveCount(0);
    // The API says the same: not found, never forbidden.
    expect((await api("GET", `/app/v1/apps/${appId}`, other.token)).status).toBe(404);
    expect((await api("GET", "/v1/customers", other.token, undefined, { "x-payrudder-tenant": sandbox })).status).toBe(404);
    // A person who is not on the operator list is signed in but not an operator: 403, not 401.
    expect((await api("GET", "/admin/v1/tenants", other.token)).status).toBe(403);

    // The operator console with a person's session: the page sends a non-operator home.
    await page.goto("/tenants");
    await page.waitForURL(/\/app(\/apps\/[^/?]+)?(?![^?])/);
    await expect(page.getByRole("link", { name: "Operator console" })).toHaveCount(0);

    // The session is revoked behind the page's back: the next request ends in the sign-in page with a reason.
    // It is the browser's own session that is revoked (the one the page signed in with), not the API session the test opened for its direct calls.
    const browserToken = await page.evaluate(() => window.sessionStorage.getItem("payrudder.sessionToken") ?? "");
    expect(browserToken).not.toBe("");
    await api("POST", "/auth/signout", browserToken);
    await page.goto("/app");
    await page.waitForURL(/\/signin/);
  });
});
