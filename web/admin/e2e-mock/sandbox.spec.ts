import { expect, test } from "@playwright/test";
import { baseTenant, MockGateway, openTenantPage } from "./gateway";
import { APP, appWithLive, liveGateway, productGateway, signInAsAda } from "./product";
import { pickLanguage } from "./ui";

// Phase 45 F: the console never lets live and sandbox be mixed up. The environment switch is always there, the sandbox is marked on every page, a live
// environment that is not active shows its go-live checklist instead of zeros, and the app opens where the person last was. The gateway is mocked.

test.describe("which environment is on screen", () => {
  test("the organisation home has the switch, and a sandbox page has the stripe and the banner; live has neither", async ({ page }) => {
    await productGateway(new MockGateway()).gw.install(page);
    await signInAsAda(page);
    await expect(page.getByTestId("env-switch")).toBeVisible();
    // Live is active in the fixture: the home opens there, unmarked.
    await expect(page.getByTestId("sandbox-stripe")).toHaveCount(0);
    await expect(page.getByTestId("sandbox-banner")).toHaveCount(0);
    await page.getByTestId("env-switch").getByRole("radio", { name: "Sandbox" }).check({ force: true });
    await expect(page).toHaveURL(/env=sandbox/);
    await expect(page.getByTestId("sandbox-stripe")).toBeAttached();
    await expect(page.getByTestId("sandbox-banner")).toContainText("Nothing here is real money");
    // The choice is written into the address, live included, so a reload keeps it.
    await page.getByTestId("env-switch").getByRole("radio", { name: "Live" }).check({ force: true });
    await expect(page).toHaveURL(/env=live/);
    await expect(page.getByTestId("sandbox-banner")).toHaveCount(0);
  });

  test("the banner follows the person into the pages of the sandbox tenant", async ({ page }) => {
    await productGateway(new MockGateway()).gw.install(page);
    await signInAsAda(page);
    await page.goto(`/app/t/a1000000-0000-4000-8000-000000000002/customers`);
    await expect(page.getByTestId("sandbox-banner")).toBeVisible();
    await page.goto(`/app/t/a1000000-0000-4000-8000-000000000001/customers`);
    await expect(page.getByTestId("sandbox-banner")).toHaveCount(0);
  });

  test("an operator's tenant page is marked too: the sandbox tenant has the stripe and the banner, the live tenant has neither", async ({ page }) => {
    const gw = baseTenant(new MockGateway(), { environment: "ENVIRONMENT_SANDBOX", name: "Acme (sandbox)" });
    await gw.install(page);
    await openTenantPage(page, "");
    await expect(page.getByTestId("tenant-id")).toBeVisible();
    await expect(page.getByTestId("sandbox-stripe")).toBeAttached();
    await expect(page.getByTestId("sandbox-banner")).toContainText("Nothing here is real money");
    // The operator's own pages (the tenant list) are not a sandbox page.
    await page.goto("/tenants");
    await expect(page.getByTestId("sandbox-banner")).toHaveCount(0);
  });

  test("an operator's live tenant page has no stripe and no banner", async ({ page }) => {
    const gw = baseTenant(new MockGateway());
    await gw.install(page);
    await openTenantPage(page, "");
    await expect(page.getByTestId("tenant-id")).toBeVisible();
    await expect(page.getByTestId("sandbox-stripe")).toHaveCount(0);
    await expect(page.getByTestId("sandbox-banner")).toHaveCount(0);
  });

  test("an app opens where the person last was, and the page never loads one environment and then flips to the other", async ({ page }) => {
    const { gw } = productGateway(new MockGateway());
    await gw.install(page);
    await signInAsAda(page);
    await page.goto(`/app/apps/${APP}?env=sandbox`);
    await expect(page.getByTestId("sandbox-banner")).toBeVisible();
    // No environment in the address now: the sandbox, because that is where the person was (live would be the default).
    const before = gw.called("GET", `/app/v1/apps/${APP}/dashboard`).length;
    await page.goto(`/app/apps/${APP}`);
    await expect(page.getByTestId("sandbox-banner")).toBeVisible();
    await expect.poll(() => gw.called("GET", `/app/v1/apps/${APP}/dashboard`).length).toBeGreaterThan(before);
    const asked = gw.called("GET", `/app/v1/apps/${APP}/dashboard`).slice(before).map((c) => c.query.get("environment"));
    expect(new Set(asked)).toEqual(new Set(["sandbox"]));
  });
});

