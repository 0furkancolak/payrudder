import { expect, test } from "@playwright/test";
import { MockGateway } from "./gateway";
import { APP, app, appWithLive, liveGateway, productGateway, signInAsAda, tenant } from "./product";
import { confirmDialog } from "./ui";

// The e-mail accounts an organisation shares among its apps (ADR 0026): set up once on the organisation's integrations screen, bound to apps there, listed on an app as
// inherited, and refused removal while an app uses them. The gateway is mocked; the key is typed once and never shown again.

const APP2 = "b0000000-0000-4000-8000-000000000002";
const app2 = { ...app, id: APP2, name: "Mobile game", slug: "mobile-game", tenants: [tenant("b1000000-0000-4000-8000-000000000001", "LIVE"), tenant("b1000000-0000-4000-8000-000000000002", "SANDBOX")] };

interface Shared { account: string; label: string; apps: { app_id: string; name: string; is_default: boolean }[] }

/** The organisation's shared accounts and what each app of it uses; the answers follow what the page sent. */
function organisation(gw: MockGateway, opts: { canWrite?: boolean } = {}) {
  const shared: Shared[] = [];
  const names: Record<string, string> = { [APP]: "Shop", [APP2]: "Mobile game" };
  const view = (s: Shared) => ({ id: "c0000000-0000-4000-8000-000000000001", account: s.account, provider: "nafru", label: s.label, base_url: "https://mail.nafru.example", from: "Acme <billing@acme.example>", key_hint: "5678", update_time: "2026-11-02T10:00:00Z", check_problem: "", used_by: s.apps });
  const base = "/app/v1/account/integrations/mail";
  gw.on((c) => {
    if (c.path === base && c.method === "GET") return { body: { accounts: shared.map(view), can_write: opts.canWrite !== false } };
    if (c.path === base && c.method === "PUT") {
      const label = String(c.body?.label ?? "");
      const account = label ? `nafru~${label}` : "nafru";
      const have = shared.find((s) => s.account === account);
      if (!have) shared.push({ account, label, apps: [] });
      return { body: view(have ?? (shared.at(-1) as Shared)) };
    }
    if (c.path === base && c.method === "DELETE") {
      const s = shared.find((x) => x.account === c.query.get("account"));
      if (s && s.apps.length > 0) return { status: 409, body: { error: { code: "FAILED_PRECONDITION", key: "plans.mail_integration.shared_in_use", message: "apps use this account: " + s.apps.map((a) => a.name).join(", ") } } };
      shared.splice(0, shared.length, ...shared.filter((x) => x !== s));
      return { status: 204, body: {} };
    }
    if (c.path === `${base}/bind` && c.method === "POST") {
      const s = shared.find((x) => x.account === c.body?.account);
      const id = String(c.body?.app_id);
      if (s && !s.apps.some((a) => a.app_id === id)) s.apps.push({ app_id: id, name: names[id] ?? id, is_default: c.body?.make_default === true || shared.every((x) => !x.apps.some((a) => a.app_id === id)) });
      return { body: {} };
    }
    if (c.path === `${base}/unbind` && c.method === "POST") {
      const s = shared.find((x) => x.account === c.body?.account);
      if (s) s.apps = s.apps.filter((a) => a.app_id !== c.body?.app_id);
      return { body: {} };
    }
    if (c.path === `${base}/test` && c.method === "POST") return { body: { ok: true, problem: "", message_id: "em_1" } };
    // What the first app lists of its own e-mail: the shared accounts it is bound to, as inherited.
    if (c.path === `/app/v1/apps/${APP}/integrations/mail` && c.method === "GET") {
      const mine = shared.filter((s) => s.apps.some((a) => a.app_id === APP));
      const accounts = mine.map((s) => ({ account: s.account, provider: "nafru", label: s.label, base_url: "", from: "", key_hint: "", check_problem: "", is_default: s.apps.find((a) => a.app_id === APP)?.is_default === true, inherited: true, update_time: "2026-11-02T10:00:00Z", checked_time: null }));
      return { body: { connected: accounts.length > 0, integration: accounts.find((a) => a.is_default) ?? null, accounts } };
    }
    return undefined;
  });
  return shared;
}

