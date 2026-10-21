import { expect, test, type Page } from "@playwright/test";
import { MockGateway } from "./gateway";
import { dashboardFixture } from "./dashboard";
import { APP, INVOICE_1, LIVE, SESSION, billingGateway, productGateway, signInAsAda, user } from "./product";
import { pickCurrency } from "./ui";

// The end-user product: sign up and in, the organisation home, an app's dashboard, API keys. The gateway is mocked; SHOTS=<dir> also writes screenshots
// of the pages (desktop and phone, light and dark) so the look can be reviewed.
const shots = process.env.SHOTS;
async function shoot(page: Page, name: string): Promise<void> {
  if (shots) await page.screenshot({ path: `${shots}/${name}.png`, fullPage: true });
}

test.describe("sign up and sign in", () => {
  test("a visitor signs up, lands on an empty organisation and creates the first app", async ({ page }) => {
    const gw = productGateway(new MockGateway(), { apps: [] }).gw;
    await gw.install(page);
    await page.goto("/");
    await expect(page.getByRole("heading", { name: "Welcome back" })).toBeVisible();
    await shoot(page, "01-signin");
    await page.getByRole("link", { name: "Create an account" }).click();
    await expect(page.getByRole("heading", { name: "Create your account" })).toBeVisible();

    await page.getByLabel("Your name").fill("Ada Lovelace");
    await page.getByLabel("Organisation").fill("Ada Labs");
    await page.getByLabel("E-mail").fill("ada@example.com");
    await page.getByLabel("Password", { exact: true }).fill("short");
    await expect(page.getByRole("button", { name: "Create account" })).toBeDisabled();
    await page.getByLabel("Password", { exact: true }).fill("a long enough password");
    await shoot(page, "02-signup");
    await page.getByRole("button", { name: "Create account" }).click();

    await page.waitForURL("**/app");
    expect(gw.called("POST", "/auth/signup")[0]?.body).toEqual({ email: "ada@example.com", password: "a long enough password", display_name: "Ada Lovelace", account_name: "Ada Labs", locale: "en" });
    // Sign-up opens no session; the page signed in with the password just chosen.
    expect(gw.called("POST", "/auth/signin")[0]?.body).toEqual({ email: "ada@example.com", password: "a long enough password" });
    await expect(page.getByRole("heading", { name: "Create your first app" })).toBeVisible();
    await shoot(page, "03-empty-home");

    await page.getByRole("button", { name: "Create app" }).first().click();
    const dialog = page.getByRole("dialog");
    await dialog.getByLabel("App name").fill("Docs bot");
    await pickCurrency(dialog.getByLabel("Currency"), "EUR");
    await shoot(page, "04-create-app");
    await dialog.getByRole("button", { name: "Create app" }).click();
    await page.waitForURL(/\/app\/apps\//);
    expect(gw.called("POST", "/app/v1/apps")[0]?.body).toMatchObject({ name: "Docs bot", currency: "EUR" });
    // Every product call carries the session token and nothing else as credential.
    expect(gw.called("GET", "/app/v1/apps").every((c) => c.headers.authorization === `Bearer ${SESSION}`)).toBe(true);
  });

  test("a wrong password says so in words; the right one signs in", async ({ page }) => {
    const gw = productGateway(new MockGateway()).gw;
    await gw.install(page);
    await page.goto("/signin");
    await page.getByLabel("E-mail").fill("ada@example.com");
    await page.getByLabel("Password", { exact: true }).fill("wrong password");
    await page.getByRole("button", { name: "Sign in" }).click();
    await expect(page.getByRole("alert")).toContainText("Wrong e-mail or password");
    await page.getByLabel("Password", { exact: true }).fill("right password!");
    await page.getByRole("button", { name: "Sign in" }).click();
    await page.waitForURL("**/app");
    await expect(page.getByRole("heading", { name: "Welcome, Ada" })).toBeVisible();
  });

  test("the password can be shown and hidden", async ({ page }) => {
    await productGateway(new MockGateway()).gw.install(page);
    await page.goto("/signin");
    const field = page.getByLabel("Password", { exact: true });
    await field.fill("secret value");
    await expect(field).toHaveAttribute("type", "password");
    await page.getByRole("button", { name: "Show password" }).click();
    await expect(field).toHaveAttribute("type", "text");
  });

  test("a session the gateway no longer accepts sends the person back to sign in, with a reason", async ({ page }) => {
    const { gw, state } = productGateway(new MockGateway());
    await gw.install(page);
    await signInAsAda(page);
    await expect(page.getByRole("link", { name: /Web shop/ })).toBeVisible();
    state.sessionOk = false;
    await page.reload();
    await page.waitForURL(/\/signin/);
    await expect(page.getByRole("status")).toContainText("Your session has ended");
  });

  test("somebody who is not signed in is sent away from the app and from the operator console, to the one sign-in page", async ({ page }) => {
    await productGateway(new MockGateway()).gw.install(page);
    await page.goto("/app");
    await page.waitForURL("**/signin");
    await page.goto("/tenants");
    await page.waitForURL("**/signin");
    await page.goto("/operator");
    await page.waitForURL("**/signin");
    // There is no token form anywhere: the operator signs in like everybody else.
    await expect(page.getByLabel(/operator token/i)).toHaveCount(0);
    await expect(page.getByText(/operator console/i)).toHaveCount(0);
  });
});

test.describe("organisation home", () => {
  test("an app card shows its status, thirty days of money per currency and what needs a look", async ({ page }) => {
    await productGateway(new MockGateway()).gw.install(page);
    await signInAsAda(page);
    const card = page.getByRole("link", { name: "Web shop" });
    await expect(card).toContainText("Needs attention");
    await expect(card).toContainText("$12,840.50");
    await expect(card).toContainText("TRY 18,500.00");
    await expect(card).toContainText("Failed payments: 9");
    await shoot(page, "05-home");
    await card.click();
    await page.waitForURL(`**/app/apps/${APP}`);
  });
});

test.describe("app dashboard", () => {
  test("shows the numbers of the live environment, one currency at a time, with the change since the period before", async ({ page }) => {
    const { gw } = productGateway(new MockGateway());
    await gw.install(page);
    await signInAsAda(page);
    await page.goto(`/app/apps/${APP}`);
    await expect(page.getByRole("heading", { name: "Web shop" })).toBeVisible();
    // The tenant's own currency first; every figure is in it.
    await expect(page.getByTestId("currency-tab-TRY")).toHaveAttribute("aria-selected", "true");
    await expect(page.getByTestId("kpi-revenue-value")).toContainText("TRY");
    await expect(page.getByTestId("kpi-revenue-delta")).toContainText("%");
    await expect(page.getByTestId("kpi-customers-value")).toHaveText("18");
    await expect(page.getByRole("img", { name: /payment attempts/ })).toBeVisible();
    await expect(page.getByRole("region", { name: "Health" })).toContainText("1 failed deliveries");
    await expect(gw.called("GET", `/app/v1/apps/${APP}/dashboard`)[0]?.query.get("environment")).toBe("live");
    await expect(gw.called("GET", `/app/v1/apps/${APP}/dashboard`)[0]?.query.get("period")).toBe("30d");
    await shoot(page, "06-dashboard");
  });

  test("the environment switch of the header shows the sandbox, which says it is not real money", async ({ page }) => {
    const { gw } = productGateway(new MockGateway());
    await gw.install(page);
    await signInAsAda(page);
    await page.goto(`/app/apps/${APP}`);
    await expect(page.getByRole("note")).toHaveCount(0);
    await page.getByTestId("env-switch").getByRole("radio", { name: "Sandbox" }).check({ force: true });
    await expect(page.getByRole("note")).toContainText("Nothing here is real money");
    await expect(page).toHaveURL(/env=sandbox/);
    await expect.poll(() => gw.called("GET", `/app/v1/apps/${APP}/dashboard`).some((c) => c.query.get("environment") === "sandbox")).toBe(true);
  });

  test("a section that did not answer is named and the rest stays", async ({ page }) => {
    const body: Record<string, unknown> = { ...dashboardFixture({}), status: "degraded", unavailable: ["payments"] };
    for (const c of body.currencies as { payments: unknown; prev_payments: unknown }[]) c.payments = c.prev_payments = null;
    await productGateway(new MockGateway(), { dashboard: body }).gw.install(page);
    await signInAsAda(page);
    await page.goto(`/app/apps/${APP}`);
    await expect(page.getByTestId("dash-degraded")).toContainText("Payments did not answer");
    await expect(page.getByTestId("kpi-revenue-value")).toContainText("TRY");
    await expect(page.getByTestId("payments-card")).toContainText("Not available right now");
  });

  test("an API key is shown once, can be copied, and a revoked one is struck out", async ({ page, context }) => {
    await context.grantPermissions(["clipboard-read", "clipboard-write"]);
    const { gw } = productGateway(new MockGateway());
    await gw.install(page);
    await signInAsAda(page);
    await page.goto(`/app/apps/${APP}/keys`);
    const keys = page.getByRole("region", { name: "API keys" });
    await expect(keys).toContainText("qk_live_abcd1234_••••••••");
    await expect(keys).not.toContainText("SECRET");

    await keys.getByRole("button", { name: "Live key" }).click();
    const reveal = page.getByRole("dialog");
    await expect(reveal.getByTestId("new-key")).toHaveText("qk_live_newkey99_SECRETSECRETSECRETSECRET");
    await shoot(page, "07-new-key");
    await reveal.getByRole("button", { name: "Copy" }).click();
    await expect(reveal.getByRole("button", { name: "Copied" })).toBeVisible();
    expect(await page.evaluate(() => navigator.clipboard.readText())).toBe("qk_live_newkey99_SECRETSECRETSECRETSECRET");
    expect(gw.called("POST", `/app/v1/apps/${APP}/api-keys`)[0]?.body).toEqual({ environment: "live" });
    await reveal.getByRole("button", { name: "I have saved it" }).click();
    // The secret is gone for good: the list holds the prefix only.
    await expect(page.getByText("SECRETSECRET")).toHaveCount(0);
    await expect(keys).toContainText("qk_live_newkey99_••••••••");

    await keys.getByRole("button", { name: "Revoke key qk_live_newkey99_••••••••" }).click();
    await page.getByRole("dialog").getByRole("button", { name: "Revoke" }).click();
    await expect(keys.getByRole("listitem").filter({ hasText: "newkey99" })).toContainText("Revoked");
    expect(gw.called("DELETE", `/app/v1/apps/${APP}/api-keys/k9`)).toHaveLength(1);
  });

  test("the deeper views are the console's own pages, spoken to as the person's tenant", async ({ page }) => {
    const { gw } = productGateway(new MockGateway());
    gw.on((c) => (c.path === "/v1/customers" ? { body: { customers: [{ id: "c1", external_id: "cus_1", name: "Acme Ltd", email: "billing@acme.test" }] } } : undefined));
    await gw.install(page);
    await signInAsAda(page);
    await page.goto(`/app/apps/${APP}`);
    await page.getByRole("link", { name: /^Customers/ }).click();
    await page.waitForURL(`**/app/t/${LIVE}/customers`);
    await expect(page.getByRole("cell", { name: "Acme Ltd" })).toBeVisible();
    const call = gw.called("GET", "/v1/customers")[0];
    expect(call?.headers.authorization).toBe(`Bearer ${SESSION}`);
    expect(call?.headers["x-quotify-tenant"]).toBe(LIVE);
    await expect(page.getByRole("link", { name: /Web shop/ }).first()).toBeVisible();
    await shoot(page, "08-customers");
  });
});

test.describe("settings and chrome", () => {
  test("settings list the organisation, its members and the session; signing out forgets the token", async ({ page }) => {
    const { gw } = productGateway(new MockGateway());
    await gw.install(page);
    await signInAsAda(page);
    await page.getByRole("link", { name: "Settings" }).click();
    await expect(page.getByTestId("account-name")).toHaveText("Ada Labs");
    await shoot(page, "09-settings");
    await page.getByRole("link", { name: "Members" }).click();
    await page.waitForURL("**/app/members");
    await expect(page.getByRole("region", { name: "Members" })).toContainText(user.email);
    await page.getByRole("link", { name: "Settings" }).click();
    await page.getByRole("button", { name: "Sign out" }).click();
    await page.waitForURL("**/signin");
    expect(gw.called("POST", "/auth/signout")).toHaveLength(1);
    expect(await page.evaluate(() => window.sessionStorage.getItem("quotify.sessionToken"))).toBeNull();
    await page.goto("/app");
    await page.waitForURL("**/signin");
  });

  test("dark mode applies to the whole app", async ({ page }) => {
    await productGateway(new MockGateway()).gw.install(page);
    await signInAsAda(page);
    await page.goto(`/app/apps/${APP}`);
    await expect(page.getByRole("heading", { name: "Web shop" })).toBeVisible();
    await page.getByRole("button", { name: "Switch to dark mode" }).click();
    await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
    await page.waitForTimeout(400); // let the colour transitions finish before the picture
    await shoot(page, "10-dashboard-dark");
    await page.getByRole("button", { name: "Switch to light mode" }).click();
    await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
  });

  test("the session token lives in this tab only", async ({ page }) => {
    await productGateway(new MockGateway()).gw.install(page);
    await signInAsAda(page);
    const stored = await page.evaluate(() => ({ session: window.sessionStorage.getItem("quotify.sessionToken"), local: Object.keys(window.localStorage) }));
    expect(stored.session).toBe(SESSION);
    expect(stored.local.filter((k) => /token|session/i.test(k))).toEqual([]);
  });
});

test.describe("on a phone", () => {
  test.use({ viewport: { width: 390, height: 844 } });

  test("the sidebar is a drawer, nothing scrolls sideways, and the dashboard reads in one column", async ({ page }) => {
    await productGateway(new MockGateway()).gw.install(page);
    await page.goto("/");
    await shoot(page, "11-signin-phone");
    await signInAsAda(page);
    await shoot(page, "12-home-phone");
    await page.goto(`/app/apps/${APP}`);
    await expect(page.getByTestId("kpi-revenue")).toBeVisible();
    await shoot(page, "13-dashboard-phone");
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
    expect(overflow).toBeLessThanOrEqual(0);
    // The sidebar is not on the page; the menu button opens it as a dialog and Escape puts focus back on the button.
    await expect(page.getByTestId("sidebar")).toBeHidden();
    const menu = page.getByRole("button", { name: "Open the menu" });
    await menu.click();
    const drawer = page.getByRole("dialog");
    await expect(drawer.getByRole("link", { name: "Dashboard" })).toHaveAttribute("aria-current", "page");
    await shoot(page, "14-drawer-phone");
    await page.keyboard.press("Escape");
    await expect(drawer).toHaveCount(0);
    await expect(menu).toBeFocused();
    await menu.click();
    await page.getByRole("dialog").getByRole("link", { name: "Customers" }).click();
    await page.waitForURL(`**/app/t/${LIVE}/customers`);
    await expect(page.getByRole("dialog")).toHaveCount(0);
  });
});

test.describe("e-mail verification and password reset", () => {
  test("a sign-up whose address is taken shows the same notice as ever and opens no session", async ({ page }) => {
    const gw = productGateway(new MockGateway()).gw;
    await gw.install(page);
    await page.goto("/signup");
    await page.getByLabel("Organisation").fill("Ada Labs");
    await page.getByLabel("E-mail").fill("ada@example.com");
    // The address exists with another password: the sign-in right after the sign-up fails, and the page says to check the inbox.
    await page.getByLabel("Password", { exact: true }).fill("another long password");
    await page.getByRole("button", { name: "Create account" }).click();
    await expect(page.getByRole("status")).toContainText("Check your inbox");
    await expect(page.getByRole("status")).toContainText("ada@example.com");
    expect(page.url()).toContain("/signup");
    expect(await page.evaluate(() => window.sessionStorage.getItem("quotify.sessionToken"))).toBeNull();
  });

  test("forgot password: the same answer for any address, then the reset page sets a new password", async ({ page }) => {
    const gw = productGateway(new MockGateway()).gw;
    await gw.install(page);
    await page.goto("/signin");
    await page.getByRole("link", { name: "Forgot your password?" }).click();
    await expect(page.getByRole("heading", { name: "Reset your password" })).toBeVisible();
    await page.getByLabel("E-mail").fill("nobody@example.com");
    await page.getByRole("button", { name: "Send reset link" }).click();
    await expect(page.getByRole("status")).toContainText("If an account exists for nobody@example.com");
    expect(gw.called("POST", "/auth/forgot")[0]?.body).toEqual({ email: "nobody@example.com", locale: "en" });

    // The link of the mail: the token is in the fragment, is read once and leaves the address bar.
    await page.goto("/reset#token=qr_good");
    await expect(page.getByRole("heading", { name: "Choose a new password" })).toBeVisible();
    expect(page.url()).not.toContain("qr_good");
    await page.getByLabel("New password", { exact: true }).fill("short");
    await expect(page.getByRole("button", { name: "Save password" })).toBeDisabled();
    await page.getByLabel("New password", { exact: true }).fill("a brand new password");
    await page.getByRole("button", { name: "Save password" }).click();
    await expect(page.getByRole("status")).toContainText("Password changed");
    expect(gw.called("POST", "/auth/reset")[0]?.body).toEqual({ token: "qr_good", password: "a brand new password" });
    await page.getByRole("link", { name: "Sign in" }).click();
    await expect(page.getByRole("heading", { name: "Welcome back" })).toBeVisible();
  });

  test("a used reset link and a page opened without a link say what to do", async ({ page }) => {
    await productGateway(new MockGateway(), { resetToken: "" }).gw.install(page);
    await page.goto("/reset#token=qr_good");
    await page.getByLabel("New password", { exact: true }).fill("a brand new password");
    await page.getByRole("button", { name: "Save password" }).click();
    await expect(page.getByRole("alert")).toContainText("invalid, has expired or was already used");
    await expect(page.getByRole("link", { name: "Ask for a new link" })).toBeVisible();
    await page.goto("/reset");
    await expect(page.getByRole("alert")).toContainText("needs the link from your e-mail");
  });

  test("an unverified person sees the banner, can resend the link and cannot make a live key until the link is followed", async ({ page }) => {
    const { gw } = productGateway(new MockGateway(), { verified: false });
    await gw.install(page);
    await signInAsAda(page);
    const banner = page.getByTestId("verify-banner");
    await expect(banner).toContainText("Confirm your e-mail address (ada@example.com)");
    await banner.getByRole("button", { name: "Send the link again" }).click();
    await expect(banner).toContainText("Link sent");
    expect(gw.called("POST", "/auth/resend-verification")).toHaveLength(1);

    await page.goto(`/app/apps/${APP}/keys`);
    await expect(page.getByRole("button", { name: "Live key" })).toBeDisabled();
    await expect(page.getByTestId("live-needs-verified")).toBeVisible();
    await expect(page.getByRole("button", { name: "Sandbox key" })).toBeEnabled();

    // The link of the mail, followed in this browser.
    await page.goto("/verify#token=qv_good");
    await expect(page.getByText("E-mail address confirmed")).toBeVisible();
    expect(page.url()).not.toContain("qv_good");
    expect(gw.called("POST", "/auth/verify")[0]?.body).toEqual({ token: "qv_good" });
    await page.goto(`/app/apps/${APP}/keys`);
    await expect(page.getByTestId("verify-banner")).toHaveCount(0);
    await expect(page.getByRole("button", { name: "Live key" })).toBeEnabled();
  });

  test("a verification link that was used is explained, signed out too", async ({ page }) => {
    await productGateway(new MockGateway(), { verifyToken: "" }).gw.install(page);
    await page.goto("/verify#token=qv_old");
    await expect(page.getByRole("alert")).toContainText("This link does not work");
    await expect(page.getByRole("link", { name: "Sign in" })).toBeVisible();
  });
});

test.describe("members and invitations", () => {
  test("an owner invites by e-mail, sees the open invitation, sends it again and cancels it", async ({ page }) => {
    const { gw } = productGateway(new MockGateway());
    await gw.install(page);
    await signInAsAda(page);
    await page.goto("/app/members");
    const panel = page.getByRole("region", { name: "Members" });
    await expect(panel.getByTestId("member-grace@example.com")).toContainText("Grace Hopper");
    await panel.getByLabel("E-mail").fill("ned@example.com");
    await panel.getByLabel("Role", { exact: true }).selectOption("admin");
    await shoot(page, "20-members");
    await panel.getByRole("button", { name: "Send invitation" }).click();
    await expect(panel.getByRole("status")).toContainText("Invitation sent to ned@example.com");
    expect(gw.called("POST", "/app/v1/account/invitations")[0]?.body).toEqual({ email: "ned@example.com", role: "admin", locale: "en" });
    const pending = panel.getByTestId("invite-ned@example.com");
    await expect(pending).toBeVisible();
    await expect(pending).toContainText("Administrator");
    await pending.getByRole("button", { name: "Send the invitation to ned@example.com again" }).click();
    await expect.poll(() => gw.called("POST", /\/app\/v1\/account\/invitations\/inv-1\/resend$/).length).toBe(1);
    await pending.getByRole("button", { name: "Cancel the invitation to ned@example.com" }).click();
    await expect(pending).toHaveCount(0);
    expect(gw.called("DELETE", /\/app\/v1\/account\/invitations\/inv-1$/)).toHaveLength(1);
  });

  test("inviting a member says so; roles can be changed, but the last owner stays", async ({ page }) => {
    const { gw } = productGateway(new MockGateway());
    await gw.install(page);
    await signInAsAda(page);
    await page.goto("/app/members");
    const panel = page.getByRole("region", { name: "Members" });
    await panel.getByLabel("E-mail").fill("grace@example.com");
    await panel.getByRole("button", { name: "Send invitation" }).click();
    await expect(panel.getByRole("alert")).toContainText("member of the account already");

    await panel.getByLabel("Role of Linus Torvalds").selectOption("admin");
    await expect.poll(() => gw.called("PATCH", /\/app\/v1\/account\/members\/55555555/).length).toBe(1);
    expect(gw.called("PATCH", /\/app\/v1\/account\/members\/55555555/)[0]?.body).toEqual({ role: "admin" });
    await expect(panel.getByLabel("Role of Linus Torvalds")).toHaveValue("admin");

    // Ada is the only owner: she cannot leave.
    await panel.getByRole("button", { name: "Leave", exact: true }).click();
    await page.getByRole("dialog").getByRole("button", { name: "Leave", exact: true }).click();
    await expect(page.getByRole("dialog").getByRole("alert")).toContainText("must keep at least one owner");
  });

  test("an owner removes a member after confirming", async ({ page }) => {
    const { gw } = productGateway(new MockGateway());
    await gw.install(page);
    await signInAsAda(page);
    await page.goto("/app/members");
    const panel = page.getByRole("region", { name: "Members" });
    await panel.getByRole("button", { name: "Remove Linus Torvalds" }).click();
    await expect(page.getByRole("dialog")).toContainText("Linus Torvalds loses access");
    await page.getByRole("dialog").getByRole("button", { name: "Remove", exact: true }).click();
    await expect(panel.getByTestId("member-linus@example.com")).toHaveCount(0);
    expect(gw.called("DELETE", /\/app\/v1\/account\/members\/55555555/)).toHaveLength(1);
  });

  test("a viewer reads the team and can leave, but sees no way to change anything", async ({ page }) => {
    const { gw } = productGateway(new MockGateway(), { role: "viewer" });
    await gw.install(page);
    await signInAsAda(page);
    await page.goto("/app/members");
    const panel = page.getByRole("region", { name: "Members" });
    await expect(panel.getByTestId("member-grace@example.com")).toBeVisible();
    await expect(panel.getByLabel("E-mail")).toHaveCount(0);
    await expect(panel.getByRole("combobox")).toHaveCount(0);
    await expect(panel.getByRole("button", { name: /^Remove / })).toHaveCount(0);
    await expect(panel.getByRole("button", { name: "Leave", exact: true })).toBeVisible();
    await expect(page.getByLabel("Account name")).toHaveCount(0);
    expect(gw.called("GET", "/app/v1/account/invitations")).toHaveLength(0);
  });

  test("an administrator cannot hand out or touch ownership", async ({ page }) => {
    await productGateway(new MockGateway(), { role: "admin" }).gw.install(page);
    await signInAsAda(page);
    await page.goto("/app/members");
    const panel = page.getByRole("region", { name: "Members" });
    await expect(panel.getByLabel("Role", { exact: true }).locator("option")).toHaveText(["Administrator", "Viewer"]);
    // Ada's own row (listed as the owner) and her role cannot be changed here; Grace and Linus can.
    await expect(panel.getByLabel("Role of Grace Hopper").locator("option")).toHaveText(["Administrator", "Viewer"]);
    await expect(panel.getByTestId("member-ada@example.com").getByRole("combobox")).toHaveCount(0);
    await expect(panel.getByRole("button", { name: "Remove Linus Torvalds" })).toBeVisible();
  });

  test("an owner renames the account", async ({ page }) => {
    const { gw } = productGateway(new MockGateway());
    await gw.install(page);
    await signInAsAda(page);
    await page.goto("/app/settings");
    const name = page.getByLabel("Account name");
    await expect(page.getByRole("button", { name: "Save name" })).toBeDisabled();
    await name.fill("Ada Holdings");
    await page.getByRole("button", { name: "Save name" }).click();
    await expect(page.getByTestId("account-name")).toHaveText("Ada Holdings");
    expect(gw.called("PATCH", "/app/v1/account")[0]?.body).toEqual({ name: "Ada Holdings" });
  });

  test("an invited newcomer chooses a password and is in; the token leaves the address bar", async ({ page }) => {
    const { gw } = productGateway(new MockGateway());
    await gw.install(page);
    await page.goto("/invite#token=qi_new");
    await expect(page.getByRole("heading", { name: "You are invited" })).toBeVisible();
    await expect(page.getByText("Join Ada Labs on Quotify as Viewer.")).toBeVisible();
    await expect(page.getByText("Invited by Ada Lovelace.")).toBeVisible();
    expect(page.url()).not.toContain("qi_new");
    await expect(page.getByLabel("E-mail")).toHaveValue("ned@example.com");
    await page.getByLabel("Your name").fill("Ned");
    await page.getByLabel("Password", { exact: true }).fill("short");
    await expect(page.getByRole("button", { name: "Create login and join" })).toBeDisabled();
    await page.getByLabel("Password", { exact: true }).fill("a long enough password");
    await shoot(page, "21-invite");
    await page.getByRole("button", { name: "Create login and join" }).click();
    await page.waitForURL("**/app");
    expect(gw.called("POST", "/auth/invitations/accept")[0]?.body).toEqual({ token: "qi_new", password: "a long enough password", display_name: "Ned", locale: "en" });
    expect(await page.evaluate(() => window.sessionStorage.getItem("quotify.sessionToken"))).toBe(SESSION);
  });

  test("an invited person who has an account signs in and joins", async ({ page }) => {
    const { gw } = productGateway(new MockGateway());
    await gw.install(page);
    await page.goto("/invite#token=qi_has");
    await expect(page.getByText("ada@example.com already has an account")).toBeVisible();
    await page.getByLabel("Password", { exact: true }).fill("right password!");
    await page.getByRole("button", { name: "Sign in and join" }).click();
    await page.waitForURL("**/app");
    expect(gw.called("POST", "/auth/signin")).toHaveLength(1);
    expect(gw.called("POST", "/app/v1/invitations/accept")[0]?.body).toEqual({ token: "qi_has" });
  });

  test("somebody already signed in with the invited address just joins; with another address they are told", async ({ page }) => {
    const { gw } = productGateway(new MockGateway());
    await gw.install(page);
    await signInAsAda(page);
    await page.goto("/invite#token=qi_has");
    await page.getByRole("button", { name: "Join the account" }).click();
    await page.waitForURL("**/app");
    expect(gw.called("POST", "/app/v1/invitations/accept")).toHaveLength(1);
    await page.goto("/invite#token=qi_other");
    await expect(page.getByRole("status")).toContainText("This invitation is for grace@example.com, but you are signed in as ada@example.com");
    await expect(page.getByRole("button", { name: "Join the account" })).toHaveCount(0);
  });

  test("an invitation that no longer works, or a page opened without one, says so", async ({ page }) => {
    await productGateway(new MockGateway()).gw.install(page);
    await page.goto("/invite#token=qi_old");
    await expect(page.getByRole("alert")).toContainText("not valid any more");
    await page.goto("/invite");
    await expect(page.getByRole("alert")).toContainText("needs the link from your invitation e-mail");
  });

  test("with two accounts the person chooses which one to work in", async ({ page }) => {
    const { gw } = productGateway(new MockGateway(), {
      memberships: [
        { account_id: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", account_name: "Ada Labs", role: "owner" },
        { account_id: "cccccccc-cccc-4ccc-8ccc-cccccccccccc", account_name: "Grace Corp", role: "viewer" },
      ],
    });
    await gw.install(page);
    await signInAsAda(page);
    const switcher = page.getByLabel("Account", { exact: true });
    await expect(switcher).toHaveValue("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa");
    await switcher.selectOption("cccccccc-cccc-4ccc-8ccc-cccccccccccc");
    await expect.poll(() => gw.called("GET", "/app/v1/apps").some((c) => c.query.get("account_id") === "cccccccc-cccc-4ccc-8ccc-cccccccccccc")).toBe(true);
    expect(gw.called("GET", "/app/v1/account").some((c) => c.query.get("account_id") === "cccccccc-cccc-4ccc-8ccc-cccccccccccc")).toBe(true);
  });

  test("a person who is in no account gets an explanation, not a wall of errors", async ({ page }) => {
    await productGateway(new MockGateway(), { memberships: [] }).gw.install(page);
    await signInAsAda(page);
    await expect(page.getByTestId("no-account")).toContainText("You are not in any account");
  });
});

test.describe("billing pages of the app", () => {
  const open = async (page: Page, state: Parameters<typeof productGateway>[1] = {}) => {
    const { gw } = productGateway(new MockGateway(), state);
    billingGateway(gw);
    await gw.install(page);
    await signInAsAda(page);
    return gw;
  };

  test("invoices: filter, load more, open one and its PDF; everything is the tenant API as the person's tenant", async ({ page }) => {
    const gw = await open(page);
    await page.goto(`/app/t/${LIVE}/invoices`);
    await expect(page.getByRole("heading", { name: "Invoices" }).first()).toBeVisible();
    await expect(page.getByTestId("invoice-INV-2026-000003")).toContainText("cus_acme");
    await expect(page.getByTestId("invoice-INV-2026-000002")).toContainText("Open");
    await expect(page.getByTestId("invoice-INV-2026-000002")).toContainText("$299.00");
    await shoot(page, "30-invoices");

    await page.getByRole("button", { name: "Load more" }).click();
    await expect(page.getByTestId("invoice-INV-2026-000001")).toBeVisible();
    await expect(page.getByRole("button", { name: "Load more" })).toHaveCount(0);

    await page.getByLabel("Status").selectOption("open");
    await expect(page.getByTestId("invoice-INV-2026-000003")).toHaveCount(0);
    expect(gw.called("GET", "/v1/invoices").some((c) => c.query.get("status") === "open")).toBe(true);
    await page.getByLabel("Status").selectOption("");

    await page.getByRole("link", { name: "INV-2026-000003" }).click();
    await page.waitForURL(`**/app/t/${LIVE}/invoices/${INVOICE_1}`);
    await expect(page.getByTestId("invoice-total")).toHaveText("$49.00");
    await expect(page.getByRole("cell", { name: /Pro plan, September/ })).toBeVisible();
    await expect(page.getByRole("cell", { name: /API calls/ })).toBeVisible();
    await shoot(page, "31-invoice");

    // The PDF is a presigned link opened in a new tab.
    await page.evaluate(() => {
      const w = window as unknown as { __opened: string[] };
      w.__opened = [];
      window.open = ((url: string) => {
        w.__opened.push(url);
        return null;
      }) as typeof window.open;
    });
    await page.getByRole("button", { name: "Open PDF" }).click();
    await expect.poll(() => page.evaluate(() => (window as unknown as { __opened: string[] }).__opened)).toEqual(["https://files.example.test/INV-2026-000003.pdf"]);

    for (const c of gw.calls.filter((x) => x.path.startsWith("/v1/"))) {
      expect(c.headers.authorization, c.path).toBe(`Bearer ${SESSION}`);
      expect(c.headers["x-quotify-tenant"], c.path).toBe(LIVE);
    }
    expect(gw.calls.filter((c) => c.path.startsWith("/admin/"))).toEqual([]);
  });

  test("payments: charges and refunds as words and per-currency amounts, with a way to the invoice", async ({ page }) => {
    const gw = await open(page);
    await page.goto(`/app/t/${LIVE}/payments`);
    const rows = page.getByTestId("payment-row");
    await expect(rows).toHaveCount(4);
    await expect(rows.nth(0)).toContainText("Paid");
    await expect(rows.nth(0)).toContainText("$49.00");
    await expect(rows.nth(1)).toContainText("Failed");
    await expect(rows.nth(2)).toContainText("Processing");
    await expect(rows.nth(3)).toContainText("Refund");
    await expect(rows.nth(3)).toContainText("Refunded");
    await shoot(page, "32-payments");
    await rows.nth(0).getByRole("link", { name: "View invoice" }).click();
    await page.waitForURL(`**/app/t/${LIVE}/invoices/${INVOICE_1}`);
    expect(gw.called("GET", "/v1/payments")[0]?.headers["x-quotify-tenant"]).toBe(LIVE);
    expect(gw.calls.filter((c) => c.path.startsWith("/admin/"))).toEqual([]);
  });

  test("packages, pricing and reports are readable, and an owner can change them", async ({ page }) => {
    const gw = await open(page);
    await page.goto(`/app/t/${LIVE}/packages`);
    await expect(page.getByRole("cell", { name: "pro", exact: true })).toBeVisible();
    await expect(page.getByRole("button", { name: "Publish version" })).toBeEnabled();
    await shoot(page, "33-packages");
    await page.getByTestId("sidebar").getByRole("link", { name: "Pricing and tax" }).click();
    await expect(page.getByRole("cell", { name: "SAVE10" })).toBeVisible();
    await expect(page.getByRole("button", { name: "Create coupon" })).toBeEnabled();
    await page.getByTestId("sidebar").getByRole("link", { name: "Reports", exact: true }).click();
    await expect(page.getByRole("cell", { name: "USD" }).first()).toBeVisible();
    expect(gw.calls.filter((c) => c.path.startsWith("/admin/"))).toEqual([]);
  });

  test("a viewer reads everything and is told so: forms are off", async ({ page }) => {
    const gw = await open(page, { role: "viewer", memberships: [{ account_id: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", account_name: "Ada Labs", role: "viewer" }] });
    await page.goto(`/app/t/${LIVE}/packages`);
    await expect(page.getByTestId("read-only-note")).toContainText("Your role is viewer");
    await expect(page.getByRole("cell", { name: "pro", exact: true })).toBeVisible();
    await expect(page.getByRole("button", { name: "Publish version" })).toHaveCount(0);
    await page.getByTestId("sidebar").getByRole("link", { name: "Pricing and tax" }).click();
    await expect(page.getByRole("cell", { name: "SAVE10" })).toBeVisible();
    await expect(page.getByRole("button", { name: "Create coupon" })).toBeDisabled();
    await expect(page.getByRole("button", { name: "Save retries" })).toBeDisabled();
    await page.goto(`/app/t/${LIVE}/invoices`);
    await expect(page.getByTestId("invoice-INV-2026-000003")).toBeVisible();
    expect(gw.calls.filter((c) => c.method !== "GET" && c.path.startsWith("/v1/"))).toEqual([]);
  });

  test("no tab of an environment calls an operator route, and the tabs are all there", async ({ page }) => {
    const gw = await open(page);
    await page.goto(`/app/t/${LIVE}/customers`);
    const nav = page.getByTestId("sidebar").getByRole("navigation", { name: "Main navigation" });
    for (const tab of ["Customers", "Invoices", "Payments", "Packages", "Pricing and tax", "Reports", "Payment links", "Credits", "Webhooks"]) {
      await expect(nav.getByRole("link", { name: tab, exact: true })).toBeVisible();
    }
    for (const path of ["customers", "invoices", "payments", "packages", "pricing", "reports", "payment-links", "credits", "webhooks"]) {
      await page.goto(`/app/t/${LIVE}/${path}`);
      await expect(page.getByTestId("page-title")).toBeVisible();
    }
    await expect(page).not.toHaveURL(/signin/); // a 401 from an operator route would have ended the session
    expect(gw.calls.filter((c) => c.path.startsWith("/admin/"))).toEqual([]);
  });
});
