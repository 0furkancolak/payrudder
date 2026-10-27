import { expect, test, type Page } from "@playwright/test";
import { baseTenant, MockGateway, signInAsOperator } from "./gateway";
import { productGateway, signInAsAda } from "./product";
import { pickCurrency } from "./ui";
import { INVOICE, PLATFORM, saasGateway, type SaasState } from "./saas";

// Payrudder as a SaaS (phase 55): the plan page of an organisation (plan, quotas, upgrade and downgrade, invoices, the portal), the upgrade prompt, and the
// operator's organisations with the manual override. The gateway is mocked.

/** window.open is recorded instead of opening a tab. */
async function recordOpen(page: Page): Promise<() => Promise<string | null>> {
  await page.addInitScript(() => {
    (window as unknown as { __opened: string | null }).__opened = null;
    window.open = (u?: string | URL) => {
      (window as unknown as { __opened: string | null }).__opened = String(u);
      return null;
    };
  });
  return () => page.evaluate(() => (window as unknown as { __opened: string | null }).__opened);
}

async function openPlan(page: Page, saas: Partial<SaasState> = {}, role = "owner") {
  const { gw, state } = saasGateway(productGateway(new MockGateway(), { role }).gw, { role, ...saas });
  await gw.install(page);
  await signInAsAda(page);
  await page.goto("/app/settings/plan");
  return { gw, state };
}

test.describe("plan and billing", () => {
  test("shows the plan, the usage against the quotas and the plans on offer, and the menu has the page", async ({ page }) => {
    await openPlan(page);
    await expect(page.getByTestId("plan-name")).toHaveText("Free");
    await expect(page.getByTestId("plan-state")).toHaveText("Active");
    const events = page.getByTestId("quota-events");
    await expect(events).toContainText("9,100 / 10,000");
    await expect(events.getByRole("progressbar")).toHaveAttribute("aria-valuenow", "91");
    await expect(page.getByTestId("quota-apps")).toContainText("1 / 1");
    await expect(page.getByTestId("offer-pro")).toContainText("$29.00 per month");
    await expect(page.getByTestId("offer-scale")).toContainText("Unlimited");
    await expect(page.getByTestId("offer-free").getByRole("button")).toHaveCount(0);
    const nav = page.getByRole("navigation", { name: "Main navigation" });
    await expect(nav.getByRole("link", { name: "Plan and billing" })).toHaveAttribute("aria-current", "page");
  });

  test("an upgrade to a paid plan asks for confirmation and starts its trial", async ({ page }) => {
    const { gw } = await openPlan(page);
    await page.getByTestId("choose-pro").click();
    await expect(page.getByRole("dialog")).toContainText("Move to Pro?");
    await page.getByTestId("plan-confirm").click();
    await expect(page.getByTestId("plan-name")).toHaveText("Pro");
    await expect(page.getByTestId("plan-state")).toHaveText("Trial");
    await expect(page.getByTestId("plan-current")).toContainText("trial ends");
    expect(gw.called("PUT", "/app/v1/account/plan")[0]?.body).toEqual({ plan_code: "pro" });
  });

  test("a downgrade that the usage does not fit is refused in the reader's language", async ({ page }) => {
    await openPlan(page, { plan: "pro", used: { events: 10, apps: 3, members: 2, api_keys: 3 } });
    await page.getByTestId("choose-free").click();
    await page.getByTestId("plan-confirm").click();
    await expect(page.getByRole("dialog").getByRole("alert")).toContainText("Your usage does not fit that plan");
    await expect(page.getByTestId("plan-name")).toHaveText("Pro");
  });

  test("an unpaid invoice is paid through a payment link, and the portal opens", async ({ page }) => {
    const opened = await recordOpen(page);
    const { gw } = await openPlan(page);
    await expect(page.getByTestId("plan-invoices")).toContainText("INV-2026-000010");
    await page.getByTestId(`pay-${INVOICE}`).click();
    await expect.poll(opened).toBe("https://pay.example.test/i/tok_plan");
    expect(gw.called("POST", `/app/v1/account/plan/invoices/${INVOICE}/pay-link`)).toHaveLength(1);
    await page.getByTestId("plan-portal").click();
    await expect.poll(opened).toBe("https://pay.example.test/c/tok_portal");
  });

  test("a viewer sees the plan but cannot change it or pay", async ({ page }) => {
    await openPlan(page, {}, "viewer");
    await expect(page.getByTestId("plan-name")).toHaveText("Free");
    await expect(page.getByTestId("choose-pro")).toHaveCount(0);
    await expect(page.getByTestId("plan-portal")).toHaveCount(0);
    await expect(page.getByText("Only an owner or an administrator can change the plan.")).toBeVisible();
  });

  test("where SaaS is off the page says so and the menu has no entry", async ({ page }) => {
    await openPlan(page, { enabled: false });
    await expect(page.getByTestId("plan-disabled")).toBeVisible();
    await expect(page.getByRole("navigation", { name: "Main navigation" }).getByRole("link", { name: "Plan and billing" })).toHaveCount(0);
    await page.goto("/app/settings");
    await expect(page.getByTestId("plan-link")).toHaveCount(0);
  });

  test("the settings page links to the plan page", async ({ page }) => {
    await openPlan(page);
    await page.goto("/app/settings");
    await page.getByTestId("plan-link").click();
    await page.waitForURL("**/app/settings/plan");
  });
});