test.describe("a live environment that is not active", () => {
  test("the home shows its checklist, not zeros, and opens in the sandbox", async ({ page }) => {
    const { gw } = productGateway(new MockGateway(), { apps: [appWithLive("INACTIVE")] });
    liveGateway(gw, { done: { email_verified: true, plan_published: true } });
    await gw.install(page);
    await signInAsAda(page);
    // Nothing is live yet, so the home opens in the sandbox, with the banner.
    await expect(page.getByTestId("sandbox-banner")).toBeVisible();
    await page.getByTestId("env-switch").getByRole("radio", { name: "Live" }).check({ force: true });
    const card = page.getByTestId("golive-card-web-shop");
    await expect(card).toContainText("2 of 5 done");
    await expect(page.getByTestId("org-dashboard")).toHaveCount(0);
    await expect(page.getByText("$12,840.50")).toHaveCount(0);
    await card.getByTestId("go-live-open").click();
    await page.waitForURL(`**/app/apps/${APP}/go-live?env=live`);
  });

  test("the app's live dashboard is the checklist too, and no dashboard is asked for", async ({ page }) => {
    const { gw } = productGateway(new MockGateway(), { apps: [appWithLive("INACTIVE")] });
    liveGateway(gw, {});
    await gw.install(page);
    await signInAsAda(page);
    await page.goto(`/app/apps/${APP}?env=live`);
    await expect(page.getByTestId("live-checklist")).toBeVisible();
    await expect(page.getByTestId("go-live-state")).toContainText("1 of 5 done");
    expect(gw.called("GET", `/app/v1/apps/${APP}/dashboard`)).toHaveLength(0);
    await page.getByTestId("to-sandbox").click();
    await expect(page.getByTestId("sandbox-banner")).toBeVisible();
  });
});

