import { expect, test } from "@playwright/test";
import { baseTenant, MockGateway, openTenantPage } from "./gateway";
import { APP, appWithLive, liveGateway, productGateway, signInAsAda } from "./product";

// Findings of the UI test pass: the Integrations page must not end the session (UI-1, UI-2), the logo keeps the environment (UI-4), unknown ids and dead
// sheets answer clearly (UI-6) and every page names itself in the tab's title (UI-16).

test.describe("session and navigation fixes", () => {
  test("the integrations page does not call the tenant API before a sheet needs it, and a 401 that is not a dead session keeps the person signed in", async ({ page }) => {
    const { gw } = productGateway(new MockGateway(), { apps: [appWithLive("ACTIVE")] });
    liveGateway(gw, {});
    gw.on((c) => (c.path === "/v1/mail-settings" ? { status: 401, body: { error: { code: "UNAUTHENTICATED", key: "identity.session.act_as_required", message: "tenant required" } } } : undefined));
    await gw.install(page);
    await signInAsAda(page);
    await page.goto(`/app/apps/${APP}/integrations?env=live`);
    await expect(page.getByTestId("integrations-screen")).toBeVisible();
    await page.waitForTimeout(500);
    expect(gw.called("GET", "/v1/mail-settings")).toHaveLength(0);
    await expect(page).not.toHaveURL(/signin/);
    // Opening the mail card asks (with the tenant); the refusal is shown, not taken for a lost session.
    await page.getByTestId("integration-open-payrudderMail").click();
    await expect.poll(() => gw.called("GET", "/v1/mail-settings").length).toBeGreaterThan(0);
    await expect(page).not.toHaveURL(/signin/);
  });

  test("the logo link keeps the environment the person last used in the app", async ({ page }) => {
    const { gw } = productGateway(new MockGateway(), { apps: [appWithLive("ACTIVE")] });
    liveGateway(gw, {});
    await gw.install(page);
    await signInAsAda(page);
    await page.goto(`/app/apps/${APP}?env=live`);
    await expect(page.getByTestId("sidebar")).toBeVisible();
    await page.getByTestId("sidebar").getByRole("link").first().click();
    await expect(page).toHaveURL(/env=live/);
    await expect(page).not.toHaveURL(/env=sandbox/);
  });

  test("an unknown app or tenant says not found; the page title names the page", async ({ page }) => {
    const { gw } = productGateway(new MockGateway(), { apps: [appWithLive("ACTIVE")] });
    liveGateway(gw, {});
    await gw.install(page);
    await signInAsAda(page);
    await page.goto("/app/apps/not-a-real-app");
    await expect(page.getByTestId("not-found")).toBeVisible();
    await page.goto("/app/t/does-not-exist/customers");
    await expect(page.getByTestId("not-found")).toBeVisible();
    await page.goto(`/app/apps/${APP}/integrations?env=live`);
    await expect(page.getByTestId("integrations-screen")).toBeVisible();
    await expect(page).toHaveTitle(/^Integrations · Payrudder$/);
    expect((await page.title()).toLowerCase()).not.toContain("operator");
  });

  test("a sheet that names no customer is taken off the address", async ({ page }) => {
    await baseTenant(new MockGateway())
      .on((c) => (c.path === "/v1/customers" ? { body: { customers: [{ id: "id-1", external_id: "cus_1", name: "Ada", email: "ada@example.com", create_time: "2026-09-01T10:00:00Z" }] } } : undefined))
      .install(page);
    await openTenantPage(page, "/customers?sheet=customer:nope");
    await expect(page.getByTestId("customer-cus_1")).toBeVisible();
    await expect(page).not.toHaveURL(/sheet=/);
  });
});
