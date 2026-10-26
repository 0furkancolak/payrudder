import { expect, test } from "@playwright/test";
import { MockGateway } from "./gateway";
import { APP, app, appWithLive, liveGateway, productGateway, signInAsAda, tenant } from "./product";

// The payment providers screen: a card per provider, a side sheet that connects it (for one app or for every app of the organisation), a test and a
// disconnect. The gateway is mocked; the real keys are never typed anywhere but the sheet and never shown again.

const APP2 = "b0000000-0000-4000-8000-000000000002";
const app2 = { ...app, id: APP2, name: "Mobile game", slug: "mobile-game", tenants: [tenant("b1000000-0000-4000-8000-000000000001", "LIVE"), tenant("b1000000-0000-4000-8000-000000000002", "SANDBOX")] };

/** The second app's live providers: what it holds, and the keys it was sent. */
function secondApp(gw: MockGateway, refuse = false) {
  const accounts: { provider: string; default: boolean; working: boolean; checked: boolean }[] = [];
  gw.on((c) => {
    const base = `/app/v1/apps/${APP2}/go-live/providers`;
    if (c.path === base && c.method === "GET") return { body: { ready: accounts.length > 0, providers: accounts.map((a) => a.provider), accounts } };
    if (c.path.startsWith(`${base}/`) && c.method === "PUT") {
      if (refuse) return { status: 400, body: { error: { code: "INVALID_ARGUMENT", key: "payments.account.health_check_failed", message: "refused" } } };
      accounts.push({ provider: c.path.split("/").pop() ?? "", default: accounts.length === 0, working: true, checked: true });
      return { body: {} };
    }
    if (c.path.startsWith(`${base}/`) && c.method === "DELETE") {
      accounts.splice(0, accounts.length);
      return { status: 204, body: {} };
    }
    return undefined;
  });
  return accounts;
}

const keys = async (sheet: import("@playwright/test").Locator) => {
  await sheet.getByTestId("lp-api_key").fill("pdl_live_SECRETKEY");
  await sheet.getByTestId("lp-webhook_secret").fill("pdl_ntfset_SECRETHOOK");
  await sheet.getByTestId("lp-usage_product_id").fill("pro_01abc");
};