test.describe("going live", () => {
  test("the owner switches live on once the list is complete and the terms are accepted", async ({ page }) => {
    const { gw } = productGateway(new MockGateway(), { apps: [appWithLive("INACTIVE")] });
    const live = liveGateway(gw, { done: { email_verified: true, legal_profile: true, payment_provider: true, plan_published: true } });
    await gw.install(page);
    await signInAsAda(page);
    await page.goto(`/app/apps/${APP}/go-live?env=live`);
    const button = page.getByTestId("go-live-activate");
    await expect(page.getByTestId("go-live-terms_accepted")).toHaveAttribute("data-done", "false");
    await expect(button).toBeDisabled();
    // The terms are a step of their own: ticking the box is not accepting, and the button waits for the acceptance.
    const accept = page.getByTestId("go-live-accept-terms");
    await expect(accept).toBeDisabled();
    await page.getByTestId("go-live-terms").click();
    await expect(button).toBeDisabled();
    await accept.click();
    await expect(page.getByTestId("go-live-terms_accepted")).toHaveAttribute("data-done", "true");
    expect(gw.called("POST", `/app/v1/apps/${APP}/go-live/terms`)).toHaveLength(1);
    await expect(button).toBeEnabled();
    await button.click();
    await expect(page.getByTestId("go-live-state")).toContainText("Active");
    expect(gw.called("POST", `/app/v1/apps/${APP}/go-live`)[0]?.body).toEqual({ accept_terms: false });
    expect(live.state.status).toBe("ACTIVE");
  });

  test("with steps missing the button stays off, and each missing step points to where the owner does it", async ({ page }) => {
    const { gw } = productGateway(new MockGateway(), { apps: [appWithLive("INACTIVE")] });
    liveGateway(gw, { done: { email_verified: true } });
    await gw.install(page);
    await signInAsAda(page);
    await page.goto(`/app/apps/${APP}/go-live?env=live`);
    await expect(page.getByTestId("go-live-activate")).toBeDisabled();
    // The owner is not told to ask the team: the profile and the provider are cards on this page, and the item scrolls to them.
    await expect(page.getByTestId("go-live-legal_profile")).not.toContainText("Contact support");
    await expect(page.getByTestId("billing-profile")).toBeVisible();
    await expect(page.getByTestId("live-provider")).toBeVisible();
    // The provider item opens the payment providers screen of the app.
    await page.getByTestId("go-live-go-payment_provider").click();
    await expect(page).toHaveURL(new RegExp(`/app/apps/${APP}/providers`));
    await expect(page.getByTestId("provider-card-paddle")).toBeVisible();
  });

  test("a member who is not the owner reads the steps but cannot change anything, and is told why", async ({ page }) => {
    const { gw } = productGateway(new MockGateway(), { apps: [appWithLive("INACTIVE")], role: "viewer" });
    liveGateway(gw, { done: { email_verified: true }, accounts: [{ provider: "paddle", default: true, working: true, checked: true }] });
    await gw.install(page);
    await signInAsAda(page);
    await page.goto(`/app/apps/${APP}/go-live?env=live`);
    await expect(page.getByTestId("profile-owner-only")).toBeVisible();
    await expect(page.getByTestId("profile-save")).toBeDisabled();
    await expect(page.getByTestId("provider-owner-only")).toBeVisible();
    await expect(page.getByTestId("provider-open-paddle")).toHaveCount(0);
    await expect(page.getByTestId("live-account-remove-paddle")).toHaveCount(0);
    await expect(page.getByTestId("live-account-paddle")).toContainText("Working");
    await expect(page.getByTestId("go-live-accept-terms")).toBeDisabled();
  });
});

