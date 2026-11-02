import { randomUUID } from "node:crypto";
import { expect, test, type Page } from "@playwright/test";
import { verifyAddress } from "./operator";

// The five complaints of the owner's first list (docs/plans/03-sheets-onboarding-tours.md) against the REAL stack (make up / make dev): a fresh sign-up, the first-run
// wizard and the tour of the home with the server's own record, a second browser that is the same person, a sidebar drawn by the real build, and sheets on a list the
// real API filled. The mocked versions are e2e-mock/complaints.spec.ts and sheet-selects.spec.ts; these exist because the mock cannot say what the server kept.
//
// These run only in CI's end-to-end job (they need the stack and Mailpit). They were written without one and have NOT been run: expect to adjust a locator or a
// wait the first time they do.

const gateway = process.env.PAYRUDDER_GATEWAY_URL ?? "http://localhost:42800";
const password = "correct horse battery";

// The console's default (playwright.config.ts) switches the welcome off; these tests are about the welcome.
test.use({ storageState: { cookies: [], origins: [] } });

type Json = Record<string, unknown>;

async function api(method: string, path: string, token: string, body?: unknown): Promise<{ status: number; body: Json }> {
  for (let attempt = 0; attempt < 8; attempt++) {
    const res = await fetch(gateway + path, { method, headers: { authorization: `Bearer ${token}`, "content-type": "application/json" }, body: body === undefined ? undefined : JSON.stringify(body) });
    if (res.status === 429) {
      await new Promise((r) => setTimeout(r, 1500));
      continue;
    }
    return { status: res.status, body: (await res.json().catch(() => ({}))) as Json };
  }
  throw new Error(`${method} ${path}: still rate limited`);
}

/** A person with a verified address and a session. */
async function signUp(): Promise<{ email: string; token: string }> {
  const email = `cmp-${randomUUID().slice(0, 8)}@example.com`;
  const since = Date.now() - 2000;
  const res = await fetch(gateway + "/auth/signup", { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify({ email, password, display_name: "Complaints", account_name: "Complaints Labs" }) });
  expect(res.status).toBe(202);
  await verifyAddress(email, since);
  const signin = await fetch(gateway + "/auth/signin", { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify({ email, password }) });
  expect(signin.status).toBe(200);
  return { email, token: ((await signin.json()) as { token: string }).token };
}

async function signInUi(page: Page, email: string): Promise<void> {
  await page.goto("/signin");
  await page.getByLabel("E-mail").fill(email);
  await page.getByLabel("Password", { exact: true }).fill(password);
  await page.getByRole("button", { name: "Sign in" }).click();
  await page.waitForURL(/\/app/);
}

/** How far the page is scrolled: the document and every scrolled ancestor of the list, summed. */
const scrolled = (page: Page) =>
  page.evaluate(() => {
    let sum = window.scrollY;
    for (let el: Element | null = document.querySelector("main"); el; el = el.parentElement) sum += (el as HTMLElement).scrollTop || 0;
    return Math.round(sum);
  });

