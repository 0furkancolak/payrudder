import { expect, test } from "@playwright/test";
import { MockGateway } from "./gateway";
import { APP, app, appWithLive, liveGateway, productGateway, signInAsAda, tenant } from "./product";

// The integrations screen (it replaced "Payment providers"): three groups, payments, e-invoice and e-mail, with a card per integration, a side sheet that connects it (for one app or for every app of the organisation), a test and a
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

test.describe("payment integrations", () => {
  test("an app's screen shows a card per provider, connects one for this app only, tests it and removes it", async ({ page }) => {
    const { gw } = productGateway(new MockGateway(), { apps: [appWithLive("ACTIVE")] });
    liveGateway(gw, { accounts: [{ provider: "iyzico", default: true, working: false, checked: true, problem: "refused" }] });
    await gw.install(page);
    await signInAsAda(page);
    await page.goto(`/app/apps/${APP}/integrations?env=live`);
    await expect(page.getByTestId("integration-card-iyzico")).toHaveAttribute("data-state", "problem");
    await expect(page.getByTestId("integration-card-iyzico")).toContainText("does not accept these keys any more");
    await expect(page.getByTestId("integration-card-paddle")).toHaveAttribute("data-state", "none");
    // The sandbox is the built-in simulator, with nothing to connect.
    await expect(page.getByTestId("integration-card-paddle")).toContainText("Built-in simulator");
    // One app: no organisation-wide choice is offered.
    await page.getByTestId("integration-open-paddle").click();
    const sheet = page.getByTestId("provider-sheet");
    await expect(sheet.getByTestId("provider-scope")).toHaveCount(0);
    await keys(sheet);
    await sheet.getByTestId("provider-connect").click();
    await expect(page.getByTestId("provider-connected")).toContainText("Paddle connected on 1 app");
    expect(gw.called("PUT", `/app/v1/apps/${APP}/go-live/providers/paddle`)).toHaveLength(1);
    await expect(page.getByTestId("integration-card-paddle")).toHaveAttribute("data-state", "working");
    expect(await page.content()).not.toContain("SECRETKEY");
    // Testing asks the provider again and says what it answered; disconnecting asks first.
    await page.getByTestId("integration-open-paddle").click();
    await sheet.getByTestId("provider-test").click();
    await expect(sheet.getByTestId("provider-test-result")).toContainText("accepts these keys");
    page.once("dialog", (d) => void d.accept());
    await sheet.getByTestId("provider-disconnect").click();
    await expect(page.getByTestId("integration-card-paddle")).toHaveAttribute("data-state", "none");
    expect(gw.called("DELETE", `/app/v1/apps/${APP}/go-live/providers/paddle`)).toHaveLength(1);
  });

  test("the organisation's screen connects a provider on every app with a live environment, and one app can differ", async ({ page }) => {
    const { gw } = productGateway(new MockGateway(), { apps: [appWithLive("ACTIVE"), app2] });
    liveGateway(gw, {});
    const second = secondApp(gw);
    await gw.install(page);
    await signInAsAda(page);
    await page.goto("/app/integrations");
    await expect(page.getByTestId("integrations-apps")).toContainText("Web shop");
    await expect(page.getByTestId("integrations-apps")).toContainText("Mobile game");
    await page.getByTestId("integration-open-paddle").click();
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
    await expect(page.getByTestId("integration-card-paddle")).toContainText("Connected on 2 of 2 apps");
    // Overriding one app: its cell opens the sheet for that app alone, where it can be disconnected without touching the other.
    await page.getByTestId(`integrations-cell-${APP2}-paddle`).click();
    await expect(sheet.getByTestId("provider-scope")).toBeVisible();
    await expect(sheet.getByTestId("provider-scope-note")).toContainText("Only this app");
    page.once("dialog", (d) => void d.accept());
    await sheet.getByTestId("provider-disconnect").click();
    expect(gw.called("DELETE", `/app/v1/apps/${APP2}/go-live/providers/paddle`)).toHaveLength(1);
    expect(gw.called("DELETE", `/app/v1/apps/${APP}/go-live/providers/paddle`)).toHaveLength(0);
    await expect(page.getByTestId("integration-card-paddle")).toContainText("Connected on 1 of 2 apps");
  });

  test("when one app refuses the keys the others still connect, and the refusal names the app", async ({ page }) => {
    const { gw } = productGateway(new MockGateway(), { apps: [appWithLive("ACTIVE"), app2] });
    liveGateway(gw, {});
    secondApp(gw, true);
    await gw.install(page);
    await signInAsAda(page);
    await page.goto("/app/integrations");
    await page.getByTestId("integration-open-paddle").click();
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
    await page.goto(`/app/apps/${APP}/integrations?env=live`);
    await expect(page.getByTestId("integrations-owner-only")).toBeVisible();
    await expect(page.getByTestId("integration-card-paddle")).toHaveAttribute("data-state", "working");
    await expect(page.getByTestId("integration-open-paddle")).toBeDisabled();
  });
});