test.describe("the owner's own steps", () => {
  test("the billing profile is checked as the server checks it, saved once, and completes its item", async ({ page }) => {
    const { gw } = productGateway(new MockGateway(), { apps: [appWithLive("INACTIVE")] });
    liveGateway(gw, { done: { email_verified: true } });
    await gw.install(page);
    await signInAsAda(page);
    await page.goto(`/app/apps/${APP}/go-live?env=live`);
    const card = page.getByTestId("billing-profile");
    await expect(page.getByTestId("go-live-legal_profile")).toHaveAttribute("data-done", "false");
    await card.getByLabel("Legal name").fill("Shop GmbH");
    await card.getByLabel("Country", { exact: true }).selectOption("DE");
    await card.getByLabel("Address line 1").fill("Hauptstr. 1");
    await card.getByLabel("City").fill("Berlin");
    // In Germany the seller's tax id is required; nothing is sent until it is there.
    await expect(card.getByText("Required for invoices in DE")).toBeVisible();
    await card.getByTestId("profile-save").click();
    await expect(card.getByRole("alert")).toContainText("Required.");
    expect(gw.called("PUT", `/app/v1/apps/${APP}/billing-profile`)).toHaveLength(0);
    await card.getByLabel("Tax id").fill("de 123-456 789");
    await card.getByTestId("profile-save").click();
    await expect(card.getByTestId("profile-saved")).toBeVisible();
    expect(gw.called("PUT", `/app/v1/apps/${APP}/billing-profile`)).toHaveLength(1);
    expect(gw.called("PUT", `/app/v1/apps/${APP}/billing-profile`)[0]?.body).toEqual({
      legal: { legal_name: "Shop GmbH", tax_id: "DE123456789" },
      address: { line1: "Hauptstr. 1", city: "Berlin", country: "DE" },
    });
    await expect(page.getByTestId("go-live-legal_profile")).toHaveAttribute("data-done", "true");
  });

  test("a live provider is connected with its keys typed once, never shown again, and removed on request", async ({ page }) => {
    const { gw } = productGateway(new MockGateway(), { apps: [appWithLive("INACTIVE")] });
    liveGateway(gw, { done: { email_verified: true } });
    await gw.install(page);
    await signInAsAda(page);
    await page.goto(`/app/apps/${APP}/go-live?env=live`);
    const card = page.getByTestId("live-provider");
    const sheet = page.getByTestId("provider-sheet");
    await expect(page.getByTestId("live-accounts-none")).toBeVisible();
    await card.getByTestId("provider-open-paddle").click();
    // Keys are password fields, and the form never asks for an endpoint.
    await expect(sheet.getByTestId("lp-api_key")).toHaveAttribute("type", "password");
    await expect(sheet.getByTestId("lp-webhook_secret")).toHaveAttribute("type", "password");
    await expect(sheet.getByLabel(/base.?url|endpoint/i)).toHaveCount(0);
    await sheet.getByTestId("provider-connect").click();
    await expect(sheet.getByRole("alert").first()).toContainText("Required.");
    expect(gw.called("PUT", `/app/v1/apps/${APP}/go-live/providers/paddle`)).toHaveLength(0);
    await sheet.getByTestId("lp-api_key").fill("pdl_live_SECRETKEY");
    await sheet.getByTestId("lp-webhook_secret").fill("pdl_ntfset_SECRETHOOK");
    await sheet.getByTestId("lp-usage_product_id").fill("pro_01abc");
    await sheet.getByTestId("provider-connect").click();
    await expect(page.getByTestId("provider-connected")).toContainText("Paddle is connected");
    expect(gw.called("PUT", `/app/v1/apps/${APP}/go-live/providers/paddle`)[0]?.body).toEqual({
      api_key: "pdl_live_SECRETKEY", webhook_secret: "pdl_ntfset_SECRETHOOK", usage_product_id: "pro_01abc",
    });
    // The secrets are gone from the form and from the page, and the checklist item is done at once.
    await expect(sheet).toHaveCount(0);
    await expect(page.getByTestId("live-account-paddle")).toContainText("Working");
    await expect(page.getByTestId("go-live-payment_provider")).toHaveAttribute("data-done", "true");
    expect(await page.content()).not.toContain("SECRETKEY");
    expect(await page.evaluate(() => [...document.querySelectorAll("input")].map((i) => i.value).join("|"))).not.toMatch(/SECRET/);
    // Removing asks first.
    page.once("dialog", (d) => void d.dismiss());
    await page.getByTestId("live-account-remove-paddle").click();
    expect(gw.called("DELETE", `/app/v1/apps/${APP}/go-live/providers/paddle`)).toHaveLength(0);
    page.once("dialog", (d) => void d.accept());
    await page.getByTestId("live-account-remove-paddle").click();
    await expect(page.getByTestId("live-accounts-none")).toBeVisible();
    await expect(page.getByTestId("go-live-payment_provider")).toHaveAttribute("data-done", "false");
  });

  test("iyzico's buyer details are filled from the profile and the person, and a bank account needs no provider check", async ({ page }) => {
    const { gw } = productGateway(new MockGateway(), {
      apps: [{ ...appWithLive("INACTIVE"), tenants: [{ ...appWithLive("INACTIVE").tenants[0]!, legal: { legal_name: "Ada Labs", tax_id: "1234567890" }, address: { line1: "Bagdat Cd. 1", city: "Istanbul", postal_code: "34000", country: "TR" } }, appWithLive("INACTIVE").tenants[1]!] }],
    });
    liveGateway(gw, {});
    await gw.install(page);
    await signInAsAda(page);
    await page.goto(`/app/apps/${APP}/go-live?env=live`);
    const card = page.getByTestId("live-provider");
    const sheet = page.getByTestId("provider-sheet");
    await card.getByTestId("provider-open-iyzico").click();
    await expect(sheet.getByTestId("lp-buyer_email")).toHaveValue("ada@example.com");
    await expect(sheet.getByTestId("lp-buyer_address")).toHaveValue("Bagdat Cd. 1");
    await expect(sheet.getByTestId("lp-buyer_country")).toHaveValue(/^(Turkey|Türkiye)$/);
    await expect(sheet.getByTestId("lp-buyer_identity_number")).toHaveValue("1234567890");
    await expect(sheet.getByTestId("lp-secret_key")).toHaveValue("");
    await page.keyboard.press("Escape");
    await expect(sheet).toHaveCount(0);
    await card.getByTestId("provider-open-banktransfer").click();
    await expect(sheet.getByLabel("IBAN")).toBeVisible();
    await expect(sheet.getByText(/Sandbox charges never reach/)).toHaveCount(0);
  });

  test("a provider that refuses the keys is explained in words, and nothing is connected", async ({ page }) => {
    const { gw } = productGateway(new MockGateway(), { apps: [appWithLive("INACTIVE")] });
    liveGateway(gw, { done: { email_verified: true }, refuseProvider: { status: 400, key: "payments.account.health_check_failed" } });
    await gw.install(page);
    await signInAsAda(page);
    await page.goto(`/app/apps/${APP}/go-live?env=live`);
    const sheet = page.getByTestId("provider-sheet");
    await page.getByTestId("provider-open-paddle").click();
    await sheet.getByTestId("lp-api_key").fill("pdl_live_x");
    await sheet.getByTestId("lp-webhook_secret").fill("pdl_ntfset_x");
    await sheet.getByTestId("lp-usage_product_id").fill("pro_01abc");
    await sheet.getByTestId("provider-connect").click();
    await expect(sheet.getByRole("alert")).toContainText("did not accept these credentials");
    // What was typed stays for another try, and nothing counts as connected.
    await expect(sheet.getByTestId("lp-api_key")).toHaveValue("pdl_live_x");
    await page.keyboard.press("Escape");
    await expect(page.getByTestId("live-accounts-none")).toBeVisible();
    await expect(page.getByTestId("go-live-payment_provider")).toHaveAttribute("data-done", "false");
  });

  test("an account the provider no longer accepts is shown as not working, with the reason", async ({ page }) => {
    const { gw } = productGateway(new MockGateway(), { apps: [appWithLive("INACTIVE")] });
    liveGateway(gw, { accounts: [{ provider: "iyzico", default: true, working: false, checked: true, problem: "refused" }] });
    await gw.install(page);
    await signInAsAda(page);
    await page.goto(`/app/apps/${APP}/go-live?env=live`);
    await expect(page.getByTestId("live-account-iyzico")).toContainText("Not working");
    await expect(page.getByTestId("live-account-problem")).toContainText("does not accept these keys any more");
  });

  test("a live key cannot be made while live is inactive, and the page points to the checklist", async ({ page }) => {
    const { gw } = productGateway(new MockGateway(), { apps: [appWithLive("INACTIVE")] });
    liveGateway(gw);
    await gw.install(page);
    await signInAsAda(page);
    await page.goto(`/app/apps/${APP}/keys?env=sandbox`);
    await expect(page.getByTestId("new-live-key")).toBeDisabled();
    await expect(page.getByTestId("live-inactive")).toContainText("once the live environment is active");
    await expect(page.getByTestId("new-sandbox-key")).toBeEnabled();
  });
});