test.describe("the operator", () => {
  test("lists the organisations with plan and usage and overrides one", async ({ page }) => {
    const { gw } = saasGateway(baseTenant(new MockGateway()));
    await gw.install(page);
    await signInAsOperator(page);
    const nav = page.getByRole("navigation", { name: "Main navigation" });
    await expect(nav.getByRole("link", { name: "Platform plans" })).toHaveAttribute("href", `/tenants/${PLATFORM}/packages`);
    await nav.getByRole("link", { name: "Organisations" }).click();
    await page.waitForURL("**/organisations");
    const row = page.getByRole("row", { name: /Ada Labs/ });
    await expect(row).toContainText("Free");
    await expect(row).toContainText("9,100 / 10,000");

    await row.getByRole("button", { name: "Manage" }).click();
    const sheet = page.getByRole("dialog");
    await sheet.getByLabel("Plan").selectOption("pro");
    await sheet.getByLabel("Events").fill("50000");
    await sheet.getByLabel("Apps").fill("x");
    await page.getByTestId("org-save").click();
    await expect(sheet).toContainText("A limit is a whole number from 0 up.");
    await sheet.getByLabel("Apps").fill("");
    await page.getByTestId("org-save").click();
    await expect(page.getByRole("row", { name: /Ada Labs/ })).toContainText("Custom limits");
    await expect(page.getByRole("row", { name: /Ada Labs/ })).toContainText("Pro");
    expect(gw.called("PUT", /\/admin\/v1\/organizations\/.+\/plan/)[0]?.body).toEqual({ plan_code: "pro", limits: { events: 50000 } });

    await page.getByRole("row", { name: /Ada Labs/ }).getByRole("button", { name: "Manage" }).click();
    await page.getByTestId("org-clear").click();
    await expect(page.getByRole("row", { name: /Ada Labs/ })).not.toContainText("Custom limits");
    expect(gw.called("DELETE", /\/overrides$/)).toHaveLength(1);
  });

  test("searches by name", async ({ page }) => {
    const { gw } = saasGateway(baseTenant(new MockGateway()));
    await gw.install(page);
    await signInAsOperator(page);
    await page.goto("/organisations");
    await page.getByLabel("Search by name or owner address").fill("nobody");
    await page.getByRole("button", { name: "Search" }).click();
    await expect(page.getByText("No organisations found.")).toBeVisible();
  });
});

test.describe("the upgrade prompt", () => {
  test("a refused create offers the plan page", async ({ page }) => {
    const { gw } = saasGateway(productGateway(new MockGateway()).gw);
    gw.on((c) => (c.method === "POST" && c.path === "/app/v1/apps" ? { status: 429, body: { error: { code: "RESOURCE_EXHAUSTED", key: "plans.saas.quota.apps", message: "limit" } } } : undefined));
    await gw.install(page);
    await signInAsAda(page);
    await page.getByRole("button", { name: "Create app" }).first().click();
    const dialog = page.getByRole("dialog");
    await dialog.getByLabel("App name").fill("Another");
    await pickCurrency(dialog.getByLabel("Currency"), "EUR");
    await dialog.getByRole("button", { name: "Create app" }).click();
    await expect(page.getByTestId("upgrade-hint")).toBeVisible();
    await expect(page.getByText("Your plan's limit of apps is reached. Upgrade to add more.").first()).toBeVisible();
    await page.getByTestId("upgrade-hint").getByRole("link", { name: "Upgrade your plan" }).click();
    await page.waitForURL("**/app/settings/plan");
  });
});