/** What the e-invoice and e-mail integrations of the live environment hold, and what they were sent. */
function extras(gw: MockGateway, initial: { account?: { provider: string; mode: string } | null; mail?: Record<string, unknown> | null } = {}) {
  const state = { account: initial.account ?? null, mail: initial.mail ?? null, categories: { invoice_issued: true, payment_receipts: true, payment_failed: true, usage_alerts: true, system: true } as Record<string, boolean> };
  const eBase = `/app/v1/apps/${APP}/integrations/e-invoice`;
  const mBase = `/app/v1/apps/${APP}/integrations/mail`;
  gw.on((c) => {
    if (c.path === eBase && c.method === "GET") return { body: { account: state.account } };
    if (c.path === eBase && c.method === "PUT") {
      state.account = { provider: String(c.body?.provider), mode: String(c.body?.mode ?? "production") };
      return { body: { provider: state.account.provider, mode: state.account.mode, has_credentials: true } };
    }
    if (c.path === eBase && c.method === "DELETE") {
      state.account = null;
      return { status: 204, body: {} };
    }
    if (c.path === `${eBase}/test`) return { body: { ok: true, checked: "credentials" } };
    if (c.path === mBase && c.method === "GET") return { body: state.mail ? { connected: true, integration: state.mail } : { connected: false } };
    if (c.path === mBase && c.method === "PUT") {
      if (c.body?.api_key === "refused") return { status: 400, body: { error: { code: "INVALID_ARGUMENT", key: "plans.mail_integration.refused", message: "refused" } } };
      state.mail = { provider: "nafru", base_url: c.body?.base_url, from: c.body?.from, key_hint: String(c.body?.api_key ?? "").slice(-4), update_time: "2026-10-21T10:00:00Z", checked_time: "2026-10-21T10:00:00Z", check_problem: "" };
      return { body: { integration: state.mail } };
    }
    if (c.path === mBase && c.method === "DELETE") {
      state.mail = null;
      return { status: 204, body: {} };
    }
    if (c.path === `${mBase}/test`) return { body: { ok: true, message_id: "em_1" } };
    if (c.path === "/v1/mail-settings" && c.method === "GET") return { body: { categories: Object.entries(state.categories).map(([category, enabled]) => ({ category, enabled })) } };
    if (c.path === "/v1/mail-settings" && c.method === "PUT") {
      for (const x of (c.body?.categories as { category: string; enabled: boolean }[]) ?? []) state.categories[x.category] = x.enabled;
      return { body: { categories: Object.entries(state.categories).map(([category, enabled]) => ({ category, enabled })) } };
    }
    return undefined;
  });
  return state;
}