test.describe("the first list's complaints, real stack", () => {
  test.describe.configure({ mode: "serial" });

  test("(5) a fresh sign-up can skip the first-run wizard, is not asked again on a reload or in a second browser, and can resume from the checklist", async ({ page, browser }) => {
    const { email } = await signUp();
    await signInUi(page, email);
    const wizard = page.getByTestId("onboarding");
    await expect(wizard).toBeVisible();
    await expect(page.getByTestId("onb-skip")).toBeVisible();
    await page.getByTestId("onb-skip").click();
    await expect(wizard).toHaveCount(0);
    await expect(page.getByTestId("getting-started")).toBeVisible();

    await page.reload();
    await expect(page.getByTestId("getting-started")).toBeVisible();
    await expect(wizard).toHaveCount(0);

    // The same person in a second browser: the server's record says the wizard was skipped.
    const other = await browser.newContext({ storageState: { cookies: [], origins: [] } });
    const second = await other.newPage();
    await signInUi(second, email);
    await expect(second.getByTestId("getting-started")).toBeVisible();
    await expect(second.getByTestId("onboarding")).toHaveCount(0);
    await other.close();

    await page.getByTestId("getting-started-resume").click();
    await expect(wizard).toBeVisible();
  });

  test("(6) the tour of the home starts once for a person with an app: not after leaving the page, a reload, or in a second browser", async ({ page, browser }) => {
    const { email, token } = await signUp();
    expect((await api("POST", "/app/v1/apps", token, { name: "Tour Shop", currency: "TRY", timezone: "Europe/Istanbul" })).status).toBe(201);
    await signInUi(page, email);
    await expect(page.getByTestId("tour-card")).toBeVisible();
    // Neither finished nor skipped: the person goes to another page.
    await page.goto("/app/members");
    await page.goto("/app");
    await expect(page.getByTestId("app-switcher")).toBeVisible();
    await expect(page.getByTestId("tour-card")).toHaveCount(0);
    await page.reload();
    await expect(page.getByTestId("tour-card")).toHaveCount(0);

    const other = await browser.newContext({ storageState: { cookies: [], origins: [] } });
    const second = await other.newPage();
    await signInUi(second, email);
    await expect(second.getByTestId("app-switcher")).toBeVisible();
    // The server answers before a tour may start by itself: give it the time a tour would need to appear.
    await second.waitForTimeout(3000);
    await expect(second.getByTestId("tour-card")).toHaveCount(0);
    await other.close();
  });

  test("(7, 9, 13) the sidebar's icons differ; a sheet opened on a long list leaves the page where it was and its lists work; a sheet holds no link to another page", async ({ page }) => {
    const { email, token } = await signUp();
    const app = (await api("POST", "/app/v1/apps", token, { name: "Sheet Shop", currency: "TRY", timezone: "Europe/Istanbul" })).body as { id: string; tenants: { id: string; environment: string }[] };
    const sandbox = app.tenants.find((t) => t.environment === "ENVIRONMENT_SANDBOX")?.id ?? "";
    const key = ((await api("POST", `/app/v1/apps/${app.id}/api-keys`, token, { environment: "sandbox" })).body as { plaintext: string }).plaintext;
    for (let i = 0; i < 40; i++) {
      const id = `cus_${String(i).padStart(2, "0")}`;
      expect((await api("POST", "/v1/customers", key, { external_id: id, name: `Customer ${id}`, email: `${id}@example.com`, billing_currency: "TRY", country: "TR" })).status).toBe(201);
    }
    await page.addInitScript(() => localStorage.setItem("payrudder.noWelcome", "1"));
    await signInUi(page, email);

    // (7) No two links of the sidebar draw the same icon.
    const icons = await page.getByTestId("sidebar-nav").getByRole("link").evaluateAll((links) =>
      links.map((a) => ({ name: (a.textContent ?? "").trim(), icon: [...(a.querySelector("svg")?.classList ?? [])].find((c) => c.startsWith("lucide-") && c !== "lucide") ?? "" })),
    );
    const names = new Map<string, string[]>();
    for (const i of icons) names.set(i.icon, [...(names.get(i.icon) ?? []), i.name]);
    expect([...names.values()].filter((n) => n.length > 1)).toEqual([]);

    // (9) A sheet on a scrolled list.
    await page.goto(`/app/t/${sandbox}/customers`);
    const row = page.getByTestId("customer-cus_09");
    await row.waitFor();
    await row.scrollIntoViewIfNeeded();
    await page.mouse.wheel(0, 300);
    await expect.poll(() => scrolled(page)).toBeGreaterThan(100);
    await page.waitForTimeout(800);
    const before = await scrolled(page);
    await row.dispatchEvent("click");
    const sheet = page.getByRole("dialog").first();
    await expect(sheet).toBeVisible();
    expect(Math.abs((await scrolled(page)) - before)).toBeLessThanOrEqual(12);

    // (13) What the sheet links to stays on the page, apart from a link that says it is one.
    const here = new URL(page.url()).origin;
    const links = await sheet.locator("a[href]").evaluateAll((as) => as.map((a) => ({ href: (a as HTMLAnchorElement).href, target: a.getAttribute("target"), testid: a.getAttribute("data-testid") ?? "" })));
    expect(links.filter((l) => new URL(l.href).origin === here && l.target !== "_blank" && !l.href.includes("sheet=") && !/open-page$/.test(l.testid))).toEqual([]);
    await page.keyboard.press("Escape");
    await expect(page.getByRole("dialog")).toHaveCount(0);
    expect(Math.abs((await scrolled(page)) - before)).toBeLessThanOrEqual(12);

    // (9) The searchable lists of a form sheet: open, keyboard, a pick, Escape closes the list and not the sheet.
    await page.getByTestId("customers-new").dispatchEvent("click");
    const form = page.getByRole("dialog").first();
    await expect(form).toBeVisible();
    const field = form.locator('input[role="combobox"]').first();
    await field.click();
    await expect(field).toHaveAttribute("aria-expanded", "true");
    await page.keyboard.press("ArrowDown");
    await page.keyboard.press("Enter");
    await expect(field).toHaveAttribute("aria-expanded", "false");
    await field.click();
    await page.keyboard.press("Escape");
    await expect(form).toBeVisible();
    await expect.poll(() => page.evaluate(() => [document.documentElement.style.overflow, document.body.style.overflow])).toEqual(["hidden", ""]);
  });
});