test.describe("keys", () => {
  test("a key is made with a scope, shows when it was last used, and rotating shows the new key once and the old one's end", async ({ page }) => {
    const { gw } = productGateway(new MockGateway());
    liveGateway(gw);
    gw.on((c) => (c.path === `/app/v1/apps/${APP}/api-keys` && c.method === "GET"
      ? { body: { api_keys: [
          { id: "k2", tenant_id: "x", prefix: "wxyz5678", environment: "ENVIRONMENT_SANDBOX", status: "STATUS_ACTIVE", scope: "SCOPE_INGEST", create_time: "2026-10-03T09:05:00Z", last_used_time: "2026-10-20T09:05:00Z" },
          { id: "k5", tenant_id: "x", prefix: "ending00", environment: "ENVIRONMENT_SANDBOX", status: "STATUS_ACTIVE", scope: "SCOPE_FULL", create_time: "2026-10-03T09:05:00Z", expire_time: "2026-10-22T10:00:00Z" },
        ] } }
      : undefined));
    await gw.install(page);
    await signInAsAda(page);
    await page.goto(`/app/apps/${APP}/keys?env=sandbox`);
    await expect(page.getByTestId("scope-k2")).toContainText("Send usage only");
    await expect(page.getByTestId("last-used-k2")).toContainText("last used");
    await expect(page.getByTestId("last-used-k5")).toContainText("never used");
    await expect(page.getByTestId("key-ends")).toContainText("Stops working on");
    // A key that already ends cannot be rotated again.
    await expect(page.getByTestId("row-action-rotate-k5")).toHaveCount(0);

    await page.getByTestId("key-scope").selectOption("read");
    await page.getByTestId("new-sandbox-key").click();
    await page.getByRole("dialog").getByRole("button", { name: "I have saved it" }).click();
    expect(gw.called("POST", `/app/v1/apps/${APP}/api-keys`)[0]?.body).toEqual({ environment: "sandbox", scope: "read" });

    await page.getByRole("row").filter({ has: page.getByTestId("scope-k2") }).getByTestId("row-actions").click();
    await page.getByTestId("row-action-rotate-k2").click();
    await page.getByTestId("rotate-overlap").selectOption("72");
    await page.getByTestId("rotate-confirm").click();
    await expect(page.getByTestId("new-key")).toHaveText("qk_test_rotated1_SECRETSECRETSECRETSECRET");
    expect(gw.called("POST", `/app/v1/apps/${APP}/api-keys/k2/rotate`)[0]?.body).toEqual({ overlap_hours: 72 });
  });
});