test.describe("integrations: groups, marks and the other integrations", () => {
  test("the three groups are there, a provider with a licensed mark shows it and one without shows only its name", async ({ page }) => {
    const { gw } = productGateway(new MockGateway(), { apps: [appWithLive("ACTIVE")] });
    liveGateway(gw, {});
    extras(gw);
    await gw.install(page);
    await signInAsAda(page);
    await page.goto(`/app/apps/${APP}/integrations?env=live`);
    await expect(page.getByRole("heading", { name: "Integrations" })).toBeVisible();
    for (const g of ["payments", "einvoice", "email"]) await expect(page.getByTestId(`integration-group-${g}`)).toBeVisible();
    await expect(page.getByTestId("integration-group-payments")).toContainText("Payments");
    await expect(page.getByTestId("integration-group-einvoice")).toContainText("E-invoice");
    await expect(page.getByTestId("integration-group-email")).toContainText("E-mail");
    // Paddle and Nafru Email have a licensed mark (light and dark files); iyzico and Nilvera are shown by name, with no tile in a logo's place.
    await expect(page.getByTestId("integration-card-paddle").locator('img[src="/brand/providers/paddle-light.svg"]')).toHaveCount(1);
    await expect(page.getByTestId("integration-card-paddle").locator('img[src="/brand/providers/paddle-dark.svg"]')).toHaveCount(1);
    await expect(page.getByTestId("integration-card-nafru").locator("img")).toHaveCount(1);
    for (const id of ["iyzico", "nilvera"]) {
      await expect(page.getByTestId(`integration-card-${id}`).locator("img")).toHaveCount(0);
      await expect(page.getByTestId(`integration-card-${id}`).locator("[data-icon], [data-logo]")).toHaveCount(0);
      await expect(page.getByTestId(`integration-name-${id}`)).toHaveText(id === "iyzico" ? "iyzico" : "Nilvera");
    }
    // Payrudder's own mail is on; the sandbox of every group names what it runs on.
    await expect(page.getByTestId("integration-state-payrudderMail")).toHaveText("Always on");
    await expect(page.getByTestId("integration-card-simulator")).toContainText("Built-in simulator");
    await expect(page.getByTestId("integration-state-nafru")).toHaveText("Not connected");
  });

  test("the old payment provider addresses lead to the integrations", async ({ page }) => {
    const { gw } = productGateway(new MockGateway(), { apps: [appWithLive("ACTIVE")] });
    liveGateway(gw, {});
    extras(gw);
    await gw.install(page);
    await signInAsAda(page);
    await page.goto(`/app/apps/${APP}/providers?env=live`);
    await expect(page).toHaveURL(new RegExp(`/app/apps/${APP}/integrations\\?env=live`));
    await expect(page.getByTestId("integrations-screen")).toBeVisible();
    await page.goto("/app/providers");
    await expect(page).toHaveURL(/\/app\/integrations$/);
    await expect(page.getByTestId("integrations-screen")).toBeVisible();
    // The sidebar names it, and its link stays in the environment the person is in.
    await page.goto(`/app/apps/${APP}?env=live`);
    await page.getByRole("link", { name: "Integrations" }).first().click();
    await expect(page).toHaveURL(new RegExp(`/app/apps/${APP}/integrations`));
  });

  test("an e-invoice integrator is connected, tested, replaced and disconnected; the credentials never come back", async ({ page }) => {
    const { gw } = productGateway(new MockGateway(), { apps: [appWithLive("ACTIVE")] });
    liveGateway(gw, {});
    const state = extras(gw);
    await gw.install(page);
    await signInAsAda(page);
    await page.goto(`/app/apps/${APP}/integrations?env=live`);
    await expect(page.getByTestId("integration-state-nilvera")).toHaveText("Not connected");
    await page.getByTestId("integration-open-nilvera").click();
    const sheet = page.getByTestId("einvoice-sheet");
    await sheet.getByTestId("ei-api-key").fill("NV-SECRET-KEY");
    await sheet.getByTestId("ei-mode").selectOption("test");
    await sheet.getByTestId("integration-connect").click();
    expect(gw.called("PUT", `/app/v1/apps/${APP}/integrations/e-invoice`)[0]?.body).toEqual({ provider: "nilvera", mode: "test", credentials: { api_key: "NV-SECRET-KEY" } });
    await expect(page.getByTestId("integration-state-nilvera")).toHaveText("Connected and working");
    await expect(page.getByTestId("integration-card-nilvera")).toContainText("Test mode");
    await expect(page.getByTestId("integration-state-webhook")).toHaveText("Not connected");
    expect(await page.content()).not.toContain("NV-SECRET-KEY");

    // Open again: it says what is stored, tests it, and warns that the other integrator would replace it.
    await page.getByTestId("integration-open-nilvera").click();
    await sheet.getByTestId("integration-test").click();
    await expect(sheet.getByTestId("integration-test-result")).toContainText("accepts these credentials");
    await expect(sheet.getByTestId("ei-api-key")).toHaveValue("");
    await sheet.getByRole("button", { name: "Cancel" }).click();
    await page.getByTestId("integration-open-webhook").click();
    await expect(page.getByTestId("einvoice-replaces")).toContainText("nilvera");
    await page.getByTestId("einvoice-sheet").getByRole("button", { name: "Cancel" }).click();

    await page.getByTestId("integration-open-nilvera").click();
    page.once("dialog", (d) => void d.accept());
    await sheet.getByTestId("integration-disconnect").click();
    await expect(page.getByTestId("integration-state-nilvera")).toHaveText("Not connected");
    expect(gw.called("DELETE", `/app/v1/apps/${APP}/integrations/e-invoice`)).toHaveLength(1);
    expect(state.account).toBeNull();
  });

  test("Nafru Email is connected with an address and a key, tested with a mail, and disconnected; Payrudder's mail has its switches", async ({ page }) => {
    const { gw } = productGateway(new MockGateway(), { apps: [appWithLive("ACTIVE")] });
    liveGateway(gw, {});
    const state = extras(gw);
    await gw.install(page);
    await signInAsAda(page);
    await page.goto(`/app/apps/${APP}/integrations?env=live`);
    await page.getByTestId("integration-open-nafru").click();
    const sheet = page.getByTestId("mail-sheet");
    await expect(sheet.getByTestId("nafru-link")).toHaveAttribute("href", /nafru\.com/);
    // A key the provider refuses is explained and not stored.
    await sheet.getByTestId("mail-base-url").fill("https://mail.nafru.example");
    await sheet.getByTestId("mail-api-key").fill("refused");
    await sheet.getByTestId("mail-from").fill("Acme Billing <billing@acme.example>");
    await sheet.getByTestId("integration-connect").click();
    await expect(sheet).toContainText("did not accept this key");
    expect(state.mail).toBeNull();
    await sheet.getByTestId("mail-api-key").fill("nk_live_secret_1234");
    await sheet.getByTestId("integration-connect").click();
    expect(gw.called("PUT", `/app/v1/apps/${APP}/integrations/mail`).at(-1)?.body).toEqual({ provider: "nafru", base_url: "https://mail.nafru.example", api_key: "nk_live_secret_1234", from: "Acme Billing <billing@acme.example>" });
    await expect(page.getByTestId("integration-state-nafru")).toHaveText("Connected and working");
    await expect(page.getByTestId("integration-card-nafru")).toContainText("billing");
    expect(await page.content()).not.toContain("nk_live_secret_1234");
    // Payrudder's own sender is no longer the one in use.
    await expect(page.getByTestId("integration-state-payrudderMail")).toHaveText("Not in use");

    await page.getByTestId("integration-open-nafru").click();
    await expect(sheet.getByTestId("mail-api-key")).toHaveValue("");
    await expect(sheet.getByTestId("integration-sheet-status")).toContainText("1234");
    await sheet.getByTestId("mail-test-to").fill("owner@example.com");
    await sheet.getByTestId("integration-test").click();
    await expect(sheet.getByTestId("integration-test-result")).toContainText("owner@example.com");
    expect(gw.called("POST", `/app/v1/apps/${APP}/integrations/mail/test`)[0]?.body).toEqual({ to: "owner@example.com" });
    page.once("dialog", (d) => void d.accept());
    await sheet.getByTestId("integration-disconnect").click();
    await expect(page.getByTestId("integration-state-nafru")).toHaveText("Not connected");
    await expect(page.getByTestId("integration-state-payrudderMail")).toHaveText("Always on");

    // The built-in sender: the switches of the kinds of mail, in a sheet.
    await page.getByTestId("integration-open-payrudderMail").click();
    const built = page.getByTestId("mail-sheet");
    await built.getByTestId("int-mail-usage_alerts").click();
    await expect.poll(() => state.categories.usage_alerts).toBe(false);
  });

  test("a viewer sees the state of e-invoice and e-mail but cannot change them", async ({ page }) => {
    const { gw } = productGateway(new MockGateway(), { apps: [appWithLive("ACTIVE")], role: "viewer" });
    liveGateway(gw, {});
    extras(gw, { account: { provider: "nilvera", mode: "production" } });
    await gw.install(page);
    await signInAsAda(page);
    await page.goto(`/app/apps/${APP}/integrations?env=live`);
    await expect(page.getByTestId("integration-state-nilvera")).toHaveText("Connected and working");
    await expect(page.getByTestId("integration-open-nilvera")).toBeDisabled();
    await expect(page.getByTestId("integration-open-nafru")).toBeDisabled();
  });

  test("the organisation's screen leaves e-invoice and e-mail to each app", async ({ page }) => {
    const { gw } = productGateway(new MockGateway(), { apps: [appWithLive("ACTIVE"), app2] });
    liveGateway(gw, {});
    secondApp(gw);
    await gw.install(page);
    await signInAsAda(page);
    await page.goto("/app/integrations");
    await expect(page.getByTestId("integration-per-app-einvoice")).toBeVisible();
    await expect(page.getByTestId("integration-open-nilvera")).toHaveCount(0);
    await expect(page.getByTestId("integration-open-nafru")).toHaveCount(0);
    await page.getByTestId(`integrations-open-app-${APP}`).click();
    await expect(page).toHaveURL(new RegExp(`/app/apps/${APP}/integrations`));
  });
});
