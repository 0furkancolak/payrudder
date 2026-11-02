import { expect, test } from "@playwright/test";
import { MockGateway } from "./gateway";
import { APP, app, appWithLive, liveGateway, productGateway, sandboxProvidersGateway, signInAsAda, tenant } from "./product";
import { confirmDialog, pickCurrency } from "./ui";

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
    await sheet.getByTestId("provider-disconnect").click();
    await confirmDialog(page, "yes");
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
    await sheet.getByTestId("provider-disconnect").click();
    await confirmDialog(page, "yes");
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
    await expect(page.getByTestId("integration-open-sandbox-paddle")).toBeDisabled();
  });

  test("the sandbox runs on the simulator until the owner connects a provider's test mode, which can be tested, replaced and removed", async ({ page }) => {
    const { gw } = productGateway(new MockGateway(), { apps: [appWithLive("ACTIVE")] });
    liveGateway(gw, {});
    const accounts = sandboxProvidersGateway(gw);
    await gw.install(page);
    await signInAsAda(page);
    await page.goto(`/app/apps/${APP}/integrations?env=live`);
    await expect(page.getByTestId("integration-sandbox-paddle")).toHaveText("Built-in simulator");
    // A bank transfer has no test mode.
    await expect(page.getByTestId("integration-open-sandbox-banktransfer")).toHaveCount(0);
    await page.getByTestId("integration-open-sandbox-paddle").click();
    const sheet = page.getByTestId("provider-sheet");
    await expect(sheet).toContainText("Connect Paddle test mode");
    await expect(sheet.getByTestId("provider-env-note")).toContainText("sandbox environment");
    await expect(sheet.getByTestId("provider-scope")).toHaveCount(0);
    await expect(page).toHaveURL(/sheet=connect-provider%3Asandbox-paddle/);
    // A key in the live format is refused by the server, the sheet says why, and what was typed stays for another try.
    await sheet.getByTestId("lp-api_key").fill("pdl_live_SECRETKEY");
    await sheet.getByTestId("lp-webhook_secret").fill("pdl_ntfset_SECRETHOOK");
    await sheet.getByTestId("lp-usage_product_id").fill("pro_01abc");
    await sheet.getByTestId("provider-connect").click();
    await expect(sheet.getByTestId("provider-failures")).toContainText("other mode");
    await expect(sheet.getByTestId("lp-api_key")).toHaveValue("pdl_live_SECRETKEY");
    expect(accounts).toHaveLength(0);
    // A sandbox key connects it: through the sandbox route, never the live one, and the live card is untouched.
    await sheet.getByTestId("lp-api_key").fill("pdl_sdbx_apikey_SECRETKEY");
    await sheet.getByTestId("provider-connect").click();
    await expect(page.getByTestId("provider-connected")).toContainText("Paddle test mode connected for the sandbox");
    expect(gw.called("PUT", `/app/v1/apps/${APP}/sandbox/providers/paddle`)).toHaveLength(2);
    expect(gw.called("PUT", `/app/v1/apps/${APP}/go-live/providers/paddle`)).toHaveLength(0);
    await expect(page.getByTestId("integration-sandbox-paddle")).toHaveText("Test mode connected");
    await expect(page.getByTestId("integration-card-paddle")).toHaveAttribute("data-state", "none");
    expect(await page.content()).not.toContain("SECRETKEY");
    // Manage: test it again, then remove it (asking first); the sandbox is the simulator again.
    await page.getByTestId("integration-open-sandbox-paddle").click();
    await expect(sheet).toContainText("Paddle test mode");
    await sheet.getByTestId("provider-test").click();
    await expect(sheet.getByTestId("provider-test-result")).toContainText("accepts these keys");
    await sheet.getByTestId("provider-disconnect").click();
    await confirmDialog(page, "yes");
    await expect(page.getByTestId("integration-sandbox-paddle")).toHaveText("Built-in simulator");
    expect(gw.called("DELETE", `/app/v1/apps/${APP}/sandbox/providers/paddle`)).toHaveLength(1);
    expect(gw.called("DELETE", `/app/v1/apps/${APP}/go-live/providers/paddle`)).toHaveLength(0);
  });

  test("a sandbox connection that the provider stopped accepting is a problem, and the sheet comes back from the address", async ({ page }) => {
    const { gw } = productGateway(new MockGateway(), { apps: [appWithLive("ACTIVE")] });
    liveGateway(gw, {});
    sandboxProvidersGateway(gw, [{ provider: "iyzico", working: false, problem: "refused" }]);
    await gw.install(page);
    await signInAsAda(page);
    await page.goto(`/app/apps/${APP}/integrations?env=live&sheet=connect-provider:sandbox-iyzico`);
    await expect(page.getByTestId("provider-sheet")).toContainText("iyzico test mode");
    await expect(page.getByTestId("provider-sheet-status")).toContainText("does not accept");
    await page.keyboard.press("Escape");
    await expect(page.getByTestId("integration-sandbox-iyzico")).toHaveText("Test mode: problem");
    // On the organisation's screen there is no test mode: the sandbox is connected for one app.
    await page.goto("/app/integrations");
    await expect(page.getByTestId("integration-open-sandbox-iyzico")).toHaveCount(0);
  });
});