test.describe("promote", () => {
  test("a preview shows the diff, applying sends the hash that was shown, and a new webhook secret is shown once", async ({ page }) => {
    const { gw } = productGateway(new MockGateway(), { apps: [appWithLive("ACTIVE")] });
    liveGateway(gw, {
      status: "ACTIVE",
      changes: [
        { family: "meters", key: "api_calls", action: "ACTION_CREATE" },
        { family: "plans", key: "pro", action: "ACTION_UNCHANGED" },
        { family: "taxes", key: "DE", action: "ACTION_CONFLICT", detail: "rate differs" },
        { family: "webhooks", key: "https://x.test/hook", action: "ACTION_CREATE", secret: "whsec_NEWSECRET" },
      ],
    });
    await gw.install(page);
    await signInAsAda(page);
    await page.goto(`/app/apps/${APP}/go-live?env=live`);
    await page.getByTestId("promote-preview").click();
    const rows = page.getByTestId("promote-changes");
    await expect(rows).toContainText("api_calls");
    await expect(rows).toContainText("rate differs");
    await expect(rows).not.toContainText("pro");
    await expect(page.getByTestId("promote-summary")).toContainText("2 to add");
    expect(gw.called("POST", `/app/v1/apps/${APP}/promote`)[0]?.body).toEqual({ dry_run: true });
    await page.getByTestId("promote-apply").click();
    await expect(page.getByTestId("promote-secrets")).toBeVisible();
    expect(gw.called("POST", `/app/v1/apps/${APP}/promote`)[1]?.body).toEqual({ dry_run: false, plan_hash: "h1" });
  });

  test("when the configuration moved between preview and apply the refusal is shown in words", async ({ page }) => {
    const { gw } = productGateway(new MockGateway(), { apps: [appWithLive("ACTIVE")] });
    const live = liveGateway(gw, { status: "ACTIVE", changes: [{ family: "meters", key: "api_calls", action: "ACTION_CREATE" }] });
    await gw.install(page);
    await signInAsAda(page);
    await page.goto(`/app/apps/${APP}/go-live?env=live`);
    await page.getByTestId("promote-preview").click();
    live.state.stale = true;
    await page.getByTestId("promote-apply").click();
    await expect(page.getByTestId("promote")).toContainText(/moved|changed|preview again/i);
  });
});