test.describe("payment providers", () => {
  test("an app's screen shows a card per provider, connects one for this app only, tests it and removes it", async ({ page }) => {
    const { gw } = productGateway(new MockGateway(), { apps: [appWithLive("ACTIVE")] });
    liveGateway(gw, { accounts: [{ provider: "iyzico", default: true, working: false, checked: true, problem: "refused" }] });
    await gw.install(page);
    await signInAsAda(page);
    await page.goto(`/app/apps/${APP}/providers?env=live`);
    await expect(page.getByTestId("provider-card-iyzico")).toHaveAttribute("data-state", "problem");
    await expect(page.getByTestId("provider-card-iyzico")).toContainText("does not accept these keys any more");
    await expect(page.getByTestId("provider-card-paddle")).toHaveAttribute("data-state", "none");
    // The sandbox is the built-in simulator, with nothing to connect.
    await expect(page.getByTestId("provider-card-paddle")).toContainText("Built-in simulator");
    // One app: no organisation-wide choice is offered.
    await page.getByTestId("provider-open-paddle").click();
    const sheet = page.getByTestId("provider-sheet");
    await expect(sheet.getByTestId("provider-scope")).toHaveCount(0);
    await keys(sheet);
    await sheet.getByTestId("provider-connect").click();
    await expect(page.getByTestId("provider-connected")).toContainText("Paddle connected on 1 app");
    expect(gw.called("PUT", `/app/v1/apps/${APP}/go-live/providers/paddle`)).toHaveLength(1);
    await expect(page.getByTestId("provider-card-paddle")).toHaveAttribute("data-state", "working");
    expect(await page.content()).not.toContain("SECRETKEY");
    // Testing asks the provider again and says what it answered; disconnecting asks first.
    await page.getByTestId("provider-open-paddle").click();
    await sheet.getByTestId("provider-test").click();
    await expect(sheet.getByTestId("provider-test-result")).toContainText("accepts these keys");
    page.once("dialog", (d) => void d.accept());
    await sheet.getByTestId("provider-disconnect").click();
    await expect(page.getByTestId("provider-card-paddle")).toHaveAttribute("data-state", "none");
    expect(gw.called("DELETE", `/app/v1/apps/${APP}/go-live/providers/paddle`)).toHaveLength(1);
  });

  test("the organisation's screen connects a provider on every app with a live environment, and one app can differ", async ({ page }) => {
    const { gw } = productGateway(new MockGateway(), { apps: [appWithLive("ACTIVE"), app2] });
    liveGateway(gw, {});
    const second = secondApp(gw);
    await gw.install(page);
    await signInAsAda(page);
    await page.goto("/app/providers");
    await expect(page.getByTestId("providers-apps")).toContainText("Web shop");
    await expect(page.getByTestId("providers-apps")).toContainText("Mobile game");
    await page.getByTestId("provider-open-paddle").click();
    const sheet = page.getByTestId("provider-sheet");
    await expect(sheet.getByTestId("provider-scope-note")).toContainText("all 2 apps");
    await keys(sheet);
    await sheet.getByTestId("provider-connect").click();
    await expect(page.getByTestId("provider-connected")).toContainText("Paddle connected on 2 app");
    expect(gw.called("PUT", `/app/v1/apps/${APP}/go-live/providers/paddle`)).toHaveLength(1);
    expect(gw.called("PUT", `/app/v1/apps/${APP2}/go-live/providers/paddle`)[0]?.body).toEqual({
      api_key: "pdl_live_SECRETKEY", webhook_secret: "pdl_ntfset_SECRETHOOK", usage_product_id: "pro_01abc",
    });
    expect(second).toHaveLength(1);
    await expect(page.getByTestId("provider-card-paddle")).toContainText("Connected on 2 of 2 apps");
    // Overriding one app: its cell opens the sheet for that app alone, where it can be disconnected without touching the other.
    await page.getByTestId(`providers-cell-${APP2}-paddle`).click();
    await expect(sheet.getByTestId("provider-scope")).toBeVisible();
    await expect(sheet.getByTestId("provider-scope-note")).toContainText("Only this app");
    page.once("dialog", (d) => void d.accept());
    await sheet.getByTestId("provider-disconnect").click();
    expect(gw.called("DELETE", `/app/v1/apps/${APP2}/go-live/providers/paddle`)).toHaveLength(1);
    expect(gw.called("DELETE", `/app/v1/apps/${APP}/go-live/providers/paddle`)).toHaveLength(0);
    await expect(page.getByTestId("provider-card-paddle")).toContainText("Connected on 1 of 2 apps");
  });

  test("when one app refuses the keys the others still connect, and the refusal names the app", async ({ page }) => {
    const { gw } = productGateway(new MockGateway(), { apps: [appWithLive("ACTIVE"), app2] });
    liveGateway(gw, {});
    secondApp(gw, true);
    await gw.install(page);
    await signInAsAda(page);
    await page.goto("/app/providers");
    await page.getByTestId("provider-open-paddle").click();
    const sheet = page.getByTestId("provider-sheet");
    await keys(sheet);
    await sheet.getByTestId("provider-connect").click();
    await expect(sheet.getByTestId("provider-failures")).toContainText("Mobile game: The provider did not accept these credentials");
    await expect(page.getByTestId("provider-connected")).toContainText("1 app(s) connected, 1 failed");
    // What was typed stays for another try.
    await expect(sheet.getByTestId("lp-api_key")).toHaveValue("pdl_live_SECRETKEY");
  });

  test("somebody who is not the owner sees the state but cannot connect", async ({ page }) => {
    const { gw } = productGateway(new MockGateway(), { apps: [appWithLive("ACTIVE")], role: "viewer" });
    liveGateway(gw, { accounts: [{ provider: "paddle", default: true, working: true, checked: true }] });
    await gw.install(page);
    await signInAsAda(page);
    await page.goto(`/app/apps/${APP}/providers?env=live`);
    await expect(page.getByTestId("providers-owner-only")).toBeVisible();
    await expect(page.getByTestId("provider-card-paddle")).toHaveAttribute("data-state", "working");
    await expect(page.getByTestId("provider-open-paddle")).toBeDisabled();
  });
});