interface EInvoiceMock { account: string; provider: string; label: string; mode: string; is_default: boolean }
interface AuditMock { id: number; kind: string; account: string; environment: string; action: string; actor: string; is_default: boolean; verified_remote: boolean; created_at: string }

/** What the e-invoice and e-mail integrations of the live environment hold, and what they were sent: several e-invoice accounts, a default, routing by country and the audit trail. */
function extras(gw: MockGateway, initial: { account?: { provider: string; mode: string } | null; accounts?: EInvoiceMock[]; mail?: Record<string, unknown> | null; events?: AuditMock[] } = {}) {
  const accounts: EInvoiceMock[] = initial.accounts ?? (initial.account ? [{ account: initial.account.provider, provider: initial.account.provider, label: "", mode: initial.account.mode, is_default: true }] : []);
  const state = {
    accounts,
    rules: [] as { country: string; account: string }[],
    events: initial.events ?? ([] as AuditMock[]),
    mail: initial.mail ?? null,
    categories: { invoice_issued: true, payment_receipts: true, payment_failed: true, usage_alerts: true, system: true } as Record<string, boolean>,
    /** The default account, or null. */
    get account(): EInvoiceMock | null {
      return accounts.find((a) => a.is_default) ?? null;
    },
  };
  const record = (action: string, account: string, kind = "einvoice") =>
    state.events.unshift({ id: state.events.length + 100, kind, account, environment: "live", action, actor: "user:11111111-1111-4111-8111-111111111111", is_default: false, verified_remote: false, created_at: "2026-10-21T10:00:00Z" });
  const eBase = `/app/v1/apps/${APP}/integrations/e-invoice`;
  const mBase = `/app/v1/apps/${APP}/integrations/mail`;
  gw.on((c) => {
    if (c.path === eBase && c.method === "GET") return { body: { account: state.account, accounts: state.accounts } };
    if (c.path === eBase && c.method === "PUT") {
      const provider = String(c.body?.provider);
      const label = String(c.body?.label ?? "");
      const key = label ? `${provider}~${label}` : provider;
      const wantsDefault = c.body?.default === true || state.accounts.length === 0;
      if (wantsDefault) for (const a of state.accounts) a.is_default = false;
      const existing = state.accounts.find((a) => a.account === key);
      if (existing) Object.assign(existing, { mode: String(c.body?.mode ?? "production"), is_default: existing.is_default || wantsDefault });
      else state.accounts.push({ account: key, provider, label, mode: String(c.body?.mode ?? "production"), is_default: wantsDefault });
      record(existing ? "replaced" : "connected", key);
      return { body: { environment: "live", account: key, provider, label, mode: String(c.body?.mode ?? "production"), is_default: wantsDefault, has_credentials: true } };
    }
    if (c.path === eBase && c.method === "DELETE") {
      const key = c.query.get("account") || state.accounts[0]?.account;
      const target = state.accounts.find((a) => a.account === key);
      if (target?.is_default && state.accounts.length > 1) return { status: 409, body: { error: { code: "FAILED_PRECONDITION", key: "einvoice.account.default_in_use", message: "default" } } };
      state.accounts.splice(0, state.accounts.length, ...state.accounts.filter((a) => a.account !== key));
      state.rules = state.rules.filter((r) => r.account !== key);
      record("removed", String(key));
      return { status: 204, body: {} };
    }
    if (c.path === `${eBase}/default` && c.method === "POST") {
      const key = String(c.body?.account);
      for (const a of state.accounts) a.is_default = a.account === key;
      record("default_set", key);
      return { status: 204, body: {} };
    }
    if (c.path === `${eBase}/routing` && c.method === "GET") return { body: { rules: state.rules } };
    if (c.path === `${eBase}/routing` && c.method === "PUT") {
      state.rules = (c.body?.rules as { country: string; account: string }[]) ?? [];
      record("routing_changed", "");
      return { body: { rules: state.rules } };
    }
    if (c.path === `${eBase}/test`) return { body: { ok: true, checked: "credentials" } };
    if (c.path === `/app/v1/apps/${APP}/integrations/audit` && c.method === "GET") {
      const kind = c.query.get("kind") ?? "";
      const after = Number(c.query.get("page_token") ?? "0");
      const size = Number(c.query.get("page_size") ?? "50");
      const all = state.events.filter((e) => (kind === "" || e.kind === kind) && (after === 0 || e.id < after));
      const page = all.slice(0, size);
      return { body: { events: page, next_page_token: all.length > size ? String(page[page.length - 1]?.id) : "" } };
    }
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
    // Every provider with a name has an official or licensed mark (light and dark files).
    await expect(page.getByTestId("integration-card-paddle").locator('img[src="/brand/providers/paddle-light.svg"]')).toHaveCount(1);
    await expect(page.getByTestId("integration-card-paddle").locator('img[src="/brand/providers/paddle-dark.svg"]')).toHaveCount(1);
    await expect(page.getByTestId("integration-card-nafru").locator("img")).toHaveCount(1);
    for (const id of ["iyzico", "nilvera"]) {
      await expect(page.getByTestId(`integration-card-${id}`).locator(`img[src="/brand/providers/${id}-light.svg"]`)).toHaveCount(1);
      await expect(page.getByTestId(`integration-card-${id}`).locator(`img[src="/brand/providers/${id}-dark.svg"]`)).toHaveCount(1);
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
    // The other integrator is a further account, not a replacement: nothing says the connected one goes away.
    await page.getByTestId("integration-open-webhook").click();
    await expect(page.getByTestId("einvoice-sheet").getByTestId("ei-label")).toBeVisible();
    await expect(page.getByTestId("einvoice-sheet")).not.toContainText("replaces");
    await page.getByTestId("einvoice-sheet").getByRole("button", { name: "Cancel" }).click();

    await page.getByTestId("integration-open-nilvera").click();
    await sheet.getByTestId("integration-disconnect").click();
    await confirmDialog(page, "yes");
    await expect(page.getByTestId("integration-state-nilvera")).toHaveText("Not connected");
    expect(gw.called("DELETE", `/app/v1/apps/${APP}/integrations/e-invoice`)).toHaveLength(1);
    expect(gw.called("DELETE", `/app/v1/apps/${APP}/integrations/e-invoice`)[0]?.query.get("account")).toBe("nilvera");
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
    // Custom templates are edited at Nafru Email, under Applications; the sheet says so.
    await expect(sheet.getByTestId("mail-templates-note")).toContainText("add Payrudder under Applications");
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
    await sheet.getByTestId("integration-disconnect").click();
    await confirmDialog(page, "yes");
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

test.describe("several accounts at one provider", () => {
  test("adds a second Paddle account under a label, makes it the default and routes a country to it", async ({ page }) => {
    const { gw } = productGateway(new MockGateway(), { apps: [appWithLive("ACTIVE")] });
    liveGateway(gw, { accounts: [{ provider: "paddle", default: true, working: true, checked: true }] });
    await gw.install(page);
    await signInAsAda(page);
    await page.goto(`/app/apps/${APP}/integrations?env=live`);
    const card = page.getByTestId("accounts-card");
    await expect(card.getByTestId("account-paddle")).toBeVisible();
    await expect(card.getByTestId("account-default-paddle")).toBeVisible();

    // One "Add integration" flow for every kind: the kind, then the provider, then that provider's own form.
    await card.getByTestId("account-add").click();
    await page.getByTestId("add-provider").selectOption("paddle");
    await page.getByTestId("add-continue").click();
    const sheet = page.getByTestId("provider-sheet");
    // A label is required, and it has the server's shape.
    await sheet.getByTestId("lp-account-label").fill("EU West");
    await expect(sheet.getByRole("alert")).toContainText("lower-case");
    await expect(sheet.getByTestId("provider-connect")).toBeDisabled();
    await sheet.getByTestId("lp-account-label").fill("eu");
    await keys(sheet);
    await sheet.getByTestId("provider-connect").click();
    expect(gw.called("PUT", `/app/v1/apps/${APP}/go-live/providers/paddle~eu`)).toHaveLength(1);
    await expect(card.getByTestId("account-paddle~eu")).toBeVisible();
    expect(await page.content()).not.toContain("SECRETKEY");

    // The default moves without sending any key again.
    await card.getByTestId("account-make-default-paddle~eu").click();
    await expect(card.getByTestId("account-default-paddle~eu")).toBeVisible();
    await expect(card.getByTestId("account-default-paddle")).toHaveCount(0);
    expect(gw.called("POST", `/app/v1/apps/${APP}/go-live/providers/paddle~eu/default`)).toHaveLength(1);

    // A rule sends a country to an account.
    await card.getByTestId("routing-add").click();
    await card.getByTestId("routing-country-0").fill("de");
    await card.getByTestId("routing-account-0").selectOption("paddle");
    await card.getByTestId("routing-save").click();
    await expect(card.getByTestId("routing-saved")).toBeVisible();
    const sent = gw.called("PUT", `/app/v1/apps/${APP}/go-live/routing`)[0]?.body as { rules: { country: string; provider: string }[] };
    expect(sent.rules).toEqual([{ country: "DE", currency: "", provider: "paddle" }]);
  });
});

test.describe("Stripe", () => {
  test("is connected with a secret key and the webhook's signing secret, shown by its name with a general icon, and has no test mode of its own", async ({ page }) => {
    const { gw } = productGateway(new MockGateway(), { apps: [appWithLive("ACTIVE")] });
    liveGateway(gw, {});
    await gw.install(page);
    await signInAsAda(page);
    await page.goto(`/app/apps/${APP}/integrations?env=live`);
    const card = page.getByTestId("integration-card-stripe");
    await expect(card).toHaveAttribute("data-state", "none");
    await expect(card).toContainText("Stripe");
    // A provider without a licensed mark is drawn with a general-purpose icon, never an image.
    await expect(card.locator("img")).toHaveCount(0);
    await expect(page.getByTestId("integration-open-sandbox-stripe")).toHaveCount(0);
    await page.getByTestId("integration-open-stripe").click();
    const sheet = page.getByTestId("provider-sheet");
    await sheet.getByTestId("lp-api_key").fill("pk_live_publishable");
    await sheet.getByTestId("lp-webhook_secret").fill("whsec_SECRETHOOK");
    await sheet.getByTestId("provider-connect").click();
    await expect(sheet.getByRole("alert").first()).toContainText("This does not look right.");
    expect(gw.called("PUT", `/app/v1/apps/${APP}/go-live/providers/stripe`)).toHaveLength(0);
    await sheet.getByTestId("lp-api_key").fill("sk_live_SECRETKEY");
    await sheet.getByTestId("provider-connect").click();
    await expect(page.getByTestId("provider-connected")).toContainText("Stripe connected");
    expect(gw.called("PUT", `/app/v1/apps/${APP}/go-live/providers/stripe`)[0]?.body).toEqual({ api_key: "sk_live_SECRETKEY", webhook_secret: "whsec_SECRETHOOK" });
    await expect(card).toHaveAttribute("data-state", "working");
    expect(await page.content()).not.toContain("SECRETKEY");
  });
});


test.describe("one list of accounts: payments, e-invoice and e-mail", () => {
  const ada = "user:11111111-1111-4111-8111-111111111111";
  const nilvera = (label: string, isDefault: boolean, mode = "production"): EInvoiceMock => ({ account: label ? `nilvera~${label}` : "nilvera", provider: "nilvera", label, mode, is_default: isDefault });

  test("lists every kind with its default and status, narrows by kind and by search, and keeps the e-mail sender in use in it", async ({ page }) => {
    const { gw } = productGateway(new MockGateway(), { apps: [appWithLive("ACTIVE")] });
    liveGateway(gw, { accounts: [{ provider: "paddle", default: true, working: true, checked: true }, { provider: "paddle~eu", default: false, working: false, checked: true, problem: "refused" }] });
    extras(gw, { accounts: [nilvera("", true), nilvera("tr", false, "test")] });
    await gw.install(page);
    await signInAsAda(page);
    await page.goto(`/app/apps/${APP}/integrations?env=live`);
    const card = page.getByTestId("accounts-card");
    for (const id of ["paddle", "paddle~eu", "nilvera", "nilvera~tr", "mail:builtin"]) await expect(card.getByTestId(`account-${id}`)).toBeVisible();
    await expect(card.getByTestId("account-kind-paddle")).toHaveText("Payments");
    await expect(card.getByTestId("account-kind-nilvera")).toHaveText("E-invoice");
    await expect(card.getByTestId("account-kind-mail:builtin")).toHaveText("E-mail");
    // One default per kind, a problem is a problem, and the built-in sender is the one in use while no own provider is connected.
    await expect(card.getByTestId("account-default-paddle")).toBeVisible();
    await expect(card.getByTestId("account-default-paddle~eu")).toHaveCount(0);
    await expect(card.getByTestId("account-state-paddle~eu")).toHaveText("Problem");
    await expect(card.getByTestId("account-default-nilvera")).toBeVisible();
    await expect(card.getByTestId("account-default-nilvera~tr")).toHaveCount(0);
    await expect(card.getByTestId("account-default-mail:builtin")).toHaveText("In use");
    // Search narrows the list to the matching accounts.
    await card.getByRole("searchbox").fill("tr");
    await expect(card.getByTestId("account-nilvera~tr")).toBeVisible();
    await expect(card.getByTestId("account-paddle")).toHaveCount(0);
  });

  test("a second e-invoice account is added under a label, made the default, routed by the buyer's country and removed; the credentials never come back", async ({ page }) => {
    const { gw } = productGateway(new MockGateway(), { apps: [appWithLive("ACTIVE")] });
    liveGateway(gw, {});
    const state = extras(gw, { accounts: [nilvera("", true)] });
    await gw.install(page);
    await signInAsAda(page);
    await page.goto(`/app/apps/${APP}/integrations?env=live`);
    const card = page.getByTestId("accounts-card");

    await card.getByTestId("account-add").click();
    await page.getByTestId("add-kind").selectOption("einvoice");
    await page.getByTestId("add-provider").selectOption("nilvera");
    await page.getByTestId("add-continue").click();
    const sheet = page.getByTestId("einvoice-sheet");
    // Another account at the same integrator must be told apart by a label of the server's shape.
    await sheet.getByTestId("ei-api-key").fill("NV-SECOND-KEY");
    await expect(sheet.getByTestId("integration-connect")).toBeDisabled();
    await sheet.getByTestId("ei-label").fill("Istanbul Office");
    await expect(sheet.getByRole("alert")).toContainText("lower-case");
    await sheet.getByTestId("ei-label").fill("tr");
    await sheet.getByTestId("ei-default").click();
    await sheet.getByTestId("integration-connect").click();
    expect(gw.called("PUT", `/app/v1/apps/${APP}/integrations/e-invoice`)[0]?.body).toEqual({ provider: "nilvera", label: "tr", default: true, mode: "production", credentials: { api_key: "NV-SECOND-KEY" } });
    await expect(card.getByTestId("account-nilvera~tr")).toBeVisible();
    await expect(card.getByTestId("account-default-nilvera~tr")).toBeVisible();
    await expect(card.getByTestId("account-default-nilvera")).toHaveCount(0);
    await expect(page.getByTestId("integration-card-nilvera")).toContainText("2 accounts connected");
    expect(await page.content()).not.toContain("NV-SECOND-KEY");

    // The default moves back with one click, without sending any credential.
    await card.getByTestId("account-make-default-nilvera").click();
    await expect(card.getByTestId("account-default-nilvera")).toBeVisible();
    expect(gw.called("POST", `/app/v1/apps/${APP}/integrations/e-invoice/default`)[0]?.body).toEqual({ account: "nilvera" });

    // A rule sends a country to an account; each country once.
    const routing = card.getByTestId("einvoice-routing-editor");
    await routing.getByTestId("einvoice-rule-add").click();
    await routing.getByTestId("einvoice-rule-country-0").fill("de");
    await routing.getByTestId("einvoice-rule-account-0").selectOption("nilvera~tr");
    await routing.getByTestId("einvoice-rule-add").click();
    await routing.getByTestId("einvoice-rule-country-1").fill("DE");
    await expect(routing.getByRole("alert")).toContainText("once");
    await expect(routing.getByTestId("einvoice-rule-save")).toBeDisabled();
    await routing.getByTestId("einvoice-rule-remove-1").click();
    await routing.getByTestId("einvoice-rule-save").click();
    await expect(routing.getByTestId("einvoice-rule-saved")).toBeVisible();
    expect(gw.called("PUT", `/app/v1/apps/${APP}/integrations/e-invoice/routing`)[0]?.body).toEqual({ rules: [{ country: "DE", account: "nilvera~tr" }] });

    // The default is removed last: the server says so, in words.
    await card.getByTestId("account-nilvera").getByTestId("row-actions").click();
    await page.getByTestId("row-action-remove").click();
    await confirmDialog(page, "yes");
    await expect(card).toContainText("Make another account the default first");
    expect(state.accounts).toHaveLength(2);
    // The other one goes, and the rule that named it goes with it.
    await card.getByTestId("account-nilvera~tr").getByTestId("row-actions").click();
    await page.getByTestId("row-action-remove").click();
    await confirmDialog(page, "yes");
    await expect(card.getByTestId("account-nilvera~tr")).toHaveCount(0);
    expect(gw.called("DELETE", `/app/v1/apps/${APP}/integrations/e-invoice`).at(-1)?.query.get("account")).toBe("nilvera~tr");
    expect(state.rules).toEqual([]);
  });

  test("the tester says which payment account, which e-invoice account and which sender takes a customer", async ({ page }) => {
    const { gw, state } = liveGateway(productGateway(new MockGateway(), { apps: [appWithLive("ACTIVE")] }).gw, {
      accounts: [{ provider: "paddle", default: true, working: true, checked: true }, { provider: "paddle~eu", default: false, working: true, checked: true }],
    });
    state.routing = [{ country: "DE", currency: "EUR", provider: "paddle~eu" }];
    extras(gw, { accounts: [nilvera("", true), nilvera("tr", false)] });
    gw.on((c) => {
      if (c.path === `/app/v1/apps/${APP}/go-live/routing/preview` && c.method === "POST") {
        const rule = (state.routing ?? []).findIndex((r) => r.country === c.body?.country && r.currency === c.body?.currency);
        return { body: { provider: rule < 0 ? "" : (state.routing ?? [])[rule]?.provider, rule_index: rule, owner: "provider", can_charge: true } };
      }
      return undefined;
    });
    await gw.install(page);
    await signInAsAda(page);
    await page.goto(`/app/apps/${APP}/integrations?env=live`);
    // The e-invoice rule: a buyer in Germany goes to the labelled account.
    const card = page.getByTestId("accounts-card");
    const routing = card.getByTestId("einvoice-routing-editor");
    await routing.getByTestId("einvoice-rule-add").click();
    await routing.getByTestId("einvoice-rule-country-0").fill("DE");
    await routing.getByTestId("einvoice-rule-account-0").selectOption("nilvera~tr");
    await routing.getByTestId("einvoice-rule-save").click();
    await expect(routing.getByTestId("einvoice-rule-saved")).toBeVisible();

    const tester = page.getByTestId("routing-tester");
    await tester.getByTestId("tester-country").fill("de");
    await pickCurrency(tester.getByRole("combobox"), "EUR");
    await tester.getByTestId("tester-run").click();
    await expect(tester.getByTestId("tester-result-payment")).toContainText("Paddle (eu), by rule 1");
    await expect(tester.getByTestId("tester-result-einvoice")).toContainText("Nilvera (tr), by the rule for DE");
    await expect(tester.getByTestId("tester-result-mail")).toContainText("built-in sender");
    // A country no rule names goes to the defaults.
    await tester.getByTestId("tester-country").fill("FR");
    await tester.getByTestId("tester-run").click();
    await expect(tester.getByTestId("tester-result-payment")).toContainText("default account");
    await expect(tester.getByTestId("tester-result-einvoice")).toContainText("Nilvera, the default account");
  });

  test("the audit lists who changed which account, newest first, a page at a time, and names the person", async ({ page }) => {
    const { gw } = productGateway(new MockGateway(), { apps: [appWithLive("ACTIVE")] });
    liveGateway(gw, {});
    const events: AuditMock[] = Array.from({ length: 23 }, (_, i) => ({
      id: 100 - i, kind: i % 2 === 0 ? "einvoice" : "payment", account: i % 2 === 0 ? "nilvera~tr" : "paddle~eu", environment: "live", action: i === 0 ? "default_set" : "connected",
      actor: i === 1 ? "operator" : ada, is_default: false, verified_remote: i === 3, created_at: "2026-10-20T10:00:00Z",
    }));
    extras(gw, { accounts: [nilvera("", true)], events });
    await gw.install(page);
    await signInAsAda(page);
    await page.goto(`/app/apps/${APP}/integrations?env=live`);
    const audit = page.getByTestId("audit-card");
    await expect(audit.getByTestId("audit-event-100")).toContainText("made nilvera~tr the default");
    await expect(audit.getByTestId("audit-event-99")).toContainText("Payrudder support connected paddle~eu");
    await expect(audit.getByTestId("audit-event-97")).toContainText("Accepted by the provider");
    // 20 on the first page; the rest on request.
    await expect(audit.locator('[data-testid^="audit-event-"]')).toHaveCount(20);
    await audit.getByTestId("audit-more").click();
    await expect(audit.locator('[data-testid^="audit-event-"]')).toHaveCount(23);
    await expect(audit.getByTestId("audit-more")).toHaveCount(0);
    // Narrowing by kind asks the server for that kind.
    await audit.getByTestId("audit-kind").selectOption("payment");
    await expect(audit.getByTestId("audit-event-100")).toHaveCount(0);
    expect(gw.called("GET", `/app/v1/apps/${APP}/integrations/audit`).some((c) => c.query.get("kind") === "payment")).toBe(true);
  });

  test("somebody who is not the owner sees the list, the tester and the audit but cannot add, move or remove anything", async ({ page }) => {
    const { gw } = productGateway(new MockGateway(), { apps: [appWithLive("ACTIVE")], role: "viewer" });
    liveGateway(gw, { accounts: [{ provider: "paddle", default: true, working: true, checked: true }] });
    extras(gw, { accounts: [nilvera("", true), nilvera("tr", false)] });
    await gw.install(page);
    await signInAsAda(page);
    await page.goto(`/app/apps/${APP}/integrations?env=live`);
    const card = page.getByTestId("accounts-card");
    await expect(card.getByTestId("account-nilvera~tr")).toBeVisible();
    await expect(card.getByTestId("account-add")).toHaveCount(0);
    await expect(card.getByTestId("account-make-default-nilvera~tr")).toHaveCount(0);
    await expect(card.getByTestId("account-manage-nilvera~tr")).toBeDisabled();
    await expect(card.getByTestId("row-actions")).toHaveCount(0);
    await expect(page.getByTestId("routing-tester")).toBeVisible();
    await expect(page.getByTestId("audit-card")).toBeVisible();
  });
});