test.describe("six languages, right to left included, and a phone", () => {
  test("the sandbox marking, the checklist and the tools read in Arabic, right to left, without sideways scrolling", async ({ page }) => {
    const { gw } = productGateway(new MockGateway(), { apps: [appWithLive("INACTIVE")] });
    liveGateway(gw, { done: { email_verified: true } });
    await gw.install(page);
    await page.setViewportSize({ width: 360, height: 740 });
    await signInAsAda(page);
    for (const path of [`/app/apps/${APP}/go-live?env=live`, `/app/apps/${APP}/sandbox?env=sandbox`, `/app/apps/${APP}/keys?env=sandbox`]) {
      await page.goto(path);
      await pickLanguage(page, "ar");
      await expect(page.locator("html")).toHaveAttribute("dir", "rtl");
      await expect(page.locator("main h1")).toBeVisible();
      // Nothing is left in English: the strings of this phase exist in every language.
      await expect(page.locator("main")).not.toContainText(/product\.(goLive|sandbox|promote|keys)\./);
      const o = await page.evaluate(() => ({ sw: document.documentElement.scrollWidth, cw: document.documentElement.clientWidth }));
      expect(o.sw, path).toBeLessThanOrEqual(o.cw);
    }
    await expect(page.getByTestId("sandbox-banner")).toContainText("بيئة الاختبار");
  });

  for (const code of ["tr", "de", "fr", "es"]) {
    test(`the go-live checklist is translated in ${code}`, async ({ page }) => {
      const { gw } = productGateway(new MockGateway(), { apps: [appWithLive("INACTIVE")] });
      liveGateway(gw, { done: { email_verified: true } });
      await gw.install(page);
      await signInAsAda(page);
      await page.goto(`/app/apps/${APP}/go-live?env=live`);
      await pickLanguage(page, code);
      await expect(page.getByTestId("go-live")).toBeVisible();
      await expect(page.getByTestId("go-live")).not.toContainText("Confirm the owner");
      await expect(page.getByTestId("go-live")).not.toContainText(/product\.goLive\./);
    });
  }
});

test.describe("the sandbox's own tools", () => {
  test("reset asks first, follows the job step by step and says what was removed; the scenarios are listed", async ({ page }) => {
    const { gw } = productGateway(new MockGateway());
    liveGateway(gw);
    await gw.install(page);
    await signInAsAda(page);
    await page.goto(`/app/apps/${APP}/sandbox?env=sandbox`);
    await expect(page.getByTestId("sandbox-reset")).toContainText("has not been reset yet");
    await expect(page.getByTestId("scenarios").locator("li")).toHaveCount(6);
    await page.getByTestId("sandbox-reset-open").click();
    expect(gw.called("POST", `/app/v1/apps/${APP}/sandbox/reset`)).toHaveLength(0);
    await page.getByTestId("sandbox-reset-confirm").click();
    const job = page.getByTestId("sandbox-reset-job");
    await expect(job).toHaveAttribute("data-state", "done");
    await expect(job.locator('[data-step="plans"]')).toContainText("7 rows removed");
    await expect(job.locator("li")).toHaveCount(6);
  });
});