test.describe("organisation-level e-mail accounts", () => {
  test("an account is set up once, bound to two apps, shown as inherited on an app and kept until no app uses it; the key never comes back", async ({ page }) => {
    const { gw } = productGateway(new MockGateway(), { apps: [appWithLive("ACTIVE"), app2] });
    liveGateway(gw, {});
    const shared = organisation(gw);
    await gw.install(page);
    await signInAsAda(page);
    await page.goto("/app/integrations");
    const card = page.getByTestId("org-mail-card");
    await expect(card.getByTestId("org-mail-empty")).toBeVisible();

    // Set up once for the organisation; a first account needs no label.
    await card.getByTestId("org-mail-add").click();
    const sheet = page.getByTestId("org-mail-sheet");
    await sheet.getByTestId("org-mail-base-url").fill("https://mail.nafru.example");
    await sheet.getByTestId("org-mail-api-key").fill("nk_org_SECRET_5678");
    await sheet.getByTestId("org-mail-from").fill("Acme <billing@acme.example>");
    await sheet.getByTestId("org-mail-save").click();
    expect(gw.called("PUT", "/app/v1/account/integrations/mail").at(-1)?.body).toEqual({ provider: "nafru", base_url: "https://mail.nafru.example", api_key: "nk_org_SECRET_5678", from: "Acme <billing@acme.example>" });
    await expect(card.getByTestId("org-mail-nafru")).toBeVisible();
    await expect(card.getByTestId("org-mail-unused-nafru")).toBeVisible();
    expect(await page.content()).not.toContain("nk_org_SECRET_5678");

    // Two apps use it; each is bound on its own, the second as the app's default.
    await card.getByTestId("org-mail-pick-nafru").selectOption(APP);
    await card.getByTestId("org-mail-bind-nafru").click();
    await expect(card.getByTestId(`org-mail-app-nafru-${APP}`)).toContainText("Shop");
    await card.getByTestId("org-mail-pick-nafru").selectOption(APP2);
    await card.getByTestId("org-mail-default-nafru").click();
    await card.getByTestId("org-mail-bind-nafru").click();
    await expect(card.getByTestId(`org-mail-app-nafru-${APP2}`)).toContainText("Mobile game");
    expect(gw.called("POST", "/app/v1/account/integrations/mail/bind").map((r) => r.body)).toEqual([{ account: "nafru", app_id: APP }, { account: "nafru", app_id: APP2, make_default: true }]);
    expect(shared[0]?.apps).toHaveLength(2);

    // While apps use it, it cannot be removed; the apps are listed under it.
    await expect(card.getByTestId("org-mail-remove-nafru")).toBeDisabled();

    // An app lists it as inherited: it can be the default but is not managed or removed there.
    await page.goto(`/app/apps/${APP}/integrations?env=live`);
    const accounts = page.getByTestId("accounts-card");
    await expect(accounts.getByTestId("account-inherited-mail:nafru")).toHaveText("Inherited from the organisation");
    await expect(accounts.getByTestId("account-default-mail:nafru")).toBeVisible();
    await expect(accounts.getByTestId("account-manage-mail:nafru")).toHaveCount(0);
    await expect(accounts.getByTestId("account-mail:nafru").getByTestId("row-action-remove")).toHaveCount(0);

    // Stopping the use of it asks first; with no app left, it is removed.
    await page.goto("/app/integrations");
    await card.getByTestId(`org-mail-unbind-nafru-${APP}`).click();
    await confirmDialog(page, "yes");
    await expect(card.getByTestId(`org-mail-app-nafru-${APP}`)).toHaveCount(0);
    await card.getByTestId(`org-mail-unbind-nafru-${APP2}`).click();
    await confirmDialog(page, "yes");
    await expect(card.getByTestId("org-mail-unused-nafru")).toBeVisible();
    expect(gw.called("POST", "/app/v1/account/integrations/mail/unbind").map((r) => r.body)).toEqual([{ account: "nafru", app_id: APP }, { account: "nafru", app_id: APP2 }]);
    await card.getByTestId("org-mail-remove-nafru").click();
    await confirmDialog(page, "yes");
    await expect(card.getByTestId("org-mail-empty")).toBeVisible();
    expect(gw.called("DELETE", "/app/v1/account/integrations/mail").at(-1)?.query.get("account")).toBe("nafru");
  });

  test("the server's refusal to remove an account that an app uses is shown in words", async ({ page }) => {
    const { gw } = productGateway(new MockGateway(), { apps: [appWithLive("ACTIVE"), app2] });
    liveGateway(gw, {});
    const shared = organisation(gw);
    shared.push({ account: "nafru", label: "", apps: [{ app_id: APP, name: "Shop", is_default: true }] });
    await gw.install(page);
    await signInAsAda(page);
    await page.goto("/app/integrations");
    const card = page.getByTestId("org-mail-card");
    await expect(card.getByTestId(`org-mail-app-nafru-${APP}`)).toBeVisible();
    // The button waits for the list under it to be empty; the same refusal from the server (another tab bound an app meanwhile) reads in words too.
    shared[0]?.apps.splice(0);
    await page.reload();
    await expect(card.getByTestId("org-mail-remove-nafru")).toBeEnabled();
    shared[0]?.apps.push({ app_id: APP2, name: "Mobile game", is_default: true });
    await card.getByTestId("org-mail-remove-nafru").click();
    await confirmDialog(page, "yes");
    await expect(card).toContainText("Apps still use this shared account");
  });

  test("a viewer sees the shared accounts and who uses them but cannot change anything", async ({ page }) => {
    const { gw } = productGateway(new MockGateway(), { apps: [appWithLive("ACTIVE"), app2], role: "viewer" });
    liveGateway(gw, {});
    const shared = organisation(gw, { canWrite: false });
    shared.push({ account: "nafru~eu", label: "eu", apps: [{ app_id: APP, name: "Shop", is_default: true }] });
    await gw.install(page);
    await signInAsAda(page);
    await page.goto("/app/integrations");
    const card = page.getByTestId("org-mail-card");
    await expect(card.getByTestId("org-mail-nafru~eu")).toContainText("Nafru Email");
    await expect(card.getByTestId(`org-mail-app-nafru~eu-${APP}`)).toBeVisible();
    await expect(card.getByTestId("org-mail-read-only")).toBeVisible();
    await expect(card.getByTestId("org-mail-add")).toHaveCount(0);
    await expect(card.getByTestId("org-mail-remove-nafru~eu")).toHaveCount(0);
    await expect(card.getByTestId("org-mail-bind-nafru~eu")).toHaveCount(0);
    await expect(card.getByTestId(`org-mail-unbind-nafru~eu-${APP}`)).toHaveCount(0);
  });
});
