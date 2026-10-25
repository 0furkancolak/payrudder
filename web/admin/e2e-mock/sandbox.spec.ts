import { expect, test } from "@playwright/test";
import { MockGateway } from "./gateway";
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
    await page.getByTestId("go-live-terms").click();
    await expect(button).toBeEnabled();
    await button.click();
    await expect(page.getByTestId("go-live-state")).toContainText("Active");
    expect(gw.called("POST", `/app/v1/apps/${APP}/go-live`)[0]?.body).toEqual({ accept_terms: true });
    expect(live.state.status).toBe("ACTIVE");
  });

  test("with steps missing the button stays off, and a person who is not the owner is told why", async ({ page }) => {
    const { gw } = productGateway(new MockGateway(), { apps: [appWithLive("INACTIVE")] });
    liveGateway(gw, { done: { email_verified: true } });
    await gw.install(page);
    await signInAsAda(page);
    await page.goto(`/app/apps/${APP}/go-live?env=live`);
    await page.getByTestId("go-live-terms").click();
    await expect(page.getByTestId("go-live-activate")).toBeDisabled();
    // The legal profile and the payment provider are set up by the team for now: a person is told so rather than sent to a page they cannot open.
    await expect(page.getByTestId("go-live-legal_profile")).toContainText("Contact support");
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
    await expect(page.getByTestId("rotate-k5")).toHaveCount(0);

    await page.getByTestId("key-scope").selectOption("read");
    await page.getByTestId("new-sandbox-key").click();
    await page.getByRole("dialog").getByRole("button", { name: "I have saved it" }).click();
    expect(gw.called("POST", `/app/v1/apps/${APP}/api-keys`)[0]?.body).toEqual({ environment: "sandbox", scope: "read" });

    await page.getByTestId("rotate-k2").click();
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
